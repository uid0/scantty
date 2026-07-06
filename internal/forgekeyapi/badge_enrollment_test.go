package forgekeyapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestArmBadgeEnrollment_PostsUserID verifies arming "enroll next scan" POSTs the
// user_id to the router @action (trailing slash).
func TestArmBadgeEnrollment_PostsUserID(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody badgeArmRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"armed":true,"user_id":7,"reader_id":null,"ttl_seconds":60}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := c.ArmBadgeEnrollment(context.Background(), 7, "")
	if err != nil {
		t.Fatalf("ArmBadgeEnrollment: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/api/forgekey/badge-enrollment/arm/"; gotPath != want {
		t.Errorf("path = %q, want %q (router @action needs trailing slash)", gotPath, want)
	}
	if gotBody.UserID != 7 {
		t.Errorf("user_id = %d, want 7", gotBody.UserID)
	}
	if !res.Armed || res.TTLSeconds != 60 {
		t.Errorf("result = %+v, want armed with ttl 60", res)
	}
}

// TestSetBadge_SetsUID verifies the manual-entry path POSTs {user_id, badge_number}.
func TestSetBadge_SetsUID(t *testing.T) {
	var gotPath string
	var gotBody badgeSetRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"user_id":7,"badge_number":"ABC123"}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	uid := "ABC123"
	res, err := c.SetBadge(context.Background(), 7, &uid)
	if err != nil {
		t.Fatalf("SetBadge: %v", err)
	}
	if want := "/api/forgekey/badge-enrollment/set-badge/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody.UserID != 7 || gotBody.BadgeNumber == nil || *gotBody.BadgeNumber != "ABC123" {
		t.Errorf("body = %+v, want user 7 badge ABC123", gotBody)
	}
	if res.BadgeNumber == nil || *res.BadgeNumber != "ABC123" {
		t.Errorf("result badge = %v, want ABC123", res.BadgeNumber)
	}
}

// TestSetBadge_ClearsWithExplicitNull verifies clearing sends badge_number: null
// on the wire (not "" and not an omitted key) so the backend clears the UID.
func TestSetBadge_ClearsWithExplicitNull(t *testing.T) {
	var rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rawBody = string(b)
		_, _ = w.Write([]byte(`{"user_id":7,"badge_number":null}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	res, err := c.SetBadge(context.Background(), 7, nil)
	if err != nil {
		t.Fatalf("SetBadge(clear): %v", err)
	}
	if !strings.Contains(rawBody, `"badge_number":null`) {
		t.Errorf("clear body = %s, want it to contain \"badge_number\":null", rawBody)
	}
	if res.BadgeNumber != nil {
		t.Errorf("cleared result badge = %v, want nil", *res.BadgeNumber)
	}
}

// TestGetBadgeEnrollmentState_PollsUserAndDecodesCapture confirms the poll passes
// user_id and decodes a captured UID.
func TestGetBadgeEnrollmentState_PollsUserAndDecodesCapture(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"armed":true,"armed_user_id":7,"reader_id":null,"captured":{"user_id":7,"badge_number":"DEADBEEF"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	st, err := c.GetBadgeEnrollmentState(context.Background(), 7, "")
	if err != nil {
		t.Fatalf("GetBadgeEnrollmentState: %v", err)
	}
	if !strings.Contains(gotQuery, "user_id=7") {
		t.Errorf("query = %q, want user_id=7", gotQuery)
	}
	if st.Captured == nil || st.Captured.BadgeNumber != "DEADBEEF" {
		t.Fatalf("captured = %+v, want DEADBEEF", st.Captured)
	}
}

// TestCancelBadgeEnrollment_PostsCancel guards the disarm path + trailing slash.
func TestCancelBadgeEnrollment_PostsCancel(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"armed":false}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	if err := c.CancelBadgeEnrollment(context.Background(), ""); err != nil {
		t.Fatalf("CancelBadgeEnrollment: %v", err)
	}
	if want := "/api/forgekey/badge-enrollment/cancel/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}
