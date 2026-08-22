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
		// A four-figure price. A kit is a bundle of parts bought as one SKU, so
		// this is the ordinary case rather than the exotic one — and a fixture
		// that only ever priced things at "34.99" is why the cost column shipped
		// too narrow to hold a real kit's price.
		{
			ID: "kit-3", Name: "Whole-printer overhaul bundle", SKU: "WPO-9",
			IsActive: true, QuantityInKit: intp(4), UnitCost: "1299.50", ComponentCount: 12,
		},
	}
}

// kitTestRetiredKit is the fixture for the header's hardest case: a long-named
// kit that ALSO carries the tags renderHeader appends after the [kit] one. The
// name is the only thing on that line that can give way, so it has to give way
// for all of them, not just for [kit].
func kitTestRetiredKit(pendingReorder bool) *omsapi.Kit {
	kit := kitTestKit()
	kit.IsRetired = true
	kit.HasPendingReorder = pendingReorder
	return kit
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
	for _, width := range kitTestWidths {
		s := kitDetail(t, kitTestKit(), nil, width)
		budget := screenBodyWidth(width)
		flat := strings.Join(strings.Fields(
			clampToBox(s.renderBody(), budget, 200)), " ")
		if !strings.Contains(flat, "Kits hold no stock of their own") {
			t.Errorf("at %d columns the Stock block does not explain a kit's zero:\n%s",
				width, s.renderBody())
		}
		// The ordinary case stays quiet about anything else: no figure quoted,
		// nothing about a save this read-only screen does not perform.
		for _, forbidden := range []string{"Recorded as", "clears", "saving"} {
			if strings.Contains(flat, forbidden) {
				t.Errorf("at %d columns a zero-stock kit's note grew %q:\n%s",
					width, forbidden, s.renderBody())
			}
		}
	}
}

