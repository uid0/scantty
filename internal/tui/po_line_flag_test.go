// A purchase-order line is never drawn without saying what state it is in:
// voided wherever it is voided, and — on the grid that flags a receipt —
// received wherever it is fully received.
//
// The order detail sheet used to draw a voided one that way. Where the line grid could
// not keep its flag column, `[voided]` moved to a reading line UNDER the row,
// and a scrolled pane can end on the row itself: the row then read
// `2  Gadget  2  $24.00` over `↓ N more below`, which is a live line on the
// screen an operator decides what is still on order from. No check asked, so
// every drawable width from 45 to 89 columns shipped it. A received line's
// `✓ received` rode the same reading line, so the same pane read a received
// line as an outstanding one; the grid's give-order now keeps the flag's column
// before the ship date's and leads the item cell with either flag below that
// (poFitLineGrid), and the receipt is swept on both of the detail sheet's grid
// forms beside the void. It is the only surface that flags a receipt: the edit
// grid's flag cell is void-only by design (poEditLineFlag), at every width.
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// poLineSays reports whether any of these pane lines tells the operator the
// FACT ("void" or "received"). Case-folded, because the surfaces spell a void
// `[voided]`, `voided` and `VOIDED` and each of those IS the fact; the fixtures
// carry no name with either word in it, so nothing else on the pane can answer
// for it.
func poLineSays(lines []string, fact string) bool {
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), fact) {
			return true
		}
	}
	return false
}

// poVoidDrawn is one voided line's identity on a pane: which line it is, and the
// pane lines that belong to it — the lines the operator reads as being about
// that line, in which the fact has to be.
type poVoidDrawn struct {
	what string
	// fact is what the lines must say: "void", or "received" for a fully
	// received line on the grid that flags one.
	fact  string
	lines []string
	// edgeCut is true when the row's item cell is a PREFIX of the line's flag
	// that runs to the pane's right edge: the flag leads the cell and only the
	// pane cut it. That is the one case accepted in place of the whole word,
	// and ONLY where the layer's floored width lies about the pane
	// (screenBodyWidth answers more cells than screenBodyCells gives, below 49
	// columns), which is where every columnar row overruns. That used to be 45
	// columns alone — the pane is 16 cells and `✓ received` would end at 17 —
	// and since the size contract put the floor at minTerminalWidth no drawable
	// width reaches it, so the exception never fires. It is DERIVED from the two
	// accessors (`lying`, below) rather than listed, so it switches itself off
	// here and would come back on its own if the floor were reopened; that is
	// why it is kept rather than deleted as a stale branch.
	edgeCut bool
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
	// drawn reads every flagged line's identity off one clipped pane, drawn
	// at terminal width w.
	drawn func(pane []string, w int) []poVoidDrawn
	// needLast requires the sweep to have reached a pane whose window ends on
	// the voided line's identity, at every width where it was drawn at all.
	needLast bool
	// widths narrows the widths this surface is walked at. Nil walks every
	// drawable width.
	widths func(t *testing.T) []int
	// facts is every state this surface's fixture must have drawn at least
	// once, so a fixture that lost a state fails instead of passing over it.
	facts []string
}

