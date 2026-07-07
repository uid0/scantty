package forgekeyapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPostSurvivesRedirect is the belt-and-suspenders net beneath the slashed
// action paths (sc-amjg): even if a path were to lose its trailing slash again
// (or an http:// base 301-upgrades to https://), the client must arrive at the
// redirect target still as a POST with the body intact — not the GET net/http's
// default redirect policy would produce. Mirrors omsapi's redirect guard.
func TestPostSurvivesRedirect(t *testing.T) {
	var gotMethod, gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Model DRF APPEND_SLASH: the un-slashed action 301s to the slashed one.
		if r.URL.Path == "/api/forgekey/devices/d1/enable" {
			http.Redirect(w, r, "/api/forgekey/devices/d1/enable/", http.StatusMovedPermanently)
			return
		}
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL, AuthToken: "tok-123"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Post(context.Background(), "/api/forgekey/devices/d1/enable", map[string]string{"note": "on"}, nil); err != nil {
		t.Fatalf("Post across redirect: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("target method = %q, want POST (redirect downgraded the method)", gotMethod)
	}
	if !strings.Contains(gotBody, `"note":"on"`) {
		t.Errorf("target body = %q, want the JSON payload preserved", gotBody)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("target Authorization = %q, want the bearer preserved on the same host", gotAuth)
	}
}

// TestPreserveMethodOnRedirectStripsAuthCrossHost guards the token: a redirect
// that leaves the original host must not carry the Authorization header along.
func TestPreserveMethodOnRedirectStripsAuthCrossHost(t *testing.T) {
	orig, err := http.NewRequest(http.MethodPost, "https://forgekey.example.com/a/", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	orig.Header.Set("Authorization", "Bearer secret")

	next, err := http.NewRequest(http.MethodGet, "https://evil.example.net/a/", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := preserveMethodOnRedirect(next, []*http.Request{orig}); err != nil {
		t.Fatalf("preserveMethodOnRedirect: %v", err)
	}
	if next.Method != http.MethodPost {
		t.Errorf("method = %q, want POST preserved", next.Method)
	}
	if got := next.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization leaked cross-host: %q", got)
	}
}
