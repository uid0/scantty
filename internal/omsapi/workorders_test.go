package omsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetWorkOrder_ParsesTools pins the LEAN tools projection the work order
// carries (WorkOrderSerializer.get_tools → build_tools_context): six keys only,
// no completion state, integer quantity, and required-first ordering preserved
// as received. This is a different shape from the full MaintenanceTool the PM
// template edits, so it decodes into its own struct.
func TestGetWorkOrder_ParsesTools(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":7,"title":"Quarterly PM","status":"open",
		"tools":[
			{"id":"tl-1","name":"Torque wrench","quantity":2,"location_hint":"Tool crib, drawer 3","is_required":true,"notes":"calibrated"},
			{"id":"tl-2","name":"Feeler gauge","quantity":1,"location_hint":"","is_required":false,"notes":""}
		]
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if cap.path != "/api/inventory/work-orders/7/" {
		t.Fatalf("path = %q", cap.path)
	}
	if len(wo.Tools) != 2 {
		t.Fatalf("tools = %+v", wo.Tools)
	}
	first := wo.Tools[0]
	if first.Name != "Torque wrench" || first.Quantity != 2 || !first.IsRequired {
		t.Errorf("tool[0] = %+v", first)
	}
	if first.LocationHint != "Tool crib, drawer 3" || first.Notes != "calibrated" {
		t.Errorf("tool[0] hint/notes = %+v", first)
	}
	if fmt.Sprint(first.ID) != "tl-1" {
		t.Errorf("tool[0] id = %v", first.ID)
	}
	if wo.Tools[1].IsRequired || wo.Tools[1].Quantity != 1 {
		t.Errorf("tool[1] = %+v", wo.Tools[1])
	}
}

// TestGetWorkOrder_ToolsEmpty: an empty tools list is contract, not an error —
// a work order with no PM template (or a template with no tools) returns [],
// and the detail screen renders "No tools specified." from it.
func TestGetWorkOrder_ToolsEmpty(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"id":7,"title":"Ad-hoc","status":"open","tools":[]}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if len(wo.Tools) != 0 {
		t.Errorf("tools = %+v", wo.Tools)
	}
}

// TestGetWorkOrder_ParsesReferenceDocuments pins the reference bundle the WO
// detail carries (WorkOrderSerializer.get_reference_documents →
// build_reference_documents_context): the eight document keys, an integer
// version, a newest-first supersedes chain under revisions, and the asset's
// quick links. Only current documents head the list — the older versions are
// the chain, never top-level rows.
func TestGetWorkOrder_ParsesReferenceDocuments(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":7,"title":"Quarterly PM","status":"open",
		"reference_documents":{
			"documents":[
				{"id":"doc-3","category":"manual","category_display":"Manual / Documentation",
				 "title":"Bandsaw manual","version":3,
				 "file_url":"http://oms.example.org/media/assets/documents/2026/07/manual.pdf",
				 "uploaded_at":"2026-07-01T12:00:00+00:00",
				 "revisions":[
					{"id":"doc-2","version":2,"file_url":"http://oms.example.org/media/v2.pdf","uploaded_at":"2026-05-04T09:30:00+00:00"},
					{"id":"doc-1","version":1,"file_url":null,"uploaded_at":null}
				 ]},
				{"id":"doc-9","category":"wiring_diagram","category_display":"Wiring Diagram",
				 "title":"Zeta wiring","version":1,"file_url":null,
				 "uploaded_at":"2026-02-11T08:00:00+00:00","revisions":[]}
			],
			"links":[
				{"label":"Manual (PDF)","url":"http://oms.example.org/media/assets/manual.pdf"},
				{"label":"Wiki","url":"https://wiki.example.com/bandsaw"}
			]
		}
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if cap.path != "/api/inventory/work-orders/7/" {
		t.Fatalf("path = %q", cap.path)
	}
	if wo.ReferenceDocuments == nil {
		t.Fatalf("reference_documents did not decode")
	}
	docs, links := wo.ReferenceDocuments.Documents, wo.ReferenceDocuments.Links
	if len(docs) != 2 || len(links) != 2 {
		t.Fatalf("documents/links = %d/%d, want 2/2", len(docs), len(links))
	}

	manual := docs[0]
	if fmt.Sprint(manual.ID) != "doc-3" || manual.Title != "Bandsaw manual" {
		t.Errorf("documents[0] id/title = %v / %q", manual.ID, manual.Title)
	}
	// version is a plain JSON number here, unlike the decimal-string quantities
	// elsewhere on the work order.
	if manual.Version != 3 {
		t.Errorf("documents[0] version = %d, want 3", manual.Version)
	}
	if manual.Category != "manual" || manual.CategoryDisplay != "Manual / Documentation" {
		t.Errorf("documents[0] category = %q / %q", manual.Category, manual.CategoryDisplay)
	}
	if !strings.HasSuffix(manual.FileURL, "manual.pdf") {
		t.Errorf("documents[0] file_url = %q", manual.FileURL)
	}
	if manual.UploadedAt != "2026-07-01T12:00:00+00:00" {
		t.Errorf("documents[0] uploaded_at = %q", manual.UploadedAt)
	}
	if len(manual.Revisions) != 2 {
		t.Fatalf("revisions = %+v", manual.Revisions)
	}
	if manual.Revisions[0].Version != 2 || manual.Revisions[1].Version != 1 {
		t.Errorf("revisions must stay newest-first: %+v", manual.Revisions)
	}
	if fmt.Sprint(manual.Revisions[0].ID) != "doc-2" {
		t.Errorf("revisions[0] id = %v", manual.Revisions[0].ID)
	}
	// A row can outlive its file: file_url/uploaded_at come back null and must
	// decode to empty rather than failing the whole work order.
	if manual.Revisions[1].FileURL != "" || manual.Revisions[1].UploadedAt != "" {
		t.Errorf("null file_url/uploaded_at should decode empty: %+v", manual.Revisions[1])
	}

	if docs[1].Title != "Zeta wiring" || len(docs[1].Revisions) != 0 || docs[1].FileURL != "" {
		t.Errorf("documents[1] = %+v", docs[1])
	}
	if links[0].Label != "Manual (PDF)" || links[1].URL != "https://wiki.example.com/bandsaw" {
		t.Errorf("links = %+v", links)
	}
}

