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
	// The one-row warning, which is what the sweep below is really about: at
	// every width it is DRAWN at it is short enough that jdeFitHeader can only
	// keep it or drop it, never cut it, because voidCaveats withholds it
	// entirely at a width where it would fold.
	//
	// THE REMEDY LEADS THE LOSS IN BOTH, and that ordering is the property, not
	// the words: jdeCaveatLines folds from the tail and jdeFitHeader trims from
	// the tail, so whatever must survive must lead. Every prefix of either
	// wording therefore either makes no loss claim or carries the remedy.
	//
	// Only the void wording says "only". On the unknown answer the order may
	// not be hidden at all — the lists would still find it — so "only search
	// finds the order" is a claim that branch cannot make; the hedge goes on
	// the loss and the remedy that leads is true either way.
	poVoidHeadline        = "Only search finds the order; voiding hides it."
	poVoidHeadlineUnknown = "Search finds the order; voiding may hide it."

	// The remedy clause each wording leads with, asserted per branch because
	// the two differ by exactly the "only" above.
	poVoidWayBack        = "Only search finds the order"
	poVoidWayBackUnknown = "Search finds the order"

	// The LOSS clause each wording trails with, and it is a FRAGMENT on
	// purpose. The rendered rule "a loss is never stated without its remedy"
	// keyed on the whole headline could not fail: the remedy leads, so it is a
	// substring, and a headline that a fold SPLIT reads as absent altogether
	// once poRemoveFlatPane has collapsed the pane. Keyed on the loss fragment
	// it fires on exactly the pane the split produces — the half that states
	// the loss, with the half naming the way back trimmed off.
	poVoidLossMark        = "voiding hides it"
	poVoidLossMarkUnknown = "voiding may hide it"

	// The key is named only in the prose, and only BEHIND the clause that says
	// where it works — see voidSearchSentence. Asserting the pair rather than
	// the whole sentence is the point: the qualifier PRECEDES the key, so a cut
	// can only ever take the key, never leave it standing bare.
	poVoidKeyClause = "Once you leave this screen, ctrl+k"

	// The number the way back tells the operator to search for. The fake serves
	// PO-2026-0042, so a frame that says "by number" and shows none fails.
	poVoidOrderNumber = "PO-2026-0042"

	// The number AND the phrase it must precede, in the one order a trim cannot
	// break: the header gives ground from the END, so a cut that keeps
	// "by number" keeps everything ahead of it. Asserted as one phrase because
	// the two halves apart say nothing about their order, and the order is the
	// whole guarantee — the tail-appended wording drew "…by number:" with the
	// number trimmed off at 80x18.
	poVoidNumberedPhrase = poVoidOrderNumber + " by number"

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

// poVoidBranch is one ANSWER about deletability together with the wording the
// void prompt draws on it. The two differ — poRemovalVoid states the outcome,
// poRemovalUnknown states it conditionally — so the sweep below has to select
// its expected strings per branch rather than assert one wording over both.
type poVoidBranch struct {
	name      string
	canDelete *bool
	removal   poLineRemoval
	headline  string
	remedy    string
	loss      string
	other     string
}

