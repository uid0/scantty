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

// Taking a line OFF a purchase order, both ways.
//
// The captain's decision is the shape of these tests: while the order is still
// the shop's own document a line put on it by mistake is a typo and is DELETED
// outright; once the supplier holds a copy the line is part of a record someone
// else also has and can only be VOIDED. Which of the two applies is the
// SERVER's answer (`can_delete_items`, served from PRE_SUPPLIER_STATUSES), and
// the whole point of the flag is that no client keeps its own copy of which
// statuses those are.
//
// So the load-bearing check here is not "a draft deletes" — it is that the
// offer follows the FLAG when the flag and the status disagree
// (TestPOLineRemove_TheOfferFollowsTheFlagAndNotTheStatus). A client that read
// `status == "draft"` passes every other test in this file and fails that one.
//
// Everything is driven through Root.Update against a stateful httptest fake, so
// what is asserted is the frame an operator sees and the request OMS receives —
// never a predicate called directly.

// ---------------------------------------------------------------------------
// The fake
// ---------------------------------------------------------------------------

// fakeRemoveLine is the stored half of a reorder_queue PurchaseOrderItem, cut
// down to what a removal cares about.
type fakeRemoveLine struct {
	id       string
	label    string
	ordered  int
	received int
	cost     string // estimated_cost, derived server-side and never null
	voided   bool
}

func (l *fakeRemoveLine) payload() map[string]any {
	return map[string]any{
		"id":                l.id,
		"description":       l.label,
		"quantity_ordered":  l.ordered,
		"quantity_received": l.received,
		"estimated_cost":    l.cost,
		"is_voided":         l.voided,
	}
}

// fakeRemovePO serves one order and records every write against its lines.
//
// canDelete is a POINTER because the three states are three different facts:
// true, false, and "this server did not serve the key at all" — which is what
// an OMS older than the flag looks like, and which must not read as a no.
type fakeRemovePO struct {
	mu        sync.Mutex
	status    string
	canDelete *bool
	lines     []*fakeRemoveLine

	// What arrived. deletes/voids are the item ids, in order; deleteBodies is
	// the raw request body of each DELETE, because "takes no reason" is a claim
	// about the request and not only about the screen.
	deletes      []string
	deleteBodies []string
	voids        []map[string]any

	// refuse, when set, is the body the next DELETE answers with at
	// refuseStatus — the hand-built {"error","code"} shape views.py writes,
	// which does NOT go through DRF's exception handler.
	refuse       string
	refuseStatus int
}

func (f *fakeRemovePO) line(id string) *fakeRemoveLine {
	for _, l := range f.lines {
		if l.id == id {
			return l
		}
	}
	return nil
}

func (f *fakeRemovePO) drop(id string) {
	out := f.lines[:0]
	for _, l := range f.lines {
		if l.id != id {
			out = append(out, l)
		}
	}
	f.lines = out
}

func (f *fakeRemovePO) orderPayload() map[string]any {
	items := make([]map[string]any, 0, len(f.lines))
	for _, l := range f.lines {
		items = append(items, l.payload())
	}
	out := map[string]any{
		"id": "po-1", "po_number": "PO-2026-0042",
		"status": f.status, "status_label": strings.ToTitle(f.status),
		"supplier_name": "Acme Bolt Co.",
		"order_date":    "2026-08-01T00:00:00Z",
		"items":         items,
	}
	// Absent when nil, which is the whole third state: the key is not written
	// at all rather than written false.
	if f.canDelete != nil {
		out["can_delete_items"] = *f.canDelete
	}
	return out
}

func (f *fakeRemovePO) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		raw, _ := io.ReadAll(r.Body)

		switch {
		case r.Method == http.MethodDelete && strings.Contains(path, "/items/"):
			id := strings.TrimSuffix(strings.Split(path, "/items/")[1], "/")
			f.deletes = append(f.deletes, id)
			f.deleteBodies = append(f.deleteBodies, string(raw))
			if f.refuse != "" {
				w.WriteHeader(f.refuseStatus)
				_, _ = w.Write([]byte(f.refuse))
				return
			}
			l := f.line(id)
			if l == nil {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error": "Line item not found"}`))
				return
			}
			deleted := map[string]any{
				"line_item": l.id, "line_shape": "item_supplier",
				"label": l.label, "description": l.label,
				"quantity_ordered": l.ordered, "estimated_cost": l.cost,
			}
			f.drop(id)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"deleted": deleted, "purchase_order": f.orderPayload(),
			})

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/void/"):
			id := strings.TrimSuffix(strings.TrimSuffix(path, "/void/"), "/")
			id = id[strings.LastIndex(id, "/")+1:]
			body := map[string]any{}
			_ = json.Unmarshal(raw, &body)
			body["__item"] = id
			f.voids = append(f.voids, body)
			l := f.line(id)
			if l == nil {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error": "Line item not found"}`))
				return
			}
			l.voided = true
			_ = json.NewEncoder(w).Encode(l.payload())

		case strings.HasPrefix(path, "/api/reorders/purchase-orders/"):
			_ = json.NewEncoder(w).Encode(f.orderPayload())

		default:
			// The association pickers load in the background and treat a
			// failure as non-fatal; nothing here waits on them.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		}
	}
}

func boolPtr(v bool) *bool { return &v }

// poDeletablePO is poViewPO with the server's flag saying its lines may be
// destroyed — the ONE thing that opens the delete confirm, so a fixture without
// it cannot reach that frame at all.
func poDeletablePO() *omsapi.PurchaseOrder {
	po := poViewPO()
	po.CanDeleteItems = boolPtr(true)
	return po
}

// poRemoveOrder is the order every case starts from: two lines, so deleting one
// is not also the last-active-line case (which has a fixture of its own).
func poRemoveOrder(status string, canDelete *bool) *fakeRemovePO {
	return &fakeRemovePO{
		status: status, canDelete: canDelete,
		lines: []*fakeRemoveLine{
			{id: "line-bolts", label: "M3×12 hex bolt, stainless", ordered: 250, cost: "31.25"},
			{id: "line-washers", label: "M3 washer", ordered: 500, cost: "12.50"},
		},
	}
}

// ---------------------------------------------------------------------------
// Driving it
// ---------------------------------------------------------------------------

// poRemoveRoot opens the order the way the operator does and hands back the
// Root plus the live detail screen.
func poRemoveRoot(t *testing.T, fake *fakeRemovePO, width int) (Root, *PurchaseOrderDetailScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewPurchaseOrderDetailScreen(deps, "po-1")
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return pump(t, next.(Root), detail.Init(), 0), detail
}

