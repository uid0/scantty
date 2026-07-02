package omsapi

import (
	"context"
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
			{"id": 1, "stint": 42, "action": "notice_sent", "actor_username": "sysbot", "occurred_at": "2026-06-01T00:00:00Z"}
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
