package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	mu, err := c.ToggleWorkOrderMaterial(context.Background(), "wo1", "mu1", true)
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
	ph, err := c.AddWorkOrderPhoto(context.Background(), "wo1", "before.jpg", []byte("JPEGBYTES"), "before")
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

func TestAddWorkOrderPhoto_OmitsEmptyCaption(t *testing.T) {
	var hasCaption bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		_, hasCaption = r.MultipartForm.Value["caption"]
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ph1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.AddWorkOrderPhoto(context.Background(), "wo1", "p.jpg", []byte("x"), ""); err != nil {
		t.Fatalf("AddWorkOrderPhoto: %v", err)
	}
	if hasCaption {
		t.Errorf("caption field should be omitted when empty")
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
	if _, err := c.AddWorkOrderPhoto(context.Background(), "wo1", "p.jpg", []byte("x"), ""); err != nil {
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
