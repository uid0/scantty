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
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func woRuneKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// loadWO builds a WO-detail screen (woID "wo1") with wo already loaded so a
// test can drive the action key handlers directly.
func loadWO(t *testing.T, deps Deps, wo *omsapi.WorkOrder) *WorkOrderDetailScreen {
	t.Helper()
	s := NewWorkOrderDetailScreen(deps, "wo1")
	next, _ := s.Update(woDetailLoadedMsg{wo: wo})
	return next.(*WorkOrderDetailScreen)
}

// TestWODetailRendersToolsRequired: the tool list renders name ×qty ·
// location_hint with a [REQ] marker, and — the point of the section — it sits
// at the TOP of the body, above Dates, because it is gear to gather before
// starting rather than a record of work done.
func TestWODetailRendersToolsRequired(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{
		ID: "wo1", Title: "Quarterly PM", Description: "spindle service",
		Tools: []omsapi.WorkOrderTool{
			{ID: "tl-1", Name: "Torque wrench", Quantity: 2, LocationHint: "Tool crib, drawer 3", IsRequired: true, Notes: "calibrated"},
			{ID: "tl-2", Name: "Feeler gauge", Quantity: 1},
		},
	})
	out := s.renderBody()

	if !strings.Contains(out, "Tools Required") {
		t.Fatalf("missing Tools Required section: %q", out)
	}
	if !strings.Contains(out, "Torque wrench ×2") || !strings.Contains(out, "Tool crib, drawer 3") {
		t.Errorf("tool line missing qty/hint: %q", out)
	}
	if !strings.Contains(out, "REQ") || !strings.Contains(out, "calibrated") {
		t.Errorf("tool line missing [REQ]/notes: %q", out)
	}
	if !strings.Contains(out, "Feeler gauge ×1") {
		t.Errorf("second tool missing: %q", out)
	}
	tools, dates := strings.Index(out, "Tools Required"), strings.Index(out, "Dates")
	if dates < 0 || tools > dates {
		t.Errorf("Tools Required must render above Dates (tools=%d dates=%d)", tools, dates)
	}
}

// TestWODetailToolsEmptyState: an empty tools list is contract (a WO with no PM
// template returns []), so the section still renders with a muted placeholder
// rather than vanishing.
func TestWODetailToolsEmptyState(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{ID: "wo1", Title: "Ad-hoc fix"})
	out := s.renderBody()
	if !strings.Contains(out, "Tools Required") || !strings.Contains(out, "No tools specified.") {
		t.Errorf("empty tools state wrong: %q", out)
	}
}

