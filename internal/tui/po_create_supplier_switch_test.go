package tui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// po_create_supplier_switch_test.go — changing the supplier under a half-built
// cart.
//
// resetSupplierScopedPickers closed the stale-catalog hole inside the pickers.
// It did not close it in the CART: a line staged from supplier A carries A's
// item_supplier_id, the backend accepts that id without cross-checking it
// against the order's supplier, and the POST then succeeds against supplier B
// naming an item only A sells. Nothing on the screen flagged it.
//
// Dropping those lines silently would be the worse defect, so the screen warns,
// names the count, and waits. These drive Root.Update against the httptest fake
// and read the CLIPPED pane at 80 columns, per the project's conventions.

// poSwitchCart opens a New PO on supplier 1 and stages two lines: one from the
// catalog (carries an item_supplier_id, so only supplier 1 can fill it) and one
// freeform (carries nothing but words, so anybody can).
func poSwitchCart(t *testing.T) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	return poSwitchCartAt(t, poPaneSizes[len(poPaneSizes)-1])
}

// poSwitchCartAt is the same fixture at an explicit terminal height. The switch
// tests used to run only at 30, which is why a confirm frame that overflowed a
// 24-row terminal passed every one of them.
func poSwitchCartAt(t *testing.T, height int) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	fake := &poPickFake{catalog: 6, pageSize: 5, suppliers: 2}
	r, screen := poPickerAtSize(t, fake, 80, height)

	// A catalog line.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // pick the highlighted row
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // add it (qty prefilled 1)

	// A freeform line.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	r = poType(t, r, "Shop rags")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
	r = poType(t, r, "2")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
	r = poType(t, r, "4.00")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if len(screen.lines) != 2 {
		t.Fatalf("setup staged %d line(s), want 2", len(screen.lines))
	}
	if screen.supplierScopedLineCount() != 1 {
		t.Fatalf("setup staged %d supplier-scoped line(s), want 1", screen.supplierScopedLineCount())
	}
	return r, screen
}

// poOpenSwitchConfirm walks from the source chooser to the confirm frame by
// highlighting the OTHER supplier and pressing enter.
func poOpenSwitchConfirm(t *testing.T, r Root, screen *PurchaseOrderCreateScreen) Root {
	t.Helper()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}) // → supplier picker
	if screen.phase != poPhaseSupplier {
		t.Fatalf("b from the source chooser landed in phase %v", screen.phase)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != poPhaseSupplierSwitch {
		t.Fatalf("committing another supplier over a staged catalog line went straight through (phase %v)", screen.phase)
	}
	return r
}

// TestPOSupplierSwitch_WarnsBeforeDroppingStagedLines: the operator is told what
// will go and how much of it, BEFORE anything goes.
func TestPOSupplierSwitch_WarnsBeforeDroppingStagedLines(t *testing.T) {
	for _, height := range poPaneSizes {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			poAssertSwitchConfirmWarns(t, height)
		})
	}
}

func poAssertSwitchConfirmWarns(t *testing.T, height int) {
	t.Helper()
	r, screen := poSwitchCartAt(t, height)
	r = poOpenSwitchConfirm(t, r, screen)

	if len(screen.lines) != 2 {
		t.Errorf("the confirm frame has already dropped lines (%d left)", len(screen.lines))
	}
	if screen.supplierID != 1 {
		t.Errorf("the confirm frame has already switched supplier (now %d)", screen.supplierID)
	}

	// It names the count, whose lines they are, and that the rest survive.
	poWantPaneLine(t, screen, "Changing supplier drops part of the cart")
	poWantPaneLine(t, screen, "1 of 2 staged line(s)")
	poWantPaneLine(t, screen, "Acme Supply")
	poWantPaneLine(t, screen, "line(s) stay.")
	poAssertFits(t, "supplier switch confirm", screen)
	if out := r.View(); !strings.Contains(out, "staged line(s) belong to") {
		t.Errorf("nothing on the status bar says why the commit stopped:\n%s", out)
	}
}

