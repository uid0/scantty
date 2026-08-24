package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Drive of the captain's own sentence — "I type in the SKU, it shows the item,
// gives me the ability to confirm the item from the supplier, and then it
// prompts me for the quantity and the price. From there, it adds the item in a
// new line" — pumped through Root.Update against a stateful httptest fake,
// because no live OMS is reachable from a task worktree (AGENTS.md).
//
// Root-level rather than screen-level on purpose: the flow is opened with a
// bare `n` from the purchase-order sheet, and only WantsRawInput keeps the
// letters the identifier row is typed with away from the root's dispatcher. A
// screen-level test would pass while the real app did something else with them.

// ---------------------------------------------------------------------------
// The fake
// ---------------------------------------------------------------------------

// poAddCatalogRow is one row of the fake supplier catalogue. The fake resolves
// an identifier the way the server does — exact identifiers beat partial name
// matches, and the answer is obvious only when the strongest tier that matched
// holds exactly one row — because the whole point of this flow is that ScanTTY
// asks rather than guesses.
type poAddCatalogRow struct {
	itemSupplier int
	name         string
	sku          string
	supplierSKU  string
	unitUPC      string
	perPackage   int
	suggestQty   int
	suggestCost  string
	isKit        bool
	// crossVendor marks a row the identifier reached through ANOTHER vendor's
	// listing — the server's weakest tier, whose label names that vendor.
	crossVendor string
	// onOrder, when non-zero, is the quantity this order already carries for the
	// row, which turns an add into a grow.
	onOrder     int
	onOrderID   string
	onOrderVoid bool
	linePrice   string
}

type poAddFake struct {
	mu      sync.Mutex
	rows    []poAddCatalogRow
	status  string
	adds    []map[string]any
	lookups []string
	// refuse, when set, is the refusal body the next add answers with.
	refuse string
	// refuseStatus is the HTTP status that refusal carries.
	refuseStatus int
	// unavailable is what the lookup reports when nothing orderable matched:
	// items the identifier really does name that this order cannot carry.
	unavailable []map[string]any
	// fail makes the lookup answer with a gateway page instead of an answer.
	fail bool
}

func (f *poAddFake) order() map[string]any {
	items := []any{}
	for _, r := range f.rows {
		if r.onOrder == 0 {
			continue
		}
		items = append(items, map[string]any{
			"id": r.onOrderID, "quantity_ordered": r.onOrder,
			"unit_cost_ordered": r.linePrice, "is_voided": r.onOrderVoid,
			"item_details": map[string]any{"name": r.name},
		})
	}
	status := f.status
	if status == "" {
		status = "draft"
	}
	return map[string]any{
		"id": "po-1", "po_number": "PO-2026-0042", "status": status,
		"status_label":     strings.ToUpper(status[:1]) + status[1:],
		"supplier_details": "Acme Fasteners & Industrial Supply Co.",
		"items":            items,
	}
}

// tier mimics the server's ladder closely enough to answer the only question
// this screen asks of it: 0 for an exact identifier, 1 for a partial name, 2
// for another vendor's listing, and -1 for no match at all.
func (r poAddCatalogRow) tier(q string) int {
	eq := func(v string) bool { return v != "" && strings.EqualFold(v, q) }
	if eq(r.supplierSKU) || eq(r.sku) || eq(r.unitUPC) || eq(r.name) {
		if r.crossVendor != "" {
			return 2
		}
		return 0
	}
	if r.name != "" && strings.Contains(strings.ToLower(r.name), strings.ToLower(q)) {
		return 1
	}
	return -1
}

func (f *poAddFake) candidate(r poAddCatalogRow, tier int) map[string]any {
	label := map[int]string{0: "supplier SKU", 1: "item name (partial)"}[tier]
	kind := map[int]string{0: "vendor_sku", 1: "partial_item_name"}[tier]
	if tier == 2 {
		label = "another supplier's listing (" + r.crossVendor + ")"
		kind = omsapi.POLineMatchOtherSupplier
	}
	c := map[string]any{
		"item_supplier": r.itemSupplier, "match_kind": kind, "match_label": label,
		"matched_value": r.supplierSKU, "is_exact": tier == 0,
		"item": map[string]any{"id": fmt.Sprintf("i%d", r.itemSupplier),
			"name": r.name, "sku": r.sku, "is_kit": r.isKit},
		"supplier_sku": r.supplierSKU, "unit_upc": r.unitUPC,
		"quantity_per_package": r.perPackage,
		"suggested_quantity":   r.suggestQty,
		"suggested_unit_cost":  r.suggestCost,
	}
	if r.onOrder > 0 {
		existing := map[string]any{
			"line_item": r.onOrderID, "quantity_ordered": r.onOrder, "is_voided": r.onOrderVoid,
		}
		if r.onOrderVoid {
			existing["repeat_increment"] = nil
			existing["quantity_ordered_after"] = nil
		} else {
			existing["repeat_increment"] = r.perPackage
			existing["quantity_ordered_after"] = r.onOrder + r.perPackage
		}
		c["already_on_order"] = existing
	}
	return c
}