// poVoidBranches DERIVES the answers that reach the void prompt instead of
// listing two cases and hoping they are the two.
//
// The whole space of the flag is three values — absent, false, true — and
// poRemovalFor is asked which removal each one means, exactly as the screen
// asks it. poRemovalDelete is filtered out because it cannot reach this frame
// at all: removalPhaseHolds closes the prompt the moment the refreshed flag
// says the line may be destroyed outright. Everything left MUST have a wording
// asserted for it, so an answer added to the iota fails here rather than being
// swept silently under whichever headline happened to be listed.
//
// This is the derivation that was missing. The sweep was built on
// poVoidSentOrder(boolPtr(false)) alone, so it was structurally blind to the
// poRemovalUnknown branch — which is how a caveat whose trailing row carried
// the order number, and nothing else, shipped green.
func poVoidBranches(t *testing.T) []poVoidBranch {
	t.Helper()
	wordings := map[poLineRemoval]struct{ headline, remedy, loss string }{
		poRemovalVoid:    {poVoidHeadline, poVoidWayBack, poVoidLossMark},
		poRemovalUnknown: {poVoidHeadlineUnknown, poVoidWayBackUnknown, poVoidLossMarkUnknown},
	}
	answers := []struct {
		name string
		flag *bool
	}{
		{"the supplier holds it", boolPtr(false)},
		{"the server never said", nil},
		{"still the shop's own", boolPtr(true)},
	}
	var out []poVoidBranch
	for _, a := range answers {
		removal := poRemovalFor(&omsapi.PurchaseOrder{CanDeleteItems: a.flag})
		if removal == poRemovalDelete {
			continue
		}
		wording, ok := wordings[removal]
		if !ok {
			t.Fatalf("removal answer %v reaches the void prompt and no wording is asserted for it", removal)
		}
		out = append(out, poVoidBranch{
			name: a.name, canDelete: a.flag, removal: removal,
			headline: wording.headline, remedy: wording.remedy, loss: wording.loss,
		})
	}
	if len(out) < 2 {
		t.Fatalf("the void prompt is reached by %d answer(s); a single-branch sweep is how the last defect here shipped green", len(out))
	}
	for i := range out {
		for j := range out {
			if i != j && out[j].headline != out[i].headline {
				out[i].other = out[j].headline
			}
		}
	}
	return out
}

// TestPOLineRemove_TheVanishingWarningIsOneRowWhereverItIsDrawn.
//
// voidCaveats' whole sacrifice order rests on the leading caveat being ONE row:
// a one-row claim can only be kept or dropped, never cut, which is what stops
// the pane from stating the loss and losing the remedy. Nothing defended it,
// and when a check was finally written it hard-coded 80 columns — so it
// defended the property at the width nobody was going to break it at.
//
// THE WIDTHS ARE DERIVED FROM ROOT'S OWN GATE, and that is the whole point of
// this test. The loss-first wording held at 80, 100 and 120 and failed at 60,
// where screenBodyWidth is 31 and jdeCaveatLines has 29 cells: the headline
// broke into "Voiding hides the order; only" and "search finds it.", and a trim
// keeping the first row alone stated the loss with the remedy gone. Every sweep
// on this frame named its own widths, so none of them could see it.
// jdeDrawableWidths walks every pane Root will draw instead.
//
// WHAT IS ASSERTED IS THE DISJUNCTION THE DESIGN NOW GUARANTEES: at every such
// width, on both answers, EITHER the caveats are absent entirely — voidCaveats
// refuses rather than draws a warning a trim can halve — OR the headline folds
// to exactly one row AND carries the remedy clause. Absence is checked as a
// UNIT: the prose states the loss too, so a gate that dropped the headline and
// kept the prose would reintroduce the dead end through the other half.
//
// The fold is asked of the LAYER with the width taken from the screen rather
// than written down, and the headline is read back off voidCaveats, so neither
// the bound nor the constants the sweeps assert can go stale against the code.
//
// IT ALSO ASSERTS WHAT THAT ONE ROW HAS TO CARRY, because one row is only worth
// having if it holds both halves. The loss and the remedy are on the same row
// so that a trim takes the claim whole or leaves it whole; a headline reworded
// to state the loss alone would be the dead end this work removed, with the
// remedy pushed into prose the header is free to drop. That used to be inferred
// in the height sweep, from the accident that the remedy clause was a substring
// of both headlines — an implication of two other checks reading as a property
// of its own. Stated here it is a property.
//
// Watched to fail: with the pre-round state restored — loss-first wordings and
// no width gate — it reports the leading caveat folding onto two rows at every
// drawable width from 45 to 79 inclusive, on both branches, and at none from 80
// up. 60 is in that range, which is where the defect was reported; the range is
// what was OBSERVED rather than derived on paper.
func TestPOLineRemove_TheVanishingWarningIsOneRowWhereverItIsDrawn(t *testing.T) {
	widths := jdeDrawableWidths()
	if len(widths) == 0 {
		t.Fatal("no drawable widths — the derivation is broken, not the screen")
	}
	for _, branch := range poVoidBranches(t) {
		t.Run(branch.name, func(t *testing.T) {
			drawnAt := 0
			for _, width := range widths {
				_, s := poVoidPrompt(t, poVoidSentOrder(branch.canDelete), width, 40, 0)
				caveats := s.voidCaveats(s.bodyWidth())
				if len(caveats) == 0 {
					// The gate. Nothing is drawn, so there is no half-warning
					// to cut — and the pane must say nothing about the loss
					// either, or the prose would be carrying it alone.
					if flat := poRemoveFlatPane(s, width, 40); strings.Contains(flat, poVoidLossClause) {
						t.Errorf("%d cols: the headline is withheld and the prose still states the loss:\n%s", width, flat)
					}
					continue
				}
				drawnAt++
				if len(caveats) < 2 {
					t.Fatalf("%d cols: the prompt drew %d caveat(s); the headline-then-detail split is what this is about",
						width, len(caveats))
				}
				if caveats[0] != branch.headline {
					t.Fatalf("%d cols: the sweep asserts %q and the screen draws %q", width, branch.headline, caveats[0])
				}
				if rows := jdeCaveatLines(caveats[0], s.bodyWidth()); len(rows) != 1 {
					t.Errorf("%d cols: the leading caveat folds onto %d rows at %d cells of pane, so a trim can cut the claim in half:\n%q",
						width, len(rows), s.bodyWidth(), caveats[0])
				}
				if !strings.Contains(caveats[0], branch.remedy) {
					t.Errorf("%d cols: the one row states the loss and not the way back, so the remedy rides prose a trim may drop:\n%q",
						width, caveats[0])
				}
			}
			// The fixture must REACH the drawn side of the disjunction, or a
			// wording that never fits anywhere would satisfy every branch of
			// this test by being absent everywhere.
			if drawnAt == 0 {
				t.Fatalf("the warning is drawn at none of the %d drawable widths, so this test asserts nothing about it", len(widths))
			}
		})
	}
}

