package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// loadProjectStorageDetail builds a project-storage detail screen with the
// stint already loaded so a test can drive the re-print key handlers directly.
func loadProjectStorageDetail(t *testing.T, deps Deps, st *omsapi.ProjectStorageStint) *ProjectStorageDetailScreen {
	t.Helper()
	s := NewProjectStorageDetailScreen(deps, st.StintID)
	next, _ := s.Update(projectStorageDetailLoadedMsg{stint: st})
	return next.(*ProjectStorageDetailScreen)
}

// TestReprintClaimTicket_ConfirmAndPost drives the full re-print flow:
// 'p' opens the y/n confirm (grabbing raw input), 'y' fires the async post,
// and the returned cmd hits POST .../reprint/ with the audit note. The
// success message then re-fetches so the new event lands in the timeline.
func TestReprintClaimTicket_ConfirmAndPost(t *testing.T) {
	var captured struct {
		method, path, note string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reprint/") {
			captured.method = r.Method
			captured.path = r.URL.Path
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			captured.note = body["note"]
			w.WriteHeader(http.StatusOK)
			return
		}
		// The success path re-fetches the stint; answer that GET.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stint_id":"PS-AB23CDFG","username":"alice","status":"active"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	st := &omsapi.ProjectStorageStint{StintID: "PS-AB23CDFG", Username: "alice", Status: "active"}
	s := loadProjectStorageDetail(t, deps, st)

	// 'p' raises the confirm and grabs raw input so y/n/esc reach the screen.
	next, _ := s.Update(runeKey('p'))
	s = next.(*ProjectStorageDetailScreen)
	if !s.confirmingReprint {
		t.Fatalf("after 'p', confirmingReprint = false, want true")
	}
	if !s.WantsRawInput() {
		t.Errorf("expected raw input while the reprint confirm is open")
	}

	// 'y' fires the async reprint; run the returned cmd and confirm the wire.
	next, cmd := s.Update(runeKey('y'))
	s = next.(*ProjectStorageDetailScreen)
	if !s.reprinting {
		t.Fatalf("after 'y', reprinting = false, want true")
	}
	if cmd == nil {
		t.Fatalf("expected a reprint cmd")
	}
	msg := cmd()
	done, ok := msg.(projectStorageReprintedMsg)
	if !ok {
		t.Fatalf("msg = %T, want projectStorageReprintedMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("reprint failed: %v", done.err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/project-storage/stints/PS-AB23CDFG/reprint/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.note == "" {
		t.Errorf("expected an audit note on the reprint request, got empty")
	}

	// Feeding the success msg back clears the confirm and re-fetches (loading).
	next, cmd = s.Update(done)
	s = next.(*ProjectStorageDetailScreen)
	if s.confirmingReprint || s.reprinting {
		t.Errorf("confirm/reprinting not cleared after success: %+v %+v", s.confirmingReprint, s.reprinting)
	}
	if !s.loading {
		t.Errorf("expected a re-fetch (loading=true) after a successful reprint")
	}
	if cmd == nil {
		t.Errorf("expected a status+refetch batch cmd after success")
	}
}

// TestReprintClaimTicket_Cancel confirms n/esc backs out without posting.
func TestReprintClaimTicket_Cancel(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused.example"), Ctx: context.Background()}
	st := &omsapi.ProjectStorageStint{StintID: "PS-AB23CDFG", Username: "bob"}
	s := loadProjectStorageDetail(t, deps, st)

	next, _ := s.Update(runeKey('p'))
	s = next.(*ProjectStorageDetailScreen)
	if !s.confirmingReprint {
		t.Fatalf("after 'p', confirmingReprint = false, want true")
	}
	next, cmd := s.Update(runeKey('n'))
	s = next.(*ProjectStorageDetailScreen)
	if s.confirmingReprint {
		t.Errorf("'n' should cancel the reprint confirm")
	}
	if cmd != nil {
		t.Errorf("cancel should not fire a cmd, got %T", cmd())
	}
}

// TestReprintClaimTicket_ErrorSurfaces confirms a failed reprint (e.g. the
// backend endpoint is not implemented yet) surfaces as an error status
// rather than a false "queued" success.
func TestReprintClaimTicket_ErrorSurfaces(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused.example"), Ctx: context.Background()}
	st := &omsapi.ProjectStorageStint{StintID: "PS-AB23CDFG"}
	s := loadProjectStorageDetail(t, deps, st)
	s.confirmingReprint = true
	s.reprinting = true

	next, cmd := s.Update(projectStorageReprintedMsg{err: context.DeadlineExceeded})
	s = next.(*ProjectStorageDetailScreen)
	if s.confirmingReprint || s.reprinting || s.loading {
		t.Errorf("error path should clear confirm/reprinting and not enter loading")
	}
	if cmd == nil {
		t.Fatalf("expected a status cmd on reprint error")
	}
	sm, ok := cmd().(StatusMsg)
	if !ok {
		t.Fatalf("msg = %T, want StatusMsg", cmd())
	}
	if sm.Level != StatusError || !strings.Contains(sm.Text, "reprint failed") {
		t.Errorf("status = %+v, want StatusError containing 'reprint failed'", sm)
	}
}

// TestMarkRemoved_ConfirmAndPost drives the full mark-removed flow: 'x' opens
// the note prompt (grabbing raw input), enter fires the async post to
// .../mark-removed/ carrying the audit note, and the success re-fetches so the
// new "removed" event lands in the timeline.
func TestMarkRemoved_ConfirmAndPost(t *testing.T) {
	var captured struct {
		method, path, note string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/mark-removed/") {
			captured.method = r.Method
			captured.path = r.URL.Path
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			captured.note = body["note"]
			w.WriteHeader(http.StatusOK)
			return
		}
		// The success path re-fetches; answer that GET (now removed).
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stint_id":"PS-AB23CDFG","username":"alice","status":"removed"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	st := &omsapi.ProjectStorageStint{StintID: "PS-AB23CDFG", Username: "alice", Status: "active"}
	s := loadProjectStorageDetail(t, deps, st)

	// 'x' raises the note prompt and grabs raw input.
	next, _ := s.Update(runeKey('x'))
	s = next.(*ProjectStorageDetailScreen)
	if !s.confirmingRemove {
		t.Fatalf("after 'x', confirmingRemove = false, want true")
	}
	if !s.WantsRawInput() {
		t.Errorf("expected raw input while the remove prompt is open")
	}

	// Type an audit note, then enter to confirm.
	s.removeNote.SetValue("left the space")
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*ProjectStorageDetailScreen)
	if !s.removing {
		t.Fatalf("after enter, removing = false, want true")
	}
	if cmd == nil {
		t.Fatalf("expected a mark-removed cmd")
	}
	msg := cmd()
	done, ok := msg.(projectStorageRemovedMsg)
	if !ok {
		t.Fatalf("msg = %T, want projectStorageRemovedMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("mark-removed failed: %v", done.err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/project-storage/stints/PS-AB23CDFG/mark-removed/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.note != "left the space" {
		t.Errorf("note = %q, want %q", captured.note, "left the space")
	}

	// Feeding the success msg back clears the confirm and re-fetches.
	next, cmd = s.Update(done)
	s = next.(*ProjectStorageDetailScreen)
	if s.confirmingRemove || s.removing {
		t.Errorf("confirm/removing not cleared after success")
	}
	if !s.loading {
		t.Errorf("expected a re-fetch (loading=true) after a successful removal")
	}
	if cmd == nil {
		t.Errorf("expected a status+refetch batch cmd after success")
	}
}

// TestMarkRemoved_AlreadyRemovedGuard confirms 'x' on an already-removed stint
// shows a clear message instead of opening the prompt / firing a doomed request.
func TestMarkRemoved_AlreadyRemovedGuard(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused.example"), Ctx: context.Background()}
	st := &omsapi.ProjectStorageStint{StintID: "PS-AB23CDFG", Status: "removed"}
	s := loadProjectStorageDetail(t, deps, st)

	next, cmd := s.Update(runeKey('x'))
	s = next.(*ProjectStorageDetailScreen)
	if s.confirmingRemove {
		t.Errorf("'x' on a removed stint should not open the remove prompt")
	}
	if cmd == nil {
		t.Fatalf("expected a status cmd for the already-removed guard")
	}
	sm, ok := cmd().(StatusMsg)
	if !ok {
		t.Fatalf("msg = %T, want StatusMsg", cmd())
	}
	if !strings.Contains(sm.Text, "already removed") {
		t.Errorf("status = %+v, want 'already removed'", sm)
	}
}

// TestMarkRemoved_Cancel confirms esc backs out of the note prompt without
// posting.
func TestMarkRemoved_Cancel(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused.example"), Ctx: context.Background()}
	st := &omsapi.ProjectStorageStint{StintID: "PS-AB23CDFG", Status: "active"}
	s := loadProjectStorageDetail(t, deps, st)

	next, _ := s.Update(runeKey('x'))
	s = next.(*ProjectStorageDetailScreen)
	if !s.confirmingRemove {
		t.Fatalf("after 'x', confirmingRemove = false, want true")
	}
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	s = next.(*ProjectStorageDetailScreen)
	if s.confirmingRemove {
		t.Errorf("esc should cancel the remove prompt")
	}
	if cmd != nil {
		t.Errorf("cancel should not fire a cmd, got %T", cmd())
	}
}
