package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// maintenance_rollup_drive_test.go — the PM due list, the rollup sheet and the
// asset maintenance history, each driven through Root.Update against a fake
// that answers with the bodies RECORDED from a real OpenMakerSuite
// (internal/omsapi/testdata/maintenance_*.json). A fake built from ScanTTY's own
// structs could not disagree with them; these bytes can.

func recordedOMS(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

type rollupFakeReply struct {
	status int
	body   []byte
}

// rollupFake answers by METHOD and PATH with a recorded body, and keeps every
// request it was sent.
type rollupFake struct {
	t       *testing.T
	mu      sync.Mutex
	replies map[string]rollupFakeReply
	calls   []rollupFakeCall
}

type rollupFakeCall struct {
	method, path, query string
	body                map[string]any
}

func newRollupFake(t *testing.T) *rollupFake {
	return &rollupFake{t: t, replies: map[string]rollupFakeReply{}}
}

func (f *rollupFake) on(method, path string, status int, fixture string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[method+" "+path] = rollupFakeReply{status: status, body: recordedOMS(f.t, fixture)}
}

func (f *rollupFake) onBody(method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[method+" "+path] = rollupFakeReply{status: status, body: []byte(body)}
}

func (f *rollupFake) sent(method, path string) []rollupFakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rollupFakeCall
	for _, c := range f.calls {
		if c.method == method && c.path == path {
			out = append(out, c)
		}
	}
	return out
}

func (f *rollupFake) serve() (Deps, func()) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		call := rollupFakeCall{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		_ = json.Unmarshal(raw, &call.body)
		f.mu.Lock()
		f.calls = append(f.calls, call)
		reply, ok := f.replies[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no fake for this path"}}`))
			return
		}
		w.WriteHeader(reply.status)
		_, _ = w.Write(reply.body)
	}))
	return Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, srv.Close
}

func rollupDrive(t *testing.T, screen Screen, deps Deps) Root {
	t.Helper()
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	r = next.(Root)
	return pump(t, r, screen.Init(), 0)
}

func rollupPane(r Root) string {
	return strings.Join(strings.Fields(stripANSI(r.View())), " ")
}

const (
	dueWeekPath   = "/api/inventory/maintenance-items/due_this_week/"
	dueMonthPath  = "/api/inventory/maintenance-items/due_this_month/"
	bulkPath      = "/api/inventory/maintenance-items/generate_work_orders_bulk/"
	historyPath   = "/api/inventory/assets/13b27d28-d92c-4ffc-ab06-b158bc4194d6/maintenance-history/"
	recordsPath   = "/api/inventory/maintenance-records/"
	sawAssetID    = "13b27d28-d92c-4ffc-ab06-b158bc4194d6"
	recordedVndID = "baf2b25f-21c9-4b0a-a64a-e1f9fda5809f"
)

// THE WEB'S THREE SECTIONS OFF THE RECORDED LISTS, AND THE CONFIRM NAMES THE
// COUNT. The recording's week holds two overdue items and two upcoming, and the
// month one more — so the month list's five rows must NOT be drawn as five more.
// The generation posts {}, the server created 3 of the 4, and the pane says both
// numbers rather than implying the fourth failed.
func TestPMDue_GeneratesWhatIsDueAndStatesTheServersCount(t *testing.T) {
	fake := newRollupFake(t)
	fake.on("GET", dueWeekPath, 200, "maintenance_due_week.json")
	fake.on("GET", dueMonthPath, 200, "maintenance_due_month.json")
	fake.on("POST", bulkPath, 201, "maintenance_generate_bulk.json")
	deps, stop := fake.serve()
	defer stop()

	screen := NewPMDueScreen(deps)
	r := rollupDrive(t, screen, deps)
	if pane := rollupPane(r); !strings.Contains(pane, "2 overdue · 2 due this week · 1 more this month") {
		t.Fatalf("sections off the recorded lists:\n%s", pane)
	}
	if !strings.Contains(rollupPane(r), "W generate due WOs") {
		t.Fatal("the bar does not name W over a week list with items in it")
	}

	r = key(t, r, poRuneKey("W"))
	if !strings.Contains(rollupPane(r), "Generate work orders for 4 PM items overdue or due in the next 7 days?") {
		t.Fatalf("the confirm does not name the count:\n%s", rollupPane(r))
	}
	if n := len(fake.sent("POST", bulkPath)); n != 0 {
		t.Fatalf("W alone posted %d generation(s); the write waits for y", n)
	}

	r = key(t, r, poRuneKey("y"))
	posts := fake.sent("POST", bulkPath)
	if len(posts) != 1 || len(posts[0].body) != 0 {
		t.Fatalf("generation posts = %+v, want one POST of {}", posts)
	}
	want := "Created 3 work orders for 4 due · the server skips items that already have an open or in-progress work order."
	if screen.note != want {
		t.Errorf("note = %q, want %q", screen.note, want)
	}
	if n := len(fake.sent("GET", dueWeekPath)); n < 2 {
		t.Errorf("the lists were read %d time(s); a generation must reload them", n)
	}
}