// TestPOLineRemove_BothVoidAnswersAreWithheldOrDrawnTogether.
//
// THE PROPERTY IS THE SYMMETRY, AND THE WIDTH IS ONLY WHAT IT CURRENTLY
// EVALUATES TO. The gate used to answer for the headline of the branch being
// drawn, so each answer got the threshold its own sentence happened to earn —
// and the CERTAIN-loss wording is two cells longer than the hedged one, so
// across a band of widths the prompt went silent about a loss the server had
// confirmed while still warning about one it had merely left possible. A
// warning about what WILL happen must survive at least as far as a warning
// about what MIGHT.
//
// voidCaveatsFit takes the maximum over the whole headline set, so the two move
// together by construction. This asserts that rather than the number: reword
// either branch and the width where the pair appears moves, and this test still
// holds. The width it evaluates to today is reported by the test itself, so a
// reader does not have to trust a number in a comment.
func TestPOLineRemove_BothVoidAnswersAreWithheldOrDrawnTogether(t *testing.T) {
	widths := jdeDrawableWidths()
	if len(widths) == 0 {
		t.Fatal("no drawable widths — the derivation is broken, not the screen")
	}
	branches := poVoidBranches(t)
	drawnFrom, withheld, both := 0, 0, 0
	for _, width := range widths {
		seen := map[string]bool{}
		for _, branch := range branches {
			_, s := poVoidPrompt(t, poVoidSentOrder(branch.canDelete), width, 40, 0)
			seen[branch.name] = len(s.voidCaveats(s.bodyWidth())) > 0
		}
		drew, held := 0, 0
		for _, ok := range seen {
			if ok {
				drew++
			} else {
				held++
			}
		}
		if drew > 0 && held > 0 {
			t.Errorf("%d cols: %d answer(s) warn and %d are withheld, so the threshold is per-wording and the severities can invert: %v",
				width, drew, held, seen)
		}
		if drew == len(branches) {
			both++
			if drawnFrom == 0 {
				drawnFrom = width
			}
		}
		if held == len(branches) {
			withheld++
		}
	}
	// Both sides of the disjunction have to be REACHED, or a gate that never
	// fires — or one that never opens — would satisfy the symmetry vacuously.
	if both == 0 || withheld == 0 {
		t.Fatalf("the gate is drawn at %d widths and withheld at %d; one side is never exercised", both, withheld)
	}
	t.Logf("both answers warn from %d columns up (%d of %d drawable widths)", drawnFrom, both, len(widths))
}

