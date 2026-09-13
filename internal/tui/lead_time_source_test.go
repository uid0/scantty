package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// The terminal half of `average_lead_time_source`: every surface in the derived
// set (lead_time_source.go) draws a supplier link's lead time with its
// provenance, and draws it EXACTLY as before when the server serves no marker.
//
// The inputs are the RECORDED OMS bodies in internal/omsapi/testdata, decoded by
// the real client, never structs written here: a fixture built from ScanTTY's
// own types cannot disagree with them (testdata/README.md). Each surface is
// driven twice — off the recording with the marker, and off its pre-#1085 twin
// from the same database — so "with" and "without" differ only by the server.

// leadWire decodes one recorded body through the client call that really reads
// it on the terminal.
func leadWire(t *testing.T, file string) *omsapi.Client {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", file))
	if err != nil {
		t.Fatalf("recorded response %s: %v", file, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return omsapi.New(srv.URL)
}

func leadWireLinks(t *testing.T, file string) []omsapi.ItemSupplier {
	t.Helper()
	links, err := leadWire(t, file).ListItemSuppliersForItem(context.Background(), "x")
	if err != nil {
		t.Fatalf("%s did not decode: %v", file, err)
	}
	return links
}

func leadWireItem(t *testing.T, file string) *omsapi.Item {
	t.Helper()
	it, err := leadWire(t, file).GetItem(context.Background(), "x")
	if err != nil {
		t.Fatalf("%s did not decode: %v", file, err)
	}
	return it
}

func leadWireSupplier(t *testing.T, file string) *omsapi.Supplier {
	t.Helper()
	sup, err := leadWire(t, file).GetSupplier(context.Background(), "1")
	if err != nil {
		t.Fatalf("%s did not decode: %v", file, err)
	}
	return sup
}

// leadAllMarks is every mark this package draws, for the absence checks.
var leadAllMarks = []string{leadMarkQuoted, leadMarkDefault, leadMarkMeasured, leadMarkUnknown}

// leadWantReadings is what the recording's rows must read as: each (days,
// source) pair the server sent, spelled the way the rows spell a lead time.
// DERIVED from the decoded links rather than restated, so the check follows the
// recording; leadRequireEveryMark then fails if the recording ever stops
// reaching one of the four.
func leadWantReadings(links []omsapi.ItemSupplier) []string {
	var out []string
	for _, l := range links {
		out = append(out, leadTimeText(l.LeadTimeDays, l.LeadTimeSource))
	}
	return out
}

func leadRequireEveryMark(t *testing.T, readings []string) {
	t.Helper()
	for _, m := range leadAllMarks {
		found := false
		for _, r := range readings {
			found = found || strings.HasSuffix(r, " "+m)
		}
		if !found {
			t.Fatalf("the recording reaches no row marked %q, so nothing here proves it is drawn", m)
		}
	}
}

// THE TWO SEVENS. The defect in one assertion: a defaulted 7 and a quoted 7
// must not read alike, and each served source has a mark of its own.
func TestLeadTimeMark_EveryServedSourceReadsDifferently(t *testing.T) {
	seen := map[string]omsapi.LeadTimeSource{}
	for _, src := range []omsapi.LeadTimeSource{
		omsapi.LeadTimeSourceUnknown, omsapi.LeadTimeSourceDefault,
		omsapi.LeadTimeSourceRecorded, omsapi.LeadTimeSourceMeasured,
	} {
		mark := leadTimeMark(src)
		if mark == "" {
			t.Errorf("source %q draws no mark", src)
		}
		if prev, dup := seen[mark]; dup {
			t.Errorf("sources %q and %q both draw %q", prev, src, mark)
		}
		seen[mark] = src
	}
	if got := leadTimeText(7, ""); got != "7d" {
		t.Errorf("no source served: %q, want the bare %q it always drew", got, "7d")
	}
	// Provenance this client has no words for has not been ESTABLISHED here, so
	// it must not borrow a known source's mark.
	if got := leadTimeMark("estimated"); got != leadMarkUnknown {
		t.Errorf("an unrecognised source draws %q, want %q", got, leadMarkUnknown)
	}
}

// EVERY SURFACE, off the recording with the marker and its twin without. The
// table IS the derived set in lead_time_source.go's header; each entry renders
// the surface the way the screen does and returns the text it draws.
func TestLeadTime_EverySupplierSurfaceDrawsTheServedProvenance(t *testing.T) {
	type surface struct {
		name string
		// draw renders the surface off the recording `file`.
		draw func(t *testing.T, file string) string
		// with / without name the recording pair the surface reads, and links
		// decodes the rows of `with` whose readings must appear.
		with, without string
		links         func(t *testing.T, file string) []omsapi.ItemSupplier
		prefix        string
	}
	fromItem := func(t *testing.T, file string) []omsapi.ItemSupplier { return leadWireItem(t, file).Suppliers }
	fromList := leadWireLinks
	fromSupplier := func(t *testing.T, file string) []omsapi.ItemSupplier { return leadWireSupplier(t, file).Items }

	surfaces := []surface{
		{
			name: "item detail · All suppliers", with: "lead_time_source_item_detail.json",
			without: "lead_time_source_item_detail_pre1085.json", links: fromItem, prefix: "lead ",
			draw: func(t *testing.T, file string) string {
				s := NewInventoryDetailScreen(Deps{}, "x")
				s.item, s.loading, s.kitAnswered = leadWireItem(t, file), false, true
				return s.renderBody()
			},
		},
		{
			name: "supplier detail · items", with: "lead_time_source_supplier_detail.json",
			without: "lead_time_source_supplier_detail_pre1085.json", links: fromSupplier, prefix: "lead ",
			draw: func(t *testing.T, file string) string {
				s := NewSupplierDetailScreen(Deps{}, "1")
				next, _ := s.Update(supplierLoadedMsg{sup: leadWireSupplier(t, file)})
				return next.(*SupplierDetailScreen).renderBody()
			},
		},
		{
			name: "item Suppliers screen", with: "lead_time_source_item_suppliers.json",
			without: "lead_time_source_item_suppliers_pre1085.json", links: fromList, prefix: "lead ",
			draw: func(t *testing.T, file string) string {
				return supUPCPane(t, leadWireLinks(t, file), 160, 60)
			},
		},
		{
			name: "New PO item picker", with: "lead_time_source_item_suppliers.json",
			without: "lead_time_source_item_suppliers_pre1085.json", links: fromList, prefix: "lead ",
			draw: func(t *testing.T, file string) string {
				s := poCreateStaged()
				s.phase = poPhaseItemPick
				s.itemSuppliersFor = s.supplierID
				s.itemSuppliersAll = leadWireLinks(t, file)
				s.itemSuppliers = s.itemSuppliersAll
				s.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
				return s.View()
			},
		},
		{
			name: "item form · supplier grid", with: "lead_time_source_item_detail.json",
			without: "lead_time_source_item_detail_pre1085.json", links: fromItem, prefix: "",
			draw: func(t *testing.T, file string) string {
				return itemBandRow(t, itemSheetWithSuppliers(t, leadWireItem(t, file).Suppliers), 0)
			},
		},
	}

	for _, sf := range surfaces {
		t.Run(sf.name, func(t *testing.T) {
			readings := leadWantReadings(sf.links(t, sf.with))
			leadRequireEveryMark(t, readings)
			out := sf.draw(t, sf.with)
			for _, r := range readings {
				if !strings.Contains(out, sf.prefix+r) {
					t.Errorf("the served reading %q is not drawn:\n%s", sf.prefix+r, out)
				}
			}

			// WITHOUT THE MARKER, exactly as before: every number still drawn,
			// and no mark anywhere — the server said nothing, so neither does
			// the terminal.
			old := sf.links(t, sf.without)
			if len(old) == 0 {
				t.Fatal("the pre-#1085 recording has no rows, so it proves nothing")
			}
			out = sf.draw(t, sf.without)
			for _, l := range old {
				if want := sf.prefix + leadTimeDays(l.LeadTimeDays); !strings.Contains(out, want) {
					t.Errorf("without the marker the lead time %q is no longer drawn:\n%s", want, out)
				}
			}
			for _, m := range leadAllMarks {
				if strings.Contains(out, m) {
					t.Errorf("without the marker the surface still draws %q:\n%s", m, out)
				}
			}
		})
	}
}

// THE FLAT LINE on the item detail is the PRIMARY link's, and carries that
// link's source — including a primary on its planning default, which is the
// exact reading the defect was reported about.
func TestLeadTime_TheItemDetailsAverageLineNamesItsSource(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"lead_time_source_item_detail.json", "Avg lead time: 7d " + leadMarkUnknown},
		{"lead_time_source_item_detail_primary_default.json", "Avg lead time: 7d " + leadMarkDefault},
		{"lead_time_source_item_detail_pre1085.json", "Avg lead time: 7d\n"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			s := NewInventoryDetailScreen(Deps{}, "x")
			s.item, s.loading, s.kitAnswered = leadWireItem(t, tc.file), false, true
			if out := s.renderBody(); !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
		})
	}
}