// The walk is a large one — every width, every height, every offset — and this
// package has run into go test's 600s per-package limit twice (AGENTS.md), so
// the widths of each surface are walked in PARALLEL. Each builds its own
// screens and nothing on the render path is shared mutable state; the sweep is
// clean under -race. The tallies are pooled behind a mutex and judged once
// every width has reported, which is what the "walk" group waits for.
func TestPOLines_ALineSaysItsStateWhereverItIsDrawn(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, sf := range poVoidSurfaces() {
		t.Run(sf.name, func(t *testing.T) {
			t.Parallel()
			ws := widths
			if sf.widths != nil {
				ws = sf.widths(t)
			}
			var (
				mu                sync.Mutex
				drawn, bare, cuts int
				lastAt, drawnAt   = map[string]int{}, map[string]int{}
				cutAt             = map[int]bool{}
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
								for _, d := range sf.drawn(pane, w) {
									key := fmt.Sprintf("%s@%d", d.fact, w)
									says := poLineSays(d.lines, d.fact)
									mu.Lock()
									drawn++
									drawnAt[key]++
									if d.last {
										lastAt[key]++
									}
									report := false
									switch {
									case says:
									case d.edgeCut:
										cuts++
										cutAt[w] = true
									default:
										bare++
										report = bare <= 3
									}
									mu.Unlock()
									if report {
										t.Errorf("at %dx%d (%s) %s is drawn with nothing saying %q:\n%s",
											w, h, pos, d.what, d.fact, strings.Join(pane, "\n"))
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
				t.Fatalf("no pane drew a flagged line at all, so nothing was checked")
			}
			if sf.needLast {
				for key, n := range drawnAt {
					if n > 0 && lastAt[key] == 0 {
						t.Errorf("%s: no pane ended on such a line, which is the state this "+
							"sweep exists for", key)
					}
				}
			}
			for _, fact := range sf.facts {
				seen := false
				for key := range drawnAt {
					seen = seen || strings.HasPrefix(key, fact+"@")
				}
				if !seen {
					t.Errorf("no pane drew a line whose state is %q, so that state was not checked", fact)
				}
			}
			var cutWidths []int
			for w := range cutAt {
				cutWidths = append(cutWidths, w)
			}
			sort.Ints(cutWidths)
			t.Logf("%d flagged identities drawn, %d cut by the pane edge alone (at widths %v), %d bare",
				drawn, cuts, cutWidths, bare)
		})
	}
}

// poVoidGridRowRe reads a line-grid row off a pane: the line's number in the
// number cell, then the gutter, then the item cell. Continuation lines are
// indented past the number cell, so they never match.
var poVoidGridRowRe = regexp.MustCompile(`^( +)(\d+)  \S`)

// poVoidGridDrawn finds the grid rows of flagged lines on a pane — voided, and
// fully received — each with the continuation lines under it that are on the
// pane too: the flag counts wherever the operator can read it as belonging to
// that row.
func poVoidGridDrawn(items []omsapi.PurchaseOrderItem) func([]string, int) []poVoidDrawn {
	return func(pane []string, w int) []poVoidDrawn {
		cells := screenBodyCells(w)
		lying := cells < screenBodyWidth(w)
		var out []poVoidDrawn
		for i, line := range pane {
			m := poVoidGridRowRe.FindStringSubmatch(line)
			if m == nil || len(m[1])+len(m[2]) > len(jdeIndent)+poGridNumMaxW {
				continue
			}
			n, _ := strconv.Atoi(m[2])
			if n < 1 || n > len(items) {
				continue
			}
			li := items[n-1]
			fact := ""
			switch {
			case li.IsVoided:
				fact = "void"
			case li.IsFullyReceived:
				fact = "received"
			default:
				continue
			}
			d := poVoidDrawn{what: fmt.Sprintf("line %d's grid row", n), fact: fact, lines: []string{line}}
			if cell := line[len(m[1])+len(m[2])+poGridGutter:]; lying && lipgloss.Width(line) == cells {
				d.edgeCut = strings.HasPrefix(poLineFlag(li), cell)
			}
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
func poVoidPaneDrawn(what, marker string) func([]string, int) []poVoidDrawn {
	return func(pane []string, _ int) []poVoidDrawn {
		for _, line := range pane {
			if strings.Contains(line, marker) {
				return []poVoidDrawn{{what: what, fact: "void", lines: pane}}
			}
		}
		return nil
	}
}

// poVoidRowDrawn is the identity of one row that describes a voided line: the
// fact has to be on that row, because the rows around it describe other things.
func poVoidRowDrawn(what string, match func(string) bool) func([]string, int) []poVoidDrawn {
	return func(pane []string, _ int) []poVoidDrawn {
		var out []poVoidDrawn
		for _, line := range pane {
			if match(line) {
				out = append(out, poVoidDrawn{what: what, fact: "void", lines: []string{line}})
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
// readings put its rows past any pane Root draws. Short names and the fewest
// readings, with voided lines in the middle and at the end and one received
// line, whose `received 1` reading is the one that decides how wide a pane
// has to be.
func poVoidOneLinePO() *omsapi.PurchaseOrder {
	po := poVoidFixturePO(false)
	po.Items = []omsapi.PurchaseOrderItem{
		{ID: 1, Description: "Bolt", QuantityOrdered: 40, EstimatedCost: omsapi.DecimalString("36.00")},
		{ID: 2, Description: "Nut", QuantityOrdered: 250, EstimatedCost: omsapi.DecimalString("31.25"), IsVoided: true},
		{ID: 3, Description: "Gear", QuantityOrdered: 1, QuantityReceived: 1, IsFullyReceived: true,
			EstimatedCost: omsapi.DecimalString("42.00")},
		{ID: 4, Description: "Tie", QuantityOrdered: 100, EstimatedCost: omsapi.DecimalString("6.00"), IsVoided: true},
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
//
// It walks PAST the 120 columns jdeDrawableWidths stops at, and on purpose. A
// received line's `received N` reading rides its row in this form, and with the
// item column at its floor that row is 98 cells — a 127-column terminal — so
// the one-line form of any order carrying a receipt is drawn only on panes the
// shared set never reaches. That ceiling is sound where a wider pane only folds
// less; here a wider pane is the only place the form exists at all.
func poVoidOneLineWidths() []int {
	widths := jdeDrawableWidths()
	for w := widths[len(widths)-1] + 1; w <= poVoidOneLineMaxWidth; w++ {
		if jdeRootDrawsAtWidth(w) {
			widths = append(widths, w)
		}
	}
	var out []int
	for _, w := range widths {
		s := jdeAtPane(poVoidOneLineDetail(), w, 40)
		s.(*PurchaseOrderDetailScreen).scroll = 1 << 20
		if strings.Contains(stripANSI(s.View()), poOneLineSKUHead) {
			out = append(out, w)
		}
	}
	return out
}

// poVoidOneLineMaxWidth bounds that walk: wide enough for the fixture's widest
// row with room to spare, so the collapse is walked from where it starts.
const poVoidOneLineMaxWidth = 160

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
			facts:    []string{"void", "received"},
		},
		{
			name:   "detail sheet/one-line grid",
			screen: poVoidOneLineDetail,
			at:     detailAt,
			step:   detailStep,
			drawn:  poVoidGridDrawn(poVoidOneLinePO().Items),
			facts:  []string{"void", "received"},
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
			at:    func(sc Screen) string { return "offset " + strconv.Itoa(edit(sc).deleteScroll) },
			step:  func(sc Screen) { sc.Update(tea.KeyMsg{Type: tea.KeyDown}) },
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

// TestPOLines_TheFlagColumnCostsTheItemColumnNothing: the grid's give-order
// keeps the flag's column before the ship date's (poFitLineGrid), and that
// trade is FREE — measured on the rendered grid, not on the fit's arithmetic.
//
// Two halves. At every drawable width where the grid draws a ship-date column
// it draws the flag column too, and there the flag column is no wider than the
// ship-date column: the widest flag drawn ends inside the room the ship date's
// column gave it. That inequality is what makes swapping one column for the
// other cost the item column nothing wherever only one of them fits.
//
// And 80 columns is PINNED, because it is the width this interface is modelled
// on and the width the give-order change moved. Before it (5f52438, and #168's
// base d9f345a before that), the same order drew these same 14-cell item cells
// beside a Ship date column, with a void pushed into the item cell as
// `[voided] Flat…` and no receipt on the row at all; now the item cells are
// unchanged and both flags stand in their own column.
func TestPOLines_TheFlagColumnCostsTheItemColumnNothing(t *testing.T) {
	// The flags as the grid spells them for this order's own lines, so a flag
	// reworded or widened is measured as it is drawn.
	var flags []string
	for _, li := range poVoidFixturePO(false).Items {
		if f := poLineFlag(li); f != "" {
			flags = append(flags, f)
		}
	}
	both := 0
	for _, w := range jdeDrawableWidths() {
		s := jdeAtPane(poVoidDetail(false)(), w, 200)
		pane := strings.Split(stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(200))), "\n")
		head, shipAt := "", -1
		for _, line := range pane {
			if strings.Contains(line, "#  Item") {
				head, shipAt = line, strings.Index(line, "Ship date")
			}
		}
		if head == "" {
			t.Fatalf("at %d columns the grid's heading is not on the pane", w)
		}
		if shipAt < 0 {
			continue
		}
		both++
		// A flag counts as the COLUMN only where it stands past the ship date's
		// heading; one leading the item cell is the fallback for a pane with no
		// flag column at all.
		shipCells := lipgloss.Width(head[:shipAt])
		flagAt, widest := -1, 0
		for _, line := range pane {
			if !poVoidGridRowRe.MatchString(line) {
				continue
			}
			for _, flag := range flags {
				i := strings.Index(line, flag)
				if i < 0 || lipgloss.Width(line[:i]) <= shipCells {
					continue
				}
				if flagAt >= 0 && lipgloss.Width(line[:i]) != flagAt {
					t.Errorf("at %d columns the flags do not stand in one column:\n%s", w, strings.Join(pane, "\n"))
				}
				flagAt = lipgloss.Width(line[:i])
				if fw := lipgloss.Width(flag); fw > widest {
					widest = fw
				}
			}
		}
		if flagAt < 0 {
			t.Errorf("at %d columns the grid draws a Ship date column and no flag column:\n%s",
				w, strings.Join(pane, "\n"))
			continue
		}
		if shipCol := flagAt - poGridGutter - shipCells; widest > shipCol {
			t.Errorf("at %d columns the widest flag is %d cells against a %d-cell ship-date "+
				"column, so keeping the flag instead would cost the item column", w, widest, shipCol)
		}
	}
	if both == 0 {
		t.Fatal("no drawable width drew both columns, so the widths were never compared")
	}

	s := jdeAtPane(poVoidDetail(false)(), 80, 200)
	pane := stripANSI(clampToBox(s.View(), screenBodyCells(80), screenBodyRows(200)))
	var at80 []string
	for _, line := range strings.Split(pane, "\n") {
		if poVoidGridRowRe.MatchString(line) || strings.Contains(line, "#  Item") {
			at80 = append(at80, line)
		}
	}
	want := []string{
		"    #  Item             Qty        Cost",
		"    1  Hex bolt M8x4…    40      $36.00",
		"    2  Flat washer M…   250      $31.25  [voided]",
		"    3  Gasket, nitri…     4       $8.00  [voided]",
		"    4  Bearing 6204-…    10      $42.00  ✓ received",
		"    5  Cable tie 200…   100       $6.00  [voided]",
	}
	if strings.Join(at80, "\n") != strings.Join(want, "\n") {
		t.Errorf("at 80 columns the grid drew:\n%s\nwant:\n%s", strings.Join(at80, "\n"), strings.Join(want, "\n"))
	}
}
