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
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Both halves of gap G14, driven through Root.Update against a stateful fake
// OMS — the shape wo_materials_drive_test.go established, and Root-level for the
// same reason: these lists claim plain letters (a / l / d / y / n) that only
// WantsRawInput keeps, so a screen-level drive would pass while the real app
// navigated away.
//
// Every assertion about a write is made against the REQUESTS THE FAKE RECEIVED,
// not against the screen's own state, because "the screen thinks it recorded a
// lockout step" and "a lockout step was recorded" are different facts and only
// the second one is on the safety record.

// fakeWOTools is a work order that remembers its tool rows and its LOTO records.
type fakeWOTools struct {
	mu       sync.Mutex
	tools    []map[string]any
	loto     []map[string]any
	requests []recordedRequest
	// refuse, when set, is the status and body every LOTO write answers with, so
	// a test can drive the server's refusal rather than imagine it.
	refuseLotoStatus int
	refuseLotoBody   string
	// templateTools is the LEAN `tools` payload: on a legacy work order it is the
	// PM template's list while tool_rows is empty.
	templateTools []map[string]any
}

type recordedRequest struct {
	method string
	path   string
	body   map[string]any
}

func (f *fakeWOTools) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		rec := recordedRequest{method: r.Method, path: r.URL.Path}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &rec.body)
		}
		f.requests = append(f.requests, rec)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tools/"):
			row := map[string]any{
				"id":          fmt.Sprintf("wt-%d", len(f.tools)+1),
				"is_ad_hoc":   true,
				"name":        rec.body["name"],
				"quantity":    1,
				"is_required": true,
			}
			if q, ok := rec.body["quantity"]; ok {
				row["quantity"] = q
			}
			if req, ok := rec.body["is_required"]; ok {
				row["is_required"] = req
			}
			if hint, ok := rec.body["location_hint"].(string); ok {
				row["location_hint"] = hint
				row["resolved_location"] = hint
			}
			if notes, ok := rec.body["notes"]; ok {
				row["notes"] = notes
			}
			f.tools = append(f.tools, row)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(row)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/tools/"):
			id := strings.TrimSuffix(strings.Split(r.URL.Path, "/tools/")[1], "/")
			for _, row := range f.tools {
				if row["id"] != id {
					continue
				}
				hint, _ := rec.body["location_hint"].(string)
				row["location_hint"] = hint
				// The server resolves: a blank hint hands the row back to the
				// linked item's stored location.
				if hint != "" {
					row["resolved_location"] = hint
				} else {
					row["resolved_location"] = "Tool crib, drawer 3"
				}
				_ = json.NewEncoder(w).Encode(row)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/tools/"):
			id := strings.TrimSuffix(strings.Split(r.URL.Path, "/tools/")[1], "/")
			kept := f.tools[:0]
			for _, row := range f.tools {
				if row["id"] != id {
					kept = append(kept, row)
				}
			}
			f.tools = kept
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/loto/"):
			if f.refuseLotoStatus != 0 {
				w.WriteHeader(f.refuseLotoStatus)
				_, _ = w.Write([]byte(f.refuseLotoBody))
				return
			}
			id := strings.TrimSuffix(strings.Split(r.URL.Path, "/loto/")[1], "/complete/")
			for _, row := range f.loto {
				if row["id"] != id {
					continue
				}
				done, _ := rec.body["is_completed"].(bool)
				row["is_completed"] = done
				if done {
					row["completed_by_name"] = "Dana Reyes"
					row["completed_at"] = "2026-09-11T08:15:00Z"
				} else {
					delete(row, "completed_by_name")
					delete(row, "completed_at")
				}
				_ = json.NewEncoder(w).Encode(row)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			tools := f.templateTools
			if len(f.tools) > 0 {
				// build_tools_context: the work order's OWN rows as soon as it
				// has one, which is what makes the template's list vanish.
				tools = nil
				for _, row := range f.tools {
					tools = append(tools, map[string]any{
						"id": row["id"], "name": row["name"], "quantity": row["quantity"],
						"location_hint": row["resolved_location"], "is_required": row["is_required"],
					})
				}
			}
			payload := map[string]any{
				"id": "wo1", "display_title": "Fix the lathe", "status": "in_progress",
			}
			if tools != nil {
				payload["tools"] = tools
			}
			if f.tools != nil {
				payload["tool_rows"] = f.tools
			}
			if f.loto != nil {
				payload["loto_completions"] = f.loto
			}
			_ = json.NewEncoder(w).Encode(payload)
		}
	}
}

// writes is every non-GET request, which is what a safety assertion is about.
func (f *fakeWOTools) writes() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, req := range f.requests {
		if req.method != http.MethodGet {
			out = append(out, req)
		}
	}
	return out
}

func woDriveRoot(t *testing.T, fake *fakeWOTools) (Root, *WorkOrderDetailScreen, func()) {
	r, screen, _, stop := woDriveRootWithDeps(t, fake)
	return r, screen, stop
}

// woReload lands the message the loader really produces, decoded by the real
// client off the fake — which is the only honest way to drive a reload arriving
// UNDER an open modal. `r` cannot do it: the review frame owns the keyboard, so
// the refresh key never reaches the view handler. The real races are the async
// one (every write fires load(), and the next step's review can be open by the
// time it lands) and a return visit through the back stack re-running Init.
func woReload(t *testing.T, r Root, deps Deps) Root {
	t.Helper()
	wo, err := deps.OMS.GetWorkOrder(deps.Ctx, "wo1")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	next, cmd := rootUpdate(t, r, woDetailLoadedMsg{wo: wo})
	return pump(t, next, cmd, 0)
}

func woDriveRootWithDeps(t *testing.T, fake *fakeWOTools) (Root, *WorkOrderDetailScreen, Deps, func()) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewWorkOrderDetailScreen(deps, "wo1")
	r := newTestRoot(screen)
	r.deps = deps
	// Both dimensions: the frames fold their prose against the width the terminal
	// really gave, and an unsized screen would fold against the fixed 51 instead.
	r, _ = rootUpdate(t, r, tea.WindowSizeMsg{Width: 80, Height: 24})
	r = pump(t, r, screen.Init(), 0)
	return r, screen, deps, srv.Close
}