// poRemoveOnStatusRow walks the operator's keys all the way to the line
// editor's status row — the row whose Ctrl-E opens whichever removal applies.
//
// The walk is BOUNDED (poReachLineField's rule): a `for` loop that pressed Down
// until the focus arrived would turn a declined key into a hang, and the
// package would then fail by timing out with whichever test happened to be
// running named in the panic.
func poRemoveOnStatusRow(t *testing.T, r Root, lineIdx int) (Root, *PurchaseOrderEditScreen) {
	t.Helper()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	s := poEditScreen(t, r)
	r = pump(t, r, s.Init(), 0)
	for i := 0; i < poEditLineBase+lineIdx; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got, ok := s.onLineRow(); !ok || got != lineIdx {
		t.Fatalf("cursor %d is not line row %d (got %d, onLine=%v)", s.cursor, lineIdx, got, ok)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseLine {
		t.Fatalf("Ctrl-E on a line row should open the line editor, phase = %v", s.phase)
	}
	for i := 0; i < poLineEditCount && s.lineFocus != poLineRowStatus; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	if s.lineFocus != poLineRowStatus {
		t.Fatalf("Down did not reach the line-status row; lineFocus = %d", s.lineFocus)
	}
	return r, s
}

// poRemoveBarLabel is the label the line editor's bar puts on Ctrl-E, or "" when
// it does not name the key at all.
func poRemoveBarLabel(s *PurchaseOrderEditScreen) string {
	for _, it := range s.lineBar() {
		if it.Key == "Ctrl-E" {
			return it.Label
		}
	}
	return ""
}

// poRemovePane is what the terminal really shows: the screen's frame clipped to
// the pane the terminal gives it. clampToBox truncates in Root.View, not in the
// screen, so reading View() unclipped passes over text the glass never gets —
// and clipping Root's whole render instead would let the 80-column status bar
// satisfy a substring the body line was cut in half by.
func poRemovePane(s Screen, width int) string {
	return clampToBox(s.View(), screenBodyWidth(width), screenBodyHeight(40))
}

// ---------------------------------------------------------------------------
// The rule the flag exists for
// ---------------------------------------------------------------------------

// TestPOLineRemove_TheOfferFollowsTheFlagAndNotTheStatus is the load-bearing
// one, and it is watched to fail against any client-side status rule.
//
// The two rows where the flag and the status DISAGREE are the whole test. A
// screen that read `status == "draft"` — the rule the serializer docstring
// exists to forbid — passes every other case in this file and fails both of
// these: it would offer an irreversible destroy on the order OMS says the
// supplier holds, and hide it on the one OMS says is still the shop's own.
func TestPOLineRemove_TheOfferFollowsTheFlagAndNotTheStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    string
		canDelete *bool
		want      string
	}{
		{"draft, flag says yes", "draft", boolPtr(true), "Delete line"},
		{"sent, flag says no", "sent", boolPtr(false), "Void line"},
		// The disagreements. OMS can add a second pre-send status (an approval
		// hold, say) or take `draft` out of the set, and this client follows
		// without an edit.
		{"draft, flag says NO", "draft", boolPtr(false), "Void line"},
		{"sent, flag says YES", "sent", boolPtr(true), "Delete line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := poRemoveOrder(tc.status, tc.canDelete)
			r, _ := poRemoveRoot(t, fake, 100)
			_, s := poRemoveOnStatusRow(t, r, 0)
			if got := poRemoveBarLabel(s); got != tc.want {
				t.Fatalf("status %q with can_delete_items=%v: bar offers %q, want %q",
					tc.status, *tc.canDelete, got, tc.want)
			}
		})
	}
}

// TestPOLineRemove_ExactlyOneRemovalIsOffered: never both, never neither.
//
// The web page renders exactly one of the two actions off the flag so the
// operator is never asked to know which applies; the terminal is held to the
// same standard. "Neither" is checked too, because a bar that named no removal
// at all would satisfy a check that only forbade both.
func TestPOLineRemove_ExactlyOneRemovalIsOffered(t *testing.T) {
	for _, canDelete := range []*bool{boolPtr(true), boolPtr(false), nil} {
		name := "flag absent"
		if canDelete != nil {
			name = fmt.Sprintf("flag=%v", *canDelete)
		}
		t.Run(name, func(t *testing.T) {
			fake := poRemoveOrder("draft", canDelete)
			r, _ := poRemoveRoot(t, fake, 100)
			r, s := poRemoveOnStatusRow(t, r, 0)
			label := poRemoveBarLabel(s)
			if label != "Delete line" && label != "Void line" {
				t.Fatalf("the bar offers no removal at all on the status row (Ctrl-E label %q)", label)
			}
			// And the OTHER one is nowhere on the pane, so the operator is
			// never shown two ways out of one row.
			pane := poRemovePane(s, 100)
			other := "Delete line"
			if label == "Delete line" {
				other = "Void line"
			}
			if strings.Contains(pane, other) {
				t.Fatalf("the pane offers %q as well as %q:\n%s", other, label, pane)
			}
		})
	}
}

// TestPOLineRemove_AnAbsentFlagOffersOnlyTheSafeHalfAndSaysSo.
//
// Absent and false are different facts and only one of them is safe to act on.
// An OMS too old to serve `can_delete_items` has not said the supplier holds
// the order — it has said nothing — so the irreversible action is not offered,
// the reversible one is, and the frame NAMES the silence rather than presenting
// void as the rule. An operator who cannot see the difference cannot report it.
func TestPOLineRemove_AnAbsentFlagOffersOnlyTheSafeHalfAndSaysSo(t *testing.T) {
	fake := poRemoveOrder("draft", nil)
	r, _ := poRemoveRoot(t, fake, 100)
	r, s := poRemoveOnStatusRow(t, r, 0)

	if got := poRemoveBarLabel(s); got != "Void line" {
		t.Fatalf("with the flag absent the bar offers %q, want the safe half", got)
	}
	pane := poRemovePane(s, 100)
	if !strings.Contains(pane, "did not report") {
		t.Fatalf("the frame does not say the server left the question unanswered:\n%s", pane)
	}
	// Pressing it must not destroy anything.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseVoidLine {
		t.Fatalf("Ctrl-E opened phase %v, want the void prompt", s.phase)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.deletes) != 0 {
		t.Fatalf("a DELETE went out on a server that never said lines may be deleted: %v", fake.deletes)
	}
}

// ---------------------------------------------------------------------------
// The delete itself
// ---------------------------------------------------------------------------

// TestPOLineRemove_ADraftLineIsDeletedWithNoReasonAsked walks the whole thing:
// the confirm NAMES what is about to be destroyed, Ctrl-X sends a DELETE with
// an empty body to the line's own URL, the line is gone from the order the
// screen reloads, and nothing was voided on the way.
func TestPOLineRemove_ADraftLineIsDeletedWithNoReasonAsked(t *testing.T) {
	fake := poRemoveOrder("draft", boolPtr(true))
	r, _ := poRemoveRoot(t, fake, 100)
	r, s := poRemoveOnStatusRow(t, r, 0)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("Ctrl-E on a deletable order opened phase %v, want the delete confirm", s.phase)
	}

	// The confirmation names what will be destroyed — the line, and the numbers
	// an operator checks before an irreversible write.
	pane := poRemovePane(s, 100)
	for _, want := range []string{"M3×12 hex bolt, stainless", "250", "$31.25", "no undo"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the delete confirm does not name %q:\n%s", want, pane)
		}
	}
	// And it asks for no reason: there is nothing focused to type into.
	if s.voidReason.Focused() {
		t.Fatal("the delete confirm focused the void-reason box — deleting takes no reason")
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if want := []string{"line-bolts"}; len(fake.deletes) != 1 || fake.deletes[0] != want[0] {
		t.Fatalf("DELETEs received %v, want %v", fake.deletes, want)
	}
	if body := strings.TrimSpace(fake.deleteBodies[0]); body != "" {
		t.Fatalf("the DELETE carried a body %q — deleting takes no reason", body)
	}
	if len(fake.voids) != 0 {
		t.Fatalf("a void went out as well as the delete: %v", fake.voids)
	}
	if fake.line("line-bolts") != nil {
		t.Fatal("the line is still on the order after the delete")
	}
	// The screen came back to the form and reloaded, so the row is gone from
	// what the operator can see.
	if s.phase != poEditPhaseForm {
		t.Fatalf("after the delete the screen is on phase %v, want the form", s.phase)
	}
	if s.po != nil && len(s.po.Items) != 1 {
		t.Fatalf("the reloaded order still shows %d lines, want 1", len(s.po.Items))
	}
}

