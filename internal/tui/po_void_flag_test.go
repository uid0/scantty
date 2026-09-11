// A voided purchase-order line is never drawn without saying so.
//
// The order detail sheet used to draw one that way. Where the line grid could
// not keep its flag column, `[voided]` moved to a reading line UNDER the row,
// and a scrolled pane can end on the row itself: the row then read
// `2  Gadget  2  $24.00` over `↓ N more below`, which is a live line on the
// screen an operator decides what is still on order from. No check asked, so
// every drawable width from 45 to 89 columns shipped it.
//
// THE SET IS DERIVED FROM THE QUESTION, NOT FROM THAT GRID: where can a voided
// line be drawn without saying it is voided? Every wire shape that says a line
// is voided was read for (PurchaseOrderItem, ReceivingLine and POLineExisting
// are the three that carry is_voided), and every surface that names one of them
// is a member:
//
//   - the detail sheet's line grid, in both its block form and its one-line
//     form (po_detail.go);
//   - the edit screen's line editor, its delete confirm and its association
//     picker, each of which names ONE line (po_edit.go);
//   - the receiving form's list of settled lines (receive_form.go);
//   - the add-line flow's candidate list and confirm, which describe the line
//     the order already carries for an item (po_add_line.go).
//
// Deliberate exclusions, each with the reason it is not swept here:
//
//   - The edit screen's line GRID flags a void on its own row
//     (po_edit.go's lineGrid), a property of one line that no height or cursor
//     can change, and TestPOEditRows_AVoidedLineSaysSoOnItsOwnRow already holds
//     it at every drawable width.
//   - The edit screen's void prompt is never offered on a line that is already
//     voided (removalOffered), so it cannot draw one.
//   - The order pad is built by OMS's export_order, which skips every line with
//     is_voided set, so no voided line reaches it.
//   - The mark-shipped modal names a line by the NUMBER the operator types and
//     draws no line of its own.
//   - A continuation line whose row has scrolled off the top (a reading, a note,
//     a kit's component breakdown) names no line at all: it carries neither the
//     line's number nor its name, and `↑ N more above` over it says its row is
//     one keypress away.
//   - The line editor with its heading scrolled away, and the price phase of
//     the add-line flow, draw fields that name no line.
//   - An item's purchase history (inventory_detail.go) and the last-paid price
//     offer built from it (po_line_price.go) DO carry voided lines, and this
//     client cannot flag them: OMS's purchase_history returns one row per
//     PurchaseOrderItem with no is_voided in it, so nothing on the wire says
//     which rows those are. That is a server fix, not a client one.
//
// WHAT "DRAWN" MEANS is the line's IDENTITY on the clipped pane — its number in
// the grid's number cell, the fixed words of a heading that names it, or a fact
// that describes it — and each surface reads that identity off the FRONT of
// what it draws. A name is not used, because the fix this sweep guards puts
// `[voided]` AHEAD of the name, and a check that looked for the name would stop
// seeing the row exactly where the name gave its cells to the flag.
//
// It is asserted on the CLIPPED PANE at every drawable width, every drawable
// height and every position the surface's own movement reaches — scroll offset,
// cursor or focus — because the defect lived at the positions where the window
// ENDS, and a fixed height or an opening offset never reaches most of them.
package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// poVoidSays reports whether any of these pane lines tells the operator a line
// is voided. Case-folded, because the surfaces spell the fact `[voided]`,
// `voided` and `VOIDED` and each of those IS the fact; the fixtures carry no
// name with "void" in it, so nothing else on the pane can answer for it.
func poVoidSays(lines []string) bool {
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "void") {
			return true
		}
	}
	return false
}

// poVoidDrawn is one voided line's identity on a pane: which line it is, and the
// pane lines that belong to it — the lines the operator reads as being about
// that line, in which the fact has to be.
type poVoidDrawn struct {
	what  string
	lines []string
	// last is true when the identity is the last body line above the
	// `↓ N more below` marker: the window ENDS on it. That is the state the
	// detail sheet's defect lived in, and the sweep requires reaching it.
	last bool
}

