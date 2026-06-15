package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureForm is a tiny stand-in for the real DMS Common API: it
// records the inbound form, returns whatever the test wants the
// upstream to say. Lets us prove our forwarding shape without
// hitting the LAN.
type captureForm struct {
	rfid          string
	contentType   string
	respStatus    int
	respBody      string
	authorization string
}

func newUpstream(t *testing.T, c *captureForm) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("upstream got method=%q, want POST", r.Method)
		}
		c.contentType = r.Header.Get("Content-Type")
		c.authorization = r.Header.Get("Authorization")
		if err := r.ParseForm(); err != nil {
			t.Errorf("upstream ParseForm: %v", err)
		}
		c.rfid = r.PostForm.Get("rfid")
		if c.respStatus == 0 {
			c.respStatus = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(c.respStatus)
		_, _ = io.WriteString(w, c.respBody)
	}))
}

func TestProxyForwardsRfidAsForm(t *testing.T) {
	cap := &captureForm{
		respBody: `{"result":{"user":{"username":"ada"}}}`,
	}
	upstream := newUpstream(t, cap)
	defer upstream.Close()

	h := proxyHandler(proxyConfig{upstream: upstream.URL})
	req := httptest.NewRequest(
		http.MethodPost, "/resolve", bytes.NewReader([]byte(`{"rfid":"12345678"}`)),
	)
	w := httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if cap.rfid != "12345678" {
		t.Errorf("upstream got rfid=%q want %q", cap.rfid, "12345678")
	}
	if !strings.HasPrefix(cap.contentType, "application/x-www-form-urlencoded") {
		t.Errorf("upstream content-type=%q want form-urlencoded", cap.contentType)
	}
	if got := w.Body.String(); !strings.Contains(got, `"username":"ada"`) {
		t.Errorf("response body missing upstream payload: %s", got)
	}
}

func TestProxyRejectsNonPost(t *testing.T) {
	h := proxyHandler(proxyConfig{upstream: "http://unused"})
	req := httptest.NewRequest(http.MethodGet, "/resolve", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d want 405", w.Code)
	}
}

func TestProxyRejectsMissingRfid(t *testing.T) {
	h := proxyHandler(proxyConfig{upstream: "http://unused"})
	req := httptest.NewRequest(http.MethodPost, "/resolve", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", w.Code)
	}
}

func TestProxyEnforcesBearerWhenTokenSet(t *testing.T) {
	cap := &captureForm{respBody: `{}`}
	upstream := newUpstream(t, cap)
	defer upstream.Close()

	h := proxyHandler(proxyConfig{upstream: upstream.URL, token: "shh"})

	// Missing bearer → 401.
	req := httptest.NewRequest(
		http.MethodPost, "/resolve", strings.NewReader(`{"rfid":"1"}`),
	)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no-auth status=%d want 401", w.Code)
	}

	// Wrong bearer → 401.
	req = httptest.NewRequest(
		http.MethodPost, "/resolve", strings.NewReader(`{"rfid":"1"}`),
	)
	req.Header.Set("Authorization", "Bearer nope")
	w = httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bad-auth status=%d want 401", w.Code)
	}

	// Correct bearer → 200 and we did NOT forward the bearer upstream
	// (the upstream is internal and has its own auth model).
	req = httptest.NewRequest(
		http.MethodPost, "/resolve", strings.NewReader(`{"rfid":"1"}`),
	)
	req.Header.Set("Authorization", "Bearer shh")
	w = httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("good-auth status=%d want 200", w.Code)
	}
	if cap.authorization != "" {
		t.Errorf("upstream saw Authorization=%q — should not be forwarded", cap.authorization)
	}
}

func TestProxyForwardsUpstreamStatus(t *testing.T) {
	// Upstream miss (e.g. unknown badge) returns 200 with no user; we
	// pass that through as-is so OMS can disambiguate.
	cap := &captureForm{respStatus: http.StatusOK, respBody: `{"result":{}}`}
	upstream := newUpstream(t, cap)
	defer upstream.Close()

	h := proxyHandler(proxyConfig{upstream: upstream.URL})
	req := httptest.NewRequest(
		http.MethodPost, "/resolve", strings.NewReader(`{"rfid":"99"}`),
	)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"result":{}`) {
		t.Errorf("body lost upstream miss shape: %s", w.Body.String())
	}
}

func TestProxyForwardsUpstreamFailureAsBadGateway(t *testing.T) {
	h := proxyHandler(proxyConfig{
		upstream: "http://127.0.0.1:1/dead",
	})
	req := httptest.NewRequest(
		http.MethodPost, "/resolve", strings.NewReader(`{"rfid":"1"}`),
	)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("dead-upstream status=%d want 502", w.Code)
	}
}

func TestProxyConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"COMMON_API_PROXY_LISTEN": ":8083",
		"COMMON_API_URL":          "http://upstream.local",
		"COMMON_API_PROXY_TOKEN":  "shh",
	}
	cfg := proxyConfigFromEnv(func(k string) string { return env[k] })
	if cfg.listen != ":8083" || cfg.upstream != "http://upstream.local" || cfg.token != "shh" {
		t.Errorf("loaded cfg=%+v", cfg)
	}
}

func TestProxyConfigFromEnvEmpty(t *testing.T) {
	cfg := proxyConfigFromEnv(func(string) string { return "" })
	if cfg.listen != "" || cfg.upstream != "" || cfg.token != "" {
		t.Errorf("empty env produced non-zero cfg=%+v", cfg)
	}
}