// TestPOSupplierSwitch_NamesExactlyTheKeysThatWork: the confirm is subject to
// the same bar-honesty rule as everything else this change touched. Both
// directions — the two keys it names act, and every other key the surrounding
// screens bind does nothing here.
func TestPOSupplierSwitch_NamesExactlyTheKeysThatWork(t *testing.T) {
	r, screen := poSwitchCart(t)
	r = poOpenSwitchConfirm(t, r, screen)

	poWantPaneLine(t, screen, "ctrl+x drops 1 line(s) and switches")
	poWantPaneLine(t, screen, "esc keeps the cart and this supplier")

	// enter is deliberately NOT named: it is the key that opened this frame, so
	// a reflexive double-tap must not be the destructive answer.
	for _, line := range poPaneLines(t, screen) {
		if strings.Contains(line, "enter") {
			t.Errorf("the confirm frame names enter, which does nothing here:\n%s", line)
		}
	}

	// Unnamed keys must be inert — the cart, the supplier and the phase all
	// stay exactly where they are.
	before := screen.phase
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyTab},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyRunes, Runes: []rune("k")},
		{Type: tea.KeyRunes, Runes: []rune("b")},
		{Type: tea.KeyRunes, Runes: []rune("d")},
		{Type: tea.KeyRunes, Runes: []rune("x")},
	} {
		r = key(t, r, k)
		if screen.phase != before {
			t.Fatalf("%v moved off the confirm frame (phase %v)", k, screen.phase)
		}
		if len(screen.lines) != 2 || screen.supplierID != 1 {
			t.Fatalf("%v changed the cart (%d lines) or the supplier (%d)", k, len(screen.lines), screen.supplierID)
		}
	}
}

// TestPOSupplierSwitch_EscKeepsBothTheCartAndTheSupplier is the decline path:
// an operator who did not mean it loses nothing at all.
func TestPOSupplierSwitch_EscKeepsBothTheCartAndTheSupplier(t *testing.T) {
	r, screen := poSwitchCart(t)
	r = poOpenSwitchConfirm(t, r, screen)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	if screen.phase != poPhaseSupplier {
		t.Fatalf("esc landed in phase %v, want back on the supplier picker", screen.phase)
	}
	if screen.supplierID != 1 {
		t.Errorf("esc switched the supplier anyway (now %d)", screen.supplierID)
	}
	if len(screen.lines) != 2 {
		t.Errorf("esc dropped %d line(s) it promised to keep", 2-len(screen.lines))
	}
	if out := r.View(); !strings.Contains(out, "kept the cart") {
		t.Errorf("declining said nothing:\n%s", out)
	}

	// The order is still buildable from where it was left.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	_ = r
}

// TestPOSupplierSwitch_ConfirmDropsOnlyTheSupplierScopedLines: accepting drops
// the catalog lines and nothing else, and lands the operator on the new
// supplier's source chooser.
func TestPOSupplierSwitch_ConfirmDropsOnlyTheSupplierScopedLines(t *testing.T) {
	r, screen := poSwitchCart(t)
	r = poOpenSwitchConfirm(t, r, screen)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	if screen.supplierID != 2 {
		t.Fatalf("the switch did not happen (supplier %d)", screen.supplierID)
	}
	if screen.phase != poPhaseSource {
		t.Fatalf("landed in phase %v, want the source chooser for the new supplier", screen.phase)
	}
	if len(screen.lines) != 1 {
		t.Fatalf("cart holds %d line(s), want only the freeform one", len(screen.lines))
	}
	kept := screen.lines[0]
	if kept.item.ItemSupplierID != nil {
		t.Errorf("a line naming item-supplier #%d survived onto another supplier's order", *kept.item.ItemSupplierID)
	}
	if kept.item.Description != "Shop rags" {
		t.Errorf("kept %q, want the supplier-agnostic freeform line", kept.item.Description)
	}
	if out := r.View(); !strings.Contains(out, "dropped 1 line(s)") {
		t.Errorf("the drop was not reported:\n%s", out)
	}
}