// poVoidSurface is one member of the derived set.
type poVoidSurface struct {
	name string
	// screen builds the surface, loaded, with a voided line on it.
	screen func() Screen
	// at is the position the surface is standing at, read AFTER a render so a
	// clamped offset reads as the offset drawn. The walk stops when a step
	// lands on a position it has already drawn.
	at func(Screen) string
	// step moves to the next position the operator can reach.
	step func(Screen)
	// drawn reads every voided line's identity off one clipped pane.
	drawn func(pane []string) []poVoidDrawn
	// needLast requires the sweep to have reached a pane whose window ends on
	// the voided line's identity, at every width where it was drawn at all.
	needLast bool
	// widths narrows the widths this surface is walked at. Nil walks every
	// drawable width.
	widths func(t *testing.T) []int
}

// The walk is a large one — every width, every height, every offset — and this
// package has run into go test's 600s per-package limit twice (AGENTS.md), so
// the widths of each surface are walked in PARALLEL. Each builds its own
// screens and nothing on the render path is shared mutable state; the sweep is
// clean under -race. The tallies are pooled behind a mutex and judged once
// every width has reported, which is what the "walk" group waits for.
func TestPOLines_AVoidedLineSaysSoWhereverItIsDrawn(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, sf := range poVoidSurfaces() {
		t.Run(sf.name, func(t *testing.T) {
			t.Parallel()
			ws := widths
			if sf.widths != nil {
				ws = sf.widths(t)
			}
			var (
				mu              sync.Mutex
				drawn, bare     int
				lastAt, drawnAt = map[int]int{}, map[int]int{}
			)
			t.Run("walk", func(t *testing.T) {
				for _, w := range ws {
					t.Run(strconv.Itoa(w), func(t *testing.T) {
						t.Parallel()
						for _, h := range heights {
							s := jdeAtPane(sf.screen(), w, h)
							seen := map[string]bool{}
							for guard := 0; guard < 1000; guard++ {
								pane := strings.Split(stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))), "\n")
								pos := sf.at(s)
								if seen[pos] {
									break
								}
								seen[pos] = true
								for _, d := range sf.drawn(pane) {
									mu.Lock()
									drawn++
									drawnAt[w]++
									if d.last {
										lastAt[w]++
									}
									report := false
									if !poVoidSays(d.lines) {
										bare++
										report = bare <= 3
									}
									mu.Unlock()
									if report {
										t.Errorf("at %dx%d (%s) %s is drawn with nothing saying it is voided:\n%s",
											w, h, pos, d.what, strings.Join(pane, "\n"))
									}
								}
								sf.step(s)
							}
						}
					})
				}
			})
			if bare > 3 {
				t.Errorf("…and %d more panes like it", bare-3)
			}
			if drawn == 0 {
				t.Fatalf("no pane drew a voided line at all, so nothing was checked")
			}
			if sf.needLast {
				for w, n := range drawnAt {
					if n > 0 && lastAt[w] == 0 {
						t.Errorf("at %d columns no pane ended on a voided line, which is the "+
							"state this sweep exists for", w)
					}
				}
			}
			t.Logf("%d voided identities drawn across %d widths, %d of them bare", drawn, len(drawnAt), bare)
		})
	}
}

// poVoidGridRowRe reads a line-grid row off a pane: the line's number in the
// number cell, then the gutter, then the item cell. Continuation lines are
// indented past the number cell, so they never match.
var poVoidGridRowRe = regexp.MustCompile(`^( +)(\d+)  \S`)

