// The item detail's kit sections (op-8n0).
//
// The contract these hold it to:
//
//	a kit says so           — the operator can tell a kit from an ordinary item
//	                          at a glance, and is told why its stock reads zero
//	the breakdown is there  — every component and the per-kit quantity that is
//	                          what receiving multiplies
//	an ordinary item is     — no tag, no section, no note: the sheet is what it
//	untouched                 was before kits existed
//	a 404 means "no"        — the item serializer cannot say whether an item is a
//	                          kit, so the screen asks /kits/ and reads its 404 as
//	                          the answer — while a REAL failure says so rather
//	                          than silently rendering the item as ordinary
//	it survives the clip    — the body is CLIPPED, not wrapped (clampToBox), so
//	                          every line is measured against the pane at 80, 100
//	                          and 120 columns and the assertion is made on the
//	                          clipped render
package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// kitTestWidths is the three terminals every kit surface is measured at: the JD
// Edwards World floor, an ordinary window, and a wide one. 80 is the one that
// matters — this project has already shipped a warning silently clipped there.
var kitTestWidths = []int{80, 100, 120}

func intp(v int) *int { return &v }

// kitTestKit is the fixture, and it is deliberately the WIDEST realistic one: a
// long kit name, a component whose name runs well past any column the pane can
// afford, a component with a note, and one whose stock is zero (which must read
// as a zero, not as "unknown"). A width test is only as honest as its widest
// fixture.
func kitTestKit() *omsapi.Kit {
	return &omsapi.Kit{
		Item: omsapi.Item{
			ID: "kit-1", Name: "Eufy printer maintenance kit (CMYK + cleaning)",
			SKU: "EIK-4", Stock: 0,
		},
		IsKit:          true,
		ComponentCount: 3,
		Components: []omsapi.KitComponent{
			{
				ID: 7, Component: "itm-c", ComponentName: "Cyan ink cartridge, high yield",
				ComponentSKU: "CI-100-XL", ComponentStock: intp(0), ComponentNeedsReorder: true,
				Quantity: 1, Notes: "CMYK set — do not split",
			},
			{
				ID: 8, Component: "itm-m",
				ComponentName: "Magenta ink cartridge for the Eufy wide-format printer",
				ComponentSKU:  "MI-100-XL", ComponentStock: intp(6), Quantity: 2,
			},
			{ID: 9, Component: "itm-x", ComponentName: "Cleaning kit", Quantity: 1},
		},
	}
}

func kitTestSupplyingKits() []omsapi.KitSummary {
	return []omsapi.KitSummary{
		{
			ID: "kit-1", Name: "Eufy printer maintenance kit (CMYK + cleaning)", SKU: "EIK-4",
			IsActive: true, QuantityInKit: intp(2), SupplierName: "Acme Office Supply",
			SupplierSKU: "ACM-88421", UnitCost: "34.99", ComponentCount: 5,
		},
		{ID: "kit-2", Name: "Discontinued ink bundle", SKU: "DIB-1", QuantityInKit: intp(1)},
	}
}

// kitDetail builds a loaded item-detail screen in whichever kit state is being
// measured, sized to `width`.
func kitDetail(t *testing.T, kit *omsapi.Kit, supplying []omsapi.KitSummary, width int) *InventoryDetailScreen {
	t.Helper()
	s := NewInventoryDetailScreen(Deps{}, "itm-1")
	s.loading = false
	s.item = &omsapi.Item{
		ID: "itm-1", Name: "Cyan ink cartridge, high yield", SKU: "CI-100-XL",
		Stock: 4, MinimumStock: 2,
	}
	if kit != nil {
		s.item = &kit.Item
	}
	s.kit = kit
	s.suppliedByKits = supplying
	s.Update(tea.WindowSizeMsg{Width: width, Height: jdeSweepHeight})
	s.scroller.Set(s.renderBody())
	return s
}