// A REFUSED GENERATION IS THE SERVER'S SENTENCE, and nothing is claimed as made.
func TestPMDue_ARefusedGenerationShowsTheServersSentence(t *testing.T) {
	fake := newRollupFake(t)
	fake.on("GET", dueWeekPath, 200, "maintenance_due_week.json")
	fake.on("GET", dueMonthPath, 200, "maintenance_due_month.json")
	fake.on("POST", bulkPath, 401, "maintenance_generate_bulk_anonymous.json")
	deps, stop := fake.serve()
	defer stop()

	screen := NewPMDueScreen(deps)
	r := rollupDrive(t, screen, deps)
	r = key(t, r, poRuneKey("W"))
	r = key(t, r, poRuneKey("y"))
	if !strings.Contains(rollupPane(r), "nothing generated: Authentication credentials were not provided.") {
		t.Fatalf("the refusal is not the server's sentence:\n%s", rollupPane(r))
	}
}

// n and esc both leave the confirm WITHOUT a write.
func TestPMDue_TheConfirmCancelsWithoutWriting(t *testing.T) {
	for _, cancel := range []tea.KeyMsg{poRuneKey("n"), {Type: tea.KeyEsc}} {
		fake := newRollupFake(t)
		fake.on("GET", dueWeekPath, 200, "maintenance_due_week.json")
		fake.on("GET", dueMonthPath, 200, "maintenance_due_month.json")
		deps, stop := fake.serve()
		screen := NewPMDueScreen(deps)
		r := rollupDrive(t, screen, deps)
		r = key(t, r, poRuneKey("W"))
		r = key(t, r, cancel)
		if screen.confirming || len(fake.sent("POST", bulkPath)) != 0 {
			t.Errorf("%s: confirming=%v posts=%d", cancel.String(), screen.confirming, len(fake.sent("POST", bulkPath)))
		}
		if _, isDue := r.screen.(*PMDueScreen); !isDue {
			t.Errorf("%s left the screen for %T; it should only close the confirm", cancel.String(), r.screen)
		}
		stop()
	}
}

// THE ROLLUP DRAWS BOTH HALVES OFF THE RECORDED BODIES, and `f` narrows the
// active list to one kind the way the web's chips do.
func TestMaintenanceRollup_DrawsTheRecordedDashboardAndFilters(t *testing.T) {
	fake := newRollupFake(t)
	fake.on("GET", "/api/inventory/maintenance/dashboard/", 200, "maintenance_dashboard.json")
	fake.on("GET", "/api/inventory/maintenance/active/", 200, "maintenance_active.json")
	deps, stop := fake.serve()
	defer stop()

	screen := NewMaintenanceRollupScreen(deps)
	r := rollupDrive(t, screen, deps)
	body := stripANSI(screen.renderBody(200))
	for _, want := range []string{
		"Active maintenance · All (4)", "Dust collector gate sticks", "Location problem",
		"Scheduled PM (6)", "7d overdue", "Unscheduled / open problems (1)",
		"All time  $89.50", "Cost by asset (last 90 days)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rollup body lacks %q:\n%s", want, body)
		}
	}
	r = key(t, r, poRuneKey("f"))
	r = key(t, r, poRuneKey("f"))
	body = stripANSI(screen.renderBody(200))
	if !strings.Contains(body, "Active maintenance · Asset problems (1)") || strings.Contains(body, "Dust collector gate sticks") {
		t.Errorf("filter to asset problems drew:\n%s", body)
	}
	_ = r
}

// ---------------------------------------------------------------------------
// Asset maintenance history
// ---------------------------------------------------------------------------

func historyFake(t *testing.T) *rollupFake {
	fake := newRollupFake(t)
	fake.on("GET", historyPath, 200, "maintenance_history.json")
	fake.onBody("GET", "/api/vendors/vendors/", 200, `{"count":1,"next":null,"previous":null,"results":[`+
		`{"id":"`+recordedVndID+`","name":"Hill Country Saw Service","vendor_kind":"general","is_active":true}]}`)
	return fake
}