// TestPOSupplierSwitch_AssetLinesAreSupplierAgnostic: an asset id names a piece
// of equipment, not a (item, supplier) pair — PurchaseOrderCreateItem couples
// only item_supplier_id to a supplier, and the asset picker's own scoping is
// ?manufacturer=, i.e. who BUILT the machine. Buying a part for it from someone
// else is ordinary, so an asset line must survive the switch.
func TestPOSupplierSwitch_AssetLinesAreSupplierAgnostic(t *testing.T) {
	fake := &poPickFake{catalog: 6, pageSize: 5, assets: 2, suppliers: 2}
	r, screen := poPickerAt(t, fake, 80)

	// One asset line and one catalog line.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // pick the highlighted asset
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})   // desc → qty
	r = poType(t, r, "1")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab}) // qty → cost
	r = poType(t, r, "12.00")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(screen.lines) != 2 {
		t.Fatalf("setup staged %d line(s), want 2", len(screen.lines))
	}

	r = poOpenSwitchConfirm(t, r, screen)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	if len(screen.lines) != 1 {
		t.Fatalf("cart holds %d line(s), want the asset line alone", len(screen.lines))
	}
	if screen.lines[0].item.AssetID == nil {
		t.Errorf("the surviving line is not the asset line: %+v", screen.lines[0].item)
	}
}

// TestPOSupplierSwitch_NoConfirmWhenThereIsNothingToLose: the confirm is a cost
// paid only when there is something to protect. An empty cart, or one holding
// nothing but supplier-agnostic lines, commits in the single keypress it always
// took — and re-committing the SAME supplier never asks at all.
func TestPOSupplierSwitch_NoConfirmWhenThereIsNothingToLose(t *testing.T) {
	t.Run("empty cart", func(t *testing.T) {
		fake := &poPickFake{catalog: 6, pageSize: 5, suppliers: 2}
		r, screen := poPickerAt(t, fake, 80)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.phase != poPhaseSource || screen.supplierID != 2 {
			t.Fatalf("an empty cart still asked (phase %v, supplier %d)", screen.phase, screen.supplierID)
		}
	})

	t.Run("freeform lines only", func(t *testing.T) {
		fake := &poPickFake{catalog: 6, pageSize: 5, suppliers: 2}
		r, screen := poPickerAt(t, fake, 80)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
		r = poType(t, r, "Shop rags")
		r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
		r = poType(t, r, "2")
		r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
		r = poType(t, r, "4.00")
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.phase != poPhaseSource || screen.supplierID != 2 {
			t.Fatalf("a supplier-agnostic cart still asked (phase %v, supplier %d)", screen.phase, screen.supplierID)
		}
		if len(screen.lines) != 1 {
			t.Errorf("the freeform line did not survive an unremarkable switch")
		}
	})

	t.Run("same supplier re-committed", func(t *testing.T) {
		r, screen := poSwitchCart(t)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // cursor still on supplier 1
		if screen.phase != poPhaseSource {
			t.Fatalf("re-committing the same supplier asked to drop the cart (phase %v)", screen.phase)
		}
		if len(screen.lines) != 2 {
			t.Errorf("re-committing the same supplier lost %d line(s)", 2-len(screen.lines))
		}
	})
}