// TestInventoryDetailKit_IdentifiesTheKitAndItsComponents is acceptance
// criterion 1: a kit is visibly a kit, and every component and per-kit quantity
// is on the screen.
func TestInventoryDetailKit_IdentifiesTheKitAndItsComponents(t *testing.T) {
	s := kitDetail(t, kitTestKit(), nil, 120)

	if head := s.renderHeader(); !strings.Contains(head, "[kit]") {
		t.Errorf("the header does not mark this item as a kit:\n%s", head)
	}
	body := s.renderBody()
	if !strings.Contains(body, "Kit contents (3)") {
		t.Errorf("no kit-contents section:\n%s", body)
	}
	// The per-kit quantity is the number receiving multiplies, so every row has
	// to carry it — a component listed without one is a component the operator
	// cannot reason about.
	for _, want := range []string{"CI-100-XL", "MI-100-XL", "Cleaning kit", "Per kit", "On hand"} {
		if !strings.Contains(body, want) {
			t.Errorf("kit section is missing %q:\n%s", want, body)
		}
	}
	// Magenta is the "2 per kit" row: find it and check the quantity column
	// really carries the 2 rather than the row merely existing.
	row := kitFindLine(body, "MI-100-XL")
	if row == "" {
		t.Fatalf("no magenta row:\n%s", body)
	}
	if !strings.Contains(row, "2") {
		t.Errorf("magenta row does not carry its per-kit quantity: %q", row)
	}
	// A component with none on the shelf reads as 0, not as unknown — that
	// distinction is the whole reason the field is a pointer.
	cyan := kitFindLine(body, "CI-100-XL")
	if strings.Contains(cyan, "—") {
		t.Errorf("zero stock rendered as unknown: %q", cyan)
	}
	if !strings.Contains(body, "needs reorder") {
		t.Errorf("a component below its reorder point did not say so:\n%s", body)
	}
	if !strings.Contains(body, "CMYK set") {
		t.Errorf("a component's note was dropped:\n%s", body)
	}
}

// TestInventoryDetailKit_StockNoteExplainsTheZero. A kit reads "Current stock:
// 0" whether five are on the shelf or none ever were, because it never carries
// stock at all. Saying so where the zero is, is the difference between a number
// and a lie.
func TestInventoryDetailKit_StockNoteExplainsTheZero(t *testing.T) {
	body := kitDetail(t, kitTestKit(), nil, 120).renderBody()
	if !strings.Contains(body, "Kits hold no stock of their own") {
		t.Errorf("the Stock block does not explain a kit's zero:\n%s", body)
	}
}

// TestInventoryDetailKit_AnOrdinaryItemIsUntouched is acceptance criterion 4.
// Nothing about kits may appear on an item that is not one — and, since the
// screen ASKS about every item it opens, "not one" includes the case where the
// answer simply came back no.
func TestInventoryDetailKit_AnOrdinaryItemIsUntouched(t *testing.T) {
	s := kitDetail(t, nil, nil, 120)
	whole := s.renderHeader() + "\n" + s.renderBody()
	for _, forbidden := range []string{"[kit]", "Kit contents", "Supplied by kits", "Kits hold no stock", "Per kit"} {
		if strings.Contains(whole, forbidden) {
			t.Errorf("a non-kit item shows the kit affordance %q:\n%s", forbidden, whole)
		}
	}
}

// TestInventoryDetailKit_SuppliedByKitsListsTheBundles is the other direction: a
// component item can be bought inside a kit, and how many it gets is the reading
// that makes that useful.
func TestInventoryDetailKit_SuppliedByKitsListsTheBundles(t *testing.T) {
	body := kitDetail(t, nil, kitTestSupplyingKits(), 120).renderBody()
	if !strings.Contains(body, "Supplied by kits (2)") {
		t.Errorf("no supplied-by section:\n%s", body)
	}
	for _, want := range []string{"EIK-4", "Acme Office Supply", "ACM-88421", "$34.99", "5 components"} {
		if !strings.Contains(body, want) {
			t.Errorf("supplied-by section is missing %q:\n%s", want, body)
		}
	}
	// An inactive kit is dimmed-but-shown upstream rather than hidden, so an
	// operator hunting for it learns why it is not in the PO picker.
	if !strings.Contains(body, "[inactive]") {
		t.Errorf("an inactive kit did not say so:\n%s", body)
	}
	// And a kit with no recorded price must not read as free.
	row := kitFindLine(body, "DIB-1")
	if !strings.Contains(row, "—") {
		t.Errorf("a kit with no unit cost did not render as unknown: %q", row)
	}
}