func (f *poAddFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/item-lookup/"):
			q := strings.TrimSpace(r.URL.Query().Get("q"))
			f.lookups = append(f.lookups, q)
			if f.fail {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, "<!DOCTYPE html><html><head><title>502</title></head>"+
					strings.Repeat("<p>the gateway is unwell</p>", 40)+"</html>")
				return
			}
			best, cands := 99, []any{}
			for _, row := range f.rows {
				if t := row.tier(q); t >= 0 && t < best {
					best = t
				}
			}
			bestTotal := 0
			for _, row := range f.rows {
				t := row.tier(q)
				if t < 0 {
					continue
				}
				cands = append(cands, f.candidate(row, t))
				if t == best {
					bestTotal++
				}
			}
			status := f.status
			if status == "" {
				status = "draft"
			}
			un := f.unavailable
			if len(cands) > 0 {
				un = nil
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"query":    q,
				"supplier": map[string]any{"id": 3, "name": "Acme Fasteners & Industrial Supply Co."},
				"purchase_order": map[string]any{"id": "po-1", "po_number": "PO-2026-0042",
					"status": status, "can_add_items": status == "draft"},
				"best_match_kind":       "vendor_sku",
				"resolves":              bestTotal == 1,
				"candidates":            cands,
				"total_candidates":      len(cands),
				"best_match_total":      bestTotal,
				"truncated":             false,
				"unavailable":           un,
				"total_unavailable":     len(un),
				"unavailable_truncated": false,
			})

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/items/"):
			body := map[string]any{}
			if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			f.adds = append(f.adds, body)
			if f.refuse != "" {
				code := f.refuseStatus
				if code == 0 {
					code = http.StatusBadRequest
				}
				w.WriteHeader(code)
				_, _ = io.WriteString(w, f.refuse)
				return
			}
			id := 0
			if v, ok := body["item_supplier"].(float64); ok {
				id = int(v)
			}
			created := true
			qty, cost := 0, ""
			for i := range f.rows {
				row := &f.rows[i]
				if row.itemSupplier != id {
					continue
				}
				qty, cost = row.suggestQty, row.suggestCost
				if v, ok := body["quantity"].(float64); ok {
					qty = int(v)
				}
				if v, ok := body["unit_cost"].(string); ok {
					cost = v
				}
				if row.onOrder > 0 {
					created = false
					row.onOrder += qty
					qty = row.onOrder
					if _, ok := body["unit_cost"]; !ok {
						cost = row.linePrice
					}
					row.linePrice = cost
				} else {
					row.onOrder = qty
					row.onOrderID = fmt.Sprintf("line-%d", row.itemSupplier)
					row.linePrice = cost
				}
			}
			status := http.StatusCreated
			if !created {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": created,
				"line_item": map[string]any{"id": fmt.Sprintf("line-%d", id),
					"quantity_ordered": qty, "unit_cost_ordered": cost},
				"purchase_order": f.order(),
			})

		default:
			_ = json.NewEncoder(w).Encode(f.order())
		}
	}
}

// poAddRows is the fixture catalogue. The first name is DELIBERATELY long — a
// real supplier catalogue is full of them, and every picker fixture in this
// package used to draw "Widget 1", so no test had ever rendered one of these
// rows at the length OMS actually carries.
func poAddRows() []poAddCatalogRow {
	return []poAddCatalogRow{
		{itemSupplier: 12, name: "Widget bracket, zinc-plated, heavy duty, 12-hole, left-hand",
			sku: "WB-1200", supplierSKU: "AF-99-12-ZP-LH-HEAVY", unitUPC: "0123456789012",
			perPackage: 25, suggestQty: 50, suggestCost: "4.5000"},
		{itemSupplier: 13, name: "Widget clamp", sku: "WC-1", supplierSKU: "AF-77",
			perPackage: 1, suggestQty: 4, suggestCost: "1.2500"},
		{itemSupplier: 14, name: "Bolt, hex M3", sku: "B-M3", supplierSKU: "AF-31",
			perPackage: 100, suggestQty: 100, suggestCost: "0.0000"},
	}
}

// poAddAt opens the add-line flow the way an operator does: a loaded draft PO
// sheet, then `n`. It returns the Root, the flow screen and the fake.
func poAddAt(t *testing.T, fake *poAddFake, width, height int) (Root, *PurchaseOrderAddLineScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewPurchaseOrderDetailScreen(deps, "po-1")
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
	r = next.(Root)
	r = pump(t, r, detail.Init(), 0)

	r = key(t, r, poRuneKey("n"))
	screen, ok := r.screen.(*PurchaseOrderAddLineScreen)
	if !ok {
		t.Fatalf("`n` on a draft PO sheet landed on %T, not the add-line flow", r.screen)
	}
	return r, screen
}