// poVoidGridDrawn finds the grid rows of voided lines on a pane, each with the
// continuation lines under it that are on the pane too — the flag counts
// wherever the operator can read it as belonging to that row.
func poVoidGridDrawn(items []omsapi.PurchaseOrderItem) func([]string) []poVoidDrawn {
	return func(pane []string) []poVoidDrawn {
		var out []poVoidDrawn
		for i, line := range pane {
			m := poVoidGridRowRe.FindStringSubmatch(line)
			if m == nil || len(m[1])+len(m[2]) > len(jdeIndent)+poGridNumMaxW {
				continue
			}
			n, _ := strconv.Atoi(m[2])
			if n < 1 || n > len(items) || !items[n-1].IsVoided {
				continue
			}
			d := poVoidDrawn{what: fmt.Sprintf("line %d's grid row", n), lines: []string{line}}
			j := i + 1
			for ; j < len(pane); j++ {
				next := pane[j]
				if !strings.HasPrefix(next, poLineGridItemIndent) || strings.TrimSpace(next) == "" {
					break
				}
				d.lines = append(d.lines, next)
			}
			d.last = j == i+1 && j < len(pane) && strings.Contains(pane[j], "more below")
			out = append(out, d)
		}
		return out
	}
}

// poVoidPaneDrawn is the identity of a frame that is ABOUT one line: the whole
// pane belongs to that line, so the fact counts anywhere on it.
func poVoidPaneDrawn(what, marker string) func([]string) []poVoidDrawn {
	return func(pane []string) []poVoidDrawn {
		for _, line := range pane {
			if strings.Contains(line, marker) {
				return []poVoidDrawn{{what: what, lines: pane}}
			}
		}
		return nil
	}
}

// poVoidRowDrawn is the identity of one row that describes a voided line: the
// fact has to be on that row, because the rows around it describe other things.
func poVoidRowDrawn(what string, match func(string) bool) func([]string) []poVoidDrawn {
	return func(pane []string) []poVoidDrawn {
		var out []poVoidDrawn
		for _, line := range pane {
			if match(line) {
				out = append(out, poVoidDrawn{what: what, lines: []string{line}})
			}
		}
		return out
	}
}

// poVoidFixturePO is an order carrying voided lines of every shape the grid
// draws differently: a plain one with a reason, a KIT (whose item cell already
// carries a tag), one also fully received (whose flag is the void's, not the
// receipt's), and one LAST, whose block ends the body. Names are long enough to
// reach the item column's bound at every width, and none says "void".
func poVoidFixturePO(withKit bool) *omsapi.PurchaseOrder {
	yes := true
	po := &omsapi.PurchaseOrder{
		ID: 7, Number: "PO-2026-0077", Status: "confirmed", StatusLabel: "Confirmed",
		SupplierDetails: "Acme Fasteners & Industrial Supply Co.",
		CanDeleteItems:  &yes,
	}
	po.Items = []omsapi.PurchaseOrderItem{
		{
			ID: 1, Description: "Hex bolt M8x40 zinc plated grade 8.8",
			ItemDetails:     map[string]any{"sku": "HB-M8-40"},
			QuantityOrdered: 40, QuantityPending: 40,
			UnitCostOrdered:      omsapi.DecimalString("0.9000"),
			EstimatedCost:        omsapi.DecimalString("36.00"),
			ExpectedShipmentDate: "2026-08-10",
		},
		{
			ID: 2, Description: "Flat washer M3, A2 stainless, DIN 125A",
			QuantityOrdered: 250,
			UnitCostOrdered: omsapi.DecimalString("0.1250"),
			EstimatedCost:   omsapi.DecimalString("31.25"),
			IsVoided:        true, VoidReason: "supplier discontinued the part",
		},
	}
	if withKit {
		po.Items = append(po.Items, omsapi.PurchaseOrderItem{
			ID: 3, Description: "Fan kit 120mm with guard and screws",
			IsKitLine:       true,
			QuantityOrdered: 2,
			EstimatedCost:   omsapi.DecimalString("48.00"),
			KitComponents: []omsapi.POKitComponent{
				{Component: "c-1", ComponentName: "Fan 120mm", QuantityPerKit: 1, Quantity: 2},
				{Component: "c-2", ComponentName: "Fan guard", QuantityPerKit: 1, Quantity: 2},
			},
			IsVoided: true,
		})
	}
	po.Items = append(po.Items,
		omsapi.PurchaseOrderItem{
			ID: 4, Description: "Gasket, nitrile 40mm",
			QuantityOrdered: 4, QuantityReceived: 4, IsFullyReceived: true,
			EstimatedCost: omsapi.DecimalString("8.00"),
			IsVoided:      true,
		},
		omsapi.PurchaseOrderItem{
			ID: 5, Description: "Bearing 6204-2RS sealed",
			QuantityOrdered: 10, QuantityReceived: 10, IsFullyReceived: true,
			EstimatedCost: omsapi.DecimalString("42.00"),
		},
		omsapi.PurchaseOrderItem{
			ID: 6, Description: "Cable tie 200mm black UV stable",
			QuantityOrdered: 100,
			EstimatedCost:   omsapi.DecimalString("6.00"),
			IsVoided:        true,
		},
	)
	return po
}