func historyDrive(t *testing.T, fake *rollupFake, staff bool) (Root, *AssetMaintenanceHistoryScreen, func()) {
	t.Helper()
	deps, stop := fake.serve()
	deps.InitialStaff = staff
	screen := NewAssetMaintenanceHistoryScreen(deps, sawAssetID, "SawStop PCS 3HP table saw")
	screen.now = func() time.Time { return time.Date(2026, 9, 14, 23, 30, 0, 0, time.UTC) }
	screen.query = screen.defaultQuery()
	return rollupDrive(t, screen, deps), screen, stop
}

func histType(t *testing.T, r Root, text string) Root {
	t.Helper()
	return key(t, r, poRuneKey(text))
}

func histDown(t *testing.T, r Root, n int) Root {
	for i := 0; i < n; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	return r
}

// THE WEB'S DEFAULT RANGE IS SENT, and the recorded total heads the list.
func TestAssetHistory_ReadsTheWebsDefaultRange(t *testing.T) {
	fake := historyFake(t)
	r, _, stop := historyDrive(t, fake, true)
	defer stop()
	gets := fake.sent("GET", historyPath)
	if len(gets) != 1 || gets[0].query != "since=2023-09-14&source=all&until=2026-09-14" {
		t.Fatalf("history read = %+v", gets)
	}
	if pane := rollupPane(r); !strings.Contains(pane, "Total $1052.00 across 3 records") {
		t.Errorf("pane lacks the recorded total:\n%s", pane)
	}
}

// LOGGING WORK: the body is the web's, the date defaults to the SERVER's today
// (UTC — the fake clock is 23:30 UTC, already tomorrow in any zone east of it),
// and a refusal is the field's own sentence with every typed
// value kept.
func TestAssetHistory_LogsBackdatedWorkAndKeepsItOnARefusal(t *testing.T) {
	fake := historyFake(t)
	fake.on("POST", recordsPath, 400, "maintenance_record_create_future.json")
	r, screen, stop := historyDrive(t, fake, true)
	defer stop()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != histPhaseCreate {
		t.Fatalf("Enter opened phase %d, want the log-work form", screen.phase)
	}
	if got := screen.inputs[histFieldDate].Value(); got != "2026-09-14" {
		t.Errorf("completed on defaults to %q, want the UTC date 2026-09-14", got)
	}
	r = histType(t, r, "Trunnion lubrication")
	r = histDown(t, r, 1)
	r = histType(t, r, "Cleaned and greased trunnions.")
	r = histDown(t, r, 1)
	for range "2026-09-14" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = histType(t, r, "2023-06-02")
	r = histDown(t, r, 3) // performer, vendor, cost
	r = histType(t, r, "75.25")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	posts := fake.sent("POST", recordsPath)
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}
	body := posts[0].body
	for k, want := range map[string]any{
		"asset": sawAssetID, "title": "Trunnion lubrication", "description": "Cleaned and greased trunnions.",
		"completed_on": "2023-06-02", "vendor": recordedVndID, "cost": "75.25", "performed_by_internal": nil,
	} {
		if got, ok := body[k]; !ok || got != want {
			t.Errorf("body[%s] = %v (present %v), want %v", k, got, ok, want)
		}
	}
	if !strings.Contains(rollupPane(r), "completed_on: completed_on cannot be in the future.") {
		t.Errorf("the refusal is not the field's own sentence:\n%s", rollupPane(r))
	}
	if screen.phase != histPhaseCreate || screen.inputs[histFieldTitle].Value() != "Trunnion lubrication" {
		t.Errorf("a refusal left phase %d title %q; nothing typed may be lost", screen.phase, screen.inputs[histFieldTitle].Value())
	}
}