// TestPOLineRemove_ASentLineIsVoidedWithItsReason is the other half: the same
// row, the same key, an order the supplier holds. The reason is REQUIRED here —
// that is what void is for — and no DELETE is ever issued.
func TestPOLineRemove_ASentLineIsVoidedWithItsReason(t *testing.T) {
	fake := poRemoveOrder("sent", boolPtr(false))
	r, _ := poRemoveRoot(t, fake, 100)
	r, s := poRemoveOnStatusRow(t, r, 0)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseVoidLine {
		t.Fatalf("Ctrl-E on an order the supplier holds opened phase %v, want the void prompt", s.phase)
	}
	r = poTypeRunes(t, r, "wrong part")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.deletes) != 0 {
		t.Fatalf("a DELETE went out on an order the supplier holds: %v", fake.deletes)
	}
	if len(fake.voids) != 1 {
		t.Fatalf("voids received %v, want exactly one", fake.voids)
	}
	if got := fake.voids[0]["reason"]; got != "wrong part" {
		t.Fatalf("the void carried reason %v, want %q", got, "wrong part")
	}
}

// TestPOLineRemove_AServerRefusalReachesTheOperatorInTheServersWords.
//
// `_destroy_item` writes its refusals by hand as {"error", "code"}, so they
// never reach OMS's DRF exception handler and parseError puts the ENTIRE raw
// body into APIError.Message. Without omsapi.asLineRefusal the operator reads
// the JSON — on the one step where losing the reason costs them the fix.
//
// The sentence is the SERVER'S. Nothing here translates it into something
// friendlier, because what makes it useful is the part only the server knows:
// which of the two true reasons applies, and what to do instead.
func TestPOLineRemove_AServerRefusalReachesTheOperatorInTheServersWords(t *testing.T) {
	const reason = "This line records 4 received, so it cannot be deleted. " +
		"Correct the received quantity to 0 first, then delete the line."
	fake := poRemoveOrder("draft", boolPtr(true))
	fake.refuseStatus = http.StatusBadRequest
	fake.refuse = fmt.Sprintf(`{"error": %q, "code": "line_received"}`, reason)

	r, _ := poRemoveRoot(t, fake, 120)
	r, s := poRemoveOnStatusRow(t, r, 0)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	if s.errMsg != reason {
		t.Fatalf("the screen recorded %q, want the server's own sentence %q", s.errMsg, reason)
	}
	if strings.Contains(s.errMsg, "{") || strings.Contains(s.errMsg, "code") {
		t.Fatalf("the raw refusal body reached the operator: %q", s.errMsg)
	}
	pane := poRemovePane(s, 120)
	if !strings.Contains(pane, "This line records 4 received") {
		t.Fatalf("the refusal is not on the pane:\n%s", pane)
	}
	if fake.line("line-bolts") == nil {
		t.Fatal("the refused line was removed from the order anyway")
	}
}

// TestPOLineRemove_AVoidRefusalReachesTheOperatorToo.
//
// `void_item` writes a THIRD shape — {"error"} with no code — which the coded
// recogniser rejects on purpose. Both are recovered, so neither removal makes
// the operator read a payload.
func TestPOLineRemove_AVoidRefusalReachesTheOperatorToo(t *testing.T) {
	const reason = "Cannot void line item that has already been received. " +
		"Use notes to document the issue instead."
	fake := &fakeRemovePO{
		status: "sent", canDelete: boolPtr(false),
		lines: []*fakeRemoveLine{
			{id: "line-bolts", label: "M3×12 hex bolt", ordered: 250, received: 250, cost: "31.25"},
			{id: "line-washers", label: "M3 washer", ordered: 500, cost: "12.50"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/void/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error": %q}`, reason)
			return
		}
		fake.handler()(w, r)
	}))
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewPurchaseOrderDetailScreen(deps, "po-1")
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	root := pump(t, next.(Root), detail.Init(), 0)

	root, s := poRemoveOnStatusRow(t, root, 0)
	root = key(t, root, tea.KeyMsg{Type: tea.KeyCtrlE})
	root = poTypeRunes(t, root, "wrong part")
	root = key(t, root, tea.KeyMsg{Type: tea.KeyEnter})

	if s.errMsg != reason {
		t.Fatalf("the screen recorded %q, want the server's own sentence %q", s.errMsg, reason)
	}
}

// ---------------------------------------------------------------------------
// The trap the web app shipped, answered rather than repeated
// ---------------------------------------------------------------------------

// The delete confirm used to warn here that deleting the last active line
// would drop the order out of every purchase-order list. oms-a8o narrowed that
// filter to orders OUTSIDE PRE_SUPPLIER_STATUSES — the very set
// `can_delete_items` is served from — so no delete this screen can reach hides
// anything, and the check that asserted the warning was replaced by one that
// asserts its ABSENCE (TestPOLineRemove_TheDeleteConfirmDropsTheLossItCannotCause,
// below) rather than deleted. The warning itself moved to the void prompt,
// where the loss survived: see the block beginning "Where the vanishing order
// actually lives now".

// ---------------------------------------------------------------------------
// The frame itself
// ---------------------------------------------------------------------------

