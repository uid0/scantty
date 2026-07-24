package omsapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListWorkOrderAttachments_FiltersByWorkOrder pins the list to the flat
// collection with a ?work_order= filter and proves the paginated envelope is
// unwrapped into a slice.
func TestListWorkOrderAttachments_FiltersByWorkOrder(t *testing.T) {
	var captured struct {
		method string
		path   string
		query  string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.query = r.URL.Query().Get("work_order")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[
			{"id":"att-1","work_order":"wo-1","file":"wo/a.pdf","attachment_url":"http://x/a.pdf","kind":"document","description":"manual"},
			{"id":"att-2","work_order":"wo-1","file":"wo/b.jpg","attachment_url":"http://x/b.jpg","kind":"photo"}
		]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	atts, err := c.ListWorkOrderAttachments(context.Background(), "wo-1")
	if err != nil {
		t.Fatalf("ListWorkOrderAttachments: %v", err)
	}
	if captured.method != "GET" {
		t.Errorf("method = %q, want GET", captured.method)
	}
	if captured.path != "/api/inventory/work-order-attachments/" {
		t.Errorf("path = %q", captured.path)
	}
	if captured.query != "wo-1" {
		t.Errorf("work_order filter = %q, want wo-1", captured.query)
	}
	if len(atts) != 2 {
		t.Fatalf("got %d attachments, want 2", len(atts))
	}
	if atts[0].Description != "manual" || atts[0].Kind != "document" || atts[0].AttachmentURL != "http://x/a.pdf" {
		t.Errorf("attachment[0] = %+v", atts[0])
	}
	if atts[1].Kind != "photo" {
		t.Errorf("attachment[1] kind = %q, want photo", atts[1].Kind)
	}
}

// TestListWorkOrderAttachments_BareArray proves the list tolerates an
// un-paginated bare array (some viewsets disable pagination).
func TestListWorkOrderAttachments_BareArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"att-9","work_order":"wo-1","kind":"other"}]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	atts, err := c.ListWorkOrderAttachments(context.Background(), "wo-1")
	if err != nil {
		t.Fatalf("ListWorkOrderAttachments: %v", err)
	}
	if len(atts) != 1 || atts[0].Kind != "other" {
		t.Fatalf("bare-array decode = %+v", atts)
	}
}

// TestUploadWorkOrderAttachment_Multipart pins the flat POST, the work_order
// form field (the URL carries no id), and the file/description/kind parts.
func TestUploadWorkOrderAttachment_Multipart(t *testing.T) {
	var captured struct {
		method      string
		path        string
		ctype       string
		workOrder   string
		fileName    string
		fileBody    string
		description string
		kind        string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.ctype = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured.workOrder = r.FormValue("work_order")
		captured.description = r.FormValue("description")
		captured.kind = r.FormValue("kind")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file part", http.StatusBadRequest)
			return
		}
		defer f.Close()
		captured.fileName = hdr.Filename
		b, _ := io.ReadAll(f)
		captured.fileBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"att-3","work_order":"wo-1","kind":"document","description":"the manual"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	att, err := c.UploadWorkOrderAttachment(
		context.Background(), "wo-1", "manual.pdf", strings.NewReader("%PDF-1.4 fake"), "the manual", "document",
	)
	if err != nil {
		t.Fatalf("UploadWorkOrderAttachment: %v", err)
	}
	if att == nil || att.ID != "att-3" {
		t.Fatalf("unexpected attachment: %+v", att)
	}
	if captured.method != "POST" {
		t.Errorf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/inventory/work-order-attachments/" {
		t.Errorf("path = %q", captured.path)
	}
	if !strings.HasPrefix(captured.ctype, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data", captured.ctype)
	}
	if captured.workOrder != "wo-1" {
		t.Errorf("work_order field = %q, want wo-1", captured.workOrder)
	}
	if captured.fileName != "manual.pdf" {
		t.Errorf("file name = %q, want manual.pdf", captured.fileName)
	}
	if captured.fileBody != "%PDF-1.4 fake" {
		t.Errorf("file body = %q", captured.fileBody)
	}
	if captured.description != "the manual" {
		t.Errorf("description = %q", captured.description)
	}
	if captured.kind != "document" {
		t.Errorf("kind = %q, want document", captured.kind)
	}
}

// TestUploadWorkOrderAttachment_OmitsBlankOptionalFields confirms a blank
// description and a blank kind are not sent as fields (the backend then applies
// its own default), mirroring the PO uploader's blank-description handling.
func TestUploadWorkOrderAttachment_OmitsBlankOptionalFields(t *testing.T) {
	sawDescription, sawKind := false, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			_, sawDescription = r.MultipartForm.Value["description"]
			_, sawKind = r.MultipartForm.Value["kind"]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"att-4"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UploadWorkOrderAttachment(
		context.Background(), "wo-1", "x.pdf", strings.NewReader("data"), "", "",
	); err != nil {
		t.Fatalf("UploadWorkOrderAttachment: %v", err)
	}
	if sawDescription {
		t.Error("blank description should not be sent as a multipart field")
	}
	if sawKind {
		t.Error("blank kind should not be sent as a multipart field")
	}
}

// TestDeleteWorkOrderAttachment pins deletion to the flat DELETE
// .../work-order-attachments/{id}/ (204) — no work-order id in the path.
func TestDeleteWorkOrderAttachment(t *testing.T) {
	var captured struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteWorkOrderAttachment(context.Background(), "att-5"); err != nil {
		t.Fatalf("DeleteWorkOrderAttachment: %v", err)
	}
	if captured.method != "DELETE" {
		t.Errorf("method = %q, want DELETE", captured.method)
	}
	if captured.path != "/api/inventory/work-order-attachments/att-5/" {
		t.Errorf("path = %q", captured.path)
	}
}

// TestDeleteWorkOrderAttachment_Forbidden surfaces the backend's staff-only 403
// as an error rather than swallowing it — the screen shows it to the operator.
func TestDeleteWorkOrderAttachment_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"You do not have permission to perform this action."}`, http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteWorkOrderAttachment(context.Background(), "att-5"); err == nil {
		t.Fatal("expected error on 403, got nil")
	}
}
