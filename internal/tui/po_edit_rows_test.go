// No row on a purchase-order EDIT frame is drawn wider than the pane, from the
// width that must HOLD (80 columns) up — and what a narrow pane makes the screen
// cut, it marks.
//
// This is the columnar pilot's half of the row-width rule, and it is a separate
// sweep from TestJDEForm_NoRowRunsPastThePane for the reason every sweep in this
// package eventually has to confront: DERIVING THE SCREENS SAYS NOTHING ABOUT
// THE STATES INSIDE ONE. That sweep walks each columnar case in the one state its
// fixture opens in, and on this screen that state is the cursor on the first
// header field — so the option strip under a focused choice row, the hint beside
// a focused association row, the line editor on every one of its six rows and
// the removal each order flag opens were all out of its reach. Fourteen rows on
// these frames overran an 80-column pane and none was ever reported, because the
// checks that existed covered only the bar's width and the essential header rows.
//
// So the STATES here are derived from the screen rather than listed:
//
//	PHASES   come from the poEditPhase iota, walked to poEditPhaseCount, and a
//	         phase with no builder fails (TestPOEditRows_EveryPhaseIsWalked).
//	ROWS     come from the screen's own counts — rowCount(), poLineEditCount,
//	         the order's lines and the picker's options — so every cursor
//	         position of every phase is drawn.
//	ANSWERS  come from poRemovalFor's whole space: can_delete_items true, false
//	         and ABSENT, which is what decides which removal the status row opens.
//
// The one judgement left is the cost row's own states (costPendingNote's
// branches), and the reason is the same one poEditPhaseCases gives: they are
// states of a text box, reached by what is typed into it, not positions.
//
// THE FIXTURE CARRIES OMS-LENGTH VALUES ON PURPOSE. A fixture that cannot reach
// a bound makes the check about it vacuous however it is worded (AGENTS.md):
// every purchasing fixture in this package carried `Gadget` and `Lathe PM`, so no
// test had ever drawn this screen with a work-order title or a line description
// of the length a real order carries.
package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// The fixture's long values. Each is distinct from its first eight characters,
// so the mark check below can tell which value a clipped cell was cut from.
const (
	poEditRowsWOTitle   = "Rebuild the Bridgeport's quill feed gearbox and replace the worn power-feed clutch"
	poEditRowsCommittee = "Metalworking and Machining Committee of the Makerspace Board"
	poEditRowsLineBolt  = "Hex-head cap screw, M3×12, A2-70 stainless, DIN 933, bright finish, box of 100"
	poEditRowsLineVoid  = "Flat washer M3, A2 stainless, DIN 125A, bag of 500 (supplier discontinued)"
	poEditRowsLineRecd  = "Nyloc nut M3, zinc plated class 8, DIN 985, bag of 250 pieces"
	poEditRowsLineNone  = "Sprocket, 40-chain, 21 tooth, 3/4 inch bore with keyway and two set screws"
)

// poEditRowsValues are the OMS-supplied strings the mark check follows onto the
// pane: wherever any of them is drawn cut, the cut must say so.
var poEditRowsValues = []string{
	"WO-7Q4K — " + poEditRowsWOTitle, "WO-9Z8Y — " + poEditRowsWOTitle, poEditRowsWOTitle,
	poEditRowsCommittee,
	poEditRowsLineBolt, poEditRowsLineVoid, poEditRowsLineRecd, poEditRowsLineNone,
}