// TestPOLineRemove_TheConfirmNamesExactlyTheKeysThatWork presses the whole key
// space at the confirm and holds the rule in both directions.
//
// Ctrl-X and not Enter is deliberate: Ctrl-E opens this frame and enter is the
// key a hand reaches for next, so binding the irreversible write to it would
// make a reflex enough to destroy a line.
func TestPOLineRemove_TheConfirmNamesExactlyTheKeysThatWork(t *testing.T) {
	for _, height := range []int{24, 30} {
		t.Run(fmt.Sprintf("80x%d", height), func(t *testing.T) {
			fresh := func(t *testing.T) (Root, *PurchaseOrderEditScreen, *fakeRemovePO) {
				t.Helper()
				fake := poRemoveOrder("draft", boolPtr(true))
				srv := httptest.NewServer(fake.handler())
				t.Cleanup(srv.Close)
				deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
				detail := NewPurchaseOrderDetailScreen(deps, "po-1")
				r := newTestRoot(detail)
				r.deps = deps
				next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: height})
				root := pump(t, next.(Root), detail.Init(), 0)
				root, s := poRemoveOnStatusRow(t, root, 0)
				root = key(t, root, tea.KeyMsg{Type: tea.KeyCtrlE})
				if s.phase != poEditPhaseDeleteLine {
					t.Fatalf("phase %v, want the delete confirm", s.phase)
				}
				return root, s, fake
			}

			_, screen, _ := fresh(t)
			named := map[string]bool{}
			for _, it := range screen.deleteBar() {
				keys, ok := poBarKeyNames[it.Key]
				if !ok {
					t.Fatalf("bar entry %q is not in poBarKeyNames", it.Key)
				}
				for _, k := range keys {
					named[k] = true
				}
			}

			for _, k := range poKeySpace() {
				r, s, fake := fresh(t)
				before := poRemoveState(s)
				next, cmd := r.Update(poPhaseKeyMsg(k))
				acted := poRemoveState(s) != before || poCmdActs(cmd)
				_ = next
				switch {
				case named[k] && !acted:
					t.Errorf("the delete confirm names %q but pressing it changes nothing", k)
				case !named[k] && !acted:
					// Every key on this frame must ANSWER. A frame that binds
					// two keys and returns nil for the rest redraws a pane that
					// is a pure function of unchanged state — byte for byte
					// identical, which reads as a wedged program.
					t.Errorf("the delete confirm answers %q with nothing at all", k)
				}
				fake.mu.Lock()
				destroyed := len(fake.deletes)
				fake.mu.Unlock()
				if k != "ctrl+x" && destroyed != 0 {
					t.Fatalf("pressing %q issued a DELETE", k)
				}
			}
		})
	}
}

// poRemoveState is the confirm's observable state.
func poRemoveState(s *PurchaseOrderEditScreen) string {
	return fmt.Sprint(s.phase, "|", s.editLineIdx, "|", s.saving, "|", s.errMsg, "|",
		s.deleteNote, "|", s.cursor, "|", s.lineFocus)
}

// TestPOLineRemove_NoTwoDeclinedKeysRedrawOneFrame: the answers are pressed IN
// SEQUENCE with no reset between them, because two keys sharing one sentence
// redraw a byte-identical pane on the second press — the reported hang exactly,
// and a sweep that resets between presses is structurally unable to see it.
func TestPOLineRemove_NoTwoDeclinedKeysRedrawOneFrame(t *testing.T) {
	fake := poRemoveOrder("draft", boolPtr(true))
	r, _ := poRemoveRoot(t, fake, 80)
	r, s := poRemoveOnStatusRow(t, r, 0)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("phase %v, want the delete confirm", s.phase)
	}
	prev := poRemovePane(s, 80)
	for _, k := range []string{"enter", "y", "d", "v", "up", "down", "pgup", "tab", "delete"} {
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
		pane := poRemovePane(s, 80)
		if pane == prev {
			t.Fatalf("pressing %q redrew a byte-identical pane:\n%s", k, pane)
		}
		prev = pane
	}
}

// TestPOLineRemove_TheConfirmFitsThePaneAndUsesAWiderOne holds rule 5 in both
// directions: nothing overruns 80 columns, and at 120 the same frame draws
// wider rather than clipping itself to a width the terminal did not give.
func TestPOLineRemove_TheConfirmFitsThePaneAndUsesAWiderOne(t *testing.T) {
	widest := 0
	for _, width := range []int{80, 100, 120} {
		fake := poRemoveOrder("draft", boolPtr(true))
		r, _ := poRemoveRoot(t, fake, width)
		r, s := poRemoveOnStatusRow(t, r, 0)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
		if s.phase != poEditPhaseDeleteLine {
			t.Fatalf("phase %v, want the delete confirm", s.phase)
		}
		pane := screenBodyWidth(width)
		longest := 0
		for _, line := range strings.Split(strings.TrimSuffix(s.View(), "\n"), "\n") {
			if n := lipgloss.Width(line); n > longest {
				longest = n
			}
		}
		if longest > pane {
			t.Fatalf("at %d columns a line is %d cells wide against a %d-column pane",
				width, longest, pane)
		}
		if width == 80 {
			widest = longest
		} else if longest <= widest {
			t.Fatalf("at %d columns the frame is no wider (%d) than it was at 80 (%d) — "+
				"a fixed 51 would draw every row abbreviated with the pane left blank",
				width, longest, widest)
		}
	}
}

