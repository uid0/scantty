package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TUI half of op-syov — the per-step photo pair (OMS #937). The template side
// (PM item form) sets a step's REFERENCE photo; the work-order side shows it
// and lets a tech file EVIDENCE against one specific step. ScanTTY renders no
// images, so both surface as URL / path text.

// ---------------------------------------------------------------------------
// PM template step editor — reference photo
// ---------------------------------------------------------------------------

// TestTaskRowHydratesReferencePhotoURL: an existing step's stored photo arrives
// as a URL and lands in refImageURL — never in refImagePath, which is reserved
// for a local file the operator picks this session (and which is what makes a
// save re-upload).
func TestTaskRowHydratesReferencePhotoURL(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "mi-1")
	s.item = &omsapi.MaintenanceItem{
		ID: "mi-1", Asset: "a1", Title: "PM",
		Tasks: []omsapi.MaintenanceTask{
			{ID: "t1", Order: 0, Title: "Check the belt", ReferenceImageURL: "https://oms/media/belt.jpg"},
			{ID: "t2", Order: 1, Title: "Grease the bearing"},
		},
	}
	s.hydrate()

	if got := s.tasks[0]; got.refImageURL != "https://oms/media/belt.jpg" || got.refImagePath != "" {
		t.Errorf("hydrated step = %+v, want the URL in refImageURL and no local path", got)
	}
	if got := s.tasks[1]; got.refImageURL != "" {
		t.Errorf("photoless step should hydrate blank, got %q", got.refImageURL)
	}
	// The list surfaces the photo as text.
	if line := taskRefPhotoLine(s.tasks[0]).render(0); !strings.Contains(line, "https://oms/media/belt.jpg") {
		t.Errorf("task list line should show the photo URL, got %q", line)
	}
	if line := taskRefPhotoLine(s.tasks[1]).render(0); line != "" {
		t.Errorf("photoless step should contribute no line, got %q", line)
	}
}

// TestTaskEditorSetsReferencePhoto drives the editor's new fourth field: tab
// past the required toggle, type a path, save. The edited row keeps its id and
// its already-uploaded URL while gaining the pending local path.
func TestTaskEditorSetsReferencePhoto(t *testing.T) {
	photo := filepath.Join(t.TempDir(), "belt.jpg")
	if err := os.WriteFile(photo, []byte("PIXELS"), 0o600); err != nil {
		t.Fatalf("write photo fixture: %v", err)
	}

	s := NewMaintenanceItemFormScreen(Deps{}, "mi-1")
	s.tasks = []taskRow{{id: "t1", title: "Check the belt", isRequired: true, refImageURL: "https://oms/media/old.jpg"}}
	s.openTaskEditor(0)

	// title → description → required → reference photo.
	for i := 0; i < 3; i++ {
		s.updateTaskEdit(mtKey("tab"))
	}
	if s.editCursor != 3 {
		t.Fatalf("editCursor = %d, want 3 (reference photo)", s.editCursor)
	}
	s.updateTaskEdit(mtKey(photo))
	if s.teRefImage.Value() != photo {
		t.Fatalf("typing at cursor 3 should reach the photo field, got %q", s.teRefImage.Value())
	}
	// One more tab reaches the remove row an EXISTING step carries (sc-0zvi
	// folded the list's `d` key into the row's own editor), and the one after
	// that wraps back to the title.
	s.updateTaskEdit(mtKey("tab"))
	if s.editCursor != taskEditRemove {
		t.Fatalf("tab past the photo should reach the remove row, got %d", s.editCursor)
	}
	s.updateTaskEdit(mtKey("tab"))
	if s.editCursor != 0 {
		t.Fatalf("tab past the last field should wrap to 0, got %d", s.editCursor)
	}

	s.editCursor = 3
	s.commitTaskEditor()
	if s.editErr != "" {
		t.Fatalf("commit rejected a valid photo path: %s", s.editErr)
	}
	got := s.tasks[0]
	if got.refImagePath != photo {
		t.Errorf("refImagePath = %q, want %q", got.refImagePath, photo)
	}
	if got.refImageURL != "https://oms/media/old.jpg" {
		t.Errorf("the stored photo URL must survive an edit, got %q", got.refImageURL)
	}
	if got.id != "t1" || !got.dirty {
		t.Errorf("edited row = %+v, want id kept and dirty set", got)
	}
	if s.phase != mFormPhaseTaskList {
		t.Errorf("commit should return to the task list, phase = %v", s.phase)
	}
	// A pending upload reads differently from one already on the server.
	if line := taskRefPhotoLine(got).render(0); !strings.Contains(line, photo) || !strings.Contains(line, "uploads on save") {
		t.Errorf("pending-photo line = %q", line)
	}
}