// TestPOReview_ASmallCartLeavesTheNotesFieldOnThePane is the same rule from the
// other end, and the end that actually broke: a SHORT cart is where
// bodyRowBudget's floor of three rows has room to overreach. Five lines, three
// optional rows repeated in the tail and one line priced from the catalog left
// the phase asking for three cart rows out of the one it had, and the focused
// PO-notes input is what clampToBox took off the bottom.
func TestPOReview_ASmallCartLeavesTheNotesFieldOnThePane(t *testing.T) {
	for _, termHeight := range []int{24, 30} {
		t.Run(fmt.Sprintf("height %d", termHeight), func(t *testing.T) {
			fake := &poPickFake{catalog: 40, pageSize: 40,
				agreements: 1, workOrders: 2, committees: 1}
			r, screen := poPickerAtSize(t, fake, 80, termHeight)

			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
			for i := 0; i < 4; i++ {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
			}
			r = poStageCostlessLine(t, r, screen)
			if len(screen.lines) != 5 {
				t.Fatalf("setup staged %d line(s), want 5", len(screen.lines))
			}
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			if screen.phase != poPhaseReview {
				t.Fatalf("d did not open the review cart (phase %v)", screen.phase)
			}

			pane := clampToBox(screen.View(), screenBodyWidth(80), screenBodyHeight(termHeight))
			for _, want := range []string{"PO notes", "Cart (5 line(s))", "priced from the supplier catalog"} {
				if !strings.Contains(pane, want) {
					t.Errorf("the %d-row pane does not carry %q:\n%s",
						screenBodyHeight(termHeight), want, pane)
				}
			}
			if !strings.Contains(pane, fmt.Sprintf("▸ %d)", screen.reviewCursor+1)) {
				t.Errorf("the highlighted line is off the pane:\n%s", pane)
			}
			poAssertFits(t, fmt.Sprintf("review with a 5-line cart at 80x%d", termHeight), screen)
		})
	}
}

// TestPOCart_TheRowsReservedAreTheRowsDrawn pins the reservation to the render.
// cartRowBudget takes the cart's non-line rows off the top, and that count was
// the constant 5 — one per item — while the catalog-pricing caveat, once it
// went through the folder at 63 cells, drew TWO. A reservation that is a row
// short is the horizontal-cut-traded-for-a-vertical-one this screen has been
// bitten by three times, so the two are measured against each other here rather
// than kept in step by hand.
func TestPOCart_TheRowsReservedAreTheRowsDrawn(t *testing.T) {
	id := 7
	cost := 3.0
	for _, costless := range []bool{false, true} {
		t.Run(fmt.Sprintf("catalog-priced line=%v", costless), func(t *testing.T) {
			s := NewPurchaseOrderCreateScreen(Deps{})
			s.terminalHeight = 30
			for i := 0; i < 8; i++ {
				it := omsapi.PurchaseOrderCreateItem{ItemSupplierID: &id, Quantity: 1, UnitCost: &cost}
				if costless && i == 0 {
					it.UnitCost = nil
				}
				s.lines = append(s.lines, poCartLine{item: it, label: fmt.Sprintf("Widget %d", i+1)})
			}
			// A window in the MIDDLE, so both scroll markers are drawn and the
			// chrome is at its widest.
			const budget = 2
			s.reviewCursor = 4
			drawn := poRenderedRows(s.renderCart(s.reviewCursor, budget))
			if got, want := drawn-budget, s.cartChromeRows(); got != want {
				t.Errorf("renderCart drew %d rows that are not lines; the budget reserves %d", got, want)
			}
		})
	}
}