// poVoidDetail is the detail sheet on the fixture.
func poVoidDetail(withKit bool) func() Screen {
	return func() Screen {
		s := NewPurchaseOrderDetailScreen(Deps{}, "7")
		s.loading = false
		s.po = poVoidFixturePO(withKit)
		return s
	}
}

// poVoidDetailWalk walks the sheet's scroll offset: one step is what Down does
// (jdeScrollStep), and the frame clamps it, so an offset that stops moving is
// the end of the body. A refused pane draws nothing to walk.
func poVoidDetailWalk() (func(Screen) string, func(Screen)) {
	at := func(sc Screen) string {
		s := sc.(*PurchaseOrderDetailScreen)
		if !s.frameDrawn(0, s.sheetBar()) {
			return "refused"
		}
		return "offset " + strconv.Itoa(s.scroll)
	}
	step := func(sc Screen) { sc.(*PurchaseOrderDetailScreen).scroll++ }
	return at, step
}

// poVoidOneLinePO is an order the one-line grid can hold: that form collapses
// only where every row, readings and all, fits the pane, and the main fixture's
// readings put its rows past any pane Root draws. Short names and no readings,
// with voided lines in the middle and at the end.
func poVoidOneLinePO() *omsapi.PurchaseOrder {
	po := poVoidFixturePO(false)
	po.Items = []omsapi.PurchaseOrderItem{
		{ID: 1, Description: "Hex bolt", QuantityOrdered: 40, EstimatedCost: omsapi.DecimalString("36.00")},
		{ID: 2, Description: "Washer M3", QuantityOrdered: 250, EstimatedCost: omsapi.DecimalString("31.25"), IsVoided: true},
		{ID: 3, Description: "Bearing", QuantityOrdered: 10, EstimatedCost: omsapi.DecimalString("42.00")},
		{ID: 4, Description: "Cable tie", QuantityOrdered: 100, EstimatedCost: omsapi.DecimalString("6.00"), IsVoided: true},
	}
	return po
}

func poVoidOneLineDetail() Screen {
	s := NewPurchaseOrderDetailScreen(Deps{}, "7")
	s.loading = false
	s.po = poVoidOneLinePO()
	return s
}

// poVoidOneLineWidths is every drawable width at which the one-line fixture's
// grid collapses to one row per line, read off the pane: the one-line grid's
// heading carries a Supplier SKU column the block form never draws.
func poVoidOneLineWidths() []int {
	var out []int
	for _, w := range jdeDrawableWidths() {
		s := jdeAtPane(poVoidOneLineDetail(), w, 40)
		s.(*PurchaseOrderDetailScreen).scroll = 1 << 20
		if strings.Contains(stripANSI(s.View()), poOneLineSKUHead) {
			out = append(out, w)
		}
	}
	return out
}

// poVoidEditOn is the edit screen opened on the fixture's voided line 2.
func poVoidEditOn(open func(*PurchaseOrderEditScreen)) func() Screen {
	return func() Screen {
		s := NewPurchaseOrderEditScreen(Deps{}, poVoidFixturePO(true))
		s.assoc.workOrders = []omsapi.WorkOrder{
			{ID: "wo-1", DisplayTitle: "Replace drive belt"},
			{ID: "wo-2", DisplayTitle: "Mill way-cover replacement"},
			{ID: "wo-3", DisplayTitle: "Compressor annual service"},
		}
		open(s)
		return s
	}
}