// TestTaskEditorRejectsBadReferencePhotoPath: the path is validated at commit,
// not at save — by save time the item write has already gone out and only the
// step reconcile would fail, which is a confusing place to learn the file was
// a typo.
func TestTaskEditorRejectsBadReferencePhotoPath(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"relative", "belt.jpg"},
		{"missing", filepath.Join(t.TempDir(), "nope.jpg")},
		{"directory", t.TempDir()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMaintenanceItemFormScreen(Deps{}, "mi-1")
			s.openTaskEditor(-1)
			s.teTitle.SetValue("Check the belt")
			s.teRefImage.SetValue(tc.path)
			s.commitTaskEditor()

			if s.editErr == "" {
				t.Fatalf("expected a validation error for %q", tc.path)
			}
			if len(s.tasks) != 0 {
				t.Errorf("no row should be added when the photo path is bad: %+v", s.tasks)
			}
			if s.phase == mFormPhaseTaskList {
				t.Errorf("a rejected commit must stay in the editor")
			}
		})
	}
}

// stepPhotoRecorder records each request's method, path and (for multipart)
// the reference_image part, so a reconcile test can prove which rows uploaded.
type stepPhotoRecorder struct {
	mu   sync.Mutex
	reqs []stepPhotoReq
}

type stepPhotoReq struct {
	method    string
	path      string
	multipart bool
	fileName  string
	fileBody  string
	fields    map[string][]string
}

func (r *stepPhotoRecorder) find(method, path string) (stepPhotoReq, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range r.reqs {
		if q.method == method && q.path == path {
			return q, true
		}
	}
	return stepPhotoReq{}, false
}

func newStepPhotoRecorder(t *testing.T) (*stepPhotoRecorder, *omsapi.Client) {
	t.Helper()
	rec := &stepPhotoRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := stepPhotoReq{method: r.Method, path: r.URL.Path}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			q.multipart = true
			if err := r.ParseMultipartForm(1 << 20); err == nil {
				q.fields = r.MultipartForm.Value
				if f, hdr, ferr := r.FormFile("reference_image"); ferr == nil {
					defer f.Close()
					raw, _ := io.ReadAll(f)
					q.fileName = hdr.Filename
					q.fileBody = string(raw)
				}
			}
		}
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, q)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	t.Cleanup(srv.Close)
	return rec, omsapi.New(srv.URL)
}

// TestReconcileTasksUploadsReferencePhoto: only the row carrying a picked photo
// goes out as multipart. The other edited row stays JSON — which is precisely
// what keeps its existing photo, since a PATCH without the key can't clear it.
func TestReconcileTasksUploadsReferencePhoto(t *testing.T) {
	photo := filepath.Join(t.TempDir(), "belt.jpg")
	if err := os.WriteFile(photo, []byte("PIXELS"), 0o600); err != nil {
		t.Fatalf("write photo fixture: %v", err)
	}

	rec, c := newStepPhotoRecorder(t)
	rows := []taskRow{
		{id: "t-photo", title: "Check the belt", isRequired: true, dirty: true, refImagePath: photo},
		{id: "t-plain", title: "Grease the bearing", loadedOrder: 1, dirty: true},
		{id: "", title: "New step"},
	}
	if err := reconcileTasks(context.Background(), c, "mi-1", rows, []string{"t-photo", "t-plain"}); err != nil {
		t.Fatalf("reconcileTasks: %v", err)
	}

	withPhoto, ok := rec.find(http.MethodPatch, "/api/inventory/maintenance-tasks/t-photo/")
	if !ok {
		t.Fatalf("expected a PATCH for the row with a photo")
	}
	if !withPhoto.multipart {
		t.Errorf("a picked photo must switch the write to multipart")
	}
	if withPhoto.fileName != "belt.jpg" || withPhoto.fileBody != "PIXELS" {
		t.Errorf("uploaded file = %q %q", withPhoto.fileName, withPhoto.fileBody)
	}
	// Order rides the upload too, or a reordered-and-rephotographed step would
	// lose its position.
	if got := withPhoto.fields["order"]; len(got) != 1 || got[0] != "0" {
		t.Errorf("order field on the multipart write = %v", got)
	}

	plain, ok := rec.find(http.MethodPatch, "/api/inventory/maintenance-tasks/t-plain/")
	if !ok {
		t.Fatalf("expected a PATCH for the edited row without a photo")
	}
	if plain.multipart {
		t.Errorf("a row with no picked photo must stay JSON, or it would re-upload nothing over the stored image")
	}
}