// TestPOReview_NotesInputStaysOnThePaneUnderALongCart: clampToBox drops rows
// from the BOTTOM, and the review phase draws the focused PO-notes input last.
// A cart long enough to fill the pane therefore took the field the operator is
// typing into off the screen with it — and folding the help line onto three
// rows made a 15-line cart, which the 'a' add-ALL flow produces routinely,
// enough to do it.
//
// The second fixture adds the three optional rows the review tail REPEATS
// beside the cart — the agreement and the two associations. With a
// catalog-priced line folding the caveat onto two rows they left the cart no
// line at all at 80x24 and it was the notes field that fell off the bottom, so
// those repeats now yield to it and say they have.
func TestPOReview_NotesInputStaysOnThePaneUnderALongCart(t *testing.T) {
	fakes := map[string]func() *poPickFake{
		"plain supplier": func() *poPickFake {
			return &poPickFake{catalog: 40, pageSize: 40}
		},
		"supplier with agreement, work orders and committees": func() *poPickFake {
			return &poPickFake{catalog: 40, pageSize: 40,
				agreements: 1, workOrders: 2, committees: 1}
		},
	}
	for name, newFake := range fakes {
		for _, termHeight := range []int{24, 30} {
			t.Run(fmt.Sprintf("%s at height %d", name, termHeight), func(t *testing.T) {
				fake := newFake()
				srv := httptest.NewServer(fake.handler())
				t.Cleanup(srv.Close)

				deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
				screen := NewPurchaseOrderCreateScreen(deps)
				r := newTestRoot(screen)
				r.deps = deps
				next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: termHeight})
				r = next.(Root)
				r = pump(t, r, screen.Init(), 0)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // commit the supplier

				// Fifteen catalog lines, the size the reorder add-ALL flow is sized
				// against, and the last of them staged with its cost CLEARED so the
				// "priced from the supplier catalog" caveat really is on the frame.
				// It never was: every fixture row carries unit_cost "3.50" and the
				// line form prefills from it, so noCost was 0 and the two rows that
				// caveat folds onto were never in any measurement this test made.
				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
				for i := 0; i < 14; i++ {
					r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // pick highlighted
					r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // add (qty prefilled)
					r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
				}
				r = poStageCostlessLine(t, r, screen)
				if len(screen.lines) != 15 {
					t.Fatalf("setup staged %d line(s), want 15", len(screen.lines))
				}
				if _, noCost := poCartTotal(screen.lines); noCost != 1 {
					t.Fatalf("setup left %d catalog-priced line(s), want 1", noCost)
				}

				r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
				if screen.phase != poPhaseReview {
					t.Fatalf("d did not open the review cart (phase %v)", screen.phase)
				}

				pane := clampToBox(screen.View(), screenBodyWidth(80), screenBodyHeight(termHeight))
				if !strings.Contains(pane, "PO notes") {
					t.Errorf("the focused PO-notes field is off the %d-row pane:\n%s",
						screenBodyHeight(termHeight), pane)
				}
				// Typing has to land somewhere the operator can see it.
				r = poType(t, r, "rush")
				pane = clampToBox(screen.View(), screenBodyWidth(80), screenBodyHeight(termHeight))
				if !strings.Contains(pane, "rush") {
					t.Errorf("what the operator typed is not on the pane:\n%s", pane)
				}
				// And the cart says how much of itself is out of view rather than
				// silently showing a subset.
				if !strings.Contains(pane, "Cart (15 line(s))") {
					t.Errorf("the cart no longer says how many lines it holds:\n%s", pane)
				}
				shown := strings.Count(pane, ") Widget ")
				if shown < 15 && !strings.Contains(pane, "more below") && !strings.Contains(pane, "more above") {
					t.Errorf("the cart shows %d of 15 lines and says nothing about the rest:\n%s", shown, pane)
				}
				// The caveat is the reason the total is a floor rather than the
				// order's value, so it has to be readable and not merely reserved
				// for: a row count kept in step by hand had it at one row while the
				// folder was giving it two.
				if !strings.Contains(pane, "priced from the supplier catalog") {
					t.Errorf("the caveat that says the total is a floor is off the pane:\n%s", pane)
				}
				// Nothing may be cut silently, whatever had to give: the repeated
				// attribution rows either read out in full or say they were left to
				// the header they repeat.
				if !screen.reviewAttributionShown() {
					if !strings.Contains(pane, "agreement / association rows need more height") {
						t.Errorf("the repeated agreement / association rows vanished with nothing saying so:\n%s", pane)
					}
				}
				poAssertFits(t, fmt.Sprintf("review with a 15-line cart at 80x%d", termHeight), screen)
			})
		}
	}
}
