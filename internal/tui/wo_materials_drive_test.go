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
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// End-to-end drive of the bead's manual check — open a work order, add a
// material with a cost, see the actual total — pumped through Root.Update
// against a stateful fake OMS, since no live instance is reachable from here.
//
// Root-level rather than screen-level on purpose: the material list claims the
// plain letters a / c / d, every one of which the global hotkey layer also
// binds. Only WantsRawInput keeps them, so a screen-level test would pass while
// the real app opened the Assets workspace instead of the add-material form.

// fakeOMS is a work order that remembers the materials added to it.
type fakeOMS struct {
	mu        sync.Mutex
	materials []map[string]any
	requests  []string
}

// price mirrors WorkOrderMaterialUsage.actual_cost — quantity_used × unit_cost,
// stamped on the line as the serializer does — and returns it for the totals.
func price(m map[string]any) float64 {
	var qty, cost float64
	fmt.Sscanf(fmt.Sprint(m["quantity_used"]), "%g", &qty)
	if _, err := fmt.Sscanf(fmt.Sprint(m["unit_cost"]), "%g", &cost); err != nil {
		delete(m, "actual_cost") // unpriced: null, not zero
		return 0
	}
	m["actual_cost"] = fmt.Sprintf("%.2f", qty*cost)
	return qty * cost
}

// total sums the lines the way the backend's actual_material_cost does: over the
// lines marked used, unpriced ones contributing nothing.
func (f *fakeOMS) total() float64 {
	var sum float64
	for _, m := range f.materials {
		lineCost := price(m)
		if used, _ := m["was_used"].(bool); used {
			sum += lineCost
		}
	}
	return sum
}

func (f *fakeOMS) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/materials/"):
			body := map[string]any{}
			if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			line := map[string]any{
				"id":               fmt.Sprintf("mu-%d", len(f.materials)+1),
				"material_name":    body["material_name"],
				"is_ad_hoc":        true,
				"quantity_planned": body["quantity_used"],
				"quantity_used":    body["quantity_used"],
				"unit":             body["unit"],
				"unit_cost":        body["unit_cost"],
				// The backend creates the line UN-used: the decrement is the
				// toggle's job, so nothing is spent until it is marked.
				"was_used": false,
			}
			f.materials = append(f.materials, line)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(line)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/materials/"):
			body := map[string]any{}
			if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			id := strings.Split(r.URL.Path, "/materials/")[1]
			id = strings.TrimSuffix(id, "/toggle/")
			for _, m := range f.materials {
				if m["id"] != id {
					continue
				}
				m["was_used"] = body["was_used"]
				if cost, ok := body["unit_cost"]; ok {
					m["unit_cost"] = cost
				}
				_ = json.NewEncoder(w).Encode(m)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			lines := f.materials
			if lines == nil {
				lines = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "wo1", "title": "Fix the lathe", "status": "in_progress",
				"material_usage":       lines,
				"actual_material_cost": fmt.Sprintf("%.2f", f.total()),
			})
		}
	}
}

// pump runs a command the way the bubbletea runtime would — feeding each
// resulting message back through Root.Update — so a test walks the same
// load/refetch chain the operator's keystroke sets off.
func pump(t *testing.T, r Root, cmd tea.Cmd, depth int) Root {
	t.Helper()
	if cmd == nil || depth > 24 {
		return r
	}
	// Commands that only mark time — the textinput cursor blink, the work-order
	// stopwatch — carry nothing a drive needs and would otherwise be waited out
	// a half-second at a time, so they are abandoned rather than awaited.
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return r
	}
	// The BLINK is abandoned one step earlier than that budget, because waiting
	// it out is what the budget cost: `textinput.Blink` answers immediately with
	// an initialBlinkMsg carrying nothing, and it is only FEEDING that message
	// back to Update that makes bubbles start the 530ms tick nobody waits for —
	// so every focused box on every rebuild spent a flat 200ms here. Recognising
	// it changes no state a drive can see (cursor.Update's initialBlinkMsg arm
	// returns the tick and touches nothing else) and it is what took this
	// package from 600s, where `go test`'s default per-package timeout killed
	// it, back to about a minute. The 200ms budget above is left alone: it is
	// the backstop for a genuine timer, and shortening it would make ~30 drives
	// racier on a loaded machine.
	if msg == nil || driveIsBlink(msg) {
		return r
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			r = pump(t, r, c, depth+1)
		}
		return r
	}
	next, nextCmd := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return pump(t, after, nextCmd, depth+1)
}