// ---------------------------------------------------------------------------
// Work-order detail — reference display + per-step evidence
// ---------------------------------------------------------------------------

// woWithStepPhotos is a two-step work order: one step with a reference photo
// and two evidence shots, one bare.
func woWithStepPhotos() *omsapi.WorkOrder {
	return &omsapi.WorkOrder{
		ID: "wo1", Title: "Quarterly PM",
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{
			{
				ID: "tc-1", TaskTitle: "Check the belt", IsRequired: true,
				TaskReferenceImageURL: "https://oms/media/ref/belt.jpg",
				EvidencePhotos: []omsapi.WorkOrderPhoto{
					{ID: "ph-1", ImageURL: "https://oms/media/wo/after1.jpg", Caption: "belt replaced", UploadedBy: "Sam Tech"},
					{ID: "ph-2", ImageURL: "https://oms/media/wo/after2.jpg"},
				},
			},
			{ID: "tc-2", TaskTitle: "Grease the bearing"},
		},
	}
}

// TestWODetailRendersStepReferenceAndEvidence: the body shows each step's
// reference photo URL and its evidence gallery as text, and a step with
// neither adds no noise.
func TestWODetailRendersStepReferenceAndEvidence(t *testing.T) {
	s := loadWO(t, Deps{}, woWithStepPhotos())
	out := s.renderBody()

	if !strings.Contains(out, "Reference photo: ") || !strings.Contains(out, "https://oms/media/ref/belt.jpg") {
		t.Errorf("missing the step's reference photo: %q", out)
	}
	if !strings.Contains(out, "Evidence (2)") {
		t.Errorf("missing the evidence count: %q", out)
	}
	// A captioned photo reads by caption; an uncaptioned one falls back to URL.
	if !strings.Contains(out, "belt replaced") || !strings.Contains(out, "by Sam Tech") {
		t.Errorf("evidence caption/uploader missing: %q", out)
	}
	if !strings.Contains(out, "https://oms/media/wo/after2.jpg") {
		t.Errorf("uncaptioned evidence should fall back to its URL: %q", out)
	}
	// The bare step contributes no photo lines at all.
	bare := strings.Index(out, "Grease the bearing")
	if bare >= 0 && strings.Contains(out[bare:], "Reference photo") {
		t.Errorf("a step with no photos should render none: %q", out[bare:])
	}
}

// TestWODetailMarksWOLevelPhotosPinnedToSteps: the work order's own photo list
// carries every photo, evidence included, so the pinned ones are flagged —
// otherwise they look like duplicate work-order-level shots.
func TestWODetailMarksWOLevelPhotosPinnedToSteps(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{
		ID: "wo1", Title: "Quarterly PM",
		Photos: []omsapi.WorkOrderPhoto{
			{ID: "ph-1", Caption: "belt replaced", TaskCompletion: "tc-1"},
			{ID: "ph-9", Caption: "overview shot"},
		},
	})
	out := s.renderBody()

	pinned := strings.Index(out, "belt replaced")
	loose := strings.Index(out, "overview shot")
	if pinned < 0 || loose < 0 {
		t.Fatalf("photos section missing rows: %q", out)
	}
	if !strings.Contains(out[pinned:loose], "step evidence") {
		t.Errorf("the pinned photo should be flagged as step evidence: %q", out[pinned:loose])
	}
	if strings.Contains(out[loose:], "step evidence") {
		t.Errorf("a work-order-level photo must not be flagged: %q", out[loose:])
	}
}

