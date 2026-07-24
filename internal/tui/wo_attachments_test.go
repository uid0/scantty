package tui

import (
	"context"
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

// drives the screen's Init load path and feeds the resulting message back in,
// standing in for the bubbletea runtime for the load-on-entry behaviour that
// distinguishes a WO's (endpoint-backed) attachments from a PO's (embedded).
func woAttachAfterInit(t *testing.T, s *WorkOrderAttachmentsScreen) *WorkOrderAttachmentsScreen {
	t.Helper()
	for _, msg := range ccDrainCmd(s.Init()) {
		next, _ := s.Update(msg)
		s = next.(*WorkOrderAttachmentsScreen)
	}
	return s
}

// TestWOAttachments_LoadOnInit proves the screen fetches the list itself on
// entry (seeded with only a work-order id) and filters by that id.
func TestWOAttachments_LoadOnInit(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("work_order")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[
			{"id":"att-1","file":"wo/manual.pdf","description":"service manual","kind":"document"}
		]}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := woAttachAfterInit(t, NewWorkOrderAttachmentsScreen(deps, "wo1"))

	if gotQuery != "wo1" {
		t.Errorf("list filtered by work_order=%q, want wo1", gotQuery)
	}
	if s.loading {
		t.Error("screen should not still be loading after the list arrives")
	}
	if len(s.attachments) != 1 || s.attachments[0].Description != "service manual" {
		t.Fatalf("attachments = %+v", s.attachments)
	}
	if out := s.viewList(); !strings.Contains(out, "manual.pdf") || !strings.Contains(out, "service manual") {
		t.Errorf("list view = %q", out)
	}
}

// TestWOAttachments_UploadValidation covers the three client-side guards before
// anything reaches the wire: a blank path, a missing file, and a kind outside
// the backend's photo/document/other set.
func TestWOAttachments_UploadValidation(t *testing.T) {
	s := NewWorkOrderAttachmentsScreen(Deps{}, "wo1")
	s.phase = woAttachPhaseUpload

	// Empty path.
	s.uploadInputs[woAttachFieldPath].SetValue("")
	s.submitUpload()
	if s.errMsg == "" || s.uploading {
		t.Errorf("empty path should error without uploading; err=%q uploading=%v", s.errMsg, s.uploading)
	}

	// Nonexistent path.
	s.uploadInputs[woAttachFieldPath].SetValue("/no/such/file/hopefully-1234.pdf")
	s.submitUpload()
	if s.errMsg == "" || s.uploading {
		t.Errorf("missing file should error without uploading; err=%q", s.errMsg)
	}

	// Real file, but an unknown kind is turned away before the request.
	dir := t.TempDir()
	f := filepath.Join(dir, "x.pdf")
	if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	s.uploadInputs[woAttachFieldPath].SetValue(f)
	s.uploadInputs[woAttachFieldKind].SetValue("blueprint")
	s.submitUpload()
	if s.errMsg == "" || s.uploading {
		t.Errorf("bad kind should error without uploading; err=%q uploading=%v", s.errMsg, s.uploading)
	}
}

