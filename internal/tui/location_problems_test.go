package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func lpTestRows() []omsapi.LocationProblem {
	return []omsapi.LocationProblem{
		{ID: "a", Status: omsapi.LocationProblemReported, StatusDisplay: "Reported", Severity: "high", SeverityDisplay: "High", Description: "leak"},
		{ID: "b", Status: omsapi.LocationProblemResolved, StatusDisplay: "Resolved", Severity: "low", Description: "old"},
		{ID: "c", Status: omsapi.LocationProblemClosed, StatusDisplay: "Closed", Severity: "medium", Description: "done"},
		{ID: "d", Status: omsapi.LocationProblemInProgress, StatusDisplay: "In Progress", Severity: "urgent", Description: "wip"},
	}
}

// TestLocationProblems_HandlesKey claims exactly the three colliding action keys.
func TestLocationProblems_HandlesKey(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	for _, k := range []string{"n", "f", "G"} {
		if !s.HandlesKey(k) {
			t.Errorf("should claim %q", k)
		}
	}
	for _, k := range []string{"j", "k", "R", "v", "r", "x", "enter"} {
		if s.HandlesKey(k) {
			t.Errorf("should NOT claim %q", k)
		}
	}
}

// TestLocationProblems_Filter cycles open→resolved→all and checks the visible set.
func TestLocationProblems_Filter(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	s.loading = false
	s.rows = lpTestRows()

	// Default filter is open → reported + in_progress (ids a, d).
	if got := lpIDs(s.visible()); got != "a,d" {
		t.Errorf("open filter visible = %q, want a,d", got)
	}
	// f → resolved (resolved + closed: b, c).
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if s.filter != lpFilterResolved {
		t.Fatalf("filter = %v, want resolved", s.filter)
	}
	if got := lpIDs(s.visible()); got != "b,c" {
		t.Errorf("resolved filter visible = %q, want b,c", got)
	}
	// f → all.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if got := lpIDs(s.visible()); got != "a,b,c,d" {
		t.Errorf("all filter visible = %q, want a,b,c,d", got)
	}
	// f → back to open.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if s.filter != lpFilterOpen {
		t.Errorf("filter should wrap back to open, got %v", s.filter)
	}
	if s.openCount() != 2 {
		t.Errorf("openCount = %d, want 2", s.openCount())
	}
}