// poVoidReceive is the receiving form on a worksheet whose settled lines are a
// voided one with a long label and one closed short.
func poVoidReceive() Screen {
	po := poVoidFixturePO(false)
	sheet := &omsapi.ReceivingWorksheet{
		PurchaseOrder: po.ID, Number: po.Number, Supplier: "Acme Supply",
		Status: "confirmed", StatusLabel: "Confirmed", CanReceive: true,
		OutstandingLineCount: 1,
		Lines: []omsapi.ReceivingLine{
			{
				PurchaseOrderItem: 1, Label: "Hex bolt M8x40 zinc plated grade 8.8",
				ItemType: "inventory_item", QuantityOrdered: 40, QuantityPending: 40,
				ReceiptState: omsapi.ReceiptStateNotReceived, ReceiptStateLabel: "Not received",
			},
			{
				PurchaseOrderItem: 2, Label: "Flat washer M3, A2 stainless, DIN 125A, pack of 250",
				ItemType: "inventory_item", QuantityOrdered: 250,
				ReceiptState: omsapi.ReceiptStateVoided, ReceiptStateLabel: "Voided",
				IsSettled: true, IsVoided: true,
			},
			{
				PurchaseOrderItem: 3, Label: "Cable tie 200mm black UV stable",
				ItemType: "inventory_item", QuantityOrdered: 100, QuantityReceived: 60,
				ReceiptState: omsapi.ReceiptStateClosedShort, ReceiptStateLabel: "Closed short",
				IsSettled: true, IsClosedShort: true,
			},
		},
	}
	s := NewReceiveFormScreen(Deps{}, po)
	s.Update(receiveSheetMsg{sheet: sheet})
	return s
}

// poVoidOnOrderQty is the quantity of the voided line the order already carries
// for the add-line fixture's first candidate: a figure no other fact on those
// frames carries.
const poVoidOnOrderQty = 8317

// poVoidAddLine is the add-line flow with a first candidate whose line on the
// order is voided, and a price of its own so its fact row can be found.
func poVoidAddLine(phase poAddPhase) func() Screen {
	return func() Screen {
		s := NewPurchaseOrderAddLineScreen(Deps{}, poVoidFixturePO(false))
		s.idIn.SetValue("widget")
		s.lookup = poAddLookupFixture()
		s.lookup.Candidates[0].SuggestedUnitCost = omsapi.DecimalString("0.47")
		s.lookup.Candidates[0].AlreadyOnOrder = &omsapi.POLineExisting{
			LineItem: "2", QuantityOrdered: poVoidOnOrderQty, IsVoided: true,
		}
		s.phase = phase
		if phase == poAddPhaseConfirm {
			c := s.lookup.Candidates[0]
			s.chosen = &c
			s.chosenFrom = poAddPhaseChoose
		}
		return s
	}
}