func TestWODetailTaskPickerTogglesCompletion(t *testing.T) {
	var captured struct {
		method, path string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		_, _ = w.Write([]byte(`{"id":"tc1","is_completed":true}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	wo := &omsapi.WorkOrder{ID: "wo1", Title: "Lube pump", TaskCompletions: []omsapi.WorkOrderTaskCompletion{
		{ID: "tc1", TaskTitle: "Step 1", IsRequired: true, IsCompleted: false},
	}}
	s := loadWO(t, deps, wo)

	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeTasks {
		t.Fatalf("mode = %v, want tasks", s.mode)
	}
	if !s.WantsRawInput() {
		t.Fatalf("expected raw input while the picker is open")
	}

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeySpace})
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatalf("expected a toggle cmd")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("toggle cmd returned nil msg")
	} else if _, ok := msg.(woTaskToggledMsg); !ok {
		t.Fatalf("msg = %T, want woTaskToggledMsg", msg)
	}

	if captured.method != "PATCH" {
		t.Fatalf("method = %q, want PATCH", captured.method)
	}
	if captured.path != "/api/inventory/work-orders/wo1/tasks/tc1/complete/" {
		t.Fatalf("path = %q", captured.path)
	}
	// Toggle flips the current is_completed (false) to true.
	if captured.body["is_completed"] != true {
		t.Fatalf("is_completed = %v, want true", captured.body["is_completed"])
	}
}

func TestWODetailMaterialPickerTogglesUsage(t *testing.T) {
	var captured struct {
		method, path string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		_, _ = w.Write([]byte(`{"id":"mu1","was_used":false}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	wo := &omsapi.WorkOrder{ID: "wo1", MaterialUsage: []omsapi.WorkOrderMaterialUsage{
		{ID: "mu1", MaterialName: "Grease", WasUsed: true},
	}}
	s := loadWO(t, deps, wo)

	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeMaterials {
		t.Fatalf("mode = %v, want materials", s.mode)
	}

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatalf("expected a toggle cmd")
	}
	if _, ok := cmd().(woMaterialToggledMsg); !ok {
		t.Fatalf("wrong toggle msg type")
	}
	if captured.path != "/api/inventory/work-orders/wo1/materials/mu1/toggle/" {
		t.Fatalf("path = %q", captured.path)
	}
	// Flips the current was_used (true) to false.
	if captured.body["was_used"] != false {
		t.Fatalf("was_used = %v, want false", captured.body["was_used"])
	}
}

func TestWODetailCompleteRequiresConfirm(t *testing.T) {
	var patched bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		patched = true
		_, _ = w.Write([]byte(`{"id":"wo1","status":"completed"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	wo := &omsapi.WorkOrder{ID: "wo1", Title: "X", Status: "in_progress"}
	s := loadWO(t, deps, wo)

	// 'c' opens a confirm overlay rather than firing immediately.
	next, cmd := s.Update(woRuneKey("c"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeConfirm {
		t.Fatalf("mode = %v, want confirm", s.mode)
	}
	if cmd != nil {
		t.Fatalf("confirm should not fire an action yet")
	}

	// 'n' cancels — still no request.
	next, cmd = s.Update(woRuneKey("n"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeView {
		t.Fatalf("mode = %v, want view after cancel", s.mode)
	}
	if cmd != nil {
		t.Fatalf("cancel should not fire an action")
	}
	if patched {
		t.Fatalf("no PATCH should have been sent on cancel")
	}

	// 'c' then 'y' actually transitions.
	next, _ = s.Update(woRuneKey("c"))
	s = next.(*WorkOrderDetailScreen)
	next, cmd = s.Update(woRuneKey("y"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeView {
		t.Fatalf("mode = %v, want view after confirm", s.mode)
	}
	if cmd == nil {
		t.Fatalf("expected transition cmd after y")
	}
	if _, ok := cmd().(woTransitionedMsg); !ok {
		t.Fatalf("wrong transition msg type")
	}
	if !patched {
		t.Fatalf("expected a PATCH after confirming")
	}
}

func TestWODetailValidationGateOpensChecklist(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	wo := &omsapi.WorkOrder{ID: "wo1", Title: "X", Status: "in_progress"}
	s := loadWO(t, deps, wo)

	// A completed-transition that trips the backend 412 gate should reopen
	// the checklist and remember to finalize afterwards.
	next, _ := s.Update(woTransitionedMsg{
		action: "completed",
		err:    &omsapi.APIError{Status: 412, Code: "validation_required", Message: "needs validation"},
	})
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeChecklist {
		t.Fatalf("mode = %v, want checklist after 412", s.mode)
	}
	if !s.finalizeAfterValidate {
		t.Fatalf("expected finalizeAfterValidate to be set")
	}
}

func TestWODetailChecklistSubmit(t *testing.T) {
	var captured struct {
		method, path string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"v1","is_complete":true}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "in_progress"})

	next, _ := s.Update(woRuneKey("v"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeChecklist {
		t.Fatalf("mode = %v, want checklist", s.mode)
	}

	// Submitting with unchecked acks is refused client-side (no request).
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderDetailScreen)
	if cmd != nil {
		t.Fatalf("should not submit with incomplete acknowledgements")
	}
	if s.checklistErr == "" {
		t.Fatalf("expected a validation error message")
	}

	// Check all three: space on each row, moving focus down between them.
	s.Update(tea.KeyMsg{Type: tea.KeySpace}) // electrical (focus 0)
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s.Update(tea.KeyMsg{Type: tea.KeySpace}) // loto (focus 1)
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeySpace}) // required (focus 2)
	s = next.(*WorkOrderDetailScreen)
	if !(s.ackElectrical && s.ackLoto && s.ackRequired) {
		t.Fatalf("acks = %v/%v/%v, want all true", s.ackElectrical, s.ackLoto, s.ackRequired)
	}

	next, cmd = s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatalf("expected a validate cmd once all acks are set")
	}
	if _, ok := cmd().(woValidatedMsg); !ok {
		t.Fatalf("wrong validate msg type")
	}
	if captured.method != "POST" || captured.path != "/api/inventory/work-orders/wo1/validate/" {
		t.Fatalf("unexpected request: %s %s", captured.method, captured.path)
	}
	for _, k := range []string{"electrical_acknowledged", "loto_acknowledged", "required_fields_acknowledged"} {
		if captured.body[k] != true {
			t.Fatalf("%s = %v, want true", k, captured.body[k])
		}
	}
}

func TestWODetailAddPhotoReadsFileAndPosts(t *testing.T) {
	var captured struct {
		path      string
		workOrder string
		filename  string
		fileData  string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		_ = r.ParseMultipartForm(1 << 20)
		captured.workOrder = r.FormValue("work_order")
		if f, hdr, err := r.FormFile("image"); err == nil {
			captured.filename = hdr.Filename
			data, _ := io.ReadAll(f)
			captured.fileData = string(data)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ph1"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	photo := filepath.Join(dir, "shot.jpg")
	if err := os.WriteFile(photo, []byte("PIXELS"), 0o600); err != nil {
		t.Fatalf("write temp photo: %v", err)
	}

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1"})

	next, _ := s.Update(woRuneKey("p"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModePhoto {
		t.Fatalf("mode = %v, want photo", s.mode)
	}
	// Type the file path into the focused path field.
	next, _ = s.Update(woRuneKey(photo))
	s = next.(*WorkOrderDetailScreen)

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatalf("expected an upload cmd")
	}
	if _, ok := cmd().(woPhotoAddedMsg); !ok {
		t.Fatalf("wrong photo msg type")
	}
	if captured.path != "/api/inventory/work-orders/wo1/add_photo/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.workOrder != "wo1" {
		t.Fatalf("work_order = %q", captured.workOrder)
	}
	if captured.filename != "shot.jpg" {
		t.Fatalf("filename = %q, want shot.jpg (basename only)", captured.filename)
	}
	if captured.fileData != "PIXELS" {
		t.Fatalf("file data = %q", captured.fileData)
	}
}

func TestWODetailAddPhotoMissingFileReportsError(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1"})

	next, _ := s.Update(woRuneKey("p"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("/no/such/file.jpg"))
	s = next.(*WorkOrderDetailScreen)

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a cmd even for a missing file (it reports the read error)")
	}
	msg := cmd()
	added, ok := msg.(woPhotoAddedMsg)
	if !ok {
		t.Fatalf("msg = %T, want woPhotoAddedMsg", msg)
	}
	if added.err == nil {
		t.Fatalf("expected a read error for a missing file")
	}
}

func TestWODetailUploadPdfReadsFileAndPosts(t *testing.T) {
	var captured struct {
		path     string
		filename string
		fileData string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		_ = r.ParseMultipartForm(1 << 20)
		if f, hdr, err := r.FormFile("pdf"); err == nil {
			captured.filename = hdr.Filename
			data, _ := io.ReadAll(f)
			captured.fileData = string(data)
		}
		_, _ = w.Write([]byte(`{"submission_id":"s1","status":"applied","work_order_id":"wo1","completed_items":[],"errors":[]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	pdf := filepath.Join(dir, "scan.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatalf("write temp pdf: %v", err)
	}

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1"})

	next, _ := s.Update(woRuneKey("U"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModePdf {
		t.Fatalf("mode = %v, want pdf", s.mode)
	}
	next, _ = s.Update(woRuneKey(pdf))
	s = next.(*WorkOrderDetailScreen)

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatalf("expected an upload cmd")
	}
	msg := cmd()
	up, ok := msg.(woPdfUploadedMsg)
	if !ok {
		t.Fatalf("msg = %T, want woPdfUploadedMsg", msg)
	}
	if up.err != nil {
		t.Fatalf("upload error: %v", up.err)
	}
	// Feeding the result back closes the modal and records a summary.
	next, _ = s.Update(up)
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeView {
		t.Fatalf("mode = %v, want view after upload", s.mode)
	}
	if !strings.Contains(s.actionMsg, "applied") {
		t.Fatalf("actionMsg = %q, want a summary mentioning status", s.actionMsg)
	}
	if captured.path != "/api/inventory/work-orders/upload-pdf/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.filename != "scan.pdf" || captured.fileData != "%PDF-1.7" {
		t.Fatalf("uploaded file = %q / %q", captured.filename, captured.fileData)
	}
}

func TestWODetailEditNotesPatches(t *testing.T) {
	var captured struct {
		method, path string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		_, _ = w.Write([]byte(`{"id":"wo1","notes":"replaced pump seal"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Notes: "old"})

	next, _ := s.Update(woRuneKey("E"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeNotes {
		t.Fatalf("mode = %v, want notes", s.mode)
	}
	// The editor seeds with the current notes.
	if s.notesIn.Value() != "old" {
		t.Fatalf("notes seed = %q, want old", s.notesIn.Value())
	}

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatalf("expected a save cmd")
	}
	msg := cmd()
	saved, ok := msg.(woNotesSavedMsg)
	if !ok {
		t.Fatalf("msg = %T, want woNotesSavedMsg", msg)
	}
	if saved.err != nil {
		t.Fatalf("save error: %v", saved.err)
	}
	// Feeding the result back returns to the read-only view with the fresh WO.
	next, _ = s.Update(saved)
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeView {
		t.Fatalf("mode = %v, want view after save", s.mode)
	}
	if s.wo == nil || s.wo.Notes != "replaced pump seal" {
		t.Fatalf("wo notes = %v, want updated", s.wo)
	}

	if captured.method != "PATCH" || captured.path != "/api/inventory/work-orders/wo1/" {
		t.Fatalf("unexpected request: %s %s", captured.method, captured.path)
	}
	if _, ok := captured.body["notes"]; !ok {
		t.Fatalf("expected notes in PATCH body, got %v", captured.body)
	}
}

func TestWODetailNoTasksKeepsViewMode(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1"}) // no tasks, no materials

	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeView {
		t.Fatalf("mode = %v, want view (no tasks to pick)", s.mode)
	}
	next, _ = s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeView {
		t.Fatalf("mode = %v, want view (no materials to pick)", s.mode)
	}
}

func TestWODetailFooterHintGatesByStatus(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}

	open := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "in_progress",
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{{ID: "t1"}}})
	hint := open.footerHint()
	if !strings.Contains(hint, "c complete") {
		t.Errorf("open WO footer missing complete hint: %q", hint)
	}
	if !strings.Contains(hint, "t tasks") {
		t.Errorf("footer missing task hint when tasks exist: %q", hint)
	}
	// Actions that exist regardless of status.
	for _, want := range []string{"p photo", "U upload-pdf", "v validate", "E notes"} {
		if !strings.Contains(hint, want) {
			t.Errorf("footer missing %q: %q", want, hint)
		}
	}

	done := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "completed"})
	if h := done.footerHint(); strings.Contains(h, "c complete") {
		t.Errorf("completed WO should not offer complete/cancel: %q", h)
	}
}

func TestWODetailLoadErrorDoesNotPanic(t *testing.T) {
	// A failed load leaves wo nil; refreshing the body must not dereference it.
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := NewWorkOrderDetailScreen(deps, "wo1")
	next, _ := s.Update(woDetailLoadedMsg{wo: nil, err: context.DeadlineExceeded})
	s = next.(*WorkOrderDetailScreen)
	if s.loadErr == "" {
		t.Fatalf("expected loadErr to be set")
	}
	// View must render the error screen, not crash.
	if out := s.View(); !strings.Contains(out, "Error") {
		t.Fatalf("expected error view, got %q", out)
	}
}

func TestExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := expandUser("~/pics/a.jpg"); got != filepath.Join(home, "pics/a.jpg") {
		t.Errorf("expandUser(~/pics/a.jpg) = %q", got)
	}
	if got := expandUser("~"); got != home {
		t.Errorf("expandUser(~) = %q, want %q", got, home)
	}
	if got := expandUser("/abs/path"); got != "/abs/path" {
		t.Errorf("absolute path should be unchanged, got %q", got)
	}
	if got := expandUser("relative/path"); got != "relative/path" {
		t.Errorf("relative path should be unchanged, got %q", got)
	}
}

func TestUploadResultSummary(t *testing.T) {
	woID := "wo9"
	r := &omsapi.WorkOrderUploadResult{
		Status:         "applied",
		WorkOrderID:    &woID,
		CompletedItems: []omsapi.WorkOrderUploadCompletedItem{{ID: "t1", TaskTitle: "A"}},
		Errors:         []string{"blurry page 2"},
	}
	got := uploadResultSummary(r)
	for _, want := range []string{"applied", "wo9", "1 task(s) completed", "1 error(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
	if s := uploadResultSummary(nil); s == "" {
		t.Errorf("nil result summary should be non-empty")
	}
}