// INTERNAL STAFF IS RESOLVED BY AN EXACT USERNAME, not by the directory's
// substring search — and a name matching nobody never reaches the record write.
func TestAssetHistory_InternalStaffIsAnExactUsername(t *testing.T) {
	fake := historyFake(t)
	fake.on("POST", recordsPath, 201, "maintenance_record_create.json")
	fake.onBody("GET", "/api/membership/users/", 200, `{"count":2,"next":null,"previous":null,"results":[`+
		`{"id":7,"username":"labstaff2"},{"id":1,"username":"labstaff"}]}`)
	r, screen, stop := historyDrive(t, fake, true)
	defer stop()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = histType(t, r, "Fence alignment")
	r = histDown(t, r, 1)
	r = histType(t, r, "Squared fence to blade.")
	r = histDown(t, r, 2) // date, performer
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRight})
	if !screen.internal {
		t.Fatal("→ on Performed by did not switch to internal staff")
	}
	r = histDown(t, r, 1)
	r = histType(t, r, "labstaf")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if n := len(fake.sent("POST", recordsPath)); n != 0 {
		t.Fatalf("a username matching nobody posted %d record(s)", n)
	}
	if !strings.Contains(rollupPane(r), `no user is named "labstaf"`) {
		t.Errorf("unmatched username not refused on the frame:\n%s", rollupPane(r))
	}
	r = histType(t, r, "f")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	posts := fake.sent("POST", recordsPath)
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}
	if posts[0].body["performed_by_internal"] != float64(1) || posts[0].body["vendor"] != nil {
		t.Errorf("internal body = %v, want performed_by_internal 1 and vendor null", posts[0].body)
	}
	if screen.phase != histPhaseList || screen.note != "work logged" {
		t.Errorf("after a landed save: phase %d note %q", screen.phase, screen.note)
	}
}

// EDIT IS OFFERED ON A LOGGED ROW AND NOWHERE ELSE, and it PATCHes the notes
// alone. The recording's first row is an outsourced work order.
func TestAssetHistory_EditsOnlyTheNotesOfALoggedRow(t *testing.T) {
	fake := historyFake(t)
	fake.on("PATCH", recordsPath+"3bfd26da-ebd0-416c-81a8-0894a560d333/", 200, "maintenance_record_patch_notes.json")
	r, screen, stop := historyDrive(t, fake, true)
	defer stop()

	if strings.Contains(rollupPane(r), "Ctrl-E") {
		t.Error("the bar names Ctrl-E over an outsourced work order")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if screen.phase != histPhaseList {
		t.Fatal("Ctrl-E acted on a work-order row")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(rollupPane(r), "Ctrl-E") {
		t.Fatalf("the bar does not name Ctrl-E over a logged row:\n%s", rollupPane(r))
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if screen.editNotes.Value() != "Used dial indicator." {
		t.Fatalf("notes box prefilled %q", screen.editNotes.Value())
	}
	r = histType(t, r, " Page 14.")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	patches := fake.sent("PATCH", recordsPath+"3bfd26da-ebd0-416c-81a8-0894a560d333/")
	if len(patches) != 1 || len(patches[0].body) != 1 || patches[0].body["notes"] != "Used dial indicator. Page 14." {
		t.Fatalf("patches = %+v, want one PATCH of {notes} alone", patches)
	}
}

// THE WEB SHOWS NO WRITE CONTROLS TO A NON-STAFF OPERATOR, so neither does this:
// Enter and Ctrl-E are unnamed and inert.
func TestAssetHistory_NoWriteKeyForANonStaffOperator(t *testing.T) {
	fake := historyFake(t)
	r, screen, stop := historyDrive(t, fake, false)
	defer stop()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	pane := rollupPane(r)
	if strings.Contains(pane, "Log work") || strings.Contains(pane, "Edit notes") {
		t.Errorf("non-staff bar names a write:\n%s", pane)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if screen.phase != histPhaseList {
		t.Errorf("a write key opened phase %d for a non-staff operator", screen.phase)
	}
	_ = r
}

// A REFUSED FILTER IS ANSWERED ON THE FILTER FORM in the server's own words —
// brackets and all — and what was typed stays in the box.
func TestAssetHistory_ARefusedFilterKeepsWhatWasTyped(t *testing.T) {
	fake := historyFake(t)
	r, screen, stop := historyDrive(t, fake, true)
	defer stop()
	fake.on("GET", historyPath, 400, "maintenance_history_bad_since.json")

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlF})
	for range "2023-09-14" {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = histType(t, r, "2025-13-01")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != histPhaseFilter || screen.filterInputs[histFilterSince].Value() != "2025-13-01" {
		t.Fatalf("refused filter left phase %d since %q", screen.phase, screen.filterInputs[histFilterSince].Value())
	}
	if !strings.Contains(rollupPane(r), "['since must be a YYYY-MM-DD date']") {
		t.Errorf("the refusal is not the server's sentence:\n%s", rollupPane(r))
	}
	if screen.query.Since != "2023-09-14" {
		t.Errorf("the list's query moved to %q on a refused filter", screen.query.Since)
	}
}
