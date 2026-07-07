package omsapi

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
)

// TestListLocationProblems_QueryAndDecode pins the list wire contract: GET to
// the collection with the location/status/severity filters set, and a full
// decode of the DRF envelope into the read struct (incl. the display labels,
// nullable resolved_at, and short-id method fields).
func TestListLocationProblems_QueryAndDecode(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{
			"id":"11111111-1111-1111-1111-111111111111",
			"location":42,"location_name":"Bay 3",
			"reported_by":"alice","description":"Ceiling leak over the CNC",
			"status":"reported","status_display":"Reported",
			"severity":"high","severity_display":"High",
			"photo_url":"http://x/media/p.jpg","paper_form_url":null,
			"work_order":null,"work_order_short_id":null,
			"third_party_work_order":null,"third_party_work_order_short_id":null,
			"resolution_notes":"","reported_at":"2026-05-01T12:00:00Z",
			"updated_at":"2026-05-01T12:00:00Z","resolved_at":null,"resolved_by":""
		}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	rows, err := c.ListLocationProblems(context.Background(), LocationProblemListParams{
		Location: 42, Status: "reported", Severity: "high",
	})
	if err != nil {
		t.Fatalf("ListLocationProblems: %v", err)
	}
	if gotPath != "/api/inventory/location-problems/" {
		t.Fatalf("path = %q", gotPath)
	}
	for _, want := range []string{"location=42", "status=reported", "severity=high"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	p := rows[0]
	if p.ID != "11111111-1111-1111-1111-111111111111" || p.Location != 42 || p.LocationName != "Bay 3" {
		t.Errorf("identity decode wrong: %+v", p)
	}
	if p.StatusDisplay != "Reported" || p.SeverityDisplay != "High" {
		t.Errorf("display labels wrong: %q / %q", p.StatusDisplay, p.SeverityDisplay)
	}
	if p.PhotoURL != "http://x/media/p.jpg" {
		t.Errorf("photo_url = %q", p.PhotoURL)
	}
	if p.ResolvedAt != nil {
		t.Errorf("resolved_at should be nil, got %v", p.ResolvedAt)
	}
	if p.IsResolved() || p.IsPromoted() {
		t.Errorf("fresh report should be neither resolved nor promoted: %+v", p)
	}
}

// TestListLocationProblems_NoFilters omits the query entirely when params are
// zero-valued (all-locations list).
func TestListLocationProblems_NoFilters(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"next":null,"previous":null,"results":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ListLocationProblems(context.Background(), LocationProblemListParams{}); err != nil {
		t.Fatalf("ListLocationProblems: %v", err)
	}
	// Only the pagination page= param should be present, none of the filters.
	for _, bad := range []string{"location=", "status=", "severity="} {
		if strings.Contains(gotQuery, bad) {
			t.Errorf("query %q should not contain %q", gotQuery, bad)
		}
	}
}

// TestReportLocationProblem_Multipart pins the create contract: a multipart POST
// to the trailing-slash report_problem @action with description + severity form
// fields and an optional photo file part; the 201 body decodes back.
func TestReportLocationProblem_Multipart(t *testing.T) {
	var gotMethod, gotPath, gotDesc, gotSev, gotPhotoName string
	var gotPhoto []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotDesc = r.FormValue("description")
		gotSev = r.FormValue("severity")
		if f, hdr, err := r.FormFile("photo"); err == nil {
			gotPhotoName = hdr.Filename
			gotPhoto, _ = io.ReadAll(f)
			f.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"22222222-2222-2222-2222-222222222222","location":7,"description":"door stuck","status":"reported","severity":"urgent"}`))
	}))
	defer srv.Close()

	// A real temp file so the os.ReadFile upload path is exercised.
	dir := t.TempDir()
	photoPath := filepath.Join(dir, "leak.jpg")
	if err := os.WriteFile(photoPath, []byte("JPEGDATA"), 0o600); err != nil {
		t.Fatalf("write temp photo: %v", err)
	}

	c := New(srv.URL)
	got, err := c.ReportLocationProblem(context.Background(), "7", LocationProblemReport{
		Description: "door stuck",
		Severity:    "urgent",
		PhotoPath:   photoPath,
	})
	if err != nil {
		t.Fatalf("ReportLocationProblem: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/api/inventory/locations/7/report_problem/" {
		t.Errorf("path = %q (want underscore + trailing slash)", gotPath)
	}
	if gotDesc != "door stuck" || gotSev != "urgent" {
		t.Errorf("fields desc=%q sev=%q", gotDesc, gotSev)
	}
	if gotPhotoName != "leak.jpg" || string(gotPhoto) != "JPEGDATA" {
		t.Errorf("photo part name=%q data=%q", gotPhotoName, string(gotPhoto))
	}
	if got == nil || got.ID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("response decode wrong: %+v", got)
	}
}

// TestReportLocationProblem_NoPhoto omits the file part + severity when unset —
// description-only is a valid report (severity defaults server-side).
func TestReportLocationProblem_NoPhoto(t *testing.T) {
	var hadPhoto bool
	var gotSev string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotSev = r.FormValue("severity")
		if _, _, err := r.FormFile("photo"); err == nil {
			hadPhoto = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"3","location":7,"description":"x","status":"reported","severity":"medium"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ReportLocationProblem(context.Background(), "7", LocationProblemReport{Description: "x"}); err != nil {
		t.Fatalf("ReportLocationProblem: %v", err)
	}
	if hadPhoto {
		t.Errorf("no photo should have been sent")
	}
	if gotSev != "" {
		t.Errorf("severity should be omitted when blank, got %q", gotSev)
	}
}

// TestResolveLocationProblem_Body pins the resolve contract: a JSON POST to the
// trailing-slash resolve/ @action carrying status + resolution_notes.
func TestResolveLocationProblem_Body(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"9","location":7,"status":"resolved","status_display":"Resolved","resolution_notes":"fixed","resolved_by":"bob","resolved_at":"2026-05-02T09:00:00Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.ResolveLocationProblem(context.Background(), "9", LocationProblemResolve{
		Status:          "resolved",
		ResolutionNotes: "fixed",
	})
	if err != nil {
		t.Fatalf("ResolveLocationProblem: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/inventory/location-problems/9/resolve/" {
		t.Fatalf("method/path = %q %q", gotMethod, gotPath)
	}
	if gotBody["status"] != "resolved" {
		t.Errorf("status = %v", gotBody["status"])
	}
	if gotBody["resolution_notes"] != "fixed" {
		t.Errorf("resolution_notes = %v", gotBody["resolution_notes"])
	}
	if got == nil || !got.IsResolved() || got.ResolvedAt == nil || got.ResolvedBy != "bob" {
		t.Errorf("resolve response decode wrong: %+v", got)
	}
}

// TestResolveLocationProblem_OmitsBlankNotes confirms resolution_notes is dropped
// from the JSON when empty (omitempty) so the stored notes are preserved — the
// backend keeps its existing value when the key is absent.
func TestResolveLocationProblem_OmitsBlankNotes(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"9","status":"closed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ResolveLocationProblem(context.Background(), "9", LocationProblemResolve{Status: "closed"}); err != nil {
		t.Fatalf("ResolveLocationProblem: %v", err)
	}
	if _, present := gotBody["resolution_notes"]; present {
		t.Errorf("blank resolution_notes should be omitted, body = %v", gotBody)
	}
	if gotBody["status"] != "closed" {
		t.Errorf("status = %v", gotBody["status"])
	}
}