// poAddPane is the CLIPPED render — what the terminal actually shows after
// Root.View has clamped the frame to the content box. Asserting the screen's
// own View() would pass over a row the pane cut.
func poAddPane(r Root) string { return r.View() }

func poAddWantPane(t *testing.T, r Root, want string) {
	t.Helper()
	if !strings.Contains(poAddPane(r), want) {
		t.Errorf("the clipped pane does not carry %q:\n%s", want, poAddPane(r))
	}
}

func poAddRejectPane(t *testing.T, r Root, unwanted string) {
	t.Helper()
	if strings.Contains(poAddPane(r), unwanted) {
		t.Errorf("the clipped pane still carries %q:\n%s", unwanted, poAddPane(r))
	}
}

// ---------------------------------------------------------------------------
// The captain's flow, end to end
// ---------------------------------------------------------------------------

func TestPOAddLine_ScanningASupplierSKUReachesAConfirmedLine(t *testing.T) {
	fake := &poAddFake{rows: poAddRows()}
	r, s := poAddAt(t, fake, 80, 24)

	// A scanner is a keyboard: one burst of characters, then Enter.
	r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != poAddPhaseConfirm {
		t.Fatalf("an exact supplier SKU landed on phase %v, want the confirm frame", s.phase)
	}
	// It shows the item, and who supplies it — enough identity to say yes.
	poAddWantPane(t, r, "Widget bracket")
	poAddWantPane(t, r, "Acme Fasteners")
	poAddWantPane(t, r, "supplier SKU")
	poAddWantPane(t, r, "Enter=Quantity & price")

	// …then it prompts for the quantity and the price, prefilled.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhasePrice {
		t.Fatalf("enter on the confirm frame landed on phase %v, want the price prompt", s.phase)
	}
	if got := s.qtyIn.Value(); got != "50" {
		t.Errorf("quantity prefill = %q, want the server's own suggestion 50", got)
	}
	if got := s.costIn.Value(); got != "4.5000" {
		t.Errorf("unit cost prefill = %q, want the server's own suggestion 4.5000", got)
	}
	poAddWantPane(t, r, "Enter=Add line")

	// One keystroke accepts both.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.adds) != 1 {
		t.Fatalf("adds = %d, want exactly one POST", len(fake.adds))
	}
	if fake.adds[0]["item_supplier"] != float64(12) {
		t.Errorf("the add named %v — it must post the row the operator confirmed, never re-resolve the identifier",
			fake.adds[0])
	}
	if _, reResolved := fake.adds[0]["identifier"]; reResolved {
		t.Errorf("the add re-posted the identifier: %v", fake.adds[0])
	}
	if s.phase != poAddPhaseIdentify {
		t.Fatalf("after the add the flow rests on phase %v, want the identifier row for the next scan", s.phase)
	}
	if s.idIn.Value() != "" {
		t.Errorf("the identifier box still holds %q — the next scan would append to it", s.idIn.Value())
	}
	if len(s.added) != 1 || !s.added[0].created {
		t.Fatalf("the tally recorded %+v", s.added)
	}
	poAddWantPane(t, r, "Added this visit: 1")
	poAddWantPane(t, r, "added 50")
}

// The prompt is OVERTYPABLE, and what the operator typed is what is posted.
func TestPOAddLine_TypingOverTheDefaultsSendsWhatWasTyped(t *testing.T) {
	fake := &poAddFake{rows: poAddRows()}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	// Clear the quantity and type another.
	for range "50" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = key(t, r, poRuneKey("7"))
	// Move to the price row and replace it.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	if s.priceFocus != poAddFieldCost {
		t.Fatalf("down left the caret on row %d", s.priceFocus)
	}
	for range "4.5000" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = key(t, r, poRuneKey("9.99"))

	// The line total is the figure being approved, and it is exact.
	poAddWantPane(t, r, "69.93")

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.adds) != 1 {
		t.Fatalf("adds = %d", len(fake.adds))
	}
	if fake.adds[0]["quantity"] != float64(7) || fake.adds[0]["unit_cost"] != "9.99" {
		t.Errorf("posted %v, want the 7 @ 9.99 the operator typed", fake.adds[0])
	}
}

