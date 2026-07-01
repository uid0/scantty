package omsapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPostSurvivesRedirect reproduces the claim-tag printer bug: an http://
// base 301-upgrades to https, and net/http's default redirect policy would
// downgrade the POST to a GET (dropping the body). The client must instead
// arrive at the redirect target still as a POST, body intact.
func TestPostSurvivesRedirect(t *testing.T) {
	var gotMethod, gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/mark/" {
			http.Redirect(w, r, "/api/mark-final/", http.StatusMovedPermanently)
			return
		}
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("tok-123", ""))
	if err := c.Post(context.Background(), "/api/mark/", map[string]string{"note": "printed"}, nil); err != nil {
		t.Fatalf("Post across redirect: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("target method = %q, want POST (redirect downgraded the method)", gotMethod)
	}
	if !strings.Contains(gotBody, `"note":"printed"`) {
		t.Errorf("target body = %q, want the JSON payload preserved", gotBody)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("target Authorization = %q, want the bearer preserved on the same host", gotAuth)
	}
}

// TestPreserveMethodOnRedirectStripsAuthCrossHost guards the token: a redirect
// that leaves the original host must not carry the Authorization header along.
func TestPreserveMethodOnRedirectStripsAuthCrossHost(t *testing.T) {
	orig, err := http.NewRequest(http.MethodPost, "https://oms.example.com/a/", strings.NewReader("{}"))
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
