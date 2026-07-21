package omsapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Contract tests for op-syov — the per-step photo pair (OMS #937):
//
//   - reference: an instructional photo set once on the TEMPLATE step
//     (MaintenanceTask.reference_image, file in / reference_image_url out).
//   - evidence: photos a tech pins to one step of a work order
//     (WorkOrderPhoto.task_completion, surfaced as the step's evidence_photos).
//
// The key names below are a pinned backend contract — ScanTTY decodes these
// exact strings, so drift here is a silent empty screen, not an error.

// multipartCapture records the parsed multipart body of one request.
type multipartCapture struct {
	method   string
	path     string
	ctype    string
	fields   map[string][]string
	fileName string
	fileBody string
}

// multipartServer serves one request, parses it as multipart/form-data, and
// records the named file part alongside every scalar field.
func multipartServer(t *testing.T, status int, resp, fileField string, into *multipartCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		into.method = r.Method
		into.path = r.URL.Path
		into.ctype = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		} else {
			into.fields = r.MultipartForm.Value
			if f, hdr, err := r.FormFile(fileField); err == nil {
				defer f.Close()
				raw, _ := io.ReadAll(f)
				into.fileName = hdr.Filename
				into.fileBody = string(raw)
			} else {
				t.Errorf("FormFile(%s): %v", fileField, err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
}

// TestMaintenanceTask_ReferenceImageDecodes pins the READ half of the template
// step's photo. reference_image itself is write-only on the serializer, so
// reference_image_url is the only key that ever comes back — and it is null
// for a step with no photo, which must decode to "" and not blow up.
func TestMaintenanceTask_ReferenceImageDecodes(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"mi-1","asset":"a1","title":"PM",
		"tasks":[
			{"id":"tk-1","maintenance_item":"mi-1","order":0,"title":"Check the belt",
			 "description":"","is_required":true,
			 "reference_image_url":"https://oms.example.com/media/maintenance_task_reference/2026/07/belt.jpg"},
			{"id":"tk-2","maintenance_item":"mi-1","order":1,"title":"Grease the bearing",
			 "is_required":false,"reference_image_url":null}
		]}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.GetMaintenanceItem(context.Background(), "mi-1")
	if err != nil {
		t.Fatalf("GetMaintenanceItem: %v", err)
	}
	if len(item.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(item.Tasks))
	}
	want := "https://oms.example.com/media/maintenance_task_reference/2026/07/belt.jpg"
	if item.Tasks[0].ReferenceImageURL != want {
		t.Errorf("tasks[0].reference_image_url = %q, want %q", item.Tasks[0].ReferenceImageURL, want)
	}
	if item.Tasks[1].ReferenceImageURL != "" {
		t.Errorf("a null reference_image_url must decode to empty, got %q", item.Tasks[1].ReferenceImageURL)
	}
}

// TestCreateMaintenanceTask_ReferenceImageMultipart asserts that attaching a
// local photo switches the step create to multipart/form-data, uploads the file
// under the serializer's reference_image field, and still carries every scalar
// — order and is_required as the string forms DRF parses out of form data.
func TestCreateMaintenanceTask_ReferenceImageMultipart(t *testing.T) {
	photo := t.TempDir() + "/belt.jpg"
	if err := os.WriteFile(photo, []byte("JPEGDATA"), 0o600); err != nil {
		t.Fatalf("write photo fixture: %v", err)
	}

	var cap multipartCapture
	srv := multipartServer(t, http.StatusCreated,
		`{"id":"tk-1","title":"Check the belt","reference_image_url":"https://oms/media/belt.jpg"}`,
		"reference_image", &cap)
	defer srv.Close()

	c := New(srv.URL)
	task, err := c.CreateMaintenanceTask(context.Background(), MaintenanceTaskWrite{
		MaintenanceItem:    "mi-1",
		Order:              2,
		Title:              "Check the belt",
		Description:        "look for cracks",
		IsRequired:         true,
		ReferenceImagePath: photo,
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceTask: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/maintenance-tasks/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if !strings.HasPrefix(cap.ctype, "multipart/form-data") {
		t.Fatalf("content-type = %q, want multipart/form-data", cap.ctype)
	}
	if cap.fileName != "belt.jpg" || cap.fileBody != "JPEGDATA" {
		t.Errorf("uploaded file = %q %q", cap.fileName, cap.fileBody)
	}
	for key, want := range map[string]string{
		"maintenance_item": "mi-1",
		"order":            "2",
		"title":            "Check the belt",
		"description":      "look for cracks",
		"is_required":      "true",
	} {
		got := cap.fields[key]
		if len(got) != 1 || got[0] != want {
			t.Errorf("field %s = %v, want [%q]", key, got, want)
		}
	}
	if task == nil || task.ReferenceImageURL != "https://oms/media/belt.jpg" {
		t.Errorf("decoded task = %+v", task)
	}
}

// TestUpdateMaintenanceTask_ReferenceImageMultipart is the edit half: a PATCH
// (not PUT) to the id-scoped path, still multipart, and is_required=false
// reaching the wire as "false" rather than being dropped.
func TestUpdateMaintenanceTask_ReferenceImageMultipart(t *testing.T) {
	photo := t.TempDir() + "/after.png"
	if err := os.WriteFile(photo, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatalf("write photo fixture: %v", err)
	}

	var cap multipartCapture
	srv := multipartServer(t, http.StatusOK, `{"id":"tk-1","title":"Check the belt"}`,
		"reference_image", &cap)
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdateMaintenanceTask(context.Background(), "tk-1", MaintenanceTaskWrite{
		MaintenanceItem:    "mi-1",
		Order:              0,
		Title:              "Check the belt",
		IsRequired:         false,
		ReferenceImagePath: photo,
	}); err != nil {
		t.Fatalf("UpdateMaintenanceTask: %v", err)
	}
	if cap.method != http.MethodPatch || cap.path != "/api/inventory/maintenance-tasks/tk-1/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.fileName != "after.png" || cap.fileBody != "PNGDATA" {
		t.Errorf("uploaded file = %q %q", cap.fileName, cap.fileBody)
	}
	if got := cap.fields["is_required"]; len(got) != 1 || got[0] != "false" {
		t.Errorf("is_required = %v, want [\"false\"]", got)
	}
}

// TestMaintenanceTask_NoPhotoPathStaysJSON is the other half of the multipart
// switch and the reason an untouched step never loses its photo: with no local
// path the write is plain JSON and carries NO reference_image key at all, so a
// PATCH leaves whatever the server already stored alone.
func TestMaintenanceTask_NoPhotoPathStaysJSON(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"id":"tk-1","title":"Check the belt"}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdateMaintenanceTask(context.Background(), "tk-1", MaintenanceTaskWrite{
		MaintenanceItem: "mi-1", Order: 1, Title: "Check the belt", IsRequired: true,
	}); err != nil {
		t.Fatalf("UpdateMaintenanceTask: %v", err)
	}
	if cap.method != http.MethodPatch {
		t.Fatalf("method = %q", cap.method)
	}
	if _, ok := cap.body["reference_image"]; ok {
		t.Errorf("reference_image must not be sent when no photo was picked, got %v", cap.body["reference_image"])
	}
	if _, ok := cap.body["ReferenceImagePath"]; ok {
		t.Errorf("the local path must never be serialized, body = %v", cap.body)
	}
	if cap.body["order"] != float64(1) || cap.body["title"] != "Check the belt" {
		t.Errorf("scalar fields lost on the JSON path: %v", cap.body)
	}
}

// TestWorkOrder_StepPhotosDecode pins the work-order read shape: each step
// carries the template's task_reference_image_url plus its own evidence_photos
// gallery. All three states have to survive — populated, an explicit empty
// array (the normal case, not an error), and null/absent.
func TestWorkOrder_StepPhotosDecode(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"wo-1","title":"Quarterly PM","status":"in_progress",
		"task_completions":[
			{"id":"tc-1","task":"tk-1","task_title":"Check the belt","task_order":0,
			 "is_required":true,"is_completed":true,
			 "task_reference_image_url":"https://oms/media/ref/belt.jpg",
			 "evidence_photos":[
				{"id":"ph-1","image_url":"https://oms/media/wo/after1.jpg","caption":"belt replaced",
				 "uploaded_at":"2026-07-20T10:00:00Z","uploaded_by_name":"Sam Tech"},
				{"id":"ph-2","image_url":"https://oms/media/wo/after2.jpg","caption":"",
				 "uploaded_at":"2026-07-20T10:05:00Z","uploaded_by_name":null}
			 ]},
			{"id":"tc-2","task":"tk-2","task_title":"Grease the bearing","task_order":1,
			 "is_required":false,"is_completed":false,
			 "task_reference_image_url":null,"evidence_photos":[]},
			{"id":"tc-3","task":null,"task_title":"Orphaned step","task_order":2}
		]}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	wo, err := c.GetWorkOrder(context.Background(), "wo-1")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if len(wo.TaskCompletions) != 3 {
		t.Fatalf("task_completions = %d, want 3", len(wo.TaskCompletions))
	}

	first := wo.TaskCompletions[0]
	if first.TaskReferenceImageURL != "https://oms/media/ref/belt.jpg" {
		t.Errorf("task_reference_image_url = %q", first.TaskReferenceImageURL)
	}
	if len(first.EvidencePhotos) != 2 {
		t.Fatalf("evidence_photos = %d, want 2", len(first.EvidencePhotos))
	}
	// The nested projection is exactly id / image_url / caption / uploaded_at /
	// uploaded_by_name — the five keys WorkOrderPhoto already models.
	p := first.EvidencePhotos[0]
	if p.ID != "ph-1" || p.ImageURL != "https://oms/media/wo/after1.jpg" || p.Caption != "belt replaced" {
		t.Errorf("evidence_photos[0] = %+v", p)
	}
	if p.UploadedBy != "Sam Tech" {
		t.Errorf("uploaded_by_name = %q", p.UploadedBy)
	}
	if p.UploadedAt.IsZero() {
		t.Errorf("uploaded_at did not decode: %+v", p)
	}
	if second := first.EvidencePhotos[1]; second.UploadedBy != "" {
		t.Errorf("a null uploaded_by_name must decode to empty, got %q", second.UploadedBy)
	}

	// An empty gallery + a null reference are the ordinary state of an
	// untouched step, not a decode failure.
	if got := wo.TaskCompletions[1]; got.TaskReferenceImageURL != "" || len(got.EvidencePhotos) != 0 {
		t.Errorf("step with no photos = %+v", got)
	}
	// task is nullable (the template step can be deleted after the WO is cut),
	// and then the backend has no reference photo to report.
	if got := wo.TaskCompletions[2]; got.TaskReferenceImageURL != "" || len(got.EvidencePhotos) != 0 {
		t.Errorf("orphaned step = %+v", got)
	}
}

