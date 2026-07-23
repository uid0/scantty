package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// apFakeOMS is a minimal stateful stand-in for the OMS endpoints the
// report → promote → complete → resolve loop touches. It models the one server
// behaviour the loop hinges on: completing the promoted work order is what
// resolves the problem report (backend WorkOrderViewSet.perform_update →
// resolve_problems_for_work_order).
type apFakeOMS struct {
	mu            sync.Mutex
	problemStatus string
	workOrderID   string
	woStatus      string
	promoteCalls  int
}

func newAPFakeOMS() *apFakeOMS {
	return &apFakeOMS{problemStatus: omsapi.AssetProblemReported}
}

func (f *apFakeOMS) problemJSON() map[string]any {
	out := map[string]any{
		"id":                              "prob-1",
		"asset":                           "asset-9",
		"asset_name":                      "Bridgeport Mill",
		"description":                     "belt frayed",
		"status":                          f.problemStatus,
		"third_party_work_order":          nil,
		"third_party_work_order_short_id": nil,
	}
	if f.workOrderID == "" {
		out["work_order"] = nil
		out["work_order_short_id"] = nil
	} else {
		out["work_order"] = f.workOrderID
		out["work_order_short_id"] = "WO-0042"
	}
	return out
}

func (f *apFakeOMS) handler(t *testing.T) http.Handler {
	t.Helper()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.Path

		switch {
		case path == "/api/inventory/assets/asset-9/":
			writeJSON(w, http.StatusOK, map[string]any{
				"id": "asset-9", "name": "Bridgeport Mill", "is_active": true,
			})

		case path == "/api/inventory/asset-problems/":
			writeJSON(w, http.StatusOK, map[string]any{
				"count": 1, "results": []any{f.problemJSON()},
			})

		case path == "/api/inventory/asset-problems/prob-1/promote-standard/":
			f.promoteCalls++
			if f.workOrderID != "" {
				writeJSON(w, http.StatusBadRequest,
					map[string]any{"error": "Already promoted to a standard work order."})
				return
			}
			f.workOrderID = "wo-77"
			f.woStatus = "open"
			f.problemStatus = omsapi.AssetProblemInProgress
			writeJSON(w, http.StatusCreated, f.problemJSON())

		case path == "/api/inventory/work-orders/wo-77/":
			if r.Method == http.MethodPatch {
				var patch map[string]any
				_ = json.NewDecoder(r.Body).Decode(&patch)
				if st, ok := patch["status"].(string); ok {
					f.woStatus = st
					// The completion edge is what resolves the promoted report.
					if st == "completed" {
						f.problemStatus = omsapi.AssetProblemResolved
					}
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"id": "wo-77", "short_id": "WO-0042", "title": "Corrective: belt frayed",
				"status": f.woStatus, "asset": "asset-9", "priority": "normal",
			})

		case strings.HasPrefix(path, "/api/inventory/work-orders/"):
			writeJSON(w, http.StatusOK, map[string]any{"count": 0, "results": []any{}})

		default:
			// Everything else the asset-detail loader probes (PM items, power
			// chain, LOTO, reservations, OOS, serialized components) is optional
			// — its loader swallows the error and renders without the section.
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "not found"})
		}
	})
}

// apPump drains a command (and everything it produces, transitively) back
// through Root.Update, standing in for the bubbletea runtime so a test can drive
// a multi-screen flow. Bounded so a self-re-arming command can't hang the suite.
func apPump(t *testing.T, r Root, cmd tea.Cmd) Root {
	t.Helper()
	for round := 0; cmd != nil && round < 20; round++ {
		msgs := ccDrainCmd(cmd)
		cmd = nil
		for _, msg := range msgs {
			next, c := r.Update(msg)
			rootAfter, ok := next.(Root)
			if !ok {
				t.Fatalf("Root.Update returned %T, want Root", next)
			}
			r = rootAfter
			cmd = tea.Batch(cmd, c)
		}
	}
	return r
}

// apPress sends a key through Root.Update and pumps whatever it produces.
func apPress(t *testing.T, r Root, key string) Root {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := r.Update(msg)
	rootAfter, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return apPump(t, rootAfter, cmd)
}