// TestPOLineRemove_TheIdentityRowFitsThePaneOnBothConfirms.
//
// The row that names what is about to be removed is measured AS ASSEMBLED,
// against the pane the terminal really gave, at every width Root will draw.
//
// It has to be measured off the screen's own View and not off the clipped pane,
// which is the trap the older guard fell into: clampToBox has already truncated
// an over-wide row by the time the pane is read, so a width assertion made
// there can never fail. That guard also walked three hand-picked widths, and
// the overflow lived at 45 through 53 — a floor of one cell applied to the NAME
// and the facts appended after it, which is a bound expressed in terms of
// something unbounded, this project's own width rule broken on the one row that
// says what an irreversible action is about to destroy.
//
// Watched to fail: with the floored-room assembly restored it reports on both
// confirms across the narrow end of the range, drawing rows several cells wider
// than the pane.
func TestPOLineRemove_TheIdentityRowFitsThePaneOnBothConfirms(t *testing.T) {
	const long = "M3×12 hex-head cap screw, A2-70 stainless, DIN 933, bright finish"
	widths := jdeDrawableWidths()
	if len(widths) == 0 {
		t.Fatal("no drawable widths — the derivation is broken, not the screen")
	}
	for _, confirm := range []struct {
		name      string
		canDelete *bool
		phase     poEditPhase
		lead      string
	}{
		{"delete", boolPtr(true), poEditPhaseDeleteLine, "Delete: "},
		{"void", boolPtr(false), poEditPhaseVoidLine, "Void: "},
	} {
		t.Run(confirm.name, func(t *testing.T) {
			clipped, abbreviated := 0, 0
			for _, width := range widths {
				fake := &fakeRemovePO{
					status: "draft", canDelete: confirm.canDelete,
					lines: []*fakeRemoveLine{
						{id: "line-bolts", label: long, ordered: 250, cost: "31.25"},
					},
				}
				if confirm.phase == poEditPhaseVoidLine {
					fake.status = "sent"
				}
				r, _ := poRemoveRoot(t, fake, width)
				r, s := poRemoveOnStatusRow(t, r, 0)
				r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
				if s.phase != confirm.phase {
					t.Fatalf("%d cols: phase %v, want %v", width, s.phase, confirm.phase)
				}
				head := ""
				for _, line := range strings.Split(s.View(), "\n") {
					if strings.Contains(line, confirm.lead) {
						head = line
					}
				}
				if head == "" {
					t.Fatalf("%d cols: the confirm never names the line at all:\n%s", width, s.View())
				}
				pane := screenBodyWidth(width)
				if got := lipgloss.Width(head); got > pane {
					t.Errorf("%d cols: the identity row is %d cells against a %d-column pane, so clampToBox cuts it: %q",
						width, got, pane, head)
				}
				// Whatever it shortened says so. The name keeps pickerClip's
				// ellipsis; the ordered quantity, where the pane was too narrow
				// to keep it beside a readable name, leaves poRowDropMark.
				if !strings.Contains(head, "· 250 ordered") {
					clipped++
					if !strings.Contains(head, strings.TrimSpace(poRowDropMark)) {
						t.Errorf("%d cols: the row gave the ordered quantity up and does not say so: %q", width, head)
					}
				}
				if strings.Contains(head, "…") {
					abbreviated++
				}
			}
			// BOTH ENDS OF THE RANGE HAVE TO BE REACHED, counted rather than
			// asserted per width: a wide pane draws this 64-cell name whole,
			// which is the correct answer there, so a per-width demand for an
			// ellipsis would report the widths that are working. What would
			// make the sweep vacuous is a fixture that never reaches the clip
			// at all, and that is what these count.
			if abbreviated == 0 {
				t.Errorf("the name is drawn whole at every drawable width, so nothing here exercises the clip")
			}
			if clipped == 0 {
				t.Errorf("the ordered quantity survives at every drawable width, so the give-order is never exercised")
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
//
// IT SWEEPS BOTH ANSWERS THAT REACH THE PROMPT, and that is not a tidy-up. Run
// on poRemovalVoid alone it went green over a real dead end one branch over:
// with the order number appended at the TAIL of the way-back sentence,
// jdeWrapNote broke the poRemovalUnknown wording so the number landed alone on
// the last prose row, and jdeFitHeader — which gives ground from the END of a
// rank — dropped exactly that row while keeping "…reaches it by number:". The
// pane then told the operator to search for a token it had stopped showing.
// Observed failing at 80x18 and at that pane alone — 100 and 120 held at every
// drawable height, because a wider fold kept "by number" and the number on one
// row. One reachable pane is the whole defect: 80 is the width this interface
// is modelled on. The poRemovalVoid branch was never wrong, but it was only RIGHT by
// accident of where its own words happened to break — a check green for a
// reason unrelated to the property it names is the failure this project keeps
// closing, so the guarantee is structural now (see voidSearchSentence).
func TestPOLineRemove_AShortVoidPaneKeepsTheVanishingOrderWarning(t *testing.T) {
	heights := jdePaneHeights()
	if len(heights) == 0 {
		t.Fatal("no drawable heights — the derivation is broken, not the screen")
	}
	// BOTH AXES, AND THE WIDTH AXIS IS DERIVED TOO. It used to run at 80, 100
	// and 120 — a judgement, and the wrong one: the one-row guarantee the whole
	// sacrifice order rests on failed at 60 columns and every listed width held,
	// so the property was checked exactly where it could not break. Below the
	// width at which the headline stops fitting on one row voidCaveats withholds
	// it, and this sweep is where that threshold is watched from the rendered
	// pane rather than from the predicate.
	//
	// Watched to fail on this axis too: with the pre-round state restored the
	// "states the loss without its remedy" check below reports from 45x14 down
	// the range to 79x12 — 60x12 among them — and at no width from 80 up. That
	// is the dead end this work exists to remove, living at every width the old
	// sweep did not name.
	widths := jdeDrawableWidths()
	if len(widths) == 0 {
		t.Fatal("no drawable widths — the derivation is broken, not the screen")
	}
	for _, branch := range poVoidBranches(t) {
		for _, width := range widths {
			t.Run(fmt.Sprintf("%s/%dcols", branch.name, width), func(t *testing.T) {
				voidPaneSweep(t, branch, width, heights)
			})
		}
	}
}

func voidPaneSweep(t *testing.T, branch poVoidBranch, width int, heights []int) {
	t.Helper()

	// ONE DRIVE PER (BRANCH, WIDTH), THEN A RESIZE PER HEIGHT. Rebuilding the
	// screen at every height spun an httptest server and replayed the whole key
	// walk per pane, which was affordable over three widths and is not over
	// every drawable one — and this package fails by TIMING OUT, naming
	// whichever test happened to be running. Dragging the terminal short is
	// also the sequence the operator actually goes through.
	tallest := heights[len(heights)-1]
	r, s := poVoidPrompt(t, poVoidSentOrder(branch.canDelete), width, tallest, 0)
	resize := func(height int) string {
		next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
		r = next.(Root)
		return poRemoveFlatPane(s, width, height)
	}

	// THE WARNING IS NOT DRAWN AT EVERY WIDTH, AND THAT IS THE DESIGN. Below the
	// width where the headlines stop folding to one row voidCaveats withholds
	// BOTH caveats rather than let a trim halve the claim, so this sweep asks
	// the screen which side of that gate it is on rather than assuming the
	// warning is there.
	//
	// THE GATE NARROWS WHAT IS ASSERTED, IT DOES NOT END THE SWEEP. This used
	// to `return` here, which quietly took every remaining property off the
	// whole narrow half of the width range — the half that had just been added
	// — and with it the rule-1 guard that the Reason box the operator types
	// into is on the pane. Only the assertions that are ABOUT A DRAWN CAVEAT
	// belong behind the gate; everything else is about the frame and holds at
	// every width.
	drawn := len(s.voidCaveats(s.bodyWidth())) > 0

	// The fixture has to REACH the bound: on the tallest pane both rows are
	// drawn, so a run where the second never appears would pass for a reason
	// unrelated to the sacrifice this test is about. It also has to reach THIS
	// branch — a fixture whose flag drew the other wording would sweep one
	// answer twice.
	if flat := resize(tallest); drawn {
		if !strings.Contains(flat, branch.headline) {
			t.Fatalf("height %d does not draw this branch's headline %q, so the sweep is measuring the wrong wording:\n%s",
				tallest, branch.headline, flat)
		}
		if branch.other != "" && strings.Contains(flat, branch.other) {
			t.Fatalf("height %d draws the OTHER answer's headline:\n%s", tallest, flat)
		}
		if !strings.Contains(flat, poVoidLossClause) || !strings.Contains(flat, poVoidStandingNote) {
			t.Fatalf("height %d draws neither caveat in full, so the sacrifice never happens:\n%s", tallest, flat)
		}
		// THE TWO WAY-BACK FACTS ARE ASSERTED POSITIVELY HERE, and that is not
		// a duplicate of the conditional guards below — it is what stops them
		// being vacuous. Each of those fires only once the pane ALREADY draws
		// the token it qualifies: "ctrl+k without its clause" is silent on a
		// pane with no ctrl+k, and "by number without the number" is silent on
		// a pane that says neither. So a sentence that stopped naming them at
		// all would satisfy both. That is a live shape rather than a
		// hypothetical: po_number is nullable on the wire, and
		// voidSearchSentence answers an empty one with a fallback carrying no
		// number — every check in this file would have stayed green while the
		// remedy quietly lost the fact it exists to deliver. The reach check is
		// where the fixture is proved to reach the bound, so it is where these
		// belong.
		if !strings.Contains(flat, poVoidKeyClause) {
			t.Fatalf("height %d never draws the qualifier-then-key clause %q, so the ctrl+k guard below is vacuous:\n%s",
				tallest, poVoidKeyClause, flat)
		}
		if !strings.Contains(flat, poVoidNumberedPhrase) {
			t.Fatalf("height %d never draws %q, so the by-number guard below is vacuous:\n%s",
				tallest, poVoidNumberedPhrase, flat)
		}
	}

	for _, height := range heights {
		flat := resize(height)
		warned := strings.Contains(flat, branch.headline)
		detail := strings.Contains(flat, poVoidLossClause)
		standing := strings.Contains(flat, poVoidStandingNote)
		if drawn && standing && !warned {
			t.Errorf("%dx%d: the void prompt keeps the caveat said elsewhere and drops the one nothing else carries:\n%s",
				width, height, flat)
		}
		// Where the gate withheld the pair, nothing else on the frame may state
		// the loss in their place: the prose carries it too, so a gate that
		// dropped the headline alone would move the dead end one row down
		// instead of removing it. The standing note is NOT in this list — it is
		// unconditional and says only what voiding does to the LINE, so the
		// sacrifice-order check above is gated instead of this one widened.
		if !drawn {
			for _, gone := range []string{branch.headline, poVoidLossClause} {
				if strings.Contains(flat, gone) {
					t.Errorf("%dx%d: the caveats are withheld at this width and the pane still says %q:\n%s",
						width, height, gone, flat)
				}
			}
		}
		// Could-not-tell and found-nothing stay apart at every height a trim
		// can reach: a pane that has given ground must not have given up the
		// hedge and left the flat claim standing, or the other way round.
		if branch.other != "" && strings.Contains(flat, branch.other) {
			t.Errorf("%dx%d: the pane draws the other answer's headline %q:\n%s",
				width, height, branch.other, flat)
		}
		if detail && !warned {
			t.Errorf("%dx%d: the prose survives and the headline it summarises does not:\n%s",
				width, height, flat)
		}
		// A LOSS IS NEVER STATED WITHOUT ITS REMEDY — the rule this frame
		// keeps, said at the surface it is kept on, and keyed on the LOSS
		// FRAGMENT rather than on the whole headline.
		//
		// That distinction is the whole check. Keyed on the headline it could
		// not fail on its own account: the remedy leads, so it is a substring
		// of it, and `warned` already implied it — this check was removed once
		// for exactly that. Worse, the pane a SPLIT headline draws reads as
		// warned == false, because poRemoveFlatPane collapses the pane before
		// matching, so the one state the rule exists for was the one state it
		// was blind to. The fragment is present on that pane and the remedy is
		// not, so this now reports it. Verified by reverting: with the
		// loss-first wordings and no width gate it fires; with either half
		// restored it does not.
		if (strings.Contains(flat, branch.loss) || detail) && !strings.Contains(flat, branch.remedy) {
			t.Errorf("%dx%d: the pane states the loss and does not name the way back:\n%s",
				width, height, flat)
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
		// "Search by number" is only a remedy if the number is on the frame.
		// Nothing else on this one carries it — the essential row names the
		// LINE and the order's number is two screens back — so the sentence
		// that names the route names the order too, or it is telling the
		// operator to search for something they would have had to write down.
		if strings.Contains(flat, "by number") && !strings.Contains(flat, poVoidOrderNumber) {
			t.Errorf("%dx%d: the pane says to search by number and shows no number:\n%s",
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

// TestPOLineRemove_ADeclinedKeyAnswersAfterTheDeleteFailed: on the confirm a
// refused DELETE leaves standing, every key the frame declines still changes
// what the operator sees.
//
// poLineActionMsg with an error sets saving false and errMsg WITHOUT changing
// the phase, so a 502 leaves this frame up with the failure on its status row
// and the bar naming Ctrl-X and Esc again. The status row holds ONE fact, and
// until the ANSWER was ranked above the standing failure on it — a refused
// per-line delete is scoped to the line this frame is about, so it takes the
// pinned header while the answer takes the row nothing can trim — every later
// declined key wrote a note nothing drew and the pane came back byte for byte
// identical: standing rule 1, on a destructive confirm, after a destroy that
// failed.
//
// Driven through the real message rather than by setting the field, so a change
// that stopped a failed delete leaving the confirm open fails here instead of
// leaving the check measuring a state nothing reaches. Pressed IN SEQUENCE with
// no reset between, because two keys sharing one wording is how this class comes
// back.
func TestPOLineRemove_ADeclinedKeyAnswersAfterTheDeleteFailed(t *testing.T) {
	fake := poRemoveOrder("draft", boolPtr(true))
	r, _ := poRemoveRoot(t, fake, 80)
	r, s := poRemoveOnStatusRow(t, r, 0)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("phase %v, want the delete confirm", s.phase)
	}

	next, _ := r.Update(poLineActionMsg{
		err:    fmt.Errorf("oms: http 502: <!DOCTYPE html><html><head><title>502</title>"),
		action: "line delete",
	})
	r = next.(Root)
	if s.phase != poEditPhaseDeleteLine {
		t.Fatalf("the refused delete closed the confirm (phase %v), so the state this "+
			"check is about is not reached", s.phase)
	}
	if s.errMsg == "" {
		t.Fatal("the refused delete left no error standing, so the status row is free " +
			"for the answer and this check asserts nothing")
	}

	prev := poRemovePane(s, 80)
	for _, k := range []string{"j", "down", "y", "up"} {
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
		pane := poRemovePane(s, 80)
		if pane == prev {
			t.Fatalf("with the failed delete standing, pressing %q redrew a byte-identical "+
				"pane — the frame wrote an answer nothing draws:\n%s", k, pane)
		}
		prev = pane
	}
	if !strings.Contains(stripANSI(prev), "up moves nothing") &&
		!strings.Contains(stripANSI(prev), "up does nothing") {
		t.Errorf("the last declined key's answer is not on the pane, so what changed was "+
			"something other than the frame answering:\n%s", prev)
	}
}

// TestPOLineRemove_ADeclinedKeyAnswersAtEveryDrawableHeight is the check above
// asked at every pane Root draws, which is where both previous placements of
// this answer broke and where neither was tested.
//
// The answer has been sited three times — inside the body the bar is measured
// against, on statusAnswer, and in a jdeHeadContext header row — and each
// placement was correct at the pane it was written against and wrong somewhere
// else. A header CONTEXT row is the one jdeFitHeader gives ground with first,
// so at the minimum drawable budget it is trimmed and the declined key wrote a
// note nothing drew again, one height band down from the last fix.
//
// Stated the way an operator would: with a destroy that the server refused
// still on the frame, press a key the frame declines, at any size of terminal,
// and something must change. Measured on the CLIPPED pane, because the screen's
// own string is not what the operator reads, and driven through the real
// poLineActionMsg so a change that stopped a failed delete leaving the confirm
// open fails here rather than leaving this measuring a state nothing reaches.
//
// One case over the height axis rather than a walk of every case at every pane:
// the class is a per-screen geometry hole, and the package is already inside
// sight of go test's per-package timeout.
func TestPOLineRemove_ADeclinedKeyAnswersAtEveryDrawableHeight(t *testing.T) {
	drawn := 0
	for _, h := range jdePaneHeights() {
		fake := poRemoveOrder("draft", boolPtr(true))
		r, _ := poRemoveRoot(t, fake, 80)
		r, s := poRemoveOnStatusRow(t, r, 0)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
		if s.phase != poEditPhaseDeleteLine {
			t.Fatalf("phase %v, want the delete confirm", s.phase)
		}
		next, _ := r.Update(poLineActionMsg{
			err:    fmt.Errorf("oms: http 502: <!DOCTYPE html><html><head><title>502</title>"),
			action: "line delete",
		})
		r = next.(Root)
		if s.errMsg == "" {
			t.Fatal("the refused delete left no error standing, so this check asserts nothing")
		}

		before := jdeClippedPane(s, 80, h)
		if jdeBarOf(s.View()) == nil {
			continue // a pane the layer refused; the notice replaces the frame
		}
		drawn++
		if nx, _ := r.Update(poPhaseKeyMsg("j")); nx != nil {
			r = nx.(Root)
		}
		if after := jdeClippedPane(s, 80, h); after == before {
			t.Errorf("at 80x%d, with the failed delete standing, a declined key redrew a "+
				"byte-identical pane — the frame's answer is on a surface this pane "+
				"trimmed:\n%s", h, after)
		}
	}
	if drawn == 0 {
		t.Fatal("the confirm was refused at every height, so this check asserted nothing " +
			"about the answer surface")
	}
}