// THE EDIT FORM'S BOX shows a lead time, so its hint names the source — of the
// number the link was opened on, and only while the box still holds it.
func TestLeadTime_TheEditBoxNamesTheSourceOfTheNumberItHolds(t *testing.T) {
	var def omsapi.ItemSupplier
	for _, l := range leadWireLinks(t, "lead_time_source_item_suppliers.json") {
		if l.LeadTimeSource == omsapi.LeadTimeSourceDefault {
			def = l
		}
	}
	if def.ID == 0 {
		t.Fatal("the recording carries no defaulted link")
	}
	s := NewItemSupplierFormScreen(Deps{}, "x", "Hex bolt", &def)
	s.hydrate()
	if got := s.leadTimeHint(); got != "days "+leadMarkDefault {
		t.Errorf("opened on a defaulted 7: hint %q", got)
	}
	s.inputs[isLeadTime].SetValue("10")
	if got := s.leadTimeHint(); got != "days" {
		t.Errorf("after typing another number the hint still describes the old one: %q", got)
	}

	pre := leadWireLinks(t, "lead_time_source_item_suppliers_pre1085.json")[0]
	s = NewItemSupplierFormScreen(Deps{}, "x", "Hex bolt", &pre)
	s.hydrate()
	if got := s.leadTimeHint(); got != "days" {
		t.Errorf("no marker served: hint %q, want the plain %q", got, "days")
	}
}