// driveIsBlink reports a cursor-blink message — the one thing every drive in
// this package skips. bubbles keeps those types unexported, so this matches on
// the type NAME, which is checked rather than trusted: the test below fails if
// the message textinput.Blink produces stops matching, so a bubbles upgrade
// that renamed it could not silently turn the drives into ones that skip
// nothing (or, worse, that skip a real message).
func driveIsBlink(msg tea.Msg) bool {
	return strings.Contains(strings.ToLower(fmt.Sprintf("%T", msg)), "blink")
}

func TestDrive_TheBlinkIsWhatTheDriveSkips(t *testing.T) {
	if !driveIsBlink(textinput.Blink()) {
		t.Fatalf("textinput.Blink now produces %T, which driveIsBlink does not match — "+
			"the drives would be waiting out a tick again, or worse, skipping a real message",
			textinput.Blink())
	}
	if driveIsBlink(StatusMsg{}) || driveIsBlink(receiveSubmittedMsg{}) {
		t.Error("driveIsBlink matches a message the drive must not skip")
	}
}

// key sends one key through Root.Update and pumps whatever it kicked off.
func key(t *testing.T, r Root, msg tea.KeyMsg) Root {
	t.Helper()
	next, cmd := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return pump(t, after, cmd, 0)
}

func TestWorkOrderMaterialCostDrive(t *testing.T) {
	fake := &fakeOMS{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewWorkOrderDetailScreen(deps, "wo1")
	r := newTestRoot(screen)
	r.deps = deps
	r = pump(t, r, screen.Init(), 0)

	// Open the material list. 'M' is also the global PM-items hotkey, which used
	// to win — the key the footer advertised navigated away from the work order
	// instead, so the list this bead builds on was unreachable in the real app.
	r = key(t, r, woRuneKey("M"))
	if screen.mode != woModeMaterials {
		t.Fatalf("mode = %v, want the material list", screen.mode)
	}

	// 'a' is also the global Assets hotkey — the modal has to keep it.
	r = key(t, r, woRuneKey("a"))
	if screen.mode != woModeAddMaterial {
		t.Fatalf("mode = %v — the global hotkey layer stole 'a'", screen.mode)
	}
	if _, stillHere := r.screen.(*WorkOrderDetailScreen); !stillHere {
		t.Fatalf("navigated away from the work order: %T", r.screen)
	}

	r = key(t, r, woRuneKey("Drive belt"))
	for i := 0; i < 3; i++ { // name → quantity → unit → unit cost
		r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
	}
	r = key(t, r, woRuneKey("42.10"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.mode != woModeMaterials {
		t.Fatalf("mode = %v, want to land back on the list after adding", screen.mode)
	}
	if len(fake.materials) != 1 {
		t.Fatalf("materials on the work order = %+v", fake.materials)
	}

	// Added but not yet used: nothing is spent until the line is marked, which
	// is also what would move stock if it were linked to an item.
	out := screen.View()
	if !strings.Contains(out, "Actual material cost: $0.00") {
		t.Errorf("an unused line must not count toward the total: %q", out)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeySpace})
	out = screen.View()
	if !strings.Contains(out, "Actual material cost: $42.10") {
		t.Errorf("the total must pick up the marked line: %q", out)
	}
	if !strings.Contains(out, "cost $42.10 ($42.10/unit)") {
		t.Errorf("the line must show what it cost: %q", out)
	}

	// Re-price it: 'c' is the last leg the bead asks for, and the toggle is the
	// only endpoint that carries a price.
	r = key(t, r, woRuneKey("c"))
	if screen.mode != woModeMaterialCost {
		t.Fatalf("mode = %v, want the cost editor", screen.mode)
	}
	for range "42.10" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = key(t, r, woRuneKey("38.37"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.mode != woModeMaterials {
		t.Fatalf("mode = %v, want the list after saving", screen.mode)
	}
	if out = screen.View(); !strings.Contains(out, "Actual material cost: $38.37") {
		t.Errorf("re-priced total missing: %q", out)
	}

	// One line added, one mark-used, one re-price — and nothing sent twice. The
	// screen re-fetches after each write, so the GETs in between are expected.
	var wrote []string
	for _, req := range fake.requests {
		if !strings.HasPrefix(req, http.MethodGet) {
			wrote = append(wrote, req)
		}
	}
	want := []string{
		"POST /api/inventory/work-orders/wo1/materials/",
		"PATCH /api/inventory/work-orders/wo1/materials/mu-1/toggle/",
		"PATCH /api/inventory/work-orders/wo1/materials/mu-1/toggle/",
	}
	if strings.Join(wrote, "\n") != strings.Join(want, "\n") {
		t.Errorf("writes =\n%s\nwant\n%s", strings.Join(wrote, "\n"), strings.Join(want, "\n"))
	}
}