// poEditRowsPO is an order whose values reach every bound on this screen: long
// names and titles everywhere one is drawn, a five-digit quantity, a voided line,
// a received line, an unpriced line (the historical offer), and a payment term
// this build does not know — so the choice row grafts `… (unrecognized)` onto
// its strip.
func poEditRowsPO(canDelete *bool) *omsapi.PurchaseOrder {
	po := poViewPO()
	po.CanDeleteItems = canDelete
	po.WorkOrderRef = &omsapi.WorkOrderRef{ShortID: "WO-7Q4K", DisplayTitle: poEditRowsWOTitle}
	po.OwningGroupRef = &omsapi.OwningGroupRef{ID: 3, Name: poEditRowsCommittee}
	po.PaymentTerms = "net_45_end_of_month_with_rebate"
	po.Items = []omsapi.PurchaseOrderItem{
		{
			ID: 1, Description: poEditRowsLineBolt,
			ItemDetails:     map[string]any{"id": "item-bolt", "sku": "M3-HEX-SS"},
			QuantityOrdered: 12500, QuantityPending: 12500,
			UnitCostOrdered:      omsapi.DecimalString("1.2500"),
			EstimatedCost:        omsapi.DecimalString("15625.00"),
			ExpectedShipmentDate: "2026-08-10",
			WorkOrderRef:         &omsapi.WorkOrderRef{ShortID: "WO-9Z8Y", DisplayTitle: poEditRowsWOTitle},
			OwningGroupRef:       &omsapi.OwningGroupRef{ID: 3, Name: poEditRowsCommittee},
			Notes:                "substitute A2 only with written approval from the metal shop lead",
		},
		{
			ID: 2, Description: poEditRowsLineVoid,
			QuantityOrdered: 250,
			UnitCostOrdered: omsapi.DecimalString("0.1250"),
			EstimatedCost:   omsapi.DecimalString("31.25"),
			IsVoided:        true, VoidReason: "supplier discontinued the part",
		},
		{
			ID: 3, Description: poEditRowsLineRecd,
			QuantityOrdered: 40, QuantityReceived: 40, IsFullyReceived: true,
			UnitCostOrdered: omsapi.DecimalString("2.0000"),
			UnitCostActual:  omsapi.DecimalString("2.0000"),
			EstimatedCost:   omsapi.DecimalString("80.00"),
			ActualCost:      omsapi.DecimalString("80.00"),
		},
		{
			ID: 4, Description: poEditRowsLineNone,
			ItemDetails:     map[string]any{"id": "item-sprocket"},
			QuantityOrdered: 8,
		},
	}
	return po
}

// poEditRowsScreen builds the edit screen over that order with the association
// option lists loaded and the unpriced line's history answered, which is what
// the screen has once the operator has been on it for a moment.
func poEditRowsScreen(canDelete *bool) *PurchaseOrderEditScreen {
	s := NewPurchaseOrderEditScreen(Deps{}, poEditRowsPO(canDelete))
	s.assoc.workOrders = []omsapi.WorkOrder{
		{ID: "wo-1", DisplayTitle: poEditRowsWOTitle},
		{ID: "wo-2", DisplayTitle: "Mill way-cover replacement"},
		{ID: "wo-3", DisplayTitle: "Compressor annual service"},
	}
	s.assoc.committees = []omsapi.SIG{{ID: 3, Name: poEditRowsCommittee}, {ID: 4, Name: "Wood shop"}}
	return s
}

// openLine opens the line editor the way Ctrl-E does, and answers the history
// lookup an unpriced line fires so the offer block is drawn.
func (s *PurchaseOrderEditScreen) poEditRowsOpenLine(idx int) {
	s.openLineEditor(idx)
	if id := poLineItemID(s.po.Items[idx]); id != "" {
		s.lastPaid.init()
		s.lastPaid.loading[id] = false
		s.lastPaid.rows[id] = &poLastPaid{
			unit: 3.75, order: "PO-2026-0007", date: "2026-06-02", status: "received", priced: true,
		}
	}
}

type poEditRowsState struct {
	phase poEditPhase
	name  string
	mk    func() *PurchaseOrderEditScreen
}

