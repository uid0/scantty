package omsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetProjectStorageStint(t *testing.T) {
	// Mirror the single-stint GET the detail view depends on. The body is a
	// trimmed ProjectStorageStintSerializer payload with the computed
	// status/expiry fields the label preview reproduces.
	body := `{
		"id": 42,
		"stint_id": "PS-AB23CDFG",
		"username": "jdoe",
		"display_name": "Jane Doe",
		"project_title": "CNC jig",
		"status": "active",
		"expiry_week": 37,
		"expiry_day_of_year": 259,
		"storage_location_name": "Rack B3",
		"purgatory_location_name": "Purgatory Shelf 2",
		"events": [
			{"id": 1, "event_type": "notice_sent", "actor_username": "sysbot", "note": "sent to jdoe", "created_at": "2026-06-01T00:00:00Z"}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/project-storage/stints/PS-AB23CDFG/" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	st, err := c.GetProjectStorageStint(context.Background(), "PS-AB23CDFG")
	if err != nil {
		t.Fatalf("GetProjectStorageStint: %v", err)
	}
	if st == nil {
		t.Fatal("expected a stint, got nil")
	}
	if st.StintID != "PS-AB23CDFG" || st.DisplayName != "Jane Doe" || st.ProjectTitle != "CNC jig" {
		t.Fatalf("unexpected stint identity: %+v", st)
	}
	if st.Status != "active" || st.ExpiryWeek != 37 || st.ExpiryDayOfYear != 259 {
		t.Fatalf("unexpected computed fields: status=%q wk=%d day=%d", st.Status, st.ExpiryWeek, st.ExpiryDayOfYear)
	}
	if st.StorageLocationName != "Rack B3" || st.PurgatoryLocationName != "Purgatory Shelf 2" {
		t.Fatalf("unexpected locations: storage=%q purgatory=%q", st.StorageLocationName, st.PurgatoryLocationName)
	}
	if len(st.Events) != 1 || st.Events[0].Action != "notice_sent" {
		t.Fatalf("unexpected events: %+v", st.Events)
	}
	// The event's real backend shape uses note / created_at — assert they
	// decode (the old action/occurred_at tags silently left these empty).
	if st.Events[0].Notes != "sent to jdoe" || st.Events[0].CreatedAt.IsZero() {
		t.Fatalf("event note/created_at did not decode: %+v", st.Events[0])
	}
}

func TestGetProjectStorageStintError(t *testing.T) {
	// A 404 (unknown stint_id) must surface as an error with a nil stint,
	// matching GetItem's contract so the detail view shows "not found"
	// rather than a zero-value stint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL)
	st, err := c.GetProjectStorageStint(context.Background(), "PS-NOPE0000")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if st != nil {
		t.Fatalf("expected nil stint on error, got %+v", st)
	}
}

func TestStartProjectStorageStint(t *testing.T) {
	// The intake (create) path is the AllowAny "start" action, NOT a POST to the
	// collection (the viewset is read-only). Assert the method/path and that the
	// body carries username + the non-blank optionals only — blank optionals
	// must be dropped by omitempty, not sent as empty strings.
	var captured struct {
		method, path string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"stint_id":"PS-NEW01234","username":"alice","project_title":"CNC jig","status":"active"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	st, err := c.StartProjectStorageStint(context.Background(), ProjectStorageStintStart{
		Username:     "alice",
		ProjectTitle: "CNC jig",
		// first/last/email/storage_location left blank → must be omitted.
	})
	if err != nil {
		t.Fatalf("StartProjectStorageStint: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/project-storage/stints/start/" {
		t.Fatalf("method/path = %q %q (want POST .../stints/start/)", captured.method, captured.path)
	}
	if captured.body["username"] != "alice" || captured.body["project_title"] != "CNC jig" {
		t.Fatalf("unexpected body: %+v", captured.body)
	}
	for _, blank := range []string{"first_name", "last_name", "email", "storage_location_name"} {
		if _, present := captured.body[blank]; present {
			t.Errorf("blank optional %q should be omitted from the payload, got %+v", blank, captured.body)
		}
	}
	if st == nil || st.StintID != "PS-NEW01234" || st.ID != 7 {
		t.Fatalf("unexpected returned stint: %+v", st)
	}
}

func TestStartProjectStorageStintConflict(t *testing.T) {
	// The member already holds a live stint → the backend 409s. That must come
	// back as an error with a nil stint so the form shows a clean message.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"active_stint_exists","detail":"member already has an active stint"}`, http.StatusConflict)
	}))
	defer srv.Close()

	c := New(srv.URL)
	st, err := c.StartProjectStorageStint(context.Background(), ProjectStorageStintStart{Username: "alice"})
	if err == nil {
		t.Fatal("expected an error on 409 conflict, got nil")
	}
	if st != nil {
		t.Fatalf("expected nil stint on conflict, got %+v", st)
	}
}

func TestMarkProjectStorageStintRemoved(t *testing.T) {
	// "remove" is the whole-stint mark-removed action (there is no item model).
	// Assert the method/path and that the audit note rides in the body.
	var captured struct {
		method, path, note string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured.note = body["note"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.MarkProjectStorageStintRemoved(context.Background(), "PS-AB23CDFG", "left the space"); err != nil {
		t.Fatalf("MarkProjectStorageStintRemoved: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/project-storage/stints/PS-AB23CDFG/mark-removed/" {
		t.Fatalf("method/path = %q %q (want POST .../PS-AB23CDFG/mark-removed/)", captured.method, captured.path)
	}
	if captured.note != "left the space" {
		t.Errorf("note = %q, want %q", captured.note, "left the space")
	}
}

func TestMarkProjectStorageStintRemovedConflict(t *testing.T) {
	// A second mark-removed → 409 already_removed → surfaced as an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"already_removed"}`, http.StatusConflict)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.MarkProjectStorageStintRemoved(context.Background(), "PS-AB23CDFG", ""); err == nil {
		t.Fatal("expected an error on 409 already_removed, got nil")
	}
}