// TestWODetailTaskPickerFilesEvidenceUnderStep is the acceptance path: from the
// task picker, p uploads a photo filed under the HIGHLIGHTED step — the POST
// carries task_completion AND the still-required work_order — and the operator
// lands back on the step list rather than being dumped to the body.
func TestWODetailTaskPickerFilesEvidenceUnderStep(t *testing.T) {
	var captured struct {
		path           string
		workOrder      string
		taskCompletion string
		filename       string
		fileData       string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		_ = r.ParseMultipartForm(1 << 20)
		captured.workOrder = r.FormValue("work_order")
		captured.taskCompletion = r.FormValue("task_completion")
		if f, hdr, err := r.FormFile("image"); err == nil {
			captured.filename = hdr.Filename
			data, _ := io.ReadAll(f)
			captured.fileData = string(data)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ph1","task_completion":"tc-2"}`))
	}))
	defer srv.Close()

	photo := filepath.Join(t.TempDir(), "after.jpg")
	if err := os.WriteFile(photo, []byte("PIXELS"), 0o600); err != nil {
		t.Fatalf("write photo fixture: %v", err)
	}

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, woWithStepPhotos())

	// Open the task picker and move to the SECOND step, so the test proves the
	// highlighted step is used and not just the first one.
	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeTasks {
		t.Fatalf("mode = %v, want tasks", s.mode)
	}
	next, _ = s.Update(woRuneKey("j"))
	s = next.(*WorkOrderDetailScreen)

	// The picker itself surfaces the reference + evidence lines.
	if picker := s.renderTaskPicker(); !strings.Contains(picker, "ref: ") || !strings.Contains(picker, "evidence: belt replaced") {
		t.Errorf("task picker should show both photo halves: %q", picker)
	}

	next, _ = s.Update(woRuneKey("p"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModePhoto {
		t.Fatalf("mode = %v, want photo", s.mode)
	}
	if s.photoTaskID != "tc-2" {
		t.Fatalf("photoTaskID = %q, want the highlighted step tc-2", s.photoTaskID)
	}
	if form := s.renderPhotoForm(); !strings.Contains(form, "Add evidence photo") || !strings.Contains(form, "Grease the bearing") {
		t.Errorf("the form should name the step it files under: %q", form)
	}

	next, _ = s.Update(woRuneKey(photo))
	s = next.(*WorkOrderDetailScreen)
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatalf("expected an upload cmd")
	}
	msg, ok := cmd().(woPhotoAddedMsg)
	if !ok {
		t.Fatalf("wrong photo msg type")
	}
	if msg.err != nil {
		t.Fatalf("upload failed: %v", msg.err)
	}

	if captured.path != "/api/inventory/work-orders/wo1/add_photo/" {
		t.Errorf("path = %q", captured.path)
	}
	if captured.taskCompletion != "tc-2" {
		t.Errorf("task_completion = %q, want tc-2", captured.taskCompletion)
	}
	if captured.workOrder != "wo1" {
		t.Errorf("work_order must still ride alongside task_completion, got %q", captured.workOrder)
	}
	if captured.filename != "after.jpg" || captured.fileData != "PIXELS" {
		t.Errorf("uploaded file = %q %q", captured.filename, captured.fileData)
	}

	next, _ = s.Update(msg)
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeTasks {
		t.Errorf("after filing evidence the operator should land back on the step list, mode = %v", s.mode)
	}
}

// TestWODetailWOLevelPhotoStaysUnpinned: p from the body is still the plain
// work-order upload — no task_completion — which is what every caller did
// before per-step evidence existed.
func TestWODetailWOLevelPhotoStaysUnpinned(t *testing.T) {
	var hasTaskCompletion bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		_, hasTaskCompletion = r.MultipartForm.Value["task_completion"]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ph1"}`))
	}))
	defer srv.Close()

	photo := filepath.Join(t.TempDir(), "overview.jpg")
	if err := os.WriteFile(photo, []byte("PIXELS"), 0o600); err != nil {
		t.Fatalf("write photo fixture: %v", err)
	}

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, woWithStepPhotos())

	next, _ := s.Update(woRuneKey("p"))
	s = next.(*WorkOrderDetailScreen)
	if s.photoTaskID != "" {
		t.Fatalf("a body-level p must not pin to a step, got %q", s.photoTaskID)
	}
	if form := s.renderPhotoForm(); strings.Contains(form, "Filed under") {
		t.Errorf("the work-order form should not claim a step: %q", form)
	}
	next, _ = s.Update(woRuneKey(photo))
	s = next.(*WorkOrderDetailScreen)
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected an upload cmd")
	}
	if msg, _ := cmd().(woPhotoAddedMsg); msg.err != nil {
		t.Fatalf("upload failed: %v", msg.err)
	}
	if hasTaskCompletion {
		t.Errorf("task_completion must be omitted for a work-order-level photo")
	}
}