// TestAssetProblemPromoteLoop_EndToEnd drives the whole lifecycle through the
// real Root key dispatch — asset detail → P problems → w create work order →
// c/y complete → esc back — and asserts the report comes back resolved. This is
// the automatable stand-in for the bead's manual "drive the TUI" pass: every
// screen transition and request goes through the same code paths the operator's
// keystrokes do, against a server that mirrors the backend's resolve-on-complete
// rule.
func TestAssetProblemPromoteLoop_EndToEnd(t *testing.T) {
	fake := newAPFakeOMS()
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewAssetDetailScreen(deps, "asset-9")
	r := Root{deps: deps, nav: NewNav(), status: NewStatusBar(), navWidth: 24, screen: detail}
	r = apPump(t, r, detail.Init())

	if detail.asset == nil || len(detail.problems) != 1 {
		t.Fatalf("asset detail did not load asset+problems: asset=%v problems=%d", detail.asset, len(detail.problems))
	}

	// P opens the actionable problems list (and must not hit the global PM board).
	r = apPress(t, r, "P")
	problems, ok := r.screen.(*AssetProblemsScreen)
	if !ok {
		t.Fatalf("P should open the problems screen, got %T", r.screen)
	}
	if len(problems.rows) != 1 || problems.rows[0].Status != omsapi.AssetProblemReported {
		t.Fatalf("problems screen rows = %+v", problems.rows)
	}

	// w promotes to an in-house corrective work order and lands on its detail.
	r = apPress(t, r, "w")
	wo, ok := r.screen.(*WorkOrderDetailScreen)
	if !ok {
		t.Fatalf("w should land on the work order detail, got %T", r.screen)
	}
	if wo.woID != "wo-77" {
		t.Fatalf("landed on work order %q, want wo-77", wo.woID)
	}
	if wo.wo == nil || wo.wo.Status != "open" {
		t.Fatalf("work order not loaded: %+v", wo.wo)
	}
	if fake.promoteCalls != 1 {
		t.Errorf("promote called %d times, want exactly 1", fake.promoteCalls)
	}

	// c → y completes it, which is what resolves the promoted report.
	r = apPress(t, r, "c")
	r = apPress(t, r, "y")
	if fake.woStatus != "completed" {
		t.Fatalf("work order status = %q, want completed", fake.woStatus)
	}

	// esc pops back to the problems list, which re-loads on restore.
	r = apPress(t, r, "esc")
	problems, ok = r.screen.(*AssetProblemsScreen)
	if !ok {
		t.Fatalf("esc should return to the problems screen, got %T", r.screen)
	}
	if len(problems.rows) != 1 || !problems.rows[0].IsResolved() {
		t.Fatalf("report should be resolved after completing its work order: %+v", problems.rows)
	}
	// Resolved reports drop out of the default "open" filter, and the row now
	// names the work order it was promoted to.
	if len(problems.visible()) != 0 {
		t.Errorf("resolved report should leave the open filter, visible = %d", len(problems.visible()))
	}
	problems.filter = apFilterAll
	problems.terminalHeight = 30
	if out := problems.View(); !strings.Contains(out, "WO-0042") {
		t.Errorf("row should name the promoted work order: %q", out)
	}
}

// TestAssetProblemPromote_SecondPressIsRefused proves the client-side gate keeps
// a doomed second promote off the wire once the row carries a work order.
func TestAssetProblemPromote_SecondPressIsRefused(t *testing.T) {
	fake := newAPFakeOMS()
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	problems := NewAssetProblemsScreen(deps, "asset-9", "Bridgeport Mill")
	r := Root{deps: deps, nav: NewNav(), status: NewStatusBar(), navWidth: 24, screen: problems}
	r = apPump(t, r, problems.Init())

	r = apPress(t, r, "w")
	if _, ok := r.screen.(*WorkOrderDetailScreen); !ok {
		t.Fatalf("first w should promote, got %T", r.screen)
	}
	if fake.promoteCalls != 1 {
		t.Fatalf("promote calls = %d, want 1", fake.promoteCalls)
	}

	// Back on the reloaded list the row now carries WO-0042 → w is a no-op.
	r = apPress(t, r, "esc")
	reloaded, ok := r.screen.(*AssetProblemsScreen)
	if !ok {
		t.Fatalf("esc should return to the problems screen, got %T", r.screen)
	}
	reloaded.filter = apFilterAll
	r = apPress(t, r, "w")
	if fake.promoteCalls != 1 {
		t.Errorf("a second w must not reach the server, calls = %d", fake.promoteCalls)
	}
	if _, ok := r.screen.(*AssetProblemsScreen); !ok {
		t.Errorf("a refused promote should stay on the list, got %T", r.screen)
	}
}