// poEditRowsStates is every state the sweep draws, DERIVED as the file header
// says: every cursor position of every phase, under every answer the order's
// removal flag can give.
func poEditRowsStates(t *testing.T) []poEditRowsState {
	t.Helper()
	var out []poEditRowsState
	add := func(phase poEditPhase, name string, mk func() *PurchaseOrderEditScreen) {
		out = append(out, poEditRowsState{phase, name, mk})
	}
	flags := []*bool{boolPtr(true), boolPtr(false), nil}
	flagName := func(f *bool) string {
		if f == nil {
			return "flag absent"
		}
		return fmt.Sprintf("can_delete_items=%v", *f)
	}
	base := poEditRowsScreen(nil)

	for c := 0; c < base.rowCount(); c++ {
		add(poEditPhaseForm, fmt.Sprintf("form, row %d", c), func() *PurchaseOrderEditScreen {
			s := poEditRowsScreen(nil)
			s.cursor = c
			s.syncFocus()
			return s
		})
	}

	for i := range base.po.Items {
		for row := 0; row < poLineEditCount; row++ {
			for _, flag := range flags {
				add(poEditPhaseLine, fmt.Sprintf("line %d editor, row %d, %s", i, row, flagName(flag)),
					func() *PurchaseOrderEditScreen {
						s := poEditRowsScreen(flag)
						s.poEditRowsOpenLine(i)
						s.lineFocus = row
						s.syncLineFocus()
						return s
					})
			}
			// The removal the status row's Ctrl-E really opens under each answer,
			// reached through the arm itself (openLineRow), so the phase drawn is
			// the one the key opens rather than one this table assumed.
			if row != poLineRowStatus {
				continue
			}
			for _, flag := range flags {
				probe := poEditRowsScreen(flag)
				probe.poEditRowsOpenLine(i)
				probe.lineFocus = row
				probe.openLineRow()
				if probe.phase == poEditPhaseLine {
					continue // nothing offered on this line under this answer
				}
				add(probe.phase, fmt.Sprintf("line %d removal, %s", i, flagName(flag)),
					func() *PurchaseOrderEditScreen {
						s := poEditRowsScreen(flag)
						s.poEditRowsOpenLine(i)
						s.lineFocus = row
						s.openLineRow()
						return s
					})
			}
		}
	}
	// The void prompt is tallest on an order's ONLY active line (voidCaveats),
	// so the removal is also opened where every other line is voided.
	for _, flag := range []*bool{boolPtr(false), nil} {
		add(poEditPhaseVoidLine, "void prompt on the only active line, "+flagName(flag),
			func() *PurchaseOrderEditScreen {
				s := poEditRowsScreen(flag)
				for j := range s.po.Items {
					s.po.Items[j].IsVoided = j != 0
				}
				s.poEditRowsOpenLine(0)
				s.lineFocus = poLineRowStatus
				s.openLineRow()
				return s
			})
	}

	// The cost row's states: what is typed decides which sentence costPendingNote
	// draws under the box, and each is a different length.
	for _, st := range []struct {
		name   string
		typed  string
		setVal bool
		arm    bool
	}{
		{"cost left as shown", "", false, false},
		{"cost confirmed", "", false, true},
		{"cost cleared", "", true, false},
		{"cost cleared after confirming", "", true, true},
		{"cost refused", "-5", true, false},
		{"cost refused and armed", "50,00", true, true},
	} {
		add(poEditPhaseLine, "line 0 editor, "+st.name, func() *PurchaseOrderEditScreen {
			s := poEditRowsScreen(nil)
			s.poEditRowsOpenLine(0)
			if st.setVal {
				s.lineInputs[poLineEditCost].SetValue(st.typed)
			}
			s.lineCostConfirmed = st.arm
			return s
		})
	}

	// The association picker: both fields, opened from the order and from every
	// line (the heading names the line), at both ends of the list.
	for _, field := range []poAssocField{poAssocFieldWorkOrder, poAssocFieldCommittee} {
		for target := poAssocLineOrder; target < len(base.po.Items); target++ {
			for _, end := range []string{"first", "last"} {
				add(poEditPhaseAssoc, fmt.Sprintf("picker %d for %d, %s row", field, target, end),
					func() *PurchaseOrderEditScreen {
						s := poEditRowsScreen(nil)
						s.openAssocPick(field, target)
						if end == "last" {
							s.assocCursor = len(s.assocRows) - 1
						}
						return s
					})
			}
		}
	}
	return poEditRowsDistinct(out)
}

// poEditRowsDistinct drops a state that draws EXACTLY what an earlier one draws,
// at both the width that must hold and the widest this sweep reaches.
//
// The walk above is a product — every row under every removal answer — and most
// of its cells are the same frame: the answer changes what the STATUS row's bar
// and note say and nothing else, so under two of the three answers every other
// row redraws its first answer byte for byte. Which cells are duplicates is read
// off the render rather than written down, so a row that later starts reading
// the flag is walked under every answer again without anybody noticing it had to
// be. Drawing each state twice to decide is a few hundred renders; walking the
// duplicates at every pane is a hundred thousand.
func poEditRowsDistinct(states []poEditRowsState) []poEditRowsState {
	seen := map[string]bool{}
	var out []poEditRowsState
	for _, st := range states {
		var key strings.Builder
		for _, w := range []int{jdeRowsMustHoldFrom, 120} {
			s := st.mk()
			s.Update(tea.WindowSizeMsg{Width: w, Height: 40})
			key.WriteString(s.View())
			key.WriteString("\x00")
		}
		if seen[key.String()] {
			continue
		}
		seen[key.String()] = true
		out = append(out, st)
	}
	return out
}