// TestWOAttachments_UploadDrivePostsMultipart drives the upload form end to end
// against a fake server: u to open, type the path, enter to upload — and asserts
// the flat POST carries the work_order id, the file basename, and the kind.
func TestWOAttachments_UploadDrivePostsMultipart(t *testing.T) {
	var captured struct {
		method, path, workOrder, kind, filename, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The upload POST is the multipart one; the follow-up reload is a GET.
		if r.Method == http.MethodPost {
			captured.method = r.Method
			captured.path = r.URL.Path
			_ = r.ParseMultipartForm(1 << 20)
			captured.workOrder = r.FormValue("work_order")
			captured.kind = r.FormValue("kind")
			if f, hdr, err := r.FormFile("file"); err == nil {
				captured.filename = hdr.Filename
				b, _ := io.ReadAll(f)
				captured.body = string(b)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"att-7"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	doc := filepath.Join(dir, "manual.pdf")
	if err := os.WriteFile(doc, []byte("%PDF fake"), 0o600); err != nil {
		t.Fatalf("write temp doc: %v", err)
	}

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewWorkOrderAttachmentsScreen(deps, "wo1")

	// u opens the upload form with the path field focused.
	next, _ := s.Update(woRuneKey("u"))
	s = next.(*WorkOrderAttachmentsScreen)
	if s.phase != woAttachPhaseUpload {
		t.Fatalf("phase = %v, want upload", s.phase)
	}
	// Type the path into the focused field; kind stays at its "document" default.
	next, _ = s.Update(woRuneKey(doc))
	s = next.(*WorkOrderAttachmentsScreen)

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*WorkOrderAttachmentsScreen)
	if cmd == nil {
		t.Fatal("expected an upload cmd")
	}
	if _, ok := cmd().(woAttachUploadedMsg); !ok {
		t.Fatalf("wrong upload msg type")
	}
	if captured.method != "POST" || captured.path != "/api/inventory/work-order-attachments/" {
		t.Fatalf("upload hit %s %s", captured.method, captured.path)
	}
	if captured.workOrder != "wo1" {
		t.Errorf("work_order = %q, want wo1", captured.workOrder)
	}
	if captured.filename != "manual.pdf" {
		t.Errorf("filename = %q, want manual.pdf (basename only)", captured.filename)
	}
	if captured.kind != "document" {
		t.Errorf("kind = %q, want document (the form default)", captured.kind)
	}
	if captured.body != "%PDF fake" {
		t.Errorf("file body = %q", captured.body)
	}
}

// TestWOAttachments_RenderSmoke checks the empty state, a populated row, the
// delete-confirm prompt, and the three-field upload form.
func TestWOAttachments_RenderSmoke(t *testing.T) {
	s := NewWorkOrderAttachmentsScreen(Deps{}, "wo1")

	// Loaded, empty.
	s.loading = false
	if out := s.viewList(); !strings.Contains(out, "No attachments") {
		t.Errorf("empty-state view = %q", out)
	}

	// One row.
	s.attachments = []omsapi.WorkOrderAttachment{
		{ID: "att-1", File: "wo/wiring.pdf", Description: "as-built wiring", Kind: "document"},
	}
	out := s.viewList()
	if !strings.Contains(out, "wiring.pdf") || !strings.Contains(out, "as-built wiring") || !strings.Contains(out, "document") {
		t.Errorf("row view = %q", out)
	}

	// Delete-confirm prompt.
	s.confirmingDelete = true
	if out := s.viewList(); !strings.Contains(out, "Delete") {
		t.Errorf("delete-confirm view missing prompt: %q", out)
	}

	// Upload form shows all three fields.
	s.confirmingDelete = false
	s.phase = woAttachPhaseUpload
	if out := s.View(); !strings.Contains(out, "File path") || !strings.Contains(out, "Description") || !strings.Contains(out, "Kind") {
		t.Errorf("upload form view = %q", out)
	}
}

// TestWorkOrderDetail_AKeyOpensAttachments is the regression test for the bug
// this bead fixes: on the work-order detail screen, pressing A must open the
// work order's attachments — NOT teleport to the global new-asset form. It
// drives the key through the real Root dispatch, so it exercises the
// HandlesKey("A") claim that beats the global shortcut.
func TestWorkOrderDetail_AKeyOpensAttachments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/inventory/work-orders/wo1/":
			// Corrective WO: no maintenance_item, so the detail screen skips the
			// estimates fetch.
			_, _ = w.Write([]byte(`{"id":"wo1","title":"Fix press","status":"open"}`))
		case r.URL.Path == "/api/inventory/work-order-attachments/":
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
		}
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewWorkOrderDetailScreen(deps, "wo1")
	r := Root{deps: deps, nav: NewNav(), status: NewStatusBar(), navWidth: 24, screen: detail}
	r = apPump(t, r, detail.Init())

	if detail.wo == nil || detail.wo.Status != "open" {
		t.Fatalf("work order did not load: %+v", detail.wo)
	}

	// Shift+A: HandlesKey("A") must route this to the WO screen, which opens the
	// attachments list — the global "A" would have built an AssetFormScreen.
	r = apPress(t, r, "A")
	if _, isAsset := r.screen.(*AssetFormScreen); isAsset {
		t.Fatal("A leaked to the global new-asset form — HandlesKey(\"A\") not honored")
	}
	att, ok := r.screen.(*WorkOrderAttachmentsScreen)
	if !ok {
		t.Fatalf("A should open the work-order attachments screen, got %T", r.screen)
	}
	if att.woID != "wo1" {
		t.Errorf("attachments screen woID = %q, want wo1", att.woID)
	}
}