// TestPOLineRemove_TheConfirmNeverHidesWhatItWillDestroy.
//
// The reported shape, found by drawing the frame at every height it draws at
// rather than at the two in poEditPaneSizes: with the line's identity in the
// BODY, jdeLines gave it ground first — a body gives rows down to nothing — so
// from 80x10 to 80x16 the frame read `Delete line item`, `↓ 13 more below`, and
// a bar saying `Ctrl-X=Delete line`. An irreversible destroy confirmed on a
// frame naming nothing it would destroy, with no key on it able to fetch the
// rest.
//
// So the identity is a PINNED, ESSENTIAL header row: jdeFitHeader gives ground
// by RANK and keeps the essential one last, and wherever the layer draws the
// frame at all this row is on the pane. Below that the layer REFUSES the frame
// with a notice naming the height it needs, which is a state the operator can
// act on rather than a confirm they cannot read.
//
// The fixture carries a name that REACHES the bound. Every other PO fixture in
// this package is seven cells long, and a check about a clip that is never
// clipped passes for reasons unrelated to the property it names.
func TestPOLineRemove_TheConfirmNeverHidesWhatItWillDestroy(t *testing.T) {
	const long = "M3×12 hex-head cap screw, A2-70 stainless, DIN 933, bright finish"
	for _, width := range []int{80, 100, 120} {
		for height := 6; height <= 40; height++ {
			fake := &fakeRemovePO{
				status: "draft", canDelete: boolPtr(true),
				lines: []*fakeRemoveLine{
					{id: "line-bolts", label: long, ordered: 250, cost: "31.25"},
					{id: "line-washers", label: "M3 washer", ordered: 500, cost: "12.50"},
				},
			}
			r, _ := poRemoveRoot(t, fake, width)
			r, s := poRemoveOnStatusRow(t, r, 0)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
			if s.phase != poEditPhaseDeleteLine {
				t.Fatalf("%dx%d: phase %v, want the delete confirm", width, height, s.phase)
			}
			next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
			r = next.(Root)
			pane := clampToBox(s.View(), screenBodyWidth(width), screenBodyHeight(height))

			// A pane too short for the frame draws the layer's refusal instead,
			// which names the height that works — a state with a way out.
			if strings.Contains(pane, "Too short:") {
				continue
			}
			// The FACT never gives.
			if !strings.Contains(pane, "· 250 ordered") {
				t.Fatalf("%dx%d: the confirm does not say what quantity it destroys:\n%s",
					width, height, pane)
			}
			// The identifier abbreviates, and what survives of it is a prefix of
			// the real name rather than nothing at all. Twelve cells is well
			// under what any pane here leaves and is the floor being asserted,
			// not the answer: at 51 columns the row has thirty-odd to give.
			head := ""
			for _, line := range strings.Split(pane, "\n") {
				if strings.Contains(line, "Delete: ") {
					head = line
				}
			}
			if head == "" {
				t.Fatalf("%dx%d: the confirm never names the line at all:\n%s", width, height, pane)
			}
			if !strings.Contains(head, long[:12]) {
				t.Fatalf("%dx%d: the line's name is not on the confirm: %q", width, height, head)
			}
			if got := lipgloss.Width(head); got > screenBodyWidth(width) {
				t.Fatalf("%dx%d: the headline is %d cells against a %d-column pane: %q",
					width, height, got, screenBodyWidth(width), head)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// What a short pane keeps
// ---------------------------------------------------------------------------

// poRemoveUndoSentence is the delete confirm's one remaining caveat, verbatim.
// jdeCaveatLines FOLDS it to the pane, so the pane is flattened before it is
// looked for — a marker chosen to survive an arbitrary fold would be a marker
// chosen because it passes.
const poRemoveUndoSentence = "Deleting takes the line off the order for good."

// poRemoveFlatPane is the clipped pane with its line breaks and fold indents
// collapsed, so a sentence the layer folded across three rows reads as the one
// sentence it is. Everything else about it is poRemovePane's rule: the SCREEN's
// own View, clipped to the pane the terminal really gives it.
func poRemoveFlatPane(s Screen, width, height int) string {
	pane := clampToBox(s.View(), screenBodyWidth(width), screenBodyHeight(height))
	return strings.Join(strings.Fields(pane), " ")
}

// THE DELETE CONFIRM'S CAVEAT ORDERING SWEEP LIVED HERE and is gone with the
// caveat it ordered. It watched the two sentences of deleteCaveats and required
// the vanishing-order one to outlive the irreversibility one at every drawable
// height; with the first retired (oms-a8o) the confirm has one caveat and there
// is no order left to hold, so a sweep kept here would pass on a body it no
// longer describes — the vacuous-fixture failure with the fixture removed
// instead of the assertion.
//
// The two properties it really carried both still have a home, and neither is
// on this frame by accident:
//
//   - "a short pane keeps the identity of what is about to be destroyed" is
//     TestPOLineRemove_TheConfirmNeverHidesWhatItWillDestroy, above, which
//     sweeps every height at three widths.
//   - "whichever caveat is emitted last is the one a short pane drops, so the
//     order between them is a decision" is
//     TestPOLineRemove_AShortVoidPaneKeepsTheVanishingOrderWarning, which holds
//     it on the frame that now carries two.

// ---------------------------------------------------------------------------
// A reload landing under an open confirm
// ---------------------------------------------------------------------------

// poRemoveThreeLineOrder is a draft whose lines can be told apart by name, so a
// confirm addressing the wrong one is visible rather than inferred.
func poRemoveThreeLineOrder() *fakeRemovePO {
	return &fakeRemovePO{
		status: "draft", canDelete: boolPtr(true),
		lines: []*fakeRemoveLine{
			{id: "line-bolts", label: "M3×12 hex bolt", ordered: 250, cost: "31.25"},
			{id: "line-washers", label: "M3 washer", ordered: 500, cost: "12.50"},
			{id: "line-nuts", label: "M3 nyloc nut", ordered: 100, cost: "9.00"},
		},
	}
}

// TestPOLineRemove_AReloadNeverRepointsAnOpenConfirmAtAnotherLine.
//
// Every line action this screen takes fires a reload, and the confirm can be
// reopened before that reload lands — so a reload arriving under an open,
// IRREVERSIBLE confirm is an ordinary sequence rather than a corner. The index
// it addresses is positional, and the clamp that used to hold it in range aimed
// it at whatever now sat at that position: the frame went on naming the line
// the operator had read and confirmed while Ctrl-X would have destroyed a
// different one.
//
// Identity is what must be carried across. Here the confirmed line survives the
// reload in a NEW position, so the confirm survives with it and the write goes
// to the line that was confirmed.
func TestPOLineRemove_AReloadNeverRepointsAnOpenConfirmAtAnotherLine(t *testing.T) {
	fake := poRemoveThreeLineOrder()
	r, _ := poRemoveRoot(t, fake, 100)
	r, s := poRemoveOnStatusRow(t, r, 2)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("phase %v, want the delete confirm", s.phase)
	}
	if pane := poRemovePane(s, 100); !strings.Contains(pane, "nyloc") {
		t.Fatalf("the confirm does not name the line it was opened on:\n%s", pane)
	}

	// A line goes off the order elsewhere and the screen's own reload lands.
	fake.mu.Lock()
	fake.drop("line-bolts")
	fake.mu.Unlock()
	r = pump(t, r, s.load(), 0)

	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("the confirm closed on a reload that still carries its line; phase %v", s.phase)
	}
	if pane := poRemovePane(s, 100); !strings.Contains(pane, "nyloc") {
		t.Fatalf("the confirm now names another line:\n%s", pane)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	fake.mu.Lock()
	got := append([]string(nil), fake.deletes...)
	fake.mu.Unlock()
	if len(got) != 1 || got[0] != "line-nuts" {
		t.Fatalf("Ctrl-X destroyed %v, but the operator confirmed line-nuts", got)
	}
}

// TestPOLineRemove_AConfirmWhoseLineIsGoneClosesAndSaysSo.
//
// The other half of the same reload: the confirmed line is no longer on the
// order at all. Deleting is what makes an EMPTY Items list reachable — voiding
// only struck a line through — and the clamp answered that with a valid-looking
// index 0 into nothing, which every reader of the frame then indexed.
//
// A removal sub-phase cannot survive onto another line, and there is no line
// left to survive onto, so it closes back to the form and the status row says
// why. Nothing is written on the way.
func TestPOLineRemove_AConfirmWhoseLineIsGoneClosesAndSaysSo(t *testing.T) {
	fake := &fakeRemovePO{
		status: "draft", canDelete: boolPtr(true),
		lines: []*fakeRemoveLine{
			{id: "line-bolts", label: "M3×12 hex bolt", ordered: 250, cost: "31.25"},
		},
	}
	r, _ := poRemoveRoot(t, fake, 100)
	r, s := poRemoveOnStatusRow(t, r, 0)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("phase %v, want the delete confirm", s.phase)
	}

	fake.mu.Lock()
	fake.drop("line-bolts")
	fake.mu.Unlock()
	r = pump(t, r, s.load(), 0)

	if s.phase != poEditPhaseForm {
		t.Fatalf("the confirm outlived the line it names; phase %v", s.phase)
	}
	pane := poRemoveFlatPane(s, 100, 40)
	if !strings.Contains(pane, poEditLineGoneNote) {
		t.Fatalf("the screen never says why the confirm went away:\n%s", pane)
	}

	// And the keys the confirm bound are the form's again — Ctrl-X on the form
	// is not a destroy, and nothing has gone out.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	fake.mu.Lock()
	got := len(fake.deletes)
	fake.mu.Unlock()
	if got != 0 {
		t.Fatalf("%d delete(s) went out for a line that is not on the order", got)
	}
}

// ---------------------------------------------------------------------------
// A removal offered over a write already in flight
// ---------------------------------------------------------------------------

// TestPOLineRemove_NoRemovalOpensOverAnInFlightLineSave.
//
// Enter on the line editor saves the line and leaves the request out; the
// status row's Ctrl-E used to open a removal on top of it. The delete confirm
// then drew `Deleting…` for a delete nobody had asked for, its own Ctrl-X was
// dropped for as long as the OTHER write was in flight, and it vanished by
// itself when that write answered — a frame reporting the wrong work that could
// not be acted on.
//
// Both halves are asserted, because the rule is a biconditional: the bar must
// not name Ctrl-E there while a write is out, and the key must not act.
func TestPOLineRemove_NoRemovalOpensOverAnInFlightLineSave(t *testing.T) {
	for _, tc := range []struct {
		name      string
		canDelete *bool
	}{
		{"deletable order", boolPtr(true)},
		{"voidable order", boolPtr(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := poRemoveOrder("draft", tc.canDelete)
			r, _ := poRemoveRoot(t, fake, 100)
			r, s := poRemoveOnStatusRow(t, r, 0)
			if poRemoveBarLabel(s) == "" {
				t.Fatal("the bar names no removal at rest, so this case cannot see one withdrawn")
			}

			// Enter saves the line. The command is deliberately NOT pumped: an
			// in-flight save is the state under test.
			next, _ := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
			r = next.(Root)
			if !s.saving {
				t.Fatal("enter on the line editor did not put a save in flight")
			}
			if label := poRemoveBarLabel(s); label != "" {
				t.Fatalf("the bar offers %q while a line save is in flight", label)
			}

			r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
			if s.phase != poEditPhaseLine {
				t.Fatalf("Ctrl-E opened phase %v over an in-flight save", s.phase)
			}
			if pane := poRemoveFlatPane(s, 100, 40); strings.Contains(pane, "Deleting…") {
				t.Fatalf("the frame reports a delete nobody asked for:\n%s", pane)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The flag is re-read, never cached across a refresh
// ---------------------------------------------------------------------------

// TestPOLineRemove_AnOpenConfirmDoesNotOutliveTheFlagThatOpenedIt.
//
// A reload landing under an open removal sub-phase is a supported, surviving
// state: the sub-phase follows its own LINE across the refresh. The line
// surviving says nothing about the ORDER, though, so the flag has to be read
// AGAIN — `can_delete_items` is served from PRE_SUPPLIER_STATUSES and its
// docstring forbids a client caching it across a refresh.
//
// Both directions are driven, not just the one that was reported: a delete
// confirm open when the order goes to the supplier, and a void prompt open when
// the order becomes the shop's own again. Neither may write, neither may
// silently become the other, and both must say what changed.
func TestPOLineRemove_AnOpenConfirmDoesNotOutliveTheFlagThatOpenedIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opened  *bool
		flipped *bool
		phase   poEditPhase
		want    string
	}{
		{"a draft that goes to the supplier", boolPtr(true), boolPtr(false),
			poEditPhaseDeleteLine, poEditDeleteFlippedNote},
		{"a sent order that becomes the shop's own", boolPtr(false), boolPtr(true),
			poEditPhaseVoidLine, poEditVoidFlippedNote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := poRemoveOrder("draft", tc.opened)
			r, _ := poRemoveRoot(t, fake, 80)
			r, s := poRemoveOnStatusRow(t, r, 0)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
			if s.phase != tc.phase {
				t.Fatalf("phase %v, want %v", s.phase, tc.phase)
			}

			fake.mu.Lock()
			fake.canDelete = tc.flipped
			fake.mu.Unlock()
			r = pump(t, r, s.load(), 0)

			if s.phase != poEditPhaseLine {
				t.Fatalf("the removal frame outlived the flag that opened it; phase %v", s.phase)
			}
			pane := poRemoveFlatPane(s, 80, 40)
			if !strings.Contains(pane, tc.want) {
				t.Fatalf("the screen never says what changed (%q missing):\n%s", tc.want, pane)
			}

			// Neither key the closed frame bound may write now.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			fake.mu.Lock()
			deletes, voids := len(fake.deletes), len(fake.voids)
			fake.mu.Unlock()
			if deletes != 0 || voids != 0 {
				t.Fatalf("%d delete(s) and %d void(s) went out after the flag changed", deletes, voids)
			}
		})
	}
}

// TestPOLineRemove_ARefusalKeepsItsFactOnTheStatusRowAt80Columns.
//
// `fitStatus` gives an error message bodyWidth-2 cells — 49 at 80 columns — and
// the status row cannot fold, so a sentence that opens with its circumstance
// loses the clause it exists for. The clause these carry is that NOTHING WAS
// WRITTEN, and it is the one thing an operator whose confirm just vanished
// needs; it leads, so 49 cells is enough for the whole of it.
func TestPOLineRemove_ARefusalKeepsItsFactOnTheStatusRowAt80Columns(t *testing.T) {
	fake := &fakeRemovePO{
		status: "draft", canDelete: boolPtr(true),
		lines: []*fakeRemoveLine{
			{id: "line-bolts", label: "M3×12 hex bolt", ordered: 250, cost: "31.25"},
		},
	}
	r, _ := poRemoveRoot(t, fake, 80)
	r, s := poRemoveOnStatusRow(t, r, 0)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("phase %v, want the delete confirm", s.phase)
	}

	fake.mu.Lock()
	fake.drop("line-bolts")
	fake.mu.Unlock()
	r = pump(t, r, s.load(), 0)

	if pane := poRemoveFlatPane(s, 80, 40); !strings.Contains(pane, poEditLineGoneNote) {
		t.Fatalf("the 80-column status row does not carry the whole refusal (%q):\n%s",
			poEditLineGoneNote, pane)
	}
}

// TestPOLineRemove_TheStandingNoteNeverNamesAKeyTheBarDropped.
//
// The status row's standing note and the action bar are two surfaces describing
// one key, and they read one predicate (removalOffered) so they cannot come
// apart. With a line save in flight the bar drops Ctrl-E; the note used to go on
// saying "Ctrl-E asks you to confirm first" on a frame where the key did
// nothing at all.
//
// What the note must NOT lose is the standing FACT about the row, so the
// at-rest half is asserted too: gating the whole sentence rather than the clause
// naming the key would pass the absence check while taking the row's own
// explanation away with it.
func TestPOLineRemove_TheStandingNoteNeverNamesAKeyTheBarDropped(t *testing.T) {
	fake := poRemoveOrder("draft", boolPtr(true))
	r, _ := poRemoveRoot(t, fake, 80)
	r, s := poRemoveOnStatusRow(t, r, 0)

	rest := poRemoveFlatPane(s, 80, 40)
	if !strings.Contains(rest, "Ctrl-E") {
		t.Fatalf("at rest the bar and the note both name Ctrl-E; this pane names it nowhere:\n%s", rest)
	}
	if !strings.Contains(rest, "can be DELETED outright") {
		t.Fatalf("the row's standing fact is missing at rest:\n%s", rest)
	}

	// Enter puts a line save in flight; the command is deliberately not pumped.
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !s.saving {
		t.Fatal("enter on the line editor did not put a save in flight")
	}
	if label := poRemoveBarLabel(s); label != "" {
		t.Fatalf("the bar still offers %q while a save is in flight", label)
	}

	flight := poRemoveFlatPane(s, 80, 40)
	if strings.Contains(flight, "Ctrl-E") {
		t.Fatalf("the body names Ctrl-E on a frame whose bar has dropped it:\n%s", flight)
	}
	if !strings.Contains(flight, "can be DELETED outright") {
		t.Fatalf("gating the key clause took the row's standing fact with it:\n%s", flight)
	}
}

// ---------------------------------------------------------------------------
// Where the vanishing order actually lives now
// ---------------------------------------------------------------------------

// The purchase-order LIST hides an order only when ALL THREE hold: the order
// HAS line items, none of them survives unvoided, and it is OUTSIDE
// PurchaseOrder.PRE_SUPPLIER_STATUSES (OMS's PurchaseOrderViewSet.get_queryset,
// oms-a8o). An order that never had lines is listed, and so is one still inside
// the pre-supplier set — an order still being built is always findable.
//
// That splits the two removals cleanly, and it is why these tests come in a
// matched pair:
//
//   - DELETE is offered only where can_delete_items is true, and that flag is
//     served from PRE_SUPPLIER_STATUSES itself. So the third conjunct is FALSE
//     on every order a delete can reach, and no delete this screen can perform
//     can hide anything. A confirm warning that it will is a documented claim
//     the code cannot honour.
//   - VOID is offered on the other two answers, and on the one that is not a
//     silence the supplier demonstrably holds the order — outside the
//     pre-supplier set, so voiding the last active line hides it. Nothing puts
//     it back either: OMS has no unvoid endpoint (is_voided is only ever set
//     true) and assert_addable refuses a line on an order past the same
//     boundary, so ctrl+k search is the way back and it is the only one.

// The void prompt's caveats, verbatim and test-side, so what is asserted is the
// wording the screen DRAWS rather than a constant both sides happen to share.
//
// They are split at the clause rather than the sentence because the two
// properties held below are about clauses: that the LOSS is never drawn without
// its WAY BACK (a warning cut before its remedy is a dead end), and that the
// unanswered flag draws a CONDITIONAL rather than either conclusion.
const (
	// The one-row warning, which is what the sweep below is really about: it is
	// short enough that jdeFitHeader can only keep it or drop it, never cut it.
	poVoidHeadline        = "Voiding hides the order; only search finds it."
	poVoidHeadlineUnknown = "Voiding may hide the order; only search finds it."
	poVoidWayBack         = "search finds it."

	// The key is named only in the prose, and only BEHIND the clause that says
	// where it works — see voidSearchSentence. Asserting the pair rather than
	// the whole sentence is the point: the qualifier PRECEDES the key, so a cut
	// can only ever take the key, never leave it standing bare.
	poVoidKeyClause = "Once you leave this screen, ctrl+k"

	// The prose behind it. poVoidLossClause is the fact the headline
	// summarises, drawn only where there is room for the detail as well.
	poVoidCondition    = "This is the order's only unvoided line"
	poVoidLossClause   = "every purchase-order list"
	poVoidUnknownWhy   = "can_delete_items"
	poVoidStandingNote = "This marks the line voided and the supplier link discontinued."
)

// poVoidPrompt drives the operator's own keys to the void prompt on the line at
// lineIdx and hands back the live screen.
func poVoidPrompt(t *testing.T, fake *fakeRemovePO, width, height, lineIdx int) (Root, *PurchaseOrderEditScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewPurchaseOrderDetailScreen(deps, "po-1")
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	r = pump(t, next.(Root), detail.Init(), 0)
	r, s := poRemoveOnStatusRow(t, r, lineIdx)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseVoidLine {
		t.Fatalf("Ctrl-E opened phase %v, want the void prompt", s.phase)
	}
	if height != 40 {
		next, _ = r.Update(tea.WindowSizeMsg{Width: width, Height: height})
		r = next.(Root)
	}
	return r, s
}

// poVoidSentOrder is an order the SUPPLIER holds, with one active line: voiding
// it empties the order outside the pre-supplier set, which is the one route
// into the vanishing order that survives oms-a8o.
func poVoidSentOrder(canDelete *bool) *fakeRemovePO {
	return &fakeRemovePO{
		status: "sent", canDelete: canDelete,
		lines: []*fakeRemoveLine{
			{id: "line-bolts", label: "M3×12 hex bolt", ordered: 250, cost: "31.25"},
		},
	}
}

// TestPOLineRemove_VoidingTheLastActiveLineSaysWhereTheOrderGoes.
//
// The warning did not become obsolete when oms-a8o shipped; it MOVED. Deleting
// can no longer hide an order, voiding the last active line of an order the
// supplier holds still can, and this is the frame that key is pressed from.
//
// The way back is named because a warning the operator cannot act on is a dead
// end — and here it is the ONLY way back, which the sentence has to say: past
// the pre-supplier boundary OMS refuses to add a line (assert_addable) and has
// no unvoid at all, so the delete confirm's old "until another line is added"
// would have been a false remedy on this path.
func TestPOLineRemove_VoidingTheLastActiveLineSaysWhereTheOrderGoes(t *testing.T) {
	_, s := poVoidPrompt(t, poVoidSentOrder(boolPtr(false)), 100, 40, 0)
	flat := poRemoveFlatPane(s, 100, 40)
	for _, want := range []string{poVoidHeadline, poVoidCondition, poVoidLossClause} {
		if !strings.Contains(flat, want) {
			t.Errorf("the void prompt does not warn about the list (%q missing):\n%s", want, flat)
		}
	}
	// The server said the supplier holds it, so the prompt states the outcome
	// rather than hedging it — could-not-tell has its own test.
	if strings.Contains(flat, poVoidHeadlineUnknown) {
		t.Errorf("the prompt hedges an outcome the flag settled:\n%s", flat)
	}

	// …and NOT where it would be false: a second active line survives the void,
	// so the order keeps a live line and stays on every list.
	_, two := poVoidPrompt(t, poRemoveOrder("sent", boolPtr(false)), 100, 40, 0)
	if flat := poRemoveFlatPane(two, 100, 40); strings.Contains(flat, poVoidLossClause) {
		t.Errorf("the list warning is drawn on an order that will still have a line:\n%s", flat)
	}
}

// TestPOLineRemove_AnUnansweredFlagSaysItCouldNotTellRatherThanConcluding.
//
// poRemovalUnknown lands on the void prompt too, and there the client does not
// know which side of the pre-supplier boundary the order is on — so it does not
// know whether voiding hides it. Found-nothing and could-not-tell are different
// facts: the frame says the server did not answer rather than asserting either
// outcome, and it still names the way back, which is the same either way.
func TestPOLineRemove_AnUnansweredFlagSaysItCouldNotTellRatherThanConcluding(t *testing.T) {
	_, s := poVoidPrompt(t, poVoidSentOrder(nil), 100, 40, 0)
	flat := poRemoveFlatPane(s, 100, 40)
	for _, want := range []string{poVoidHeadlineUnknown, poVoidCondition, poVoidUnknownWhy} {
		if !strings.Contains(flat, want) {
			t.Errorf("the void prompt concludes instead of saying it could not tell (%q missing):\n%s", want, flat)
		}
	}
	if strings.Contains(flat, poVoidHeadline) {
		t.Errorf("the frame states the outcome flatly on a server that never answered:\n%s", flat)
	}
}

// TestPOLineRemove_TheDeleteConfirmDropsTheLossItCannotCause.
//
// The mirror of the two above, and the reason this file no longer has a test
// asserting the delete confirm warns. can_delete_items is served from
// PRE_SUPPLIER_STATUSES and the list's third disjunct reads the same frozenset,
// so an order a delete can reach is listed whatever the delete leaves behind —
// zero lines, or voided ghosts. A warning describing a loss that cannot happen
// is as wrong as silence about one that can.
//
// Watched to fail: with the old caveat restored it reports on every one of
// these fixtures.
func TestPOLineRemove_TheDeleteConfirmDropsTheLossItCannotCause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []*fakeRemoveLine
	}{
		{"the order's only line", []*fakeRemoveLine{
			{id: "line-bolts", label: "M3×12 hex bolt", ordered: 250, cost: "31.25"},
		}},
		{"the last ACTIVE line, over a voided ghost", []*fakeRemoveLine{
			{id: "line-ghost", label: "M3 washer", ordered: 500, cost: "12.50", voided: true},
			{id: "line-bolts", label: "M3×12 hex bolt", ordered: 250, cost: "31.25"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeRemovePO{status: "draft", canDelete: boolPtr(true), lines: tc.lines}
			r, _ := poRemoveRoot(t, fake, 100)
			idx := len(tc.lines) - 1
			r, s := poRemoveOnStatusRow(t, r, idx)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
			if s.phase != poEditPhaseDeleteLine {
				t.Fatalf("phase %v, want the delete confirm", s.phase)
			}
			flat := poRemoveFlatPane(s, 100, 40)
			for _, gone := range []string{poVoidHeadline, poVoidCondition, poVoidLossClause, "hides an order with no active lines", "ctrl+k"} {
				if strings.Contains(flat, gone) {
					t.Errorf("the delete confirm still asserts a loss it cannot cause (%q present):\n%s", gone, flat)
				}
			}
			// The caveat it DOES carry is untouched: deleting is still the
			// irreversible half and the frame still says so.
			if !strings.Contains(flat, poRemoveUndoSentence) {
				t.Errorf("the confirm lost the caveat that is still true:\n%s", flat)
			}
		})
	}
}

// TestPOLineRemove_AShortVoidPaneKeepsTheVanishingOrderWarning.
//
// The same sacrifice-order decision the delete confirm made, one surface over.
// The void prompt's body holds the operator's REASON box, which must stay on
// the pane or every rune typed redraws a byte-identical frame — so the caveats
// ride the pinned header, where jdeFitHeader gives ground BY RANK and, within a
// rank, from the END. The vanishing warning is therefore emitted FIRST among
// the context rows: it is the one fact on the frame nothing else carries, while
// "this marks the line voided" is restated by the bar's `Enter=Void line` and
// by the header's own essential row.
//
// Watched to fail: with the two context rows the other way round it reports
// across the short end of the range.
func TestPOLineRemove_AShortVoidPaneKeepsTheVanishingOrderWarning(t *testing.T) {
	heights := jdePaneHeights()
	if len(heights) == 0 {
		t.Fatal("no drawable heights — the derivation is broken, not the screen")
	}
	// Both axes. 80 is the width this interface is modelled on and the one that
	// must HOLD, and it is where the fold is tightest — 51 cells of pane — so a
	// sweep run only at 100 would measure the caveat at a width the operator
	// may not have.
	for _, width := range []int{80, 100, 120} {
		t.Run(fmt.Sprintf("%dcols", width), func(t *testing.T) {
			voidPaneSweep(t, width, heights)
		})
	}
}

func voidPaneSweep(t *testing.T, width int, heights []int) {
	t.Helper()

	// The fixture has to REACH the bound: on the tallest pane both rows are
	// drawn, so a run where the second never appears would pass for a reason
	// unrelated to the sacrifice this test is about.
	tallest := heights[len(heights)-1]
	if _, s := poVoidPrompt(t, poVoidSentOrder(boolPtr(false)), width, tallest, 0); true {
		flat := poRemoveFlatPane(s, width, tallest)
		if !strings.Contains(flat, poVoidLossClause) || !strings.Contains(flat, poVoidStandingNote) {
			t.Fatalf("height %d draws neither caveat in full, so the sacrifice never happens:\n%s", tallest, flat)
		}
	}

	for _, height := range heights {
		_, s := poVoidPrompt(t, poVoidSentOrder(boolPtr(false)), width, height, 0)
		flat := poRemoveFlatPane(s, width, height)
		warned := strings.Contains(flat, poVoidHeadline)
		detail := strings.Contains(flat, poVoidLossClause)
		standing := strings.Contains(flat, poVoidStandingNote)
		if standing && !warned {
			t.Errorf("%dx%d: the void prompt keeps the caveat said elsewhere and drops the one nothing else carries:\n%s",
				width, height, flat)
		}
		if detail && !warned {
			t.Errorf("%dx%d: the prose survives and the headline it summarises does not:\n%s",
				width, height, flat)
		}
		// A warning cut before its remedy is a dead end, which is its own
		// defect: wherever the pane says a thing is about to be lost it also
		// says how to get it back. This is what forced the headline down to ONE
		// row (voidCaveats) — as a single long caveat the 80x14 pane read "…and
		// nothing puts it back:" and stopped, and the two-row rewrite of it
		// stopped at "…every purchase-order list;" on 80x13.
		if (warned || detail) && !strings.Contains(flat, poVoidWayBack) {
			t.Errorf("%dx%d: the pane warns and does not name the way back:\n%s", width, height, flat)
		}
		// A key named on a frame that does not honour it is worse than no key
		// at all. This screen takes raw input, so ctrl+k never reaches the
		// root's search palette here — it reaches the focused Reason box, where
		// bubbles deletes to end of line. So wherever the pane spells the key
		// it also carries the clause saying when it applies.
		if strings.Contains(flat, "ctrl+k") && !strings.Contains(flat, poVoidKeyClause) {
			t.Errorf("%dx%d: the pane names ctrl+k without saying it works only after leaving:\n%s",
				width, height, flat)
		}
		// Rule 1: the box the operator types into is on the pane at every
		// height the frame is DRAWN at, or a keystroke changes nothing visible.
		// A pane the layer REFUSES is not one of them — it draws a bounded
		// notice naming the height it needs and says the moving keys are held,
		// which is the layer's designed answer and not this screen's to
		// override (the same reading po_line_remove's other height sweeps make).
		if strings.Contains(flat, "Too short:") {
			continue
		}
		if !strings.Contains(flat, "Reason") {
			t.Errorf("%dx%d: the reason box is off the pane:\n%s", width, height, flat)
		}
	}
}