// TestPOEditRows_EveryPhaseIsWalked: the iota to its sentinel. A phase added
// tomorrow is drawn by nothing here until somebody says how it is reached.
func TestPOEditRows_EveryPhaseIsWalked(t *testing.T) {
	seen := map[poEditPhase]bool{}
	for _, st := range poEditRowsStates(t) {
		s := st.mk()
		if s.phase != st.phase {
			t.Errorf("%s: built phase %v, want %v", st.name, s.phase, st.phase)
		}
		seen[s.phase] = true
	}
	for p := poEditPhase(0); p < poEditPhaseCount; p++ {
		if !seen[p] {
			t.Errorf("phase %v is reached by no state this sweep builds, so no row it draws is "+
				"measured against the pane", p)
		}
	}
}

// poEditRowsWidths is every terminal width Root draws from the one that must
// HOLD up. Below 80 the layer's own label column and field floor outgrow the
// pane on every columnar screen at once; that band is recorded, per screen, in
// jdeRowsPastThePane and belongs to the layer rather than to this screen.
func poEditRowsWidths() []int {
	var out []int
	for _, w := range jdeDrawableWidths() {
		if w >= jdeRowsMustHoldFrom {
			out = append(out, w)
		}
	}
	return out
}

// poEditRowsAboveTheBar is the frame's lines above the action bar's rule — the
// rows a sheet draws. The bar's own lines are the layer's and are held by
// TestJDEForm_TheActionBarSurvivesEveryHeight; a refused pane draws no rule, and
// every line of its notice is judged.
func poEditRowsAboveTheBar(view string) []string {
	lines := strings.Split(view, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if plain := stripANSI(lines[i]); plain != "" && strings.Trim(plain, "-") == "" {
			return lines[:i]
		}
	}
	return lines
}

// poEditRowsUnmarkedCut reports a drawn line that carries a CUT value with
// nothing saying so: a prefix of one of the fixture's long values, eight
// characters or more, followed by anything but the ellipsis. "" when the line
// draws every value it touches whole, or marks the cut.
func poEditRowsUnmarkedCut(line string) string {
	plain := stripANSI(line)
	for _, v := range poEditRowsValues {
		if strings.Contains(plain, v) {
			continue
		}
		// Every place the value's first eight characters are drawn, extended as
		// far as the line goes on agreeing with the value — one forward pass per
		// occurrence, because this runs on every line of every pane.
		lead := string([]rune(v)[:8])
		for from := 0; ; {
			at := strings.Index(plain[from:], lead)
			if at < 0 {
				break
			}
			at += from
			n := 0
			for n < len(v) && at+n < len(plain) && plain[at+n] == v[n] {
				n++
			}
			// Back off to a rune boundary: the byte after the shared prefix can
			// be the middle of a multi-byte rune both strings begin alike.
			for n > 0 && n < len(v) && !utf8.RuneStart(v[n]) {
				n--
			}
			if !strings.HasPrefix(plain[at+n:], "…") {
				return fmt.Sprintf("%q is cut to %q with no mark", v, v[:n])
			}
			from = at + len(lead)
		}
	}
	return ""
}