// TestInventoryDetailKit_TheStockNoteOwnsUpToAStrayFigure. The model forbids a
// kit carrying stock, but InventoryItem.save() never runs full_clean(), so a
// non-zero figure does reach this screen — which is why the item form quotes it
// before writing it back down. Asserting "kits hold no stock of their own" on
// the line directly under "Current stock: 7" tells the operator something they
// can see is untrue, and has the two screens describe one record differently.
//
// Measured on the CLIPPED render: the acknowledging reading is the longer of the
// two, so it is the one a 51-column pane would cut.
func TestInventoryDetailKit_TheStockNoteOwnsUpToAStrayFigure(t *testing.T) {
	stray := kitTestKit()
	stray.Stock = 7
	for _, width := range kitTestWidths {
		s := kitDetail(t, stray, nil, width)
		body := s.renderBody()
		budget := screenBodyWidth(width)
		flat := strings.Join(strings.Fields(clampToBox(body, budget, 200)), " ")

		if !strings.Contains(flat, "Recorded as 7 on hand") {
			t.Errorf("at %d columns the note does not acknowledge the figure above it:\n%s", width, body)
		}
		if !strings.Contains(flat, "a kit holds no stock of its own") {
			t.Errorf("at %d columns the note does not explain what the figure is not:\n%s", width, body)
		}
		// This screen SAVES NOTHING, so it must not borrow the item form's
		// promise that a save will clear the figure — that would swap one untrue
		// sentence for another.
		for _, forbidden := range []string{"clears", "clear", "saving", "save"} {
			if strings.Contains(flat, forbidden) {
				t.Errorf("at %d columns a read-only screen claims %q:\n%s", width, forbidden, body)
			}
		}
		// Wrapped, not clipped: the clamp must not have changed a single line.
		if clamped := clampToBox(body, budget, len(strings.Split(body, "\n"))); clamped != body {
			for _, line := range strings.Split(body, "\n") {
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("at %d columns a line is %d wide against a %d pane: %q",
						width, w, budget, line)
				}
			}
		}
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
	if !strings.Contains(body, "Supplied by kits (3)") {
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
		// A kit whose header carries a SECOND tag after [kit]. This is the state
		// the [kit] reservation used to lose: the name was fitted against [kit]
		// alone, so the name line already filled the pane and renderHeader's
		// [retired] fell off the end of it.
		{"a retired kit", kitTestRetiredKit(false), nil},
		{"a retired kit with a reorder pending", kitTestRetiredKit(true), nil},
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

// TestInventoryDetailKit_TheHeaderKeepsEveryTagAtTheFloor. The name line is only
// the FIRST half of the header's first line: renderHeader appends [retired],
// "needs reorder" and [reorder pending] straight after it. Fitting the name
// against [kit] alone filled the pane exactly, so the tags that followed were
// pushed past the edge and clampToBox ate them — the very failure the fit
// exists to prevent, moved one tag along.
func TestInventoryDetailKit_TheHeaderKeepsEveryTagAtTheFloor(t *testing.T) {
	cases := []struct {
		name string
		kit  *omsapi.Kit
		tags []string
	}{
		{"retired", kitTestRetiredKit(false), []string{"[kit]", "[retired]"}},
		{"retired with a reorder pending", kitTestRetiredKit(true),
			[]string{"[kit]", "[retired]", "[reorder pending]"}},
	}
	for _, tc := range cases {
		s := kitDetail(t, tc.kit, nil, 80)
		budget := screenBodyWidth(80)
		head := s.renderHeader()
		// The clip is the assertion: what the operator sees is the clamped render.
		clipped := clampToBox(head, budget, 8)
		for _, tag := range tc.tags {
			if !strings.Contains(clipped, tag) {
				t.Errorf("%s: the clipped header at 80 columns lost %q:\n%s", tc.name, tag, clipped)
			}
		}
		// And the name is still there in some readable form, rather than having
		// been given away entirely to make room.
		if !strings.Contains(clipped, "Eufy") {
			t.Errorf("%s: the clipped header lost the name outright:\n%s", tc.name, clipped)
		}
	}
}

// TestInventoryDetailKit_AFourFigurePriceIsLegibleAtEveryWidth. Two defects in
// one row, in order: padCell pads and never truncates, so an over-wide price
// used to push the row past the pane where clampToBox cut it silently
// ("$1299.50" read as "$1299.5", a plausible price and the wrong one); fitting
// the cell fixed the silence but left the price ELIDED at every terminal width,
// because the column was sized for "On hand" rather than for money. A kit is
// bought as one SKU, so four figures is the ordinary case — the column has to
// hold it, not merely admit it cannot.
func TestInventoryDetailKit_AFourFigurePriceIsLegibleAtEveryWidth(t *testing.T) {
	for _, width := range kitTestWidths {
		s := kitDetail(t, nil, kitTestSupplyingKits(), width)
		budget := screenBodyWidth(width)
		body := s.renderBody()

		row := kitFindLine(body, "WPO-9")
		if row == "" {
			t.Fatalf("no row for the four-figure kit at %d columns:\n%s", width, body)
		}
		// The row still fits, which is what stops clampToBox eating the price.
		if w := lipgloss.Width(row); w > budget {
			t.Fatalf("at %d columns the row is %d wide against a %d pane: %q", width, w, budget, row)
		}
		if !strings.Contains(row, "$1299.50") {
			t.Errorf("at %d columns the price is not readable in full: %q", width, row)
		}
		// And the cheap kit beside it is unchanged.
		if cheap := kitFindLine(body, "EIK-4"); !strings.Contains(cheap, "$34.99") {
			t.Errorf("at %d columns an ordinary price stopped rendering: %q", width, cheap)
		}
	}
}

// TestInventoryDetailKit_AKitHidesTheStockActions is the other half of the
// convention AGENTS.md states: a key the bar does not name must do nothing, and
// a key it names must do something. A kit carries no stock of its own, so
// counting and consuming it are meaningless — and a count would PERSIST, since
// the backend writes stock without running the model's "a kit cannot carry
// stock" check.
func TestInventoryDetailKit_AKitHidesTheStockActions(t *testing.T) {
	kit := kitDetail(t, kitTestKit(), nil, 120)
	kit.terminalHeight = jdeSweepHeight
	for _, gone := range []string{"c count", "u use"} {
		if view := kit.View(); strings.Contains(view, gone) {
			t.Errorf("a kit's footer still names %q:\n%s", gone, view)
		}
	}
	// The keys they named must now do nothing at all: no modal, no state change.
	kit.Update(runeKey('c'))
	if kit.ccStep != ccStepNone {
		t.Errorf("c opened a cycle count on a kit (step %d)", kit.ccStep)
	}
	kit.Update(runeKey('u'))
	if kit.cnStep != consumeStepNone {
		t.Errorf("u opened a consume prompt on a kit (step %d)", kit.cnStep)
	}
	// The rest of the footer is untouched — this hides two actions, not a screen.
	for _, kept := range []string{"o/enter reorder", "T retire", "x delete", "s suppliers"} {
		if view := kit.View(); !strings.Contains(view, kept) {
			t.Errorf("a kit's footer lost %q, which is still meaningful for a kit:\n%s", kept, view)
		}
	}
}

// TestInventoryDetailKit_AnOrdinaryItemKeepsTheStockActions is acceptance
// criterion 4 on the footer: hiding the kit's meaningless actions must not cost
// an ordinary item the two keys it has always had.
func TestInventoryDetailKit_AnOrdinaryItemKeepsTheStockActions(t *testing.T) {
	s := kitDetail(t, nil, nil, 120)
	s.terminalHeight = jdeSweepHeight
	for _, want := range []string{"c count", "u use"} {
		if view := s.View(); !strings.Contains(view, want) {
			t.Errorf("an ordinary item's footer lost %q:\n%s", want, view)
		}
	}
	s.Update(runeKey('c'))
	if s.ccStep == ccStepNone {
		t.Error("c no longer opens a cycle count on an ordinary item")
	}
	s.ccStep = ccStepNone
	s.Update(runeKey('u'))
	if s.cnStep == consumeStepNone {
		t.Error("u no longer opens a consume prompt on an ordinary item")
	}
}

// kitTestOpenClosedKit is a kit whose count mode was set to open/closed — which
// is reachable only BECAUSE this change made SetItemCountMode kit-routable, so
// the exposure is one this work created.
func kitTestOpenClosedKit() *omsapi.Kit {
	kit := kitTestKit()
	level := 3
	kit.CountMode = omsapi.CountModeOpenClosed
	kit.CountLevel = &level
	kit.PackagingLevels = []omsapi.PackagingLevel{
		{ID: 3, Name: "case", SortOrder: 0, BaseUnits: 100},
		{ID: 4, Name: "bag", SortOrder: 1, BaseUnits: 1},
	}
	return kit
}

// TestInventoryDetailKit_AKitNeverOffersThePackKey. Packing is a STOCK
// operation and a kit holds none, so the action is meaningless — and
// pack-container is a detail action on the kit-excluding item viewset, so it is
// a flat 404 for a kit id. Both halves of the convention: the bar does not name
// the key, and the key therefore does nothing.
func TestInventoryDetailKit_AKitNeverOffersThePackKey(t *testing.T) {
	s := kitDetail(t, kitTestOpenClosedKit(), nil, 120)
	s.terminalHeight = jdeSweepHeight
	if view := s.View(); strings.Contains(view, "p packs") {
		t.Errorf("a kit's footer names the pack key:\n%s", view)
	}
	s.Update(runeKey('p'))
	if s.pkStep != packStepNone {
		t.Errorf("p opened the pack prompt on a kit (step %d)", s.pkStep)
	}
}

// TestInventoryDetailKit_AnOrdinaryPackedItemKeepsThePackKey is the other half:
// suppressing the key for a kit must not have cost an ordinary sealed+open item
// the affordance it has always had.
func TestInventoryDetailKit_AnOrdinaryPackedItemKeepsThePackKey(t *testing.T) {
	s := packDetail(bagItem())
	s.terminalHeight = jdeSweepHeight
	if view := s.View(); !strings.Contains(view, "p packs") {
		t.Errorf("an open/closed item's footer lost the pack key:\n%s", view)
	}
	s.Update(runeKey('p'))
	if s.pkStep == packStepNone {
		t.Error("p no longer opens the pack prompt on an ordinary open/closed item")
	}
}

// kitTestSerializedKit is the stray-data case on this screen: a kit whose stored
// is_serialized is true. The server refuses to put a kit in that state —
// KitSerializer.validate rejects a truthy is_serialized and the model's
// _clean_kit says the same — so it is reachable only the way stray stock is,
// through a direct write that never runs full_clean(). The screen must decline
// to act on it rather than dressing it up as a feature.
func kitTestSerializedKit() *omsapi.Kit {
	kit := kitTestKit()
	kit.IsSerialized = true
	kit.SerialTrackingMode = "asset"
	return kit
}

// kitSerializedItem is an ORDINARY serialized item, which is what every one of
// these assertions has to leave completely untouched.
func kitSerializedItem() *omsapi.Item {
	return &omsapi.Item{
		ID: "itm-1", Name: "Cyan ink cartridge, high yield", SKU: "CI-100-XL",
		Stock: 4, MinimumStock: 2, IsSerialized: true, SerialTrackingMode: "asset",
		SerializedStock: &omsapi.SerializedStock{Available: 4, OnHand: 4},
	}
}

// TestInventoryDetailKit_AKitHidesTheSerialActions. A kit's COMPONENTS carry the
// serials — the kit is bought as one SKU and decomposes on receipt — so the
// section, the two keys and their footer entries are all meaningless on it, in
// the same way c / u / p are. Both halves of the convention, as always: the bar
// does not name the keys, so the keys must do nothing.
func TestInventoryDetailKit_AKitHidesTheSerialActions(t *testing.T) {
	s := kitDetail(t, kitTestSerializedKit(), nil, 120)
	s.terminalHeight = jdeSweepHeight

	view := s.View()
	for _, gone := range []string{"Serialized tracking", "i instances", "b batch-scan"} {
		if strings.Contains(view, gone) {
			t.Errorf("a kit's screen still carries %q:\n%s", gone, view)
		}
	}
	for _, key := range []rune{'i', 'b'} {
		if _, cmd := s.Update(runeKey(key)); cmd != nil {
			if msg, ok := cmd().(SwitchScreenMsg); ok {
				t.Errorf("%c navigated away from a kit to %T", key, msg.Screen)
			}
		}
	}
	// The rest of the screen is untouched — this hides two actions, not a screen.
	for _, kept := range []string{"o/enter reorder", "s suppliers", "E edit", "x delete"} {
		if !strings.Contains(view, kept) {
			t.Errorf("a kit's footer lost %q:\n%s", kept, view)
		}
	}
	if !strings.Contains(view, "Kit contents") {
		t.Errorf("a kit lost the section that says what its components are:\n%s", view)
	}
}

// TestInventoryDetailKit_AnOrdinarySerializedItemKeepsTheSerialActions is
// acceptance criterion 4 on this pair: suppressing them for a kit must not have
// cost an ordinary serialized item the section or either key.
func TestInventoryDetailKit_AnOrdinarySerializedItemKeepsTheSerialActions(t *testing.T) {
	s := kitDetail(t, nil, nil, 120)
	s.item = kitSerializedItem()
	s.scroller.Set(s.renderBody())
	s.terminalHeight = jdeSweepHeight

	view := s.View()
	for _, want := range []string{"Serialized tracking", "i instances", "b batch-scan"} {
		if !strings.Contains(view, want) {
			t.Errorf("an ordinary serialized item lost %q:\n%s", want, view)
		}
	}
	for _, key := range []rune{'i', 'b'} {
		_, cmd := s.Update(runeKey(key))
		if cmd == nil {
			t.Fatalf("%c no longer does anything on an ordinary serialized item", key)
		}
		if _, ok := cmd().(SwitchScreenMsg); !ok {
			t.Errorf("%c did not open a screen on an ordinary serialized item", key)
		}
	}
}