// TestWorkOrderPhoto_TaskCompletionDecodes covers the top-level photos list,
// which carries every photo — pinned or not. task_completion is what separates
// a step's evidence from a work-order-level shot, and it is null for the
// latter (every photo taken before per-step evidence existed).
func TestWorkOrderPhoto_TaskCompletionDecodes(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"wo-1","title":"Quarterly PM",
		"photos":[
			{"id":"ph-1","work_order":"wo-1","task_completion":"tc-1",
			 "image_url":"https://oms/media/wo/after1.jpg","caption":"belt replaced",
			 "uploaded_at":"2026-07-20T10:00:00Z","uploaded_by_name":"Sam Tech"},
			{"id":"ph-9","work_order":"wo-1","task_completion":null,
			 "image_url":"https://oms/media/wo/overview.jpg","caption":"overview",
			 "uploaded_at":"2026-07-19T09:00:00Z","uploaded_by_name":"Sam Tech"}
		]}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	wo, err := c.GetWorkOrder(context.Background(), "wo-1")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if len(wo.Photos) != 2 {
		t.Fatalf("photos = %d, want 2", len(wo.Photos))
	}
	if wo.Photos[0].TaskCompletion != "tc-1" {
		t.Errorf("pinned photo task_completion = %v, want tc-1", wo.Photos[0].TaskCompletion)
	}
	if wo.Photos[1].TaskCompletion != nil {
		t.Errorf("work-order-level photo task_completion = %v, want nil", wo.Photos[1].TaskCompletion)
	}
}