// A NEW LINK'S BOX STARTS BLANK AND A BLANK SENDS NO KEY, so a link added from
// the terminal without a lead time is stored as the DEFAULT. With the old "7"
// pre-filled it went out as a sent 7, which OMS labels a quote — and the mark
// above would then have drawn "(quoted)" on a number nobody quoted.
func TestLeadTime_ANewLinksUntouchedBoxIsTheDefault(t *testing.T) {
	s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Hex bolt", nil)
	if v := s.inputs[isLeadTime].Value(); v != "" {
		t.Fatalf("the create box starts %q, want blank", v)
	}
	id := 3
	s.supplierID = &id
	s.inputs[isSKU].SetValue("S-1")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatal(err)
	}
	if w.AverageLeadTime != nil {
		t.Errorf("an untouched box sends %d; it must send no key", *w.AverageLeadTime)
	}
	if got := s.leadTimeHint(); !strings.Contains(got, "blank = default") {
		t.Errorf("the create hint does not say what blank means: %q", got)
	}
	s.inputs[isLeadTime].SetValue("7")
	if w, _ := s.buildPayload(); w.AverageLeadTime == nil || *w.AverageLeadTime != 7 {
		t.Errorf("a typed 7 must be sent: %v", w.AverageLeadTime)
	}
}

// THE TRADE, cell by cell: where the column cannot hold both, the mark stays
// whole and the number gives through the fact cut mark — never a bare number,
// and never a shortened number that reads as a real one.
func TestLeadTime_TheMarkOutlivesTheNumberInANarrowCell(t *testing.T) {
	const mark = leadMarkMeasured
	for w := 1; w <= 20; w++ {
		cell := leadTimeFactCell("1.2345678e+07d", mark, w, alignRight)
		if lipgloss.Width(cell) != w {
			t.Errorf("w=%d: cell is %d wide: %q", w, lipgloss.Width(cell), cell)
		}
		switch {
		case w >= lipgloss.Width(mark)+2:
			if !strings.Contains(cell, mark) {
				t.Errorf("w=%d: the mark gave before the number: %q", w, cell)
			}
			if !strings.Contains(cell, "1.2345678e+07d") && !strings.HasPrefix(strings.TrimSpace(cell), paneCutMark+" ") {
				t.Errorf("w=%d: the number was cut without the cut mark: %q", w, cell)
			}
		default:
			if strings.ContainsAny(cell, "0123456789") {
				t.Errorf("w=%d: a number is drawn beside a mark that could not fit: %q", w, cell)
			}
		}
	}
}