// TestGetWorkOrder_ReferenceDocumentsAbsentOrEmpty: both an empty bundle (an
// asset with no documents and no quick links) and a wholly absent one (a
// backend older than op-pzae, or the list serializer) are contract. Neither is
// an error, and both leave the detail screen showing "No linked documents."
func TestGetWorkOrder_ReferenceDocumentsAbsentOrEmpty(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":7,"title":"Ad-hoc","status":"open","reference_documents":{"documents":[],"links":[]}}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if wo.ReferenceDocuments == nil {
		t.Fatalf("an empty bundle should still decode into a non-nil struct")
	}
	if len(wo.ReferenceDocuments.Documents) != 0 || len(wo.ReferenceDocuments.Links) != 0 {
		t.Errorf("bundle = %+v, want empty", wo.ReferenceDocuments)
	}

	var cap2 capture
	srv2 := captureServer(t, http.StatusOK, `{"id":7,"title":"Ad-hoc","status":"open"}`, &cap2)
	defer srv2.Close()

	wo2, err := New(srv2.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder (absent bundle): %v", err)
	}
	if wo2.ReferenceDocuments != nil {
		t.Errorf("absent reference_documents should stay nil, got %+v", wo2.ReferenceDocuments)
	}
}

func TestCompleteWorkOrderTask_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tc1","is_completed":true,"task_title":"Torque bolts"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	tc, err := c.CompleteWorkOrderTask(context.Background(), "wo1", "tc1", true, "all good")
	if err != nil {
		t.Fatalf("CompleteWorkOrderTask: %v", err)
	}
	if tc == nil || !tc.IsCompleted {
		t.Fatalf("unexpected task completion: %+v", tc)
	}
	if captured.method != "PATCH" {
		t.Fatalf("method = %q, want PATCH", captured.method)
	}
	if captured.path != "/api/inventory/work-orders/wo1/tasks/tc1/complete/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["is_completed"] != true {
		t.Fatalf("is_completed = %v", captured.body["is_completed"])
	}
	if captured.body["notes"] != "all good" {
		t.Fatalf("notes = %v", captured.body["notes"])
	}
}

func TestCompleteWorkOrderTask_OmitsEmptyNotes(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"id":"tc1","is_completed":false}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CompleteWorkOrderTask(context.Background(), "wo1", "tc1", false, ""); err != nil {
		t.Fatalf("CompleteWorkOrderTask: %v", err)
	}
	if _, present := body["notes"]; present {
		t.Errorf("notes should be omitted when empty, got %v", body["notes"])
	}
	if body["is_completed"] != false {
		t.Errorf("is_completed = %v, want false", body["is_completed"])
	}
}