// Genuine ambiguity presents the candidates and lets the operator pick; an
// exact match does not. Both halves, because collapsing either one is how a
// wrong item ends up on a purchase order.
func TestPOAddLine_AmbiguityAsksAndAnExactMatchDoesNot(t *testing.T) {
	fake := &poAddFake{rows: poAddRows()}
	r, s := poAddAt(t, fake, 80, 24)

	// "widget" is a partial name on two rows: the server cannot narrow it.
	r = key(t, r, poRuneKey("widget"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseChoose {
		t.Fatalf("an ambiguous identifier landed on phase %v, want the choice list", s.phase)
	}
	poAddWantPane(t, r, "Which one?")
	poAddWantPane(t, r, "matches 2 items")
	poAddWantPane(t, r, "Enter=Choose")

	// The cursor moves and the pane says so.
	before := poAddPane(r)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	if poAddPane(r) == before {
		t.Error("down redrew a byte-for-byte identical pane on the choice list")
	}
	if s.cursor != 1 {
		t.Fatalf("cursor = %d", s.cursor)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseConfirm || s.chosen.ItemSupplier != 13 {
		t.Fatalf("enter on the second row staged %+v at phase %v", s.chosen, s.phase)
	}

	// Esc goes back to the list the operator picked from, not out of the flow.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != poAddPhaseChoose {
		t.Fatalf("esc from a confirm reached through the list landed on %v", s.phase)
	}
}

// A rival vendor's code still finds the item, and the provenance is on the
// frame — nothing is silently substituted.
func TestPOAddLine_CrossVendorProvenanceIsOnTheConfirmFrame(t *testing.T) {
	rows := poAddRows()
	rows[0].crossVendor = "Globex Industrial"
	fake := &poAddFake{rows: rows}
	r, s := poAddAt(t, fake, 80, 24)

	r = key(t, r, poRuneKey("0123456789012"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseConfirm {
		t.Fatalf("phase = %v", s.phase)
	}
	poAddWantPane(t, r, "Globex Industrial")
	// And the caveat spelling out that nothing was swapped is reachable — the
	// frame scrolls rather than losing its tail. One WORD is what is looked for,
	// because the sentence is folded to the pane and any phrase can straddle a
	// fold; the fold itself is the layer's job and is asserted elsewhere.
	if !poAddSeenWhileScrolling(t, r, s, "substituted") {
		t.Errorf("the cross-vendor caveat is not reachable on the confirm frame:\n%s", poAddPane(r))
	}
}

// poAddSeenWhileScrolling pages the confirm frame from the top and reports
// whether `want` ever lands in the CLIPPED render.
func poAddSeenWhileScrolling(t *testing.T, r Root, s *PurchaseOrderAddLineScreen, want string) bool {
	t.Helper()
	s.scroll = 0
	for i := 0; i <= s.confirmBody().Len(); i++ {
		if strings.Contains(poAddPane(r), want) {
			return true
		}
		before := s.scroll
		r = key(t, r, tea.KeyMsg{Type: tea.KeyPgDown})
		if s.scroll == before {
			break
		}
	}
	return strings.Contains(poAddPane(r), want)
}

// A refusal shows the SERVER's reason — not a raw JSON dump, not a hang — and
// hands the operator back everything they typed.
func TestPOAddLine_ARefusalShowsTheServersReasonAndKeepsTheEntry(t *testing.T) {
	fake := &poAddFake{rows: poAddRows(),
		refuse: `{"error": "Acme Fasteners no longer supplies Widget bracket (marked discontinued).", "code": "discontinued"}`}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("AF-77"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	for range "4" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = key(t, r, poRuneKey("6"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != poAddPhasePrice {
		t.Fatalf("a refusal left the flow on phase %v, want the price prompt it came from", s.phase)
	}
	if s.pending {
		t.Error("the screen is still frozen after the refusal came back")
	}
	poAddWantPane(t, r, "no longer supplies")
	poAddRejectPane(t, r, `"code"`)
	poAddRejectPane(t, r, "oms: http 400")
	if got := s.qtyIn.Value(); got != "6" {
		t.Errorf("quantity = %q — a refusal must not throw away what was typed", got)
	}
	// And the bar still names a key that works.
	poAddWantPane(t, r, "Enter=Add line")
}

// "Found nothing" and "could not tell" are different facts and the operator
// acts differently on each. They must never share a sentence.
func TestPOAddLine_FoundNothingIsNotCouldNotTell(t *testing.T) {
	found := &poAddFake{rows: poAddRows()}
	r, s := poAddAt(t, found, 80, 24)
	r = key(t, r, poRuneKey("hovercraft"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseIdentify {
		t.Fatalf("phase = %v", s.phase)
	}
	poAddWantPane(t, r, "nothing matching")
	nothing := poAddPane(r)

	cannot := &poAddFake{rows: poAddRows(), fail: true}
	r2, s2 := poAddAt(t, cannot, 80, 24)
	r2 = key(t, r2, poRuneKey("hovercraft"))
	r2 = key(t, r2, tea.KeyMsg{Type: tea.KeyEnter})
	if s2.phase != poAddPhaseIdentify {
		t.Fatalf("phase = %v", s2.phase)
	}
	poAddWantPane(t, r2, "could not tell")
	poAddRejectPane(t, r2, "nothing matching")
	if poAddPane(r2) == nothing {
		t.Error("a failed lookup and an empty one drew the same pane")
	}
	// The gateway's whole HTML page must not have run off the pane or wedged it.
	poAddAssertFits(t, "failed lookup", r2, 80)
}

// An identifier that names items this order cannot carry is a THIRD fact, and
// the server's own explanation is what the operator needs.
func TestPOAddLine_AnUnavailableItemIsExplained(t *testing.T) {
	fake := &poAddFake{rows: nil, unavailable: []map[string]any{{
		"item":    map[string]any{"id": "i9", "name": "Widget bracket", "sku": "WB-1200"},
		"reason":  "not_supplied",
		"message": "Acme Fasteners does not supply Widget bracket — that item is supplied by Globex.",
	}}}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("WB-1200"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseIdentify {
		t.Fatalf("phase = %v", s.phase)
	}
	poAddWantPane(t, r, "does not supply")
	poAddRejectPane(t, r, "nothing matching")
}

// A repeat add GROWS the line already there and leaves its price alone. The
// prompt must not prefill the fresh-line price and post it, which would reprice
// a line the operator only meant to add one more box to.
func TestPOAddLine_ARepeatAddDoesNotSilentlyReprice(t *testing.T) {
	rows := poAddRows()
	rows[0].onOrder = 5
	rows[0].onOrderID = "line-12"
	rows[0].linePrice = "3.1000"
	fake := &poAddFake{rows: rows}
	r, s := poAddAt(t, fake, 80, 24)

	r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	poAddWantPane(t, r, "5 ordered")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if got := s.qtyIn.Value(); got != "25" {
		t.Errorf("quantity prefill = %q, want the one-package repeat increment 25", got)
	}
	if got := s.costIn.Value(); got != "" {
		t.Errorf("unit cost prefill = %q — a repeat must start blank so the line's price is kept", got)
	}
	// And the row SAYS what blank does, naming the price the line carries.
	poAddWantPane(t, r, "blank keeps 3.1000")

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.adds) != 1 {
		t.Fatalf("adds = %d", len(fake.adds))
	}
	if _, sent := fake.adds[0]["unit_cost"]; sent {
		t.Errorf("the add sent a price it was never given: %v", fake.adds[0])
	}
	if len(s.added) != 1 || s.added[0].created {
		t.Fatalf("the tally recorded %+v, want a GROWN line", s.added)
	}
	poAddWantPane(t, r, "grew that line")
}

// A price the operator DOES type on a repeat is sent — the row says it reprices
// the whole line, and that has to be true.
func TestPOAddLine_ATypedRepeatPriceIsSent(t *testing.T) {
	rows := poAddRows()
	rows[0].onOrder = 5
	rows[0].onOrderID = "line-12"
	rows[0].linePrice = "3.1000"
	fake := &poAddFake{rows: rows}
	r, _ := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = key(t, r, poRuneKey("2.75"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if fake.adds[0]["unit_cost"] != "2.75" {
		t.Errorf("posted %v, want the 2.75 the operator typed", fake.adds[0])
	}
}

// An item with NO price on file defaults to 0.00 server-side. That default is
// still shown — it is what the server would apply — but the fact behind it is
// said out loud, because a zero accepted by reflex is a zero-priced line.
func TestPOAddLine_AZeroDefaultSaysThereIsNoPriceOnFile(t *testing.T) {
	fake := &poAddFake{rows: poAddRows()}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("AF-31"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if got := s.costIn.Value(); got != "0.0000" {
		t.Errorf("unit cost prefill = %q, want the server's own default", got)
	}
	poAddWantPane(t, r, "no price on file")
}

// The order stopping being a draft is the SERVER's answer, reported rather than
// re-derived — and it never reads as "nothing matched".
func TestPOAddLine_ANonDraftOrderSaysSo(t *testing.T) {
	fake := &poAddFake{rows: poAddRows(), status: "sent"}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	po := &omsapi.PurchaseOrder{ID: "po-1", Number: "PO-2026-0042", Status: "draft",
		SupplierDetails: "Acme Fasteners & Industrial Supply Co."}
	s := NewPurchaseOrderAddLineScreen(deps, po)
	r := newTestRoot(s)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = next.(Root)

	r = key(t, r, poRuneKey("AF-77"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	poAddWantPane(t, r, "only while it is a draft")
	poAddRejectPane(t, r, "nothing matching")
}

// The sheet names `n` only where it works, and a press in the wrong status
// still answers rather than redrawing the same pane.
func TestPOAddLine_TheSheetOffersItOnlyOnADraft(t *testing.T) {
	draft := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
	draft.loading = false
	draft.po = poShortPO()
	if !barHas(draft.sheetBar(), "n", "Add line") {
		t.Errorf("a draft PO's bar does not name `n`: %+v", draft.sheetBar())
	}

	sent := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
	sent.loading = false
	sent.po = poShortPO()
	sent.po.Status = "sent"
	if barHas(sent.sheetBar(), "n", "Add line") {
		t.Errorf("a SENT PO's bar names `n`, which the backend would refuse: %+v", sent.sheetBar())
	}
	next, cmd := sent.Update(poRuneKey("n"))
	if _, stillHere := next.(*PurchaseOrderDetailScreen); !stillHere {
		t.Fatalf("`n` on a sent PO navigated to %T", next)
	}
	if cmd == nil {
		t.Fatal("`n` on a sent PO answered with nothing at all")
	}
	if msg, ok := cmd().(StatusMsg); !ok || !strings.Contains(msg.Text, "draft") {
		t.Errorf("`n` on a sent PO said %v, want a reason naming draft", cmd())
	}
}

// Leaving destroys the screen, so the bar has to say what that costs BEFORE the
// press — there is no frame left afterwards to say it on.
func TestPOAddLine_LeavingSaysWhatItCosts(t *testing.T) {
	fake := &poAddFake{rows: poAddRows()}
	r, s := poAddAt(t, fake, 80, 24)
	if !barHas(s.bar(), "Esc", "Back to order") {
		t.Errorf("an empty box should offer a plain way back: %+v", s.bar())
	}
	r = key(t, r, poRuneKey("AF-77"))
	if !barHas(s.bar(), "Esc", "Discard & back") {
		t.Errorf("a box with a scan in it should say leaving discards it: %+v", s.bar())
	}
	poAddWantPane(t, r, "Esc=Discard & back")

	// And one phase on, where what would be dropped is the quantity and price.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if !barHas(s.bar(), "Esc", "Back") {
		t.Fatalf("phase = %v, bar = %+v", s.phase, s.bar())
	}
	r = key(t, r, poRuneKey("9"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != poAddPhaseConfirm {
		t.Fatalf("esc from the price prompt landed on %v", s.phase)
	}
	if !barHas(s.bar(), "Esc", "Back, drop entry") {
		t.Errorf("the confirm frame does not say esc drops what was typed: %+v", s.bar())
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	poAddWantPane(t, r, "dropped the quantity and price")
}

// A capped candidate list must be shown as capped. Being told "20" when 63
// matched sends an operator hunting for an item that was never in the list.
func TestPOAddLine_ACappedCandidateListSaysSo(t *testing.T) {
	s := NewPurchaseOrderAddLineScreen(Deps{}, &omsapi.PurchaseOrder{
		ID: "po-1", Number: "PO-2026-0042", Status: "draft", SupplierDetails: "Acme"})
	got := s.ambiguitySentence("bolt", &omsapi.POLineLookup{
		BestMatchTotal: 63, TotalCandidates: 63, Truncated: true,
		Candidates: make([]omsapi.POLineCandidate, 20),
	})
	for _, want := range []string{"63", "20 of 63", "narrow the search"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ambiguity sentence %q does not carry %q", got, want)
		}
	}
	whole := s.ambiguitySentence("bolt", &omsapi.POLineLookup{
		BestMatchTotal: 2, TotalCandidates: 2, Candidates: make([]omsapi.POLineCandidate, 2),
	})
	if strings.Contains(whole, "of 2") {
		t.Errorf("an un-capped list claimed to be capped: %q", whole)
	}
}

// ---------------------------------------------------------------------------
// It fits — asserted on the CLIPPED render, at the width the terminal gives
// ---------------------------------------------------------------------------

// poAddAssertFits checks that no line the SCREEN builds overruns the pane it
// will be drawn into. A row that did would lose its tail to clampToBox, which
// is where these frames name the key that gets the operator out.
//
// It measures the screen's own View(), not Root's: Root.View has ALREADY run
// clampToBox, so every line of it is at most screenBodyWidth by construction
// and an assertion against it can never fail on the class it was written for.
// That vacuity is what let the working status row ship 54 cells wide into a
// 51-column pane. poAssertFits (po_create_picker_status_test.go) is the shape
// this follows; the clipped-render assertions (poAddWantPane and friends) are a
// different check and stay as they are — they ask what SURVIVES the clip.
func poAddAssertFits(t *testing.T, what string, r Root, width int) {
	t.Helper()
	screen, ok := r.screen.(*PurchaseOrderAddLineScreen)
	if !ok {
		t.Fatalf("%s: the flow is on %T, not the add-line screen, so no frame was measured",
			what, r.screen)
	}
	budget := screenBodyWidth(width)
	for i, line := range strings.Split(strings.TrimSuffix(screen.View(), "\n"), "\n") {
		if n := lipgloss.Width(line); n > budget {
			t.Errorf("%s line %d is %d cells and the %d-column terminal's pane keeps %d, "+
				"so clampToBox cuts it after %q\n\tfull line: %q",
				what, i+1, n, width, budget, cellPrefix(line, budget), line)
		}
	}
}

// TestPOAddLine_EveryFrameReadsAtEightyAndUsesAWiderTerminal holds both
// directions at once: nothing overruns the narrowest supported pane, and the
// same frame draws WIDER when the terminal is wider. A layout checked only at
// 80 can be tuned by hard-coding 51, which throws away forty columns a real
// terminal had for the rows an operator reads to decide what they are buying.
func TestPOAddLine_EveryFrameReadsAtEightyAndUsesAWiderTerminal(t *testing.T) {
	long := "Widget bracket, zinc-plated, heavy duty, 12-hole, left-hand"
	for _, reach := range poAddReaches() {
		t.Run(reach.name, func(t *testing.T) {
			var narrow, wide string
			for _, width := range []int{80, 100, 120} {
				fake := &poAddFake{rows: poAddRows()}
				r, s := poAddAt(t, fake, width, 24)
				r = reach.drive(t, r, s)
				poAddAssertFits(t, reach.name, r, width)
				switch width {
				case 80:
					narrow = poAddPane(r)
				case 120:
					wide = poAddPane(r)
				}
			}
			if reach.showsName && strings.Contains(narrow, long) {
				t.Errorf("%s drew the whole 58-cell name inside a 51-column pane", reach.name)
			}
			if reach.showsName && !strings.Contains(wide, long) {
				t.Errorf("%s abbreviated the name on a 120-column terminal that had room for it:\n%s",
					reach.name, wide)
			}
		})
	}
}

type poAddReach struct {
	name      string
	showsName bool
	drive     func(t *testing.T, r Root, s *PurchaseOrderAddLineScreen) Root
}

func poAddReaches() []poAddReach {
	press := func(keys ...tea.KeyMsg) func(*testing.T, Root, *PurchaseOrderAddLineScreen) Root {
		return func(t *testing.T, r Root, _ *PurchaseOrderAddLineScreen) Root {
			for _, k := range keys {
				r = key(t, r, k)
			}
			return r
		}
	}
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	return []poAddReach{
		{name: "identify", drive: press()},
		// The lookup is fired WITHOUT pumping, so the working frame is really in
		// flight while it is measured. It is the frame whose status row carries
		// "Looking up <query> in <supplier>'s catalogue…", which at this
		// fixture's supplier name is 54 cells against a 51-column pane.
		{name: "looking", drive: func(t *testing.T, r Root, _ *PurchaseOrderAddLineScreen) Root {
			r = key(t, r, poRuneKey("widget"))
			next, _ := r.Update(enter)
			return next.(Root)
		}},
		{name: "choose", showsName: true, drive: press(poRuneKey("widget"), enter)},
		{name: "confirm", showsName: true,
			drive: press(poRuneKey("AF-99-12-ZP-LH-HEAVY"), enter)},
		{name: "price", showsName: true,
			drive: press(poRuneKey("AF-99-12-ZP-LH-HEAVY"), enter, enter)},
		{name: "added", showsName: true,
			drive: press(poRuneKey("AF-99-12-ZP-LH-HEAVY"), enter, enter, enter)},
	}
}

// A lookup reply for a query the operator has moved off must be DROPPED, not
// painted over the frame they went back to. Two lookups cannot be told apart by
// the supplier they echo, so the request carries a stamp.
func TestPOAddLine_AStaleLookupNeverPaintsOverTheFrame(t *testing.T) {
	fake := &poAddFake{rows: poAddRows()}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("widget"))

	// Fire the lookup WITHOUT pumping: it is genuinely still out.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if s.phase != poAddPhaseLooking {
		t.Fatalf("phase = %v, want the working frame", s.phase)
	}
	poAddWantPane(t, r, "Looking up")

	// The operator gives up on it.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != poAddPhaseIdentify {
		t.Fatalf("esc left phase %v", s.phase)
	}
	if s.idIn.Value() != "widget" {
		t.Errorf("esc threw away the identifier: %q", s.idIn.Value())
	}

	// Now let the abandoned reply land.
	r = pump(t, r, cmd, 0)
	if s.phase != poAddPhaseIdentify {
		t.Errorf("a reply the operator walked away from moved the flow to %v", s.phase)
	}
}

// ---------------------------------------------------------------------------
// The typed rows are FIELD-parsed before anything is posted
// ---------------------------------------------------------------------------

// A price row is parsed as MONEY, not as a number. big.Rat.SetString accepts a
// fraction, an exponent and a negative, and this screen posts the row verbatim,
// so a mis-keyed leading minus either created a negative-priced line or came
// back as a DRF validation envelope — which AsLineEntryError deliberately does
// not recognise, so the operator read "the add did not answer — the line may or
// may not be on the order" about a request that definitively added nothing.
//
// The refusal is local field parsing only: no request leaves the terminal, the
// entry survives, and the sentence names the key, the field and the way out.
func TestPOAddLine_AMalformedPriceIsRefusedBeforeAnythingIsPosted(t *testing.T) {
	for _, typed := range []string{"-5", "1/3", "1e9", "4.5.0", "four fifty"} {
		t.Run(typed, func(t *testing.T) {
			fake := &poAddFake{rows: poAddRows()}
			r, s := poAddAt(t, fake, 80, 24)
			r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if s.phase != poAddPhasePrice {
				t.Fatalf("the flow is on %v, not the price prompt", s.phase)
			}

			// Onto the cost row, clear the prefilled suggestion, type the entry.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
			for range s.costIn.Value() {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
			}
			r = key(t, r, poRuneKey(typed))
			if got := s.costIn.Value(); got != typed {
				t.Fatalf("the cost row holds %q, not %q", got, typed)
			}

			before := poAddPane(r)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

			fake.mu.Lock()
			posts := len(fake.adds)
			fake.mu.Unlock()
			if posts != 0 {
				t.Errorf("a unit cost of %q was posted; the server answered %d add(s)", typed, posts)
			}
			if s.phase != poAddPhasePrice {
				t.Errorf("the refusal moved the flow to %v", s.phase)
			}
			if s.costIn.Value() != typed {
				t.Errorf("the refusal threw away what was typed: %q", s.costIn.Value())
			}
			if poAddPane(r) == before {
				t.Error("enter redrew a byte-identical pane, which reads as a wedged program")
			}
			for _, want := range []string{"enter did not add it", "unit cost", "clear the row"} {
				poAddWantPane(t, r, want)
			}
			// A row it cannot post is a row it must not price either.
			poAddRejectPane(t, r, "Line total")
		})
	}
}

// The rows the price prompt EXISTS for must stay on the pane when the body
// overflows, and that is what a read-only band numbered into the navigable
// range destroys: AddFittedFields numbers rows rowBase+i, so a band based at
// jdeNoRow used to exempt only its first line and hand its second the number 0
// — the Quantity input's own row. block(0) then spanned from the read-only
// Supplier SKU line through the Quantity line, Window anchored on that inflated
// block, and the frame started on the read-only lines and dropped Unit cost.
func TestPOAddLine_AShortPaneKeepsBothTypedRowsOnThePricePrompt(t *testing.T) {
	for _, height := range []int{18, 19, 20, 21, 24} {
		fake := &poAddFake{rows: poAddRows()}
		r, s := poAddAt(t, fake, 80, height)
		r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != poAddPhasePrice {
			t.Fatalf("at 80x%d the flow is on %v, not the price prompt", height, s.phase)
		}
		pane := poAddPane(r)
		for _, want := range []string{"Quantity", "Unit cost"} {
			if !strings.Contains(pane, want) {
				t.Errorf("at 80x%d the price prompt drew no %q row:\n%s", height, want, pane)
			}
		}
	}
}

// The FACTS on a candidate row never give and the identifier does — the same
// rule poFitRow applies to the line above, applied to the line below it.
//
// The match label is OMS prose: "another supplier's listing (Globex
// Industrial)" is 45 cells. Budgeted BEHIND it, the price and the on-order
// count were what a 51-column pane cut, and "50 @ 4." reads as a whole price —
// on the row an operator is picking from.
func TestPOAddLine_ACandidateRowKeepsItsFactsBehindALongMatchLabel(t *testing.T) {
	fake := &poAddFake{rows: []poAddCatalogRow{
		{itemSupplier: 21, name: "Widget bracket, zinc-plated, heavy duty, 12-hole, left-hand",
			sku: "WB-1200", supplierSKU: "XV-1", perPackage: 25, suggestQty: 50,
			suggestCost: "4.5000", crossVendor: "Globex Industrial",
			onOrder: 5, onOrderID: "line-21", linePrice: "4.5000"},
		{itemSupplier: 22, name: "Widget clamp", sku: "WC-1", supplierSKU: "XV-1",
			perPackage: 1, suggestQty: 4, suggestCost: "9.9900",
			crossVendor: "Globex Industrial"},
	}}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("XV-1"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseChoose {
		t.Fatalf("two equally strong matches landed on %v, not the choice list", s.phase)
	}

	poAddAssertFits(t, "choose, long match label", r, 80)
	for _, want := range []string{"50 @ 4.5000", "on order: 5", "4 @ 9.9900"} {
		poAddWantPane(t, r, want)
	}
	// The label is what abbreviated, and it says so.
	poAddWantPane(t, r, "another supplie")
	poAddRejectPane(t, r, "another supplier's listing (Globex Industrial)")
}