// THE TRADE, on the grid at every drawable width: a band whose figures sit at
// every ceiling at once is the only state that forces one, and there every row
// must keep its mark whole AND stay inside the pane. Before the lead column
// could give, the mark's extra cells would have pushed that row past the pane
// at the 80-column floor.
func TestLeadTime_TheGridKeepsEveryMarkAtEveryWidth(t *testing.T) {
	var sups []omsapi.ItemSupplier
	for i, src := range []omsapi.LeadTimeSource{
		omsapi.LeadTimeSourceMeasured, omsapi.LeadTimeSourceUnknown,
		omsapi.LeadTimeSourceDefault, omsapi.LeadTimeSourceRecorded,
	} {
		sups = append(sups, omsapi.ItemSupplier{
			// A SHORT identity, so the supplier cell never clips the SKU the rows
			// are found by: what is under test is the fact columns.
			ID: i + 1, SupplierName: "Acme", SupplierSKU: "Z9",
			UnitCost: "1234567890.12", LeadTimeDays: 12345678, LeadTimeSource: src, IsActive: true,
		})
	}
	var traded int
	for _, w := range jdeDrawableWidths() {
		s := itemSheetWithSuppliers(t, sups)
		s.Update(tea.WindowSizeMsg{Width: w, Height: jdeSweepHeight})
		band := &jdeLines{}
		s.supplierBand(band)
		budget := screenBodyCells(w)
		rows := 0
		for _, line := range band.text {
			if !strings.Contains(line, "(Z9)") {
				continue
			}
			rows++
			if lw := lipgloss.Width(line); lw > budget {
				t.Errorf("width %d: a grid row is %d cells on a %d-cell pane: %q", w, lw, budget, line)
			}
			if !strings.Contains(line, paneCutMark+" (") && !strings.Contains(line, "e+07d (") {
				t.Errorf("width %d: a row lost its lead time's mark: %q", w, line)
			}
			if strings.Contains(line, paneCutMark+" (") {
				traded++
			}
		}
		if rows != len(sups) {
			t.Fatalf("width %d: found %d grid rows, want %d", w, rows, len(sups))
		}
	}
	if traded == 0 {
		t.Fatal("no width forced the trade, so the give-order is untested")
	}
}

// THE TRADE, on the New PO picker row at every drawable width: the lead reading
// is a trailer the row drops from the right, and where it cannot be drawn whole
// the NUMBER gives first ("lead … (measured)") before the reading goes. At no
// width is the number drawn without its mark.
func TestLeadTime_ThePickerRowGivesTheNumberBeforeTheMark(t *testing.T) {
	const whole = "lead 14d " + leadMarkMeasured
	cut := "lead " + paneCutMark + " " + leadMarkMeasured
	var sawWhole, sawCut, sawGone int
	for _, w := range jdeDrawableWidths() {
		s := poCreateStaged()
		s.phase = poPhaseItemPick
		s.itemSuppliersFor = s.supplierID
		s.itemSuppliersAll = []omsapi.ItemSupplier{{
			ID: 1, ItemName: "Hex bolt M8x40 zinc plated grade 8.8", SupplierSKU: "AF-99-12-ZP-LH",
			UnitCost: "3.50", PackQuantity: 24, LeadTimeDays: 14, LeadTimeSource: omsapi.LeadTimeSourceMeasured,
		}}
		s.itemSuppliers = s.itemSuppliersAll
		s.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		row := ""
		for _, line := range strings.Split(clampToBox(s.View(), screenBodyCells(w), 40), "\n") {
			if strings.Contains(line, "AF-99") {
				row = line
			}
		}
		if row == "" {
			t.Fatalf("width %d: the picker row is not drawn", w)
		}
		switch {
		case strings.Contains(row, whole):
			sawWhole++
		case strings.Contains(row, cut):
			sawCut++
		case strings.Contains(row, "lead "):
			t.Errorf("width %d: the lead reading is drawn without its mark: %q", w, row)
		default:
			sawGone++
		}
	}
	if sawWhole == 0 || sawCut == 0 {
		t.Fatalf("whole at %d widths, number cut at %d, gone at %d: the give-order was not reached on both sides",
			sawWhole, sawCut, sawGone)
	}
	t.Logf("whole at %d widths, number cut at %d, gone at %d", sawWhole, sawCut, sawGone)
}