// TestInventoryDetailKit_SuppliedBySectionIsAbsentWhenEmpty. This section is
// context nobody asked for, so an empty one is pure noise on every item in a
// catalogue that has no kits at all.
func TestInventoryDetailKit_SuppliedBySectionIsAbsentWhenEmpty(t *testing.T) {
	if body := kitDetail(t, nil, nil, 120).renderBody(); strings.Contains(body, "Supplied by kits") {
		t.Errorf("an empty supplied-by section was drawn:\n%s", body)
	}
}

// TestInventoryDetailKit_TheClippedRenderLosesNothing is acceptance criterion 3,
// and it asserts on the CLIPPED render rather than the raw one: Root hands every
// screen through clampToBox, which TRUNCATES an over-wide line with nothing to
// show it did. A test that measured the unclipped body would pass on a layout
// the operator never actually sees.
func TestInventoryDetailKit_TheClippedRenderLosesNothing(t *testing.T) {
	states := []struct {
		name      string
		kit       *omsapi.Kit
		supplying []omsapi.KitSummary
	}{
		{"a kit", kitTestKit(), nil},
		{"a component of kits", nil, kitTestSupplyingKits()},
		{"an empty kit", &omsapi.Kit{Item: omsapi.Item{ID: "kit-0", Name: "Empty kit"}, IsKit: true}, nil},
	}
	for _, st := range states {
		for _, width := range kitTestWidths {
			s := kitDetail(t, st.kit, st.supplying, width)
			budget := screenBodyWidth(width)
			body := s.renderBody()
			// The clip is the assertion: if clamping changes a single line, that
			// line is one the operator sees cut off.
			if clipped := clampToBox(body, budget, len(strings.Split(body, "\n"))); clipped != body {
				for _, line := range strings.Split(body, "\n") {
					if w := lipgloss.Width(line); w > budget {
						t.Errorf("%s at %d columns: a line is %d wide but the pane is %d — it is clipped: %q",
							st.name, width, w, budget, line)
					}
				}
			}
			// And the header, which carries the [kit] tag.
			head := s.renderHeader()
			if clipped := clampToBox(head, budget, 8); clipped != head {
				t.Errorf("%s at %d columns: the header is clipped:\n%s", st.name, width, head)
			}
		}
	}
}

// TestInventoryDetailKit_TheGridColumnsLineUp is what makes it a grid rather
// than ragged lines: a name cell that overflowed its width would shove the
// quantities right on that row alone, which is the defect a printed parts list
// never has.
func TestInventoryDetailKit_TheGridColumnsLineUp(t *testing.T) {
	for _, width := range kitTestWidths {
		body := kitDetail(t, kitTestKit(), nil, width).renderBody()
		header := kitFindLine(body, "Per kit")
		if header == "" {
			t.Fatalf("no column header at %d columns:\n%s", width, body)
		}
		want := lipgloss.Width(header)
		for _, sku := range []string{"CI-100-XL", "MI-100-XL", "Cleaning kit"} {
			row := kitFindLine(body, sku)
			if row == "" {
				t.Fatalf("no row for %s at %d columns:\n%s", sku, width, body)
			}
			if got := lipgloss.Width(row); got != want {
				t.Errorf("at %d columns the %s row ends at column %d but the header at %d — the grid is ragged: %q",
					width, sku, got, want, row)
			}
		}
	}
}

// kitFindLine returns the first line of `body` containing `sub`, or "".
func kitFindLine(body, sub string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Where the answer comes from
// ---------------------------------------------------------------------------

// kitDetailServer is a fake OMS answering exactly the calls the item detail
// makes. kitStatus decides what /kits/{id}/ says, which is the whole point: that
// endpoint's STATUS is how the client learns whether an item is a kit.
func kitDetailServer(t *testing.T, kitStatus int) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/inventory/items/kit-1/":
			_, _ = w.Write([]byte(`{"id":"kit-1","name":"Eufy Ink Kit","sku":"EIK-4","current_stock":0}`))
		case "/api/inventory/kits/kit-1/":
			if kitStatus != http.StatusOK {
				w.WriteHeader(kitStatus)
				_, _ = w.Write([]byte(`{"detail":"nope"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"kit-1","name":"Eufy Ink Kit","sku":"EIK-4","is_kit":true,
				"component_count":1,
				"components":[{"id":7,"component":"itm-c","component_name":"Cyan ink",
				"component_sku":"CI-100","component_current_stock":3,"quantity":4}]}`))
		case "/api/inventory/items/kit-1/kits/":
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
		}
	}))
	return srv, &seen
}

