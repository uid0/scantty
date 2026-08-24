package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
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
	// capAt, when non-zero, is the server-side cap on the candidate list: the
	// reply carries that many candidates, reports the full count in
	// total_candidates and sets truncated. It is the only way to drive the
	// capped-list sentence through the real screen.
	capAt int
	// raceExisting simulates ANOTHER terminal putting a line on the order in the
	// window between this one's lookup and its add: the lookup reports no
	// existing line, and the add nonetheless comes back created=false with the
	// line standing at that quantity plus what was posted.
	raceExisting int
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
			total := len(cands)
			truncated := false
			if f.capAt > 0 && f.capAt < len(cands) {
				cands, truncated = cands[:f.capAt], true
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
				"total_candidates":      total,
				"best_match_total":      bestTotal,
				"truncated":             truncated,
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
			if f.raceExisting > 0 {
				created = false
				qty += f.raceExisting
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

// A capped candidate list must be shown as capped, and the number behind the
// word "matches" must be the number of MATCHES.
//
// Written this way on purpose. Its predecessor called ambiguitySentence
// directly and asserted only that "63", "20 of 63" and "narrow the search"
// appeared SOMEWHERE in the result — every one of which the truncation clause
// supplies on its own — so the lead was unpinned and the test passed over
// three different wordings in turn, including one that said "matches 20 items
// … · 20 of 63 shown": two different claims about the same figure in one
// sentence. So this drives the real screen and parses the figure BOUND to the
// word, rather than looking for digits anywhere in the line. Do not simplify it
// back to a substring check.
func TestPOAddLine_ACappedCandidateListSaysSo(t *testing.T) {
	rows := []poAddCatalogRow{}
	for i := 0; i < 5; i++ {
		rows = append(rows, poAddCatalogRow{
			itemSupplier: 50 + i, name: fmt.Sprintf("Widget bracket, variant %d", i+1),
			sku: fmt.Sprintf("WV-%d", i), supplierSKU: fmt.Sprintf("AF-1%d", i),
			perPackage: 1, suggestQty: 2, suggestCost: "4.5000",
		})
	}

	t.Run("capped", func(t *testing.T) {
		fake := &poAddFake{rows: rows, capAt: 2}
		r, s := poAddAt(t, fake, 80, 24)
		r = key(t, r, poRuneKey("widget"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != poAddPhaseChoose {
			t.Fatalf("a capped ambiguous lookup landed on %v, not the choice list", s.phase)
		}
		if got, want := len(s.candidates()), 2; got != want {
			t.Fatalf("the screen holds %d candidates, want the capped %d", got, want)
		}

		if got := poAddMatchCount(t, r); got != len(rows) {
			t.Errorf("the sentence says %d items matched; %d did (%d are on the pane)",
				got, len(rows), len(s.candidates()))
		}
		poAddWantPane(t, r, "the first 2 are offered here")
		poAddWantPane(t, r, "narrow the search")
	})

	t.Run("uncapped", func(t *testing.T) {
		fake := &poAddFake{rows: rows}
		r, s := poAddAt(t, fake, 80, 24)
		r = key(t, r, poRuneKey("widget"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != poAddPhaseChoose {
			t.Fatalf("an ambiguous lookup landed on %v, not the choice list", s.phase)
		}
		if got := poAddMatchCount(t, r); got != len(rows) {
			t.Errorf("the sentence says %d items matched; %d did", got, len(rows))
		}
		// Nothing was capped, so no clause may claim it was.
		poAddRejectPane(t, r, "offered here")
		poAddRejectPane(t, r, "narrow the search")
	})
}

// poAddMatchCount reads the figure the rendered sentence binds to the word
// "matches". The note FOLDS, so the pane is flattened first — a claim split
// across two lines is still one claim.
func poAddMatchCount(t *testing.T, r Root) int {
	t.Helper()
	flat := poAddFlatPane(r)
	m := regexp.MustCompile(`matches (\d+) items`).FindStringSubmatch(flat)
	if m == nil {
		t.Fatalf("no \"matches N items\" claim on the pane:\n%s", poAddPane(r))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("the match count %q is not a number", m[1])
	}
	return n
}

// poAddFlatPane is the rendered pane as one whitespace-normalised line, so a
// sentence pickerWrap folded can still be read as the sentence it is.
func poAddFlatPane(r Root) string {
	return strings.Join(strings.Fields(poAddPane(r)), " ")
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

// ---------------------------------------------------------------------------
// One reader per typed row, so the total and the submit cannot disagree
// ---------------------------------------------------------------------------

// The Line total row is the figure the operator is really approving, so it is
// drawn EXACTLY when both rows hold a value the submit will accept, and when it
// is drawn it equals quantity × unit cost.
//
// Both halves are asserted here. A total over an entry Enter is about to refuse
// is the defect two disagreeing readers produced — quantity "5.5" drew one and
// was then rejected as "not a whole number of 1 or more", and "+5" was posted
// with the total hidden. A total over a BLANK row would be the opposite
// defect: blank means "the server decides", which Enter accepts and which is
// the ordinary repeat-add entry, and the number the server will decide on is
// not one this screen holds.
func TestPOAddLine_TheLineTotalAppearsExactlyWhenBothRowsAreAccepted(t *testing.T) {
	for _, entry := range []struct {
		qty, cost string
		accepted  bool
		// total is the figure the row must carry, empty when no row may be drawn.
		total string
	}{
		{qty: "50", cost: "4.50", accepted: true, total: "225.00"},
		{qty: "+5", cost: "4.50", accepted: true, total: "22.50"},
		{qty: "1", cost: "0", accepted: true, total: "0.00"},
		// Blank is accepted and draws nothing: the server decides the number.
		{qty: "", cost: "4.50", accepted: true},
		{qty: "50", cost: "", accepted: true},
		{qty: "", cost: "", accepted: true},
		{qty: "5.5", cost: "4.50"},
		{qty: "0", cost: "4.50"},
		{qty: "-1", cost: "4.50"},
		{qty: "two", cost: "4.50"},
		{qty: "50", cost: "-4.50"},
		{qty: "50", cost: "1/3"},
		{qty: "50", cost: "1e9"},
	} {
		t.Run("fresh "+entry.qty+"|"+entry.cost, func(t *testing.T) {
			fake := &poAddFake{rows: poAddRows()}
			r, s := poAddAt(t, fake, 80, 24)
			r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if s.phase != poAddPhasePrice {
				t.Fatalf("the flow is on %v, not the price prompt", s.phase)
			}
			r = poAddRetype(t, r, s, entry.qty, entry.cost)
			poAddAssertTotal(t, r, fake, entry.qty, entry.cost, entry.accepted, entry.total)
		})
	}
}

// The repeat-add path reaches the same prompt with the cost row deliberately
// BLANK — the server keeps the price the line already carries unless one is
// sent — so the ordinary repeat entry is accepted with no total drawn, and one
// appears the moment the operator types a price over it.
func TestPOAddLine_ARepeatAddDrawsNoTotalUntilAPriceIsTyped(t *testing.T) {
	rows := func() []poAddCatalogRow {
		out := poAddRows()
		out[0].onOrder, out[0].onOrderID, out[0].linePrice = 25, "line-12", "4.5000"
		return out
	}
	reach := func(t *testing.T) (Root, *poAddFake, *PurchaseOrderAddLineScreen) {
		t.Helper()
		fake := &poAddFake{rows: rows()}
		r, s := poAddAt(t, fake, 80, 24)
		r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != poAddPhasePrice {
			t.Fatalf("the flow is on %v, not the price prompt", s.phase)
		}
		if s.costIn.Value() != "" {
			t.Fatalf("a repeat add prefilled the cost row with %q", s.costIn.Value())
		}
		return r, fake, s
	}

	t.Run("defaults", func(t *testing.T) {
		r, fake, s := reach(t)
		poAddAssertTotal(t, r, fake, s.qtyIn.Value(), "", true, "")
	})
	t.Run("a price typed over the blank", func(t *testing.T) {
		r, fake, s := reach(t)
		r = poAddRetype(t, r, s, "4", "2.50")
		poAddAssertTotal(t, r, fake, "4", "2.50", true, "10.00")
	})
}

// poAddAssertTotal presses enter on the price prompt and holds the whole
// invariant: the row is drawn exactly when the entry is accepted AND both rows
// hold a value, and when drawn it carries `total`.
func poAddAssertTotal(t *testing.T, r Root, fake *poAddFake, qty, cost string, accepted bool, total string) {
	t.Helper()
	row := ""
	for _, line := range strings.Split(poAddPane(r), "\n") {
		if strings.Contains(line, "Line total") {
			row = line
		}
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	fake.mu.Lock()
	posted := len(fake.adds) > 0
	fake.mu.Unlock()
	if posted != accepted {
		t.Fatalf("quantity %q cost %q: enter posted=%v, want %v", qty, cost, posted, accepted)
	}
	if (row != "") != (total != "") {
		t.Fatalf("quantity %q cost %q: the Line total row drawn=%v, want %v (enter posted=%v)\n\t%q",
			qty, cost, row != "", total != "", posted, row)
	}
	if total != "" && !strings.Contains(row, total) {
		t.Errorf("quantity %q cost %q: the Line total row reads %q, want it to carry %q",
			qty, cost, row, total)
	}
}

// poAddRetype clears both price rows and types the given entry into them,
// leaving the caret back on the quantity row.
func poAddRetype(t *testing.T, r Root, s *PurchaseOrderAddLineScreen, qty, cost string) Root {
	t.Helper()
	clear := func() Root {
		for i := len(s.qtyIn.Value()) + len(s.costIn.Value()); i > 0; i-- {
			r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
		}
		return r
	}
	r = clear()
	if qty != "" {
		r = key(t, r, poRuneKey(qty))
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = clear()
	if cost != "" {
		r = key(t, r, poRuneKey(cost))
	}
	if s.qtyIn.Value() != qty || s.costIn.Value() != cost {
		t.Fatalf("the rows hold %q / %q, not %q / %q", s.qtyIn.Value(), s.costIn.Value(), qty, cost)
	}
	return key(t, r, tea.KeyMsg{Type: tea.KeyUp})
}

// ---------------------------------------------------------------------------
// The confirm frame confirms the VALUE, not the server's prose about it
// ---------------------------------------------------------------------------

// On a confirm surface the fact being confirmed survives whole and the
// identifier abbreviates. The matched VALUE is what the operator scanned; the
// server's match label is prose explaining why it matched, and the Supplier SKU
// row above and the pinned "matched on …" note both still carry it.
//
// Assembled label-first and clipped as one string, the row gave up the value:
// an ordinary supplier SKU came out cut mid-value at 80 columns, and a
// cross-vendor label filled the row and dropped the scanned value entirely.
func TestPOAddLine_TheConfirmFrameKeepsTheScannedValueWhole(t *testing.T) {
	scanned := "AF-99-12-ZP-LH-HEAVY"
	for _, vendor := range []string{"", "Globex Industrial"} {
		name := "same vendor"
		if vendor != "" {
			name = "cross vendor"
		}
		t.Run(name, func(t *testing.T) {
			rows := poAddRows()
			rows[0].crossVendor = vendor
			fake := &poAddFake{rows: rows}
			r, s := poAddAt(t, fake, 80, 24)
			r = key(t, r, poRuneKey(scanned))
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if s.phase != poAddPhaseConfirm {
				t.Fatalf("an exact identifier landed on %v, not the confirm frame", s.phase)
			}

			poAddAssertFits(t, "confirm, "+name, r, 80)
			matched := poAddLineWith(t, poAddPane(r), "Matched")
			if !strings.Contains(matched, `"`+scanned+`"`) {
				t.Errorf("the Matched row does not carry the whole scanned value %q:\n\t%q", scanned, matched)
			}
		})
	}
}

// poAddLineWith returns the single pane line carrying `marker`.
func poAddLineWith(t *testing.T, pane, marker string) string {
	t.Helper()
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("no pane line carries %q:\n%s", marker, pane)
	return ""
}

// ---------------------------------------------------------------------------
// A delta the reply does not support is not reported as one
// ---------------------------------------------------------------------------

// (purchase_order, item_supplier) is unique, so another terminal can put a line
// on the order in the window between this one's lookup and its add. The reply
// then says created=false while the lookup reported no existing line: we know
// where the line ENDED UP and we cannot know what it grew BY, and subtracting a
// before-figure we never had drew "grew that line by 12 to 12".
func TestPOAddLine_AGrownLineTheLookupNeverSawReportsNoInventedDelta(t *testing.T) {
	fake := &poAddFake{rows: poAddRows(), raceExisting: 7}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != poAddPhaseIdentify {
		t.Fatalf("the add landed on %v, not back at the identifier row", s.phase)
	}
	if len(s.added) != 1 {
		t.Fatalf("the tally holds %d entries, want 1", len(s.added))
	}
	// 50 posted onto a line another terminal had already put 7 on.
	if got := s.added[0].after; got != 57 {
		t.Fatalf("the line stands at %d, want 57", got)
	}
	pane := poAddPane(r)
	if !strings.Contains(pane, "now stands at 57") {
		t.Errorf("the reply does not say where the line stands:\n%s", pane)
	}
	for _, invented := range []string{"grew that line by", "by 57 to 57", "  0 @ "} {
		if strings.Contains(pane, invented) {
			t.Errorf("the pane reports a delta nobody measured (%q):\n%s", invented, pane)
		}
	}
	poAddAssertFits(t, "raced grow", r, 80)
}

// ---------------------------------------------------------------------------
// The choice list: the count describes the rows, and a page is a paneful
// ---------------------------------------------------------------------------

// poAddMixedTierRows is a catalogue where ONE query reaches two different match
// tiers — two rows carry the identifier as their supplier SKU, a third merely
// has it inside its name. Every other fixture in this file puts every match in
// one tier, which is exactly why a sentence quoting the strongest tier's count
// over a list of all tiers could not be caught here.
func poAddMixedTierRows() []poAddCatalogRow {
	return []poAddCatalogRow{
		{itemSupplier: 31, name: "Widget bracket", sku: "WB-1", supplierSKU: "WIDGET-A",
			perPackage: 1, suggestQty: 2, suggestCost: "4.5000"},
		{itemSupplier: 32, name: "Widget clamp", sku: "WC-1", supplierSKU: "WIDGET-A",
			perPackage: 1, suggestQty: 3, suggestCost: "1.2500"},
		{itemSupplier: 33, name: "WIDGET-A mounting plate", sku: "WM-1", supplierSKU: "AF-55",
			perPackage: 1, suggestQty: 4, suggestCost: "2.0000"},
	}
}

// A count in the ambiguity sentence is a promise about rows the operator can
// count on screen, so it must be the size of the list chooseBody draws — not
// the size of the strongest TIER, which is a different quantity and used to be
// quoted alongside a total taken from the other one.
func TestPOAddLine_TheAmbiguityCountIsTheListTheOperatorSees(t *testing.T) {
	rows := poAddMixedTierRows()
	fake := &poAddFake{rows: rows}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("WIDGET-A"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseChoose {
		t.Fatalf("two equally strong matches landed on %v, not the choice list", s.phase)
	}
	if got := len(s.candidates()); got != len(rows) {
		t.Fatalf("the server sent %d candidates, want %d", got, len(rows))
	}
	if best := s.lookup.BestMatchTotal; best == len(rows) {
		t.Fatalf("the fixture no longer mixes tiers: best_match_total is %d of %d candidates",
			best, len(rows))
	}

	pane := poAddPane(r)
	if !strings.Contains(pane, fmt.Sprintf("matches %d items", len(rows))) {
		t.Errorf("the sentence does not count the %d rows it drew:\n%s", len(rows), pane)
	}
	if strings.Contains(pane, fmt.Sprintf("matches %d items", s.lookup.BestMatchTotal)) {
		t.Errorf("the sentence counts the strongest tier (%d) over a list of %d:\n%s",
			s.lookup.BestMatchTotal, len(rows), pane)
	}
	// Every row it counted is really drawn, weaker tiers included.
	for _, row := range rows {
		poAddWantPane(t, r, row.name)
	}
}

// PgDn covers one PANEFUL of candidates. Measured against the whole pane with
// no allowance for the bar or for the answer pinned above the body, the step
// came out as the entire list and PgDn was End — on the row list an operator
// picks a purchase-order line from, while the bar said "Page".
func TestPOAddLine_APageOfCandidatesIsAPanefulNotTheWholeList(t *testing.T) {
	rows := []poAddCatalogRow{}
	for i := 0; i < 6; i++ {
		rows = append(rows, poAddCatalogRow{
			itemSupplier: 40 + i, name: fmt.Sprintf("Widget bracket, variant %d", i+1),
			sku: fmt.Sprintf("WB-%d", i), supplierSKU: fmt.Sprintf("AF-%02d", i),
			perPackage: 1, suggestQty: 2, suggestCost: "4.5000",
		})
	}
	fake := &poAddFake{rows: rows}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("widget"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseChoose {
		t.Fatalf("six partial matches landed on %v, not the choice list", s.phase)
	}
	if !s.choosePages() {
		t.Fatalf("the bar does not name paging at 80x24 with %d candidates, so there is "+
			"no step to check", len(rows))
	}

	last := len(rows) - 1
	r = key(t, r, tea.KeyMsg{Type: tea.KeyPgDown})
	if s.cursor == 0 {
		t.Fatalf("pgdown moved nothing while the bar named it")
	}
	if s.cursor >= last {
		t.Fatalf("pgdown jumped from the first candidate to %d of %d — that is End, not a page",
			s.cursor, last)
	}
	first := s.cursor
	r = key(t, r, tea.KeyMsg{Type: tea.KeyPgDown})
	if s.cursor <= first {
		t.Fatalf("a second pgdown did not move past %d", first)
	}
	// The step is re-derived at each position, so pgup is not required to land
	// back on the exact row pgdown left — only to cover ground the other way.
	back := s.cursor
	r = key(t, r, tea.KeyMsg{Type: tea.KeyPgUp})
	if s.cursor >= back {
		t.Errorf("pgup did not move back from %d", back)
	}
	poAddAssertFits(t, "choose, paging", r, 80)
}

// ---------------------------------------------------------------------------
// The confirm note is worded for the path that reached it
// ---------------------------------------------------------------------------

// Two states reach the confirm frame and `esc` does a different thing in each,
// so the note that names it has to be worded twice. Off the choice list esc
// returns to that list with the other matches already drawn; telling that
// operator to narrow a search describes the other path's key.
func TestPOAddLine_TheConfirmNoteWordsBothPathsThatReachIt(t *testing.T) {
	t.Run("straight off a lookup that resolved", func(t *testing.T) {
		rows := append(poAddMixedTierRows(), poAddCatalogRow{
			itemSupplier: 34, name: "Bracket AF-77 spare", sku: "BS-1", supplierSKU: "AF-91",
			perPackage: 1, suggestQty: 1, suggestCost: "3.0000"})
		fake := &poAddFake{rows: rows}
		r, s := poAddAt(t, fake, 80, 24)
		// "AF-77" is row 13's supplier SKU exactly and sits inside the name above,
		// so the best tier holds one row and the lookup resolves with company.
		rows[1].supplierSKU = "AF-77"
		fake.rows = rows
		r = key(t, r, poRuneKey("AF-77"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != poAddPhaseConfirm {
			t.Fatalf("a lone strongest match landed on %v, not the confirm frame", s.phase)
		}
		if s.lookup.TotalCandidates < 2 {
			t.Fatalf("the fixture sent %d candidates, so there is no tail to word",
				s.lookup.TotalCandidates)
		}
		// A fold-safe token: pickerWrap breaks this tail between "narrow the" and
		// "the search", and the fit assertions elsewhere own the folding.
		if got := s.lookup.TotalCandidates; got != 2 {
			t.Fatalf("the fixture sent %d candidates, so the tail is not the singular case", got)
		}
		// One is the ordinary shape of a resolving lookup with company, and the
		// tail used to read "1 other items also matched … to see them".
		poAddWantPane(t, r, "1 other item also matched")
		poAddRejectPane(t, r, "other items")
		poAddRejectPane(t, r, "esc goes back to the")
	})

	t.Run("off the choice list", func(t *testing.T) {
		fake := &poAddFake{rows: poAddMixedTierRows()}
		r, s := poAddAt(t, fake, 80, 24)
		r = key(t, r, poRuneKey("WIDGET-A"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != poAddPhaseChoose {
			t.Fatalf("landed on %v, not the choice list", s.phase)
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != poAddPhaseConfirm {
			t.Fatalf("enter on a candidate landed on %v, not the confirm frame", s.phase)
		}
		// esc really does go back to the list, which is what the note now claims.
		poAddWantPane(t, r, "esc goes back to the 3 matches")
		poAddRejectPane(t, r, "narrow")
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
		if s.phase != poAddPhaseChoose {
			t.Errorf("esc went to %v, not back to the candidate list the note named", s.phase)
		}
	})
}

// ---------------------------------------------------------------------------
// The working line is bounded by ONE rule, in both directions
// ---------------------------------------------------------------------------

// "Looking up <query> in <supplier>'s catalogue…" used to clip the query and
// the supplier to a hard-coded twenty cells EACH and was then clipped again by
// the status row's own bound, so the row lost its own closing words —
// `… & In…'s cata…` — on
// the frame whose whole job is saying what is happening. The same constant threw
// away sixty-odd columns a 120-column terminal had for the supplier's name.
//
// Both halves are asserted here, and neither was reachable before: poAddAssertFits
// sees a row the outer clip has already made fit, and the "looking" reach is
// showsName: false so the wide direction was never exercised.
func TestPOAddLine_TheLookingRowIsBoundedOnceAndUsesTheWholeTerminal(t *testing.T) {
	const (
		tail  = "'s catalogue…"
		query = "AF-99-12-ZP-LH-HEAVY"
	)
	// The fixture's supplier is "Acme Fasteners & Industrial Supply Co." — 38
	// cells, longer than the row can hold at 80 and shorter than it has at 120.
	looking := func(t *testing.T, width int) string {
		t.Helper()
		fake := &poAddFake{rows: poAddRows()}
		r, _ := poAddAt(t, fake, width, 24)
		r = key(t, r, poRuneKey(query))
		// Fire the lookup WITHOUT pumping: the working row is what is measured.
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
		r = next.(Root)
		poAddAssertFits(t, fmt.Sprintf("looking at %d columns", width), r, width)
		return poAddLineWith(t, poAddPane(r), "Looking up")
	}

	narrow := looking(t, 80)
	if !strings.Contains(narrow, tail) {
		t.Errorf("at 80 columns the row lost its own closing words — a second bound cut "+
			"what the first had already fitted:\n\t%q", narrow)
	}
	wide := looking(t, 120)
	if !strings.Contains(wide, tail) {
		t.Errorf("at 120 columns the row lost its own closing words:\n\t%q", wide)
	}

	// The supplier is what the extra room buys, and it must buy some.
	supplier := "Acme Fasteners & Industrial Supply Co."
	narrowKeeps := poAddLongestPrefixOf(narrow, supplier)
	wideKeeps := poAddLongestPrefixOf(wide, supplier)
	if wideKeeps <= narrowKeeps {
		t.Errorf("a 120-column terminal drew %d cells of the supplier name and an 80-column one "+
			"drew %d — the row is not using the width it was given:\n\t%q\n\t%q",
			wideKeeps, narrowKeeps, narrow, wide)
	}
	if wideKeeps != len(supplier) {
		t.Errorf("at 120 columns the supplier name is still abbreviated to %d of %d cells:\n\t%q",
			wideKeeps, len(supplier), wide)
	}
}

// poAddLongestPrefixOf is how much of `whole` the line actually carries.
func poAddLongestPrefixOf(line, whole string) int {
	for n := len(whole); n > 0; n-- {
		if strings.Contains(line, whole[:n]) {
			return n
		}
	}
	return 0
}