func TestToggleWorkOrderMaterial_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		_, _ = w.Write([]byte(`{"id":"mu1","material_name":"Grease","was_used":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	mu, err := c.ToggleWorkOrderMaterial(context.Background(), "wo1", "mu1", true, WorkOrderMaterialEdit{})
	if err != nil {
		t.Fatalf("ToggleWorkOrderMaterial: %v", err)
	}
	if mu == nil || !mu.WasUsed {
		t.Fatalf("unexpected material usage: %+v", mu)
	}
	if captured.method != "PATCH" {
		t.Fatalf("method = %q, want PATCH", captured.method)
	}
	if captured.path != "/api/inventory/work-orders/wo1/materials/mu1/toggle/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["was_used"] != true {
		t.Fatalf("was_used = %v", captured.body["was_used"])
	}
}

func TestAddWorkOrderPhoto_Multipart(t *testing.T) {
	var captured struct {
		method      string
		path        string
		contentType string
		workOrder   string
		caption     string
		filename    string
		fileData    string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.contentType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		captured.workOrder = r.FormValue("work_order")
		captured.caption = r.FormValue("caption")
		f, hdr, err := r.FormFile("image")
		if err != nil {
			t.Errorf("FormFile(image): %v", err)
		} else {
			captured.filename = hdr.Filename
			data, _ := io.ReadAll(f)
			captured.fileData = string(data)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ph1","caption":"before"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	ph, err := c.AddWorkOrderPhoto(context.Background(), "wo1", "before.jpg", []byte("JPEGBYTES"), "before", "")
	if err != nil {
		t.Fatalf("AddWorkOrderPhoto: %v", err)
	}
	if ph == nil || ph.Caption != "before" {
		t.Fatalf("unexpected photo: %+v", ph)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/inventory/work-orders/wo1/add_photo/" {
		t.Fatalf("path = %q", captured.path)
	}
	if !strings.HasPrefix(captured.contentType, "multipart/form-data") {
		t.Fatalf("content-type = %q, want multipart/form-data", captured.contentType)
	}
	if captured.workOrder != "wo1" {
		t.Fatalf("work_order field = %q", captured.workOrder)
	}
	if captured.caption != "before" {
		t.Fatalf("caption field = %q", captured.caption)
	}
	if captured.filename != "before.jpg" {
		t.Fatalf("filename = %q", captured.filename)
	}
	if captured.fileData != "JPEGBYTES" {
		t.Fatalf("file data = %q", captured.fileData)
	}
}

// TestAddWorkOrderPhoto_OmitsEmptyOptionalFields pins BOTH optional form
// fields off the wire when unset. task_completion matters most: the backend
// treats absent-or-blank as "work-order-level", but a stray empty part would
// still be parsed by DRF, and every pre-existing caller passes "" for it.
func TestAddWorkOrderPhoto_OmitsEmptyOptionalFields(t *testing.T) {
	var hasCaption, hasTaskCompletion bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		_, hasCaption = r.MultipartForm.Value["caption"]
		_, hasTaskCompletion = r.MultipartForm.Value["task_completion"]
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ph1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.AddWorkOrderPhoto(context.Background(), "wo1", "p.jpg", []byte("x"), "", ""); err != nil {
		t.Fatalf("AddWorkOrderPhoto: %v", err)
	}
	if hasCaption {
		t.Errorf("caption field should be omitted when empty")
	}
	if hasTaskCompletion {
		t.Errorf("task_completion field should be omitted when empty")
	}
}

// TestAddWorkOrderPhoto_PinsEvidenceToStep is the per-step evidence contract
// (op-syov): the photo is filed under one step via task_completion, and
// work_order STILL rides in the body — the serializer requires it, so dropping
// it in favour of the step id would 400 every evidence upload.
func TestAddWorkOrderPhoto_PinsEvidenceToStep(t *testing.T) {
	var captured struct {
		path           string
		workOrder      string
		taskCompletion string
		caption        string
		filename       string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		captured.workOrder = r.FormValue("work_order")
		captured.taskCompletion = r.FormValue("task_completion")
		captured.caption = r.FormValue("caption")
		if _, hdr, err := r.FormFile("image"); err == nil {
			captured.filename = hdr.Filename
		} else {
			t.Errorf("FormFile(image): %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ph9","task_completion":"tc-7","caption":"after"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	ph, err := c.AddWorkOrderPhoto(context.Background(), "wo1", "after.jpg", []byte("JPEG"), "after", "tc-7")
	if err != nil {
		t.Fatalf("AddWorkOrderPhoto: %v", err)
	}
	if captured.path != "/api/inventory/work-orders/wo1/add_photo/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.taskCompletion != "tc-7" {
		t.Errorf("task_completion field = %q, want tc-7", captured.taskCompletion)
	}
	if captured.workOrder != "wo1" {
		t.Errorf("work_order must still be sent alongside task_completion, got %q", captured.workOrder)
	}
	if captured.caption != "after" || captured.filename != "after.jpg" {
		t.Errorf("caption/filename = %q %q", captured.caption, captured.filename)
	}
	// The response echoes the full photo serializer, task_completion included.
	if ph == nil || ph.TaskCompletion != "tc-7" {
		t.Errorf("decoded photo = %+v, want TaskCompletion tc-7", ph)
	}
}

func TestUploadWorkOrderPdf_Multipart(t *testing.T) {
	var captured struct {
		method   string
		path     string
		filename string
		fileData string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		f, hdr, err := r.FormFile("pdf")
		if err != nil {
			t.Errorf("FormFile(pdf): %v", err)
		} else {
			captured.filename = hdr.Filename
			data, _ := io.ReadAll(f)
			captured.fileData = string(data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"submission_id":"sub1","status":"applied","work_order_id":"wo9","completed_items":[{"id":"tc1","task_title":"Torque"}],"errors":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.UploadWorkOrderPdf(context.Background(), "scan.pdf", []byte("%PDF-1.7"))
	if err != nil {
		t.Fatalf("UploadWorkOrderPdf: %v", err)
	}
	if res == nil || res.Status != "applied" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.WorkOrderID == nil || *res.WorkOrderID != "wo9" {
		t.Fatalf("work_order_id = %v", res.WorkOrderID)
	}
	if len(res.CompletedItems) != 1 || res.CompletedItems[0].TaskTitle != "Torque" {
		t.Fatalf("completed_items = %+v", res.CompletedItems)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/inventory/work-orders/upload-pdf/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.filename != "scan.pdf" {
		t.Fatalf("filename = %q", captured.filename)
	}
	if captured.fileData != "%PDF-1.7" {
		t.Fatalf("file data = %q", captured.fileData)
	}
}

func TestValidateWorkOrderChecklist_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"v1","is_complete":true,"electrical_acknowledged":true,"loto_acknowledged":true,"required_fields_acknowledged":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	v, err := c.ValidateWorkOrderChecklist(context.Background(), "wo1", true, true, true, "checked")
	if err != nil {
		t.Fatalf("ValidateWorkOrderChecklist: %v", err)
	}
	if v == nil || !v.IsComplete {
		t.Fatalf("unexpected validation: %+v", v)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/inventory/work-orders/wo1/validate/" {
		t.Fatalf("path = %q", captured.path)
	}
	for _, k := range []string{"electrical_acknowledged", "loto_acknowledged", "required_fields_acknowledged"} {
		if captured.body[k] != true {
			t.Fatalf("%s = %v, want true", k, captured.body[k])
		}
	}
	if captured.body["notes"] != "checked" {
		t.Fatalf("notes = %v", captured.body["notes"])
	}
}

func TestValidateWorkOrderChecklist_OmitsEmptyNotes(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"v1","is_complete":false}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ValidateWorkOrderChecklist(context.Background(), "wo1", true, false, true, ""); err != nil {
		t.Fatalf("ValidateWorkOrderChecklist: %v", err)
	}
	if _, present := body["notes"]; present {
		t.Errorf("notes should be omitted when empty")
	}
	if body["loto_acknowledged"] != false {
		t.Errorf("loto_acknowledged = %v, want false", body["loto_acknowledged"])
	}
}

// TestPostMultipart_RefreshesOn401 proves the multipart upload path honours the
// same 401 -> refresh -> retry contract as the JSON do() path, so a photo/PDF
// upload survives an expired access token instead of hard-failing.
func TestPostMultipart_RefreshesOn401(t *testing.T) {
	var uploadAttempts, refreshes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/refresh/":
			refreshes++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access":"newtok","refresh":"newref"}`))
		case "/api/inventory/work-orders/wo1/add_photo/":
			uploadAttempts++
			if r.Header.Get("Authorization") != "Bearer newtok" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"detail":"expired"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"ph1"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("oldtok", "refresh"))
	if _, err := c.AddWorkOrderPhoto(context.Background(), "wo1", "p.jpg", []byte("x"), "", ""); err != nil {
		t.Fatalf("AddWorkOrderPhoto: %v", err)
	}
	if refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes)
	}
	if uploadAttempts != 2 {
		t.Errorf("upload attempts = %d, want 2 (401 then retry)", uploadAttempts)
	}
	if c.AccessToken() != "newtok" {
		t.Errorf("access token not updated after refresh: %q", c.AccessToken())
	}
}