// kitPumpScreen runs a screen's Init to completion against a fake server, the
// way the drive tests do — this screen fans four requests out in parallel and
// the kit answer is one of them.
func kitPumpScreen(t *testing.T, s *InventoryDetailScreen, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 12 {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			kitPumpScreen(t, s, c, depth+1)
		}
		return
	}
	_, next := s.Update(msg)
	kitPumpScreen(t, s, next, depth+1)
}

// TestInventoryDetailKit_AsksTheKitsEndpointAndRendersTheAnswer drives the real
// load path: the item comes from /items/ (with include_kits, or it would 404)
// and the kit answer from /kits/, and only the second one can say "kit".
func TestInventoryDetailKit_AsksTheKitsEndpointAndRendersTheAnswer(t *testing.T) {
	srv, seen := kitDetailServer(t, http.StatusOK)
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "kit-1")
	s.Update(tea.WindowSizeMsg{Width: 120, Height: jdeSweepHeight})
	kitPumpScreen(t, s, s.Init(), 0)

	if !kitSawPath(*seen, "/api/inventory/kits/kit-1/") {
		t.Fatalf("the screen never asked whether the item is a kit: %v", *seen)
	}
	if !s.isKit() {
		t.Fatalf("a 200 from /kits/ did not make the item a kit")
	}
	body := s.renderBody()
	if !strings.Contains(body, "Kit contents (1)") || !strings.Contains(body, "CI-100") {
		t.Errorf("the fetched bill of materials did not render:\n%s", body)
	}
}

// TestInventoryDetailKit_A404MeansOrdinaryAndSaysNothing. The 404 is the API's
// answer, so it must produce a completely ordinary screen — not an error, and
// not a kit affordance.
func TestInventoryDetailKit_A404MeansOrdinaryAndSaysNothing(t *testing.T) {
	srv, _ := kitDetailServer(t, http.StatusNotFound)
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "kit-1")
	s.Update(tea.WindowSizeMsg{Width: 120, Height: jdeSweepHeight})
	kitPumpScreen(t, s, s.Init(), 0)

	if s.isKit() {
		t.Fatal("a 404 from /kits/ made the item a kit")
	}
	whole := s.renderHeader() + "\n" + s.renderBody()
	if strings.Contains(whole, "[kit]") || strings.Contains(whole, "Kit contents") {
		t.Errorf("a 404 drew a kit affordance:\n%s", whole)
	}
	if strings.Contains(whole, "Kit status unavailable") {
		t.Errorf("a 404 was reported as a failure — it is the ANSWER:\n%s", whole)
	}
}

// TestInventoryDetailKit_AnUnansweredQuestionSaysSo. Anything that is NOT a 404
// left the question open, and the screen must say so: silently rendering the
// item as ordinary would hide a kit's entire nature behind a transient failure,
// with "Current stock: 0" left standing as if it meant something.
func TestInventoryDetailKit_AnUnansweredQuestionSaysSo(t *testing.T) {
	srv, _ := kitDetailServer(t, http.StatusInternalServerError)
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "kit-1")
	s.Update(tea.WindowSizeMsg{Width: 120, Height: jdeSweepHeight})
	kitPumpScreen(t, s, s.Init(), 0)

	if s.isKit() {
		t.Fatal("a 500 was taken as a yes")
	}
	body := s.renderBody()
	if !strings.Contains(body, "Kit status unavailable") {
		t.Errorf("an unanswered kit question was silently swallowed:\n%s", body)
	}
	// And it still fits the floor.
	budget := screenBodyWidth(80)
	s.Update(tea.WindowSizeMsg{Width: 80, Height: jdeSweepHeight})
	narrow := s.renderBody()
	if clipped := clampToBox(narrow, budget, len(strings.Split(narrow, "\n"))); clipped != narrow {
		t.Errorf("the unavailable line is clipped at 80 columns:\n%s", kitFindLine(narrow, "Kit status"))
	}
}

func kitSawPath(seen []string, want string) bool {
	for _, p := range seen {
		if p == want {
			return true
		}
	}
	return false
}
