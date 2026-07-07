package forgekeyapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGrantAuthorization_PostsCardFieldSet verifies a grant POSTs exactly the web
// asset-access card's field set {asset, user, notes} — no expires_at / is_active
// / authorized_by (server-set).
func TestGrantAuthorization_PostsCardFieldSet(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":5,"asset":"a-1","user":7,"is_active":true}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	_, err := c.GrantAuthorization(context.Background(), AuthorizationWrite{Asset: "a-1", User: 7, Notes: "trained"})
	if err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/api/forgekey/authorizations/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody["asset"] != "a-1" || gotBody["notes"] != "trained" {
		t.Errorf("body = %v, want asset a-1 notes trained", gotBody)
	}
	if _, leaked := gotBody["expires_at"]; leaked {
		t.Errorf("body leaked expires_at %v — the web card never sends it", gotBody["expires_at"])
	}
	if _, leaked := gotBody["is_active"]; leaked {
		t.Errorf("body leaked is_active — not a grant input: %v", gotBody)
	}
}

// TestGrantAuthorization_OmitsBlankNotes confirms an empty note is dropped (omitempty).
func TestGrantAuthorization_OmitsBlankNotes(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":5}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	if _, err := c.GrantAuthorization(context.Background(), AuthorizationWrite{Asset: "a-1", User: 7}); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	if _, present := gotBody["notes"]; present {
		t.Errorf("blank notes should be omitted, body = %v", gotBody)
	}
}

// TestRevokeAuthorization_TrailingSlash guards the fix: revoke is a DefaultRouter
// @action, so the POST must hit .../{id}/revoke/ WITH a trailing slash (the web
// path); the un-slashed form redirect-drops the POST.
func TestRevokeAuthorization_TrailingSlash(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	if err := c.RevokeAuthorization(context.Background(), "42", "left the makerspace"); err != nil {
		t.Fatalf("RevokeAuthorization: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/api/forgekey/authorizations/42/revoke/"; gotPath != want {
		t.Errorf("path = %q, want %q (trailing slash required)", gotPath, want)
	}
	if gotBody["notes"] != "left the makerspace" {
		t.Errorf("notes = %v, want the revoke reason", gotBody["notes"])
	}
}