// statusText is what Root's status bar is currently saying. The drives read it
// rather than the command a handler returned, because pump has already fed that
// command back through Root.Update — which is the path the operator's terminal
// takes, and the only one that proves the message survived it.
func statusText(r Root) string {
	return r.status.message
}

// lipglossWidth is the cells a rendered line really occupies, ANSI-aware — the
// same measure every bound in this package budgets against.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

func rootUpdate(t *testing.T, r Root, msg tea.Msg) (Root, tea.Cmd) {
	t.Helper()
	next, cmd := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after, cmd
}

// --- Tools -----------------------------------------------------------------

// TestWOTools_AddRestageAndRemove walks the whole tools half the way an operator
// does and asserts the three requests that actually went out. The restage runs
// TWICE on purpose — once with a location, once BLANK — because blank is a real
// write (it clears the per-job hint and lets the stored location stand in again)
// and a client that skipped it would silently leave the old hint on the job.
func TestWOTools_AddRestageAndRemove(t *testing.T) {
	fake := &fakeWOTools{}
	r, screen, stop := woDriveRoot(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("T"))
	if screen.mode != woModeTools {
		t.Fatalf("mode = %v, want the tool list", screen.mode)
	}
	r = key(t, r, woRuneKey("a"))
	if screen.mode != woModeAddTool {
		t.Fatalf("mode = %v — the add-tool form did not open", screen.mode)
	}
	if _, stillHere := r.screen.(*WorkOrderDetailScreen); !stillHere {
		t.Fatalf("navigated away from the work order: %T", r.screen)
	}

	r = key(t, r, woRuneKey("Scissor lift key"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab}) // → quantity
	r = key(t, r, woRuneKey("2"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab}) // → location
	r = key(t, r, woRuneKey("Bench 2"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.mode != woModeTools {
		t.Fatalf("mode = %v, want to land back on the list after adding", screen.mode)
	}
	added := fake.writes()
	if len(added) != 1 {
		t.Fatalf("writes = %+v, want exactly the add", added)
	}
	if added[0].method != http.MethodPost ||
		added[0].path != "/api/inventory/work-orders/wo1/tools/" {
		t.Fatalf("add = %s %s", added[0].method, added[0].path)
	}
	if added[0].body["name"] != "Scissor lift key" ||
		fmt.Sprint(added[0].body["quantity"]) != "2" ||
		added[0].body["location_hint"] != "Bench 2" {
		t.Errorf("add body = %v", added[0].body)
	}
	// is_required defaults TRUE server-side, and the operator left it checked, so
	// the shortest body that says what they chose is the one that goes.
	if _, present := added[0].body["is_required"]; present {
		t.Errorf("is_required must be absent while it matches the default: %v", added[0].body)
	}
	if out := screen.View(); !strings.Contains(out, "Scissor lift key") ||
		!strings.Contains(out, "staged: Bench 2") {
		t.Errorf("the new row and where it is staged must be on the list: %q", out)
	}

	// Restage it somewhere else.
	r = key(t, r, woRuneKey("l"))
	if screen.mode != woModeToolLocation {
		t.Fatalf("mode = %v, want the restage box", screen.mode)
	}
	for range "Bench 2" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = key(t, r, woRuneKey("Cart by the lathe"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.mode != woModeTools {
		t.Fatalf("mode = %v, want the list after restaging", screen.mode)
	}

	// And CLEAR the hint: blank must still be sent.
	r = key(t, r, woRuneKey("l"))
	for range "Cart by the lathe" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	// Then drop the row.
	r = key(t, r, woRuneKey("d"))

	wrote := fake.writes()
	var got []string
	for _, req := range wrote {
		got = append(got, req.method+" "+req.path)
	}
	want := []string{
		"POST /api/inventory/work-orders/wo1/tools/",
		"PATCH /api/inventory/work-orders/wo1/tools/wt-1/",
		"PATCH /api/inventory/work-orders/wo1/tools/wt-1/",
		"DELETE /api/inventory/work-orders/wo1/tools/wt-1/",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if wrote[1].body["location_hint"] != "Cart by the lathe" {
		t.Errorf("restage body = %v", wrote[1].body)
	}
	hint, present := wrote[2].body["location_hint"]
	if !present || hint != "" {
		t.Errorf("clearing the hint must send an explicit empty string, got %v (present=%v)", hint, present)
	}
	if len(fake.tools) != 0 {
		t.Errorf("the row is still on the work order: %+v", fake.tools)
	}
}

// TestWOTools_ATemplateRowIsRefusedWithoutASendAndTheReasonIsGiven: is_ad_hoc is
// a fact the wire already carried about the row, so the 400 the backend would
// answer with is stated here instead of being sent and bounced. No request may
// leave, and the operator must be told WHY the key declined — a silent decline is
// this project's wedged-program report.
func TestWOTools_ATemplateRowIsRefusedWithoutASendAndTheReasonIsGiven(t *testing.T) {
	fake := &fakeWOTools{tools: []map[string]any{{
		"id": "wt-1", "name": "Torque wrench", "quantity": 1, "is_ad_hoc": false,
		"is_required": true, "location_hint": "", "resolved_location": "Tool crib, drawer 3",
		"inventory_item_name": `Torque wrench 1/2"`,
	}}}
	r, screen, stop := woDriveRoot(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("T"))
	// The template row's location comes from the linked item, so the list must
	// read the RESOLVED value: the hint is blank and a surface reading it would
	// show nothing for a tool that has a perfectly good place.
	if out := screen.View(); !strings.Contains(out, "stored: Tool crib, drawer 3") {
		t.Errorf("the resolved location must be on the row: %q", out)
	}
	r = key(t, r, woRuneKey("d"))
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("a template row must not reach the server: %+v", wrote)
	}
	if got := statusText(r); !strings.Contains(got, "only added tools can be removed") {
		t.Errorf("the decline must say why: %q", got)
	}
	// Restaging, on the other hand, is allowed on EVERY row — per-job staging is
	// the point of the model and it never rewrites the PM template.
	r = key(t, r, woRuneKey("l"))
	if screen.mode != woModeToolLocation {
		t.Fatalf("mode = %v, want the restage box on a template row too", screen.mode)
	}
	r = key(t, r, woRuneKey("Bench 2"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	wrote := fake.writes()
	if len(wrote) != 1 || wrote[0].method != http.MethodPatch {
		t.Fatalf("writes = %+v, want the restage", wrote)
	}
}

// TestWOTools_TheAddFormNamesWhatItDoesToALegacyTemplateList: on a work order
// that owns no rows but DISPLAYS its PM template's, the first ad-hoc row makes
// the job own its list — and build_tools_context then serves that one row instead
// of the template's. The template's tools are not deleted, but they stop being
// shown here, and an operator is entitled to know that before pressing enter
// rather than by watching four tools turn into one.
func TestWOTools_TheAddFormNamesWhatItDoesToALegacyTemplateList(t *testing.T) {
	fake := &fakeWOTools{templateTools: []map[string]any{
		{"id": "mt-1", "name": "Multimeter", "quantity": 1, "is_required": true},
		{"id": "mt-2", "name": "Feeler gauge", "quantity": 1, "is_required": false},
	}}
	r, screen, stop := woDriveRoot(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("T"))
	list := screen.View()
	if !strings.Contains(list, "PM template") {
		t.Errorf("the list must say whose tools are on screen: %q", list)
	}
	r = key(t, r, woRuneKey("a"))
	form := screen.View()
	if !strings.Contains(form, "stop being shown") {
		t.Errorf("the add form must name the consequence: %q", form)
	}
	if !strings.Contains(form, "not deleted") {
		t.Errorf("the add form must also say what is NOT lost: %q", form)
	}
}

// TestWOTools_ABlankNameIsRefusedLocallyAndAZeroQuantityIsNot draws the line this
// half is careful about: the terminal refuses only what it cannot send, and
// leaves every judgement the server owns to the server. A blank name has no wire
// representation the serializer accepts and is caught here; a typed ZERO does —
// and coercing it to 1 would record a tool nobody asked for, so it goes and the
// server's min_value refusal is what comes back.
func TestWOTools_ABlankNameIsRefusedLocallyAndAZeroQuantityIsNot(t *testing.T) {
	fake := &fakeWOTools{}
	r, screen, stop := woDriveRoot(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("T"))
	r = key(t, r, woRuneKey("a"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.mode != woModeAddTool {
		t.Fatalf("a blank name must keep the form open, mode = %v", screen.mode)
	}
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("nothing may be sent for a blank name: %+v", wrote)
	}
	if out := screen.View(); !strings.Contains(out, "tool name is required") {
		t.Errorf("the form must say what is missing: %q", out)
	}

	r = key(t, r, woRuneKey("Shim stock"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
	r = key(t, r, woRuneKey("0"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	wrote := fake.writes()
	if len(wrote) != 1 {
		t.Fatalf("a typed zero must reach the server to be refused there: %+v", wrote)
	}
	if fmt.Sprint(wrote[0].body["quantity"]) != "0" {
		t.Errorf("quantity = %v, want the zero the operator typed", wrote[0].body["quantity"])
	}

	// A quantity that is not a number at all has no wire form, so it IS local.
	r = key(t, r, woRuneKey("a"))
	r = key(t, r, woRuneKey("Pry bar"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyTab})
	r = key(t, r, woRuneKey("a few"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.mode != woModeAddTool {
		t.Fatalf("an unparseable quantity must keep the form open, mode = %v", screen.mode)
	}
	if wrote := fake.writes(); len(wrote) != 1 {
		t.Fatalf("nothing further may be sent: %+v", wrote)
	}
}

// TestWOTools_ARestageFollowsItsOwnRowAcrossAReload: the tool cursor indexes the
// slice positionally, so a reload that shortened the list would re-point it. The
// restage box holds the row's own ID, so a reload that removed the row it was
// opened on refuses rather than writing a location onto whatever now sits at that
// index.
func TestWOTools_ARestageFollowsItsOwnRowAcrossAReload(t *testing.T) {
	fake := &fakeWOTools{tools: []map[string]any{
		{"id": "wt-1", "name": "Torque wrench", "quantity": 1, "is_ad_hoc": true, "is_required": true},
		{"id": "wt-2", "name": "Feeler gauge", "quantity": 1, "is_ad_hoc": true, "is_required": false},
	}}
	r, screen, deps, stop := woDriveRootWithDeps(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("T"))
	r = key(t, r, woRuneKey("l"))
	if screen.mode != woModeToolLocation || screen.locToolID != "wt-1" {
		t.Fatalf("mode = %v, row = %q", screen.mode, screen.locToolID)
	}
	r = key(t, r, woRuneKey("Bench 2"))

	fake.mu.Lock()
	fake.tools = fake.tools[1:] // wt-1 is gone; wt-2 now sits at index 0
	fake.mu.Unlock()
	r = woReload(t, r, deps)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("the restage must not land on another row: %+v", wrote)
	}
	if screen.mode != woModeTools {
		t.Fatalf("mode = %v, want the list", screen.mode)
	}
	if got := statusText(r); !strings.Contains(got, "no longer on this work order") {
		t.Errorf("status = %q", got)
	}
}

// --- Lockout / tagout ------------------------------------------------------

// lotoFixture is a work order whose asset has two energy sources. The first
// carries FULL-LENGTH descriptive text — exactly the 200 / 200 / 300 characters
// WorkOrderLotoCompletion's own max_length allows — because the whole claim on
// the review frame is that a lockout instruction is never cut, and a fixture
// carrying "Panel B" could not tell a frame that folds from one that clips. It is
// prose rather than a run of one letter for the same reason: that is the shape the
// field really holds, and a solid token would exercise pickerWrap's mid-token
// break instead of the ordinary fold.
func lotoFixture() *fakeWOTools {
	return &fakeWOTools{loto: []map[string]any{
		{
			"id": "lc-1", "source_type": "electrical",
			"source_label":     woPad("Electrical 240V main disconnect", "alpha", 200),
			"isolation_point":  woPad("Panel B breaker 14 north wall", "bravo", 200),
			"required_devices": woPad("Red padlock 12 and breaker lockout clamp", "charlie", 300),
			"is_completed":     false,
		},
		{
			"id": "lc-2", "source_type": "pneumatic",
			"source_label": "Pneumatic (90 psi)", "isolation_point": "Wall valve W3",
			"required_devices": "Blue padlock #4", "is_completed": false,
		},
	}}
}

// woPad grows a lead sentence to EXACTLY n characters with numbered repeats of
// one distinctive word, so each field's text is unmistakable for another's and
// the length is the model's own bound rather than approximately it.
func woPad(lead, word string, n int) string {
	out := lead
	for i := 1; len(out) < n; i++ {
		out += fmt.Sprintf(" %s%d", word, i)
	}
	return out[:n]
}

// TestWOLoto_RecordingTakesTwoDeliberateKeysAndNavigationIsNeverOneOfThem is the
// safety claim, driven key by key: every press it takes to REACH the write sends
// nothing, and only `y` on the review frame does.
func TestWOLoto_RecordingTakesTwoDeliberateKeysAndNavigationIsNeverOneOfThem(t *testing.T) {
	fake := lotoFixture()
	r, screen, stop := woDriveRoot(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("L"))
	if screen.mode != woModeLoto {
		t.Fatalf("mode = %v, want the lockout list", screen.mode)
	}
	// Walking the list writes nothing.
	r = key(t, r, woRuneKey("j"))
	r = key(t, r, woRuneKey("k"))
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("moving the cursor must not record anything: %+v", wrote)
	}

	// SPACE is the press this is most careful about: it toggles the row on both
	// neighbouring lists, so a hand carrying that habit here must be told no.
	r = key(t, r, tea.KeyMsg{Type: tea.KeySpace})
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("space must not record a lockout step: %+v", wrote)
	}
	if screen.mode != woModeLoto {
		t.Fatalf("space must not open anything either, mode = %v", screen.mode)
	}
	if out := screen.View(); !strings.Contains(out, "space does not record") ||
		!strings.Contains(out, "enter") {
		t.Errorf("space must say what it declined and which key works: %q", out)
	}

	// Opening the review frame writes nothing.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.mode != woModeLotoConfirm {
		t.Fatalf("mode = %v, want the review frame", screen.mode)
	}
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("opening the review must not record anything: %+v", wrote)
	}

	// A REFLEXIVE SECOND ENTER — the press a hand makes next, and the reason the
	// write is not bound to enter — must also send nothing, and must say so.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("a second enter must not record anything: %+v", wrote)
	}
	if screen.mode != woModeLotoConfirm {
		t.Fatalf("a second enter must leave the frame up, mode = %v", screen.mode)
	}
	if out := woSquash(screen.View()); !strings.Contains(out, woSquash("press y to record")) {
		t.Errorf("the declined key must name the one that works: %q", screen.View())
	}

	// And now the one key that does.
	r = key(t, r, woRuneKey("y"))
	wrote := fake.writes()
	if len(wrote) != 1 {
		t.Fatalf("writes = %+v, want exactly the completion", wrote)
	}
	if wrote[0].method != http.MethodPatch ||
		wrote[0].path != "/api/inventory/work-orders/wo1/loto/lc-1/complete/" {
		t.Fatalf("write = %s %s", wrote[0].method, wrote[0].path)
	}
	if wrote[0].body["is_completed"] != true {
		t.Errorf("is_completed = %v", wrote[0].body["is_completed"])
	}
	if _, present := wrote[0].body["notes"]; present {
		t.Errorf("no note is offered here, so none may be sent: %v", wrote[0].body)
	}
	// Back on the list with the record on screen, because a tech working through
	// a procedure has the next step to mark.
	if screen.mode != woModeLoto {
		t.Fatalf("mode = %v, want the list after recording", screen.mode)
	}
	if out := screen.View(); !strings.Contains(out, "Dana Reyes") {
		t.Errorf("the list must show who the record names: %q", out)
	}
	if out := screen.View(); !strings.Contains(out, "(1/2 isolated)") {
		t.Errorf("the count must move: %q", out)
	}
}

// TestWOLoto_TheReviewFrameShowsTheStepWholeAtEveryPane is requirement 2: the
// operator must see WHICH step they are completing, in full, before they confirm.
//
// The fixture's three descriptive fields are the model's own max_length, so every
// one of them outruns the 51 cells an 80-column pane gives — which makes this a
// claim about FOLDING and REACHING rather than about fitting. Two properties, and
// they are asserted separately because a short pane genuinely cannot hold both at
// once and conflating them would let one stand in for the other:
//
//   - PINNED: the title (which names the DIRECTION the write goes) and the step's
//     own label are on the clipped pane from EVERY scroll position. clampToBox
//     drops from the bottom, so a pinned head is the one part a short pane cannot
//     take, and what must survive is which step `y` is about.
//   - REACHABLE: every character of all three fields, and the sentence saying what
//     pressing y does, can be read with the movement keys the frame names. Nothing
//     is truncated at any pane — which is the requirement: a lockout instruction
//     that does not fit is a layout problem, and this is the layout's answer.
//
// Measured on the CLIPPED pane, because a screen-level View passes while the
// terminal cuts the line.
func TestWOLoto_TheReviewFrameShowsTheStepWholeAtEveryPane(t *testing.T) {
	rec := lotoFixture().loto[0]
	mustReach := map[string]string{
		"source_label":     fmt.Sprint(rec["source_label"]),
		"isolation_point":  fmt.Sprint(rec["isolation_point"]),
		"required_devices": fmt.Sprint(rec["required_devices"]),
		"what y does":      "Pressing y says you have isolated this energy source on this job",
		"no sequence":      woLotoAnyOrder,
	}
	// A WIDE terminal must be ADDITIVE: the 80-column layout is what has to hold,
	// and extra columns may only buy more of the value per line — never trim
	// anything 80 columns showed. Recorded per width and compared below, because a
	// fold hard-coded at 51 would draw the same abbreviated rows on a 120-column
	// terminal with forty columns of pane left blank.
	folds := map[int]int{}
	head := func(s *WorkOrderDetailScreen) string {
		return strings.SplitN(s.View(), "\n\n", 2)[0]
	}
	for _, width := range []int{80, 100, 120} {
		for _, height := range []int{24, 30, 40, 60} {
			r, screen, stop := woDriveRoot(t, lotoFixture())
			r, _ = rootUpdate(t, r, tea.WindowSizeMsg{Width: width, Height: height})
			r = key(t, r, woRuneKey("L"))
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if screen.mode != woModeLotoConfirm {
				stop()
				t.Fatalf("%dx%d: mode = %v", width, height, screen.mode)
			}
			cells, rows := screenBodyCells(width), screenBodyRows(height)
			pane := func() string { return clampToBox(screen.View(), cells, rows) }

			// Walk the whole frame with the keys it names, collecting what was
			// actually drawn at each position, and check the pinned head at every
			// one of them rather than only at the top.
			seen := pane()
			for i := 0; i < 60; i++ {
				at := pane()
				for _, pinned := range []string{"Record lockout step", fmt.Sprint(rec["source_label"])} {
					if !strings.Contains(woSquash(at), woSquash(pinned)) {
						t.Fatalf("%dx%d: %q left the pane after %d scrolls:\n%s",
							width, height, pinned, i, at)
					}
				}
				r = key(t, r, woRuneKey("j"))
				seen += "\n" + pane()
			}
			flat := woSquash(seen)
			for name, value := range mustReach {
				if !strings.Contains(flat, woSquash(value)) {
					t.Errorf("%dx%d: %s is not readable in full on the review frame", width, height, name)
				}
			}
			folds[width] = len(strings.Split(strings.TrimRight(head(screen), "\n"), "\n"))
			stop()
		}
	}
	// Strictly fewer lines at 100 than at 80 — which a fold hard-coded at 51 could
	// not manage — and never MORE as the pane grows. Not strictly fewer at every
	// step: a 200-character value folds onto three lines at both 71 and 91 cells,
	// so demanding a decrease there would be demanding something arithmetic does
	// not owe.
	if folds[80] <= folds[100] || folds[100] < folds[120] {
		t.Errorf("the pinned step label folds onto %d/%d/%d lines at 80/100/120 columns — "+
			"a wider terminal must spend the extra pane on the value, not leave it blank",
			folds[80], folds[100], folds[120])
	}
}

// woSquash reduces a rendered pane to its visible non-space characters.
//
// It is how "nothing was cut" is asserted about text that was deliberately
// BROKEN ACROSS LINES: pickerWrap indents continuations and drops the space it
// folded at, and where a token has nowhere to break it splits mid-token — so a
// folded value simply is not a substring of the pane in any
// whitespace-preserving form. Dropping whitespace from both sides asks the only
// question that survives folding: does every visible character of the value
// appear, in order, somewhere on what was drawn. Each fixture field carries its
// own distinctive word (woPad) so one field cannot vouch for another.
func woSquash(s string) string {
	var b strings.Builder
	for _, r := range stripANSI(s) {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestWOLoto_ARecordCanBeTakenBackAndTheDirectionIsNeverImplied: the endpoint is
// a toggle, so clearing a record is a write of its own — and it is the one whose
// wording must not be shared with recording, because "recorded" and "cleared" are
// opposite facts about a safety record.
func TestWOLoto_ARecordCanBeTakenBackAndTheDirectionIsNeverImplied(t *testing.T) {
	fake := lotoFixture()
	fake.loto[0]["is_completed"] = true
	fake.loto[0]["completed_by_name"] = "Dana Reyes"
	r, screen, stop := woDriveRoot(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("L"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	// The DIRECTION is pinned in the title and named on the legend — both on the
	// pane at every height — and the prose explaining it is reachable in the
	// scrolled body, which is where the step's own text lives too.
	frame := screen.View()
	if !strings.Contains(frame, "Clear lockout record") {
		t.Errorf("the frame must name the direction it is about to write: %q", frame)
	}
	if !strings.Contains(woSquash(frame), woSquash("y clear the record")) {
		t.Errorf("the legend must say which way y goes: %q", frame)
	}
	reached := woSquash(frame)
	for i := 0; i < 40; i++ {
		r = key(t, r, woRuneKey("j"))
		reached += woSquash(screen.View())
	}
	if !strings.Contains(reached, woSquash("takes the record back")) {
		t.Errorf("the frame must say what clearing costs: %q", screen.View())
	}
	r = key(t, r, woRuneKey("y"))
	wrote := fake.writes()
	if len(wrote) != 1 || wrote[0].body["is_completed"] != false {
		t.Fatalf("writes = %+v, want is_completed false", wrote)
	}
	if out := statusText(r); !strings.Contains(out, "cleared") {
		t.Errorf("the answer must say which way it went: %q", out)
	}
}

// TestWOLoto_NothingHereEnforcesASequence is the NEGATIVE half, and it is the
// rule an implementation is most tempted to invent: complete_loto has no ordering
// gate, so recording the LAST step first must reach the server exactly as typed,
// and the frame must SAY that no sequence is enforced rather than leave an
// operator to assume one is.
func TestWOLoto_NothingHereEnforcesASequence(t *testing.T) {
	fake := lotoFixture()
	r, screen, stop := woDriveRoot(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("L"))
	r = key(t, r, woRuneKey("j")) // the SECOND source, with the first untouched
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	reached := woSquash(screen.View())
	for i := 0; i < 40; i++ {
		r = key(t, r, woRuneKey("j"))
		reached += woSquash(screen.View())
	}
	if !strings.Contains(reached, woSquash(woLotoAnyOrder)) {
		t.Errorf("the frame must report that no sequence is enforced: %q", screen.View())
	}
	r = key(t, r, woRuneKey("y"))
	wrote := fake.writes()
	if len(wrote) != 1 ||
		wrote[0].path != "/api/inventory/work-orders/wo1/loto/lc-2/complete/" {
		t.Fatalf("writes = %+v, want the second source recorded first", wrote)
	}
}

// TestWOLoto_TheServersRefusalReachesTheOperatorOnOneMarkedLine: the two refusals
// complete_loto can answer with are hand-written DRF {"detail": …} bodies, and
// omsapi.parseError hands the whole raw body over when the envelope carries no
// code — so a gateway page arrives here verbatim, newlines and all. The sentence
// must reach the operator; it must be MARKED; and it must be ONE row, because the
// footer's height is what the scroller above is budgeted against and clampToBox
// drops from the bottom, taking the hint line and every key named on it.
func TestWOLoto_TheServersRefusalReachesTheOperatorOnOneMarkedLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"not on this work order", http.StatusNotFound,
			`{"detail":"LOTO completion record not found."}`, "LOTO completion record not found"},
		{"a gateway page", http.StatusBadGateway,
			"<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\n" +
				"<center><h1>502 Bad Gateway</h1></center>\n</body>\n</html>\n", "502 Bad Gateway"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := lotoFixture()
			fake.refuseLotoStatus, fake.refuseLotoBody = tc.status, tc.body
			r, screen, stop := woDriveRoot(t, fake)
			defer stop()

			r = key(t, r, woRuneKey("L"))
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			r = key(t, r, woRuneKey("y"))

			// The refusal reaches the frame the operator is standing on…
			if !strings.Contains(woSquash(screen.View()), woSquash(tc.want)) {
				t.Errorf("the review frame must relay the refusal: %q", screen.View())
			}
			// …and the BODY's answer row, which is where it must be one line.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc}) // off the confirm
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc}) // off the list
			if screen.mode != woModeView {
				t.Fatalf("mode = %v, want the body", screen.mode)
			}
			line := screen.actionLine()
			if strings.Contains(line, "\n") {
				t.Errorf("the answer row must be ONE line: %q", line)
			}
			if !strings.HasPrefix(stripANSI(line), jdeStatusErrMark) {
				t.Errorf("a refusal must be marked: %q", stripANSI(line))
			}
			if w := lipglossWidth(line); w > screenBodyCells(80) {
				t.Errorf("the answer row is %d cells against a pane of %d: %q",
					w, screenBodyCells(80), stripANSI(line))
			}
			// Nothing was recorded, and the row still reads as not recorded.
			if out := screen.View(); !strings.Contains(out, "not recorded") {
				t.Errorf("a refused step must not read as isolated: %q", out)
			}
		})
	}
}

// TestWOLoto_AReloadThatFlipsTheStepClosesTheReviewRatherThanRewordingIt: the
// review frame says what `y` is about to do, so if the row's recorded state
// changes underneath — somebody ticking it on the web — `y` must not quietly come
// to mean the opposite. The frame closes and says what changed, and nothing is
// written.
func TestWOLoto_AReloadThatFlipsTheStepClosesTheReviewRatherThanRewordingIt(t *testing.T) {
	fake := lotoFixture()
	r, screen, deps, stop := woDriveRootWithDeps(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("L"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.mode != woModeLotoConfirm {
		t.Fatalf("mode = %v", screen.mode)
	}
	if !strings.Contains(screen.View(), "Record lockout step") {
		t.Fatalf("frame = %q", screen.View())
	}

	// Somebody else records it, and this screen reloads.
	fake.mu.Lock()
	fake.loto[0]["is_completed"] = true
	fake.loto[0]["completed_by_name"] = "Sam Okafor"
	fake.mu.Unlock()
	r = woReload(t, r, deps)

	if screen.mode != woModeLoto {
		t.Fatalf("mode = %v, want the review closed", screen.mode)
	}
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("nothing may be written: %+v", wrote)
	}
	if out := statusText(r); !strings.Contains(out, "Nothing was recorded") {
		t.Errorf("the operator must be told why the frame closed: %q", out)
	}
	// And the cursor still stands on the step they were reading.
	if rec, ok := screen.currentLoto(); !ok || rec.IDString() != "lc-1" {
		t.Errorf("cursor = %d, want it still on lc-1", screen.lotoCursor)
	}
}

// TestWOLoto_ARemovedStepClosesTheReviewInsteadOfWritingToWhateverIsNowThere: the
// cursor indexes the slice positionally, so a reload that shortened the list
// would re-point it. The review frame follows its ROW by id, which is what stops
// a confirm naming one lockout step while `y` records another.
func TestWOLoto_ARemovedStepClosesTheReviewInsteadOfWritingToWhateverIsNowThere(t *testing.T) {
	fake := lotoFixture()
	r, screen, deps, stop := woDriveRootWithDeps(t, fake)
	defer stop()

	r = key(t, r, woRuneKey("L"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	fake.mu.Lock()
	fake.loto = fake.loto[1:] // lc-1 is gone; lc-2 now sits at index 0
	fake.mu.Unlock()
	r = woReload(t, r, deps)

	if screen.mode != woModeLoto {
		t.Fatalf("mode = %v, want the review closed", screen.mode)
	}
	if wrote := fake.writes(); len(wrote) != 0 {
		t.Fatalf("nothing may be written: %+v", wrote)
	}
	if out := statusText(r); !strings.Contains(out, "no longer on this work order") {
		t.Errorf("status = %q", out)
	}
}

// TestWOLoto_TheFooterNamesTheKeyExactlyWhereItWorks: unlike tools there is
// nothing here to CREATE — rows are cut when the work order is generated — so on
// an asset with no recorded energy sources `L` can only decline, and naming it
// would be the bar claiming a key that does nothing. Both directions, because a
// key that works and is not named is the same rule broken from the other side.
func TestWOLoto_TheFooterNamesTheKeyExactlyWhereItWorks(t *testing.T) {
	empty := &fakeWOTools{}
	r, screen, stop := woDriveRoot(t, empty)
	if out := screen.View(); strings.Contains(out, "L lockout") {
		t.Errorf("the footer must not name L with no energy sources: %q", out)
	}
	r = key(t, r, woRuneKey("L"))
	if screen.mode != woModeView {
		t.Fatalf("mode = %v, want L to have declined", screen.mode)
	}
	if got := statusText(r); !strings.Contains(got, "no energy sources") {
		t.Errorf("the decline must say why: %q", got)
	}
	stop()

	r, screen, stop = woDriveRoot(t, lotoFixture())
	defer stop()
	if out := screen.View(); !strings.Contains(out, "L lockout (0/2)") {
		t.Errorf("the footer must name L, and the count a tech came for: %q", out)
	}
	r = key(t, r, woRuneKey("L"))
	if screen.mode != woModeLoto {
		t.Fatalf("mode = %v, want the lockout list", screen.mode)
	}
}

// TestWOLoto_NoKeyBUTyRecordsAnything is the safety claim made over the whole KEY
// SPACE rather than over a list of keys somebody thought of.
//
// A curated vocabulary is exactly how this project has repeatedly shipped a key
// that acted while nothing pressed it (AGENTS.md's `N`, `tab` and
// `poPhaseSupplierSwitch`), and here the thing that would slip through is a
// keystroke that writes a lockout record. So every printable ASCII rune and every
// named special is pressed on the review frame, each against a FRESH screen so no
// press can be masked by a previous one having closed the frame — and exactly two
// of them may reach the server.
func TestWOLoto_NoKeyBUTyRecordsAnything(t *testing.T) {
	var space []tea.KeyMsg
	for r := rune(' '); r <= '~'; r++ {
		space = append(space, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	for _, k := range []tea.KeyType{
		tea.KeyEnter, tea.KeyEsc, tea.KeySpace, tea.KeyTab, tea.KeyShiftTab,
		tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown,
		tea.KeyBackspace, tea.KeyDelete, tea.KeyCtrlD, tea.KeyCtrlU,
		tea.KeyCtrlN, tea.KeyCtrlP, tea.KeyCtrlE, tea.KeyCtrlX,
	} {
		space = append(space, tea.KeyMsg{Type: k})
	}

	writers := map[string]bool{}
	for _, k := range space {
		fake := lotoFixture()
		r, screen, stop := woDriveRoot(t, fake)
		r = key(t, r, woRuneKey("L"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if screen.mode != woModeLotoConfirm {
			stop()
			t.Fatalf("%q: the review frame did not open", k.String())
		}
		before := len(fake.writes())
		r = key(t, r, k)
		if len(fake.writes()) > before {
			writers[k.String()] = true
		}
		stop()
	}
	want := map[string]bool{"y": true, "Y": true}
	if fmt.Sprint(writers) != fmt.Sprint(want) {
		t.Errorf("keys that recorded a lockout step = %v, want only y/Y", writers)
	}
}

// TestWOTools_TheToolListActsOnlyWhereItsBarSaysSo walks the same key space over
// the tool list, in both states its bar changes shape in: with a row to act on,
// and empty — where it names `a` and `esc` alone, because a key that acts on a
// highlighted row has none.
//
// Each press is judged on the CLIPPED pane, not on a state fingerprint: rule 1 is
// about a change the operator can distinguish, and a key that moved an int nothing
// draws has not acted (AGENTS.md's listKeyEffectAt).
func TestWOTools_TheToolListActsOnlyWhereItsBarSaysSo(t *testing.T) {
	rows := []map[string]any{
		{"id": "wt-1", "name": "Torque wrench", "quantity": 1, "is_ad_hoc": true, "is_required": true},
		{"id": "wt-2", "name": "Feeler gauge", "quantity": 1, "is_ad_hoc": true, "is_required": false},
	}
	for _, tc := range []struct {
		name  string
		tools []map[string]any
		named []string
	}{
		{"with rows", rows, []string{"j", "k", "a", "l", "d", "esc"}},
		{"empty", nil, []string{"a", "esc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isNamed := map[string]bool{}
			for _, n := range tc.named {
				isNamed[n] = true
			}
			// The two movement tokens the bar spells as pairs, plus `q`, which
			// every picker on this screen binds as a synonym for esc exactly as
			// the tasks and materials lists do. Recorded rather than silently
			// tolerated, so a synonym is a decision and not a gap.
			for _, alias := range []string{"up", "down", "q"} {
				isNamed[alias] = true
			}
			acts := map[string]bool{}
			for _, k := range woPrintableSpace() {
				fake := &fakeWOTools{tools: tc.tools}
				r, screen, stop := woDriveRoot(t, fake)
				r = key(t, r, woRuneKey("T"))
				if screen.mode != woModeTools {
					stop()
					t.Fatalf("the tool list did not open")
				}
				before := clampToBox(screen.View(), screenBodyCells(80), screenBodyRows(24))
				beforeMode := screen.mode
				r = key(t, r, k)
				after := clampToBox(screen.View(), screenBodyCells(80), screenBodyRows(24))
				if before != after || beforeMode != screen.mode || len(fake.writes()) > 0 {
					acts[k.String()] = true
				}
				stop()
			}
			for spelling := range acts {
				if !isNamed[spelling] {
					t.Errorf("%q acts on the tool list but the bar does not name it", spelling)
				}
			}
			// THE OTHER DIRECTION, without which the check above passes over a bar
			// naming keys that do nothing. Asked per KEY for the keys that have an
			// answer from the resting position, and per PAIR for movement: `k` at
			// the top row correctly does nothing, so a per-key floor there would
			// fail on right behaviour (AGENTS.md's forward-per-token rule).
			for _, k := range tc.named {
				if k == "j" || k == "k" {
					continue
				}
				if !acts[k] {
					t.Errorf("the bar names %q on the tool list (%s) and it does nothing", k, tc.name)
				}
			}
			if moves := acts["j"] || acts["k"]; moves != (len(tc.tools) > 1) {
				t.Errorf("%s: j/k move = %v, want %v for %d row(s)",
					tc.name, moves, len(tc.tools) > 1, len(tc.tools))
			}
		})
	}
}

// woType sends one rune WITHOUT settling the command it returns. Typing is
// synchronous — the only command a rune produces is the cursor blink — and pump's
// 200ms backstop would otherwise be paid per keystroke: 240 of them is 48 seconds
// in a package that has already hit `go test`'s per-package timeout once.
func woType(t *testing.T, r Root, c rune) Root {
	t.Helper()
	next, _ := rootUpdate(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c}})
	return next
}

// woPrintableSpace is every printable ASCII rune plus the named specials a
// columnar list can receive — the space, not a vocabulary.
func woPrintableSpace() []tea.KeyMsg {
	var out []tea.KeyMsg
	for r := rune(' '); r <= '~'; r++ {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	for _, k := range []tea.KeyType{
		tea.KeyEnter, tea.KeyEsc, tea.KeySpace, tea.KeyTab, tea.KeyShiftTab,
		tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown,
		tea.KeyBackspace, tea.KeyDelete,
	} {
		out = append(out, tea.KeyMsg{Type: k})
	}
	return out
}

// TestWOToolsLoto_NoLineRunsPastThePane: every frame this work adds draws OMS-
// supplied text — tool names, notes, lockout labels, isolation points, device
// lists — and none of it may be handed to clampToBox over-wide.
//
// Measured on what the SCREEN hands over rather than on the clipped pane, because
// after clampToBox no line can be too wide: the truncation has already happened,
// and what it takes with the tail of a styled line is the closing SGR reset, which
// leaves the terminal coloured for everything drawn afterwards.
//
// The fixtures carry FULL-LENGTH values at every field's own max_length, because a
// width check whose fixture cannot reach the bound asserts nothing (AGENTS.md's
// vacuous-fixture rule) — and the sweep FAILS if no frame in a run was wide enough
// to have needed folding at the narrowest pane.
//
// 80/100/120 rather than every width Root draws, and the reason is recorded rather
// than left as an omission: 80 columns is the width that must HOLD, and below it
// this screen overruns for reasons that are neither new nor local. At width 45 the
// pane is 16 cells — pickerWrap floors its own budget at 12, a list row's caret
// gutter is 6 of the 16, and a section HEADING ("Lockout / Tagout (0/2 isolated)")
// has nowhere to fold at all. That is true of every TextScroller detail sheet in
// the package, not of this change, and closing it means giving these screens the
// refusal-or-fold treatment ListScreen already has — the shape of sc-jde-lift
// rather than a patch. Widening the set here without that work would only record a
// failure this change did not cause.
func TestWOToolsLoto_NoLineRunsPastThePane(t *testing.T) {
	fake := lotoFixture()
	fake.tools = []map[string]any{{
		"id": "wt-1", "is_ad_hoc": true, "is_required": true, "quantity": 3,
		"name":              woPad("Hydraulic torque multiplier 3/4 drive", "delta", 200),
		"location_hint":     woPad("Mezzanine rack C shelf 4 behind the press", "echo", 200),
		"resolved_location": woPad("Mezzanine rack C shelf 4 behind the press", "echo", 200),
		"notes":             woPad("Torque to 210 Nm in three passes", "foxtrot", 300),
	}}
	frames := []struct {
		name string
		open func(t *testing.T, r Root) Root
	}{
		{"body", func(t *testing.T, r Root) Root { return r }},
		{"tool list", func(t *testing.T, r Root) Root { return key(t, r, woRuneKey("T")) }},
		{"add tool", func(t *testing.T, r Root) Root {
			return key(t, key(t, r, woRuneKey("T")), woRuneKey("a"))
		}},
		{"restage", func(t *testing.T, r Root) Root {
			return key(t, key(t, r, woRuneKey("T")), woRuneKey("l"))
		}},
		{"lockout list", func(t *testing.T, r Root) Root { return key(t, r, woRuneKey("L")) }},
		{"lockout review", func(t *testing.T, r Root) Root {
			return key(t, key(t, r, woRuneKey("L")), tea.KeyMsg{Type: tea.KeyEnter})
		}},
	}
	folded := 0
	for _, width := range []int{80, 100, 120} {
		cells := screenBodyCells(width)
		for _, f := range frames {
			r, screen, stop := woDriveRoot(t, fake)
			r, _ = rootUpdate(t, r, tea.WindowSizeMsg{Width: width, Height: 40})
			r = f.open(t, r)
			for _, line := range strings.Split(screen.View(), "\n") {
				if w := lipglossWidth(line); w > cells {
					t.Errorf("%s at width %d: a %d-cell line into a %d-cell pane: %q",
						f.name, width, w, cells, stripANSI(line))
				}
				if width == 80 && lipglossWidth(line) > cells-8 {
					folded++
				}
			}
			stop()
		}
	}
	if folded == 0 {
		t.Error("no frame came near the 80-column pane — the fixtures cannot reach the bound " +
			"this test is about, so it would pass with every fold deleted")
	}
}

// TestWOTools_EveryKeystrokeMovesTheTypedRow: past the column where a box fills
// its row, the value has to SCROLL — or every further keystroke redraws the row
// byte for byte with the caret already clipped off the pane, which is the reported
// hang reached by typing.
//
// The runes are DISTINCT on purpose: a viewport full of one repeated character
// looks the same however far it has scrolled, so a test that holds one key down
// passes without scrolling at all.
func TestWOTools_EveryKeystrokeMovesTheTypedRow(t *testing.T) {
	alphabet := []rune("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	for _, tc := range []struct {
		name string
		open func(t *testing.T, r Root) Root
	}{
		{"add tool", func(t *testing.T, r Root) Root {
			return key(t, key(t, r, woRuneKey("T")), woRuneKey("a"))
		}},
		{"restage", func(t *testing.T, r Root) Root {
			return key(t, key(t, r, woRuneKey("T")), woRuneKey("l"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeWOTools{tools: []map[string]any{{
				"id": "wt-1", "name": "Torque wrench", "quantity": 1,
				"is_ad_hoc": true, "is_required": true,
			}}}
			r, screen, stop := woDriveRoot(t, fake)
			defer stop()
			r = tc.open(t, r)

			for i := 0; i < 120; i++ {
				before := stripANSI(screen.View())
				r = woType(t, r, alphabet[i%len(alphabet)])
				if after := stripANSI(screen.View()); before == after {
					t.Fatalf("%s: keystroke %d (%q) redrew the pane byte for byte — "+
						"the box stopped scrolling at %d characters",
						tc.name, i+1, string(alphabet[i%len(alphabet)]), i)
				}
			}
			// And no line ever ran past the pane while that was happening.
			for _, line := range strings.Split(screen.View(), "\n") {
				if w := lipglossWidth(line); w > screenBodyCells(80) {
					t.Errorf("%s: a %d-cell line into a %d-cell pane: %q",
						tc.name, w, screenBodyCells(80), stripANSI(line))
				}
			}
		})
	}
}