func poVoidSurfaces() []poVoidSurface {
	detailAt, detailStep := poVoidDetailWalk()
	edit := func(sc Screen) *PurchaseOrderEditScreen { return sc.(*PurchaseOrderEditScreen) }
	add := func(sc Screen) *PurchaseOrderAddLineScreen { return sc.(*PurchaseOrderAddLineScreen) }
	const voidedIdx = 1 // the fixture's line 2

	return []poVoidSurface{
		{
			name:     "detail sheet/block grid",
			screen:   poVoidDetail(true),
			at:       detailAt,
			step:     detailStep,
			drawn:    poVoidGridDrawn(poVoidFixturePO(true).Items),
			needLast: true,
		},
		{
			name:   "detail sheet/one-line grid",
			screen: poVoidOneLineDetail,
			at:     detailAt,
			step:   detailStep,
			drawn:  poVoidGridDrawn(poVoidOneLinePO().Items),
			widths: func(t *testing.T) []int {
				ws := poVoidOneLineWidths()
				if len(ws) == 0 {
					t.Fatalf("the one-line fixture never collapses to the one-line grid, so that form is not swept")
				}
				return ws
			},
		},
		{
			name: "edit/line editor",
			screen: poVoidEditOn(func(s *PurchaseOrderEditScreen) {
				s.openLineEditor(voidedIdx)
			}),
			at: func(sc Screen) string { return "row " + strconv.Itoa(edit(sc).lineFocus) },
			step: func(sc Screen) {
				s := edit(sc)
				s.lineFocus = (s.lineFocus + 1) % poLineEditCount
				s.syncLineFocus()
			},
			drawn: poVoidPaneDrawn("the line editor on line 2", poLineEditHeading),
		},
		{
			name: "edit/delete confirm",
			screen: poVoidEditOn(func(s *PurchaseOrderEditScreen) {
				s.openLineEditor(voidedIdx)
				s.openDeleteLine(voidedIdx)
			}),
			at:   func(sc Screen) string { return "offset " + strconv.Itoa(edit(sc).deleteScroll) },
			step: func(sc Screen) { sc.Update(tea.KeyMsg{Type: tea.KeyDown}) },
			drawn: poVoidPaneDrawn("the delete confirm on line 2", "Delete:"),
		},
		{
			name: "edit/association picker",
			screen: poVoidEditOn(func(s *PurchaseOrderEditScreen) {
				s.openLineEditor(voidedIdx)
				s.openAssocPick(poAssocFieldWorkOrder, voidedIdx)
			}),
			at: func(sc Screen) string { return "row " + strconv.Itoa(edit(sc).assocCursor) },
			step: func(sc Screen) {
				s := edit(sc)
				if s.assocCursor < len(s.assocRows)-1 {
					s.assocCursor++
				}
			},
			// The heading names the line by its NUMBER; the name after it is
			// what a narrow pane gives.
			drawn: poVoidRowDrawn("the association picker's heading for line 2", func(line string) bool {
				return strings.Contains(line, "line 2")
			}),
		},
		{
			name:   "receiving/settled lines",
			screen: poVoidReceive,
			at:     func(sc Screen) string { return "row " + strconv.Itoa(sc.(*ReceiveFormScreen).focused) },
			step:   func(sc Screen) { sc.Update(tea.KeyMsg{Type: tea.KeyDown}) },
			// The settled list numbers its rows "1. ", "2. " — the live lines
			// above it are numbered without the stop — and the voided line is
			// the first of them.
			drawn: poVoidRowDrawn("settled line 1", func(line string) bool {
				return strings.HasPrefix(line, receiveMetaIndent+"1. ")
			}),
		},
		{
			name:   "add line/candidate list",
			screen: poVoidAddLine(poAddPhaseChoose),
			at:     func(sc Screen) string { return "row " + strconv.Itoa(add(sc).cursor) },
			step: func(sc Screen) {
				s := add(sc)
				if s.cursor < len(s.lookup.Candidates)-1 {
					s.cursor++
				}
			},
			// The candidate's own price is its FACT row's identity: the facts
			// never give, and no other candidate is priced 0.47.
			drawn: poVoidRowDrawn("the candidate whose line on the order is voided", func(line string) bool {
				return strings.Contains(line, "$0.47") || strings.Contains(line, strconv.Itoa(poVoidOnOrderQty))
			}),
		},
		{
			name:   "add line/confirm",
			screen: poVoidAddLine(poAddPhaseConfirm),
			// The frame clamps the offset and stores it back, so an offset
			// that stops moving is the end of the body; a refused pane hands
			// it back untouched and draws nothing to walk.
			at: func(sc Screen) string {
				s := add(sc)
				if !s.frameDrawn(len(s.headerLines()), s.bar()) {
					return "refused"
				}
				return "offset " + strconv.Itoa(s.scroll)
			},
			step: func(sc Screen) { add(sc).scroll++ },
			// The row that describes the order's existing line, found by its
			// LABEL column's leader — whatever the label says — or by the
			// quantity on it, whichever the pane still draws.
			drawn: poVoidRowDrawn("the row for the order's existing line", func(line string) bool {
				return strings.Contains(line, "On order") || strings.Contains(line, "Voided line") ||
					strings.Contains(line, strconv.Itoa(poVoidOnOrderQty))
			}),
		},
	}
}