// TestPOEditRows_NothingRunsPastThePane: at every width from 80 up and every
// height Root draws, no line of any edit-screen state is wider than the pane,
// and no long value on it is cut without the ellipsis that says so.
//
// Watched failing before it was trusted, on both halves. Against the commit
// before the conversion it reported 70 of its 73 states, in every phase. The
// mark half cannot fire against that commit — its cuts were all clampToBox's,
// made after the screen's View — so it was watched against the conversion with
// poEditFieldLines' clip mutated to drop its ellipsis, and reported the order's
// work order drawn as "WO-7Q4K — Rebuild the Bri" with nothing to say it was cut.
func TestPOEditRows_NothingRunsPastThePane(t *testing.T) {
	widths, heights := poEditRowsWidths(), jdePaneHeights()
	panes := 0
	for _, st := range poEditRowsStates(t) {
		var wide, unmarked string
		for _, w := range widths {
			for _, h := range heights {
				s := st.mk()
				s.Update(tea.WindowSizeMsg{Width: w, Height: h})
				panes++
				for _, line := range poEditRowsAboveTheBar(s.View()) {
					if wide == "" && lipgloss.Width(line) > screenBodyCells(w) {
						wide = fmt.Sprintf("at %dx%d %q is %d cells against %d", w, h,
							stripANSI(line), lipgloss.Width(line), screenBodyCells(w))
					}
					if unmarked == "" {
						if cut := poEditRowsUnmarkedCut(line); cut != "" {
							unmarked = fmt.Sprintf("at %dx%d %s: %q", w, h, cut, stripANSI(line))
						}
					}
				}
			}
		}
		if wide != "" {
			t.Errorf("%s: a row runs past the pane's edge — clampToBox takes its tail with no "+
				"mark; %s", st.name, wide)
		}
		if unmarked != "" {
			t.Errorf("%s: a value is cut and reads as complete — %s", st.name, unmarked)
		}
	}
	if panes == 0 {
		t.Fatal("no pane was drawn, so nothing was measured")
	}
}

// poEditRowsPane is a state's pane as Root clips it at w×h, one string per row
// with the styling gone.
func poEditRowsPane(s *PurchaseOrderEditScreen, w, h int) []string {
	s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return strings.Split(stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))), "\n")
}

// TestPOEditRows_TheCostBoxNamesItsDenominator: wherever the line editor's cost
// box is on the pane, the quantity its figure is a total FOR is on the same row.
//
// This is the row that caused a WRONG WRITE. The denominator rode only the hint,
// and at 80 columns the hint was cut off the pane entirely: the row read
// `Total line cost ..... 50.00`, a box that looks like a unit price on a row that
// sends a line TOTAL, and OMS divides that total by the quantity ordered — so a
// unit price of 10.00 typed on a five-line recorded 2.00 a unit. It is asserted
// on the clipped pane at every drawable width and height, and in every focus the
// editor has, because a hint can be folded or windowed away from its box and the
// label cannot.
func TestPOEditRows_TheCostBoxNamesItsDenominator(t *testing.T) {
	const box, label = "15625.00", "Total for 12500 ordered"
	found, from80 := 0, 0
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			for row := 0; row < poLineEditCount; row++ {
				s := poEditRowsScreen(nil)
				s.poEditRowsOpenLine(0)
				s.lineFocus = row
				s.syncLineFocus()
				for _, line := range poEditRowsPane(s, w, h) {
					if !strings.Contains(line, jdeLeader+box) {
						continue
					}
					found++
					if w >= jdeRowsMustHoldFrom {
						from80++
					}
					if !strings.Contains(line, label+jdeLeader) {
						t.Errorf("at %dx%d (focus row %d) the cost box is drawn without the "+
							"quantity it is a total for: %q", w, h, row, line)
					}
				}
			}
		}
	}
	if found == 0 || from80 == 0 {
		t.Fatalf("the cost box was found on %d panes (%d from 80 columns up), so this check "+
			"asserted nothing where it matters", found, from80)
	}
}