// TestLocationProblems_EmptyFilterCursor keeps the cursor at 0 when paging down
// an empty filtered list (no rows match the filter), preserving the invariant.
func TestLocationProblems_EmptyFilterCursor(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	s.loading = false
	// Only resolved/closed rows, but the default filter is "open" → visible empty.
	s.rows = []omsapi.LocationProblem{
		{ID: "b", Status: omsapi.LocationProblemResolved},
		{ID: "c", Status: omsapi.LocationProblemClosed},
	}
	if len(s.visible()) != 0 {
		t.Fatalf("expected empty visible set")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if s.cursor < 0 {
		t.Errorf("pgdown on empty list left cursor = %d, want >= 0", s.cursor)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if s.cursor < 0 {
		t.Errorf("end on empty list left cursor = %d, want >= 0", s.cursor)
	}
	if _, ok := s.selected(); ok {
		t.Errorf("no row should be selectable on an empty list")
	}
}

// TestLocationProblems_ResolveOverlay opens the resolve overlay on an open
// problem, toggles resolved↔closed, and cancels with esc.
func TestLocationProblems_ResolveOverlay(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	s.loading = false
	s.rows = lpTestRows() // default open filter → cursor 0 = id "a"

	if s.WantsRawInput() {
		t.Fatalf("should not want raw input before overlay")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if !s.resolving || !s.WantsRawInput() {
		t.Fatalf("R should open the resolve overlay + raw input")
	}
	if s.resolveTarget != "a" {
		t.Errorf("resolveTarget = %q, want a", s.resolveTarget)
	}
	if s.resolveClosed {
		t.Errorf("should default to resolved (not closed)")
	}
	// tab toggles to closed.
	s.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !s.resolveClosed {
		t.Errorf("tab should toggle to closed")
	}
	// esc cancels.
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.resolving || s.WantsRawInput() {
		t.Errorf("esc should close the overlay")
	}
}

// TestLocationProblems_ResolveGatedOnResolved refuses to open the overlay on an
// already-resolved problem (mirrors the web gating resolve off once terminal).
func TestLocationProblems_ResolveGatedOnResolved(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	s.loading = false
	s.rows = lpTestRows()
	s.filter = lpFilterResolved // cursor 0 = id "b" (resolved)

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if s.resolving {
		t.Errorf("R must not open the overlay on a resolved problem")
	}
}

// TestLocationProblems_ViewDetail toggles the read-only detail overlay.
func TestLocationProblems_ViewDetail(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	s.loading = false
	s.rows = lpTestRows()

	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !s.viewing || !s.WantsRawInput() {
		t.Fatalf("enter should open the detail overlay + raw input")
	}
	out := s.View()
	if !strings.Contains(out, "Description") {
		t.Errorf("detail view missing Description: %q", out)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.viewing {
		t.Errorf("esc should close the detail overlay")
	}
}

// TestLocationProblems_ResolveBody maps the toggle + notes into the payload.
func TestLocationProblems_ResolveBody(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	s.resolveNotes.SetValue("  fixed the leak  ")

	s.resolveClosed = false
	if b := s.resolveBody(); b.Status != omsapi.LocationProblemResolved || b.ResolutionNotes != "fixed the leak" {
		t.Errorf("resolved body = %+v", b)
	}
	s.resolveClosed = true
	if b := s.resolveBody(); b.Status != omsapi.LocationProblemClosed {
		t.Errorf("closed body status = %q", b.Status)
	}
}

// TestLocationProblems_Load exercises the Init→load→decode path against a fake
// server returning the DRF envelope.
func TestLocationProblems_Load(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("location"); got != "42" {
			t.Errorf("location filter = %q, want 42", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":"a","location":42,"status":"reported","severity":"high","description":"leak"}]}`))
	}))
	defer srv.Close()

	s := NewLocationProblemsScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, 42, "Bay 3")
	msg := s.Init()()
	s.Update(msg)
	if s.loading {
		t.Fatalf("still loading after load msg")
	}
	if len(s.rows) != 1 || s.rows[0].ID != "a" {
		t.Fatalf("rows = %+v", s.rows)
	}
}

// TestLocationProblems_RenderSmoke guards the list render path.
func TestLocationProblems_RenderSmoke(t *testing.T) {
	s := NewLocationProblemsScreen(Deps{}, 42, "Bay 3")
	s.loading = false
	s.rows = lpTestRows()
	s.terminalHeight = 30
	out := s.View()
	if !strings.Contains(out, "Bay 3") || !strings.Contains(out, "problems") {
		t.Errorf("list view missing header: %q", out)
	}
	if !strings.Contains(out, "R resolve") {
		t.Errorf("list view missing footer action hints: %q", out)
	}
}

// TestLocationDetail_PKeyOpensProblems guards the wiring: p on the location
// detail opens the problems screen scoped to that location. A regression test
// per the sc-7rbe lesson (a wiring key that a linter/edit could silently drop).
func TestLocationDetail_PKeyOpensProblems(t *testing.T) {
	s := NewLocationDetailScreen(Deps{}, "42")
	s.loading = false
	s.loc = &omsapi.Location{ID: 42, Name: "Bay 3"}

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd == nil {
		t.Fatalf("p should return a switch command")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("expected SwitchScreenMsg")
	}
	lp, ok := sw.Screen.(*LocationProblemsScreen)
	if !ok {
		t.Fatalf("expected *LocationProblemsScreen, got %T", sw.Screen)
	}
	if lp.locID != 42 || lp.locName != "Bay 3" {
		t.Errorf("problems screen scoped wrong: id=%d name=%q", lp.locID, lp.locName)
	}
}

func lpIDs(rows []omsapi.LocationProblem) string {
	ids := make([]string, 0, len(rows))
	for _, p := range rows {
		ids = append(ids, p.ID)
	}
	return strings.Join(ids, ",")
}