// TestPOEditRows_AVoidedLineSaysSoOnItsOwnRow: at every drawable width, the
// voided line's GRID ROW itself carries `[voided]` — whether the fit kept the
// flag column or gave it up, and whether the cursor is on the row or not.
//
// On the row and not under it, because the row is what a window can end on
// (lineGrid gives the reasoning). Being a property of one line, it holds at every
// height by construction, so the walk is over the widths — every one Root draws,
// since the fit's give-order is a function of the pane — and both branches of
// the fit are required to have been reached, or the check was made on one.
func TestPOEditRows_AVoidedLineSaysSoOnItsOwnRow(t *testing.T) {
	const voided = 1 // poEditRowsPO's second line
	dropped, kept := 0, 0
	for _, w := range jdeDrawableWidths() {
		if poFitLineGrid(screenBodyWidth(w), poEditRowsPO(nil).Items).flag {
			kept++
		} else {
			dropped++
		}
		for _, cursor := range []int{0, poEditLineBase + voided} {
			s := poEditRowsScreen(nil)
			s.cursor = cursor
			s.syncFocus()
			rowPrefix := jdeIndent + padCell("2", poGridNumW, alignRight) + "  "
			drawn := false
			for _, line := range poEditRowsPane(s, w, 60) {
				if !strings.HasPrefix(line, rowPrefix) {
					continue
				}
				drawn = true
				if !strings.Contains(line, "[voided]") {
					t.Errorf("at %d columns (cursor %d) the voided line's row reads as a live "+
						"one: %q", w, cursor, line)
				}
			}
			if !drawn {
				t.Errorf("at %d columns (cursor %d) the voided line's row is not on the pane at "+
					"all, so nothing was checked", w, cursor)
			}
		}
	}
	if dropped == 0 || kept == 0 {
		t.Fatalf("the fit dropped the flag column at %d widths and kept it at %d; both "+
			"branches have to be drawn for this to mean anything", dropped, kept)
	}
}

// TestPOEditRows_TheDeleteConfirmSaysTheVoidGoesToo: on a line already voided,
// the delete confirm says that the destroy takes the void record with it — whole,
// wherever it says it at all, and ahead of the line total in the order the
// header gives ground.
//
// The sentence used to be 79 cells written unfolded, so the pane cut it to
// "This line is already voided; deleting removes it": a complete-looking
// sentence without the consequence, on the frame whose next key destroys it.
func TestPOEditRows_TheDeleteConfirmSaysTheVoidGoesToo(t *testing.T) {
	said, drawn := 0, 0
	for _, w := range poEditRowsWidths() {
		for _, h := range jdePaneHeights() {
			s := poEditRowsScreen(boolPtr(true))
			s.poEditRowsOpenLine(1)
			s.lineFocus = poLineRowStatus
			s.openLineRow()
			if s.phase != poEditPhaseDeleteLine {
				t.Fatalf("Ctrl-E on a voided line's status row, on an order the server says may "+
					"lose lines, opened phase %v rather than the delete confirm", s.phase)
			}
			flat := receiveFlat(strings.Join(poEditRowsPane(s, w, h), "\n"))
			if !strings.Contains(flat, "Delete:") {
				continue // refused pane
			}
			drawn++
			hasNote := strings.Contains(flat, poDeleteVoidedNote)
			if hasNote {
				said++
			}
			if !hasNote && strings.Contains(flat, "Deleting also") {
				t.Errorf("at %dx%d the void clause is drawn in part:\n%s", w, h, flat)
			}
			if strings.Contains(flat, "Line total on the order") && !hasNote {
				t.Errorf("at %dx%d the confirm keeps the line total — a fact the line editor "+
					"behind it already shows — and drops the void it is about to destroy:\n%s",
					w, h, flat)
			}
		}
	}
	if said == 0 || drawn == 0 {
		t.Fatalf("the confirm was drawn on %d panes and said the void goes on %d", drawn, said)
	}
}

// TestPOEditRows_TheVoidReasonSaysItIsRequired: wherever the void prompt's Reason
// box is on the pane from 80 columns up, "required" is beside it. The box used to
// keep its declared forty cells and push the hint past the pane, so the first the
// operator heard of the rule was Enter refusing an empty box.
func TestPOEditRows_TheVoidReasonSaysItIsRequired(t *testing.T) {
	boxes := 0
	for _, w := range poEditRowsWidths() {
		for _, h := range jdePaneHeights() {
			s := poEditRowsScreen(boolPtr(false))
			s.poEditRowsOpenLine(0)
			s.lineFocus = poLineRowStatus
			s.openLineRow()
			for _, line := range poEditRowsPane(s, w, h) {
				if !strings.HasPrefix(strings.TrimSpace(line), "Reason"+strings.TrimRight(jdeLeader, " ")) {
					continue
				}
				boxes++
				if !strings.Contains(line, "required") {
					t.Errorf("at %dx%d the Reason box is drawn without saying it is required: %q",
						w, h, line)
				}
			}
		}
	}
	if boxes == 0 {
		t.Fatal("the Reason box was never found on the pane, so nothing was checked")
	}
}
