// Pi-side proxy for the Dallas-Makerspace Common API.
//
// The Common API exposes a badge → identity endpoint
// (POST /api/v1/lookupByRfid). It lives on the LAN, so OMS can't
// reach it from the public DMZ. The Raspberry Pi running this
// daemon is already inside the firewall (that's how it prints
// labels), so we let it double as a thin HTTP proxy.
//
// OMS settings ``COMMON_API_PROXY_URL`` should point at this
// listener's ``/resolve`` endpoint; ``COMMON_API_PROXY_TOKEN`` is
// the bearer it sends, and must match the env var of the same name
// on the Pi side.
//
// The proxy is opt-in: set ``COMMON_API_PROXY_LISTEN`` (e.g.
// ``:8083``) and ``COMMON_API_URL``. If either is empty the proxy
// stays off and the daemon behaves like before (poll-only).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultProxyTimeout = 10 * time.Second
	// Cap incoming request bodies so a runaway client can't pin the
	// Pi's tiny RAM. The real payload is a single RFID number — a few
	// dozen bytes — so 4 KiB is way more than needed.
	maxRequestBytes int64 = 4 * 1024
)

type proxyConfig struct {
	listen        string
	upstream      string
	token         string
	requestPause  time.Duration // upstream timeout
}

// resolveRequest is the JSON shape OMS sends. Mirrors the kiosk's call.
type resolveRequest struct {
	RFID string `json:"rfid"`
}

// startProxy spins up the HTTP listener on a background goroutine and
// returns once the listener is bound (so config errors surface before
// main() blocks on its poll loop). The returned shutdown func should
// be called on SIGTERM.
//
// Errors during request handling are logged, not propagated — the
// daemon's primary job is the print queue and one bad lookup must
// not take it down.
func startProxy(ctx context.Context, cfg proxyConfig) (shutdown func(context.Context) error, err error) {
	if cfg.listen == "" || cfg.upstream == "" {
		// Opt-out: no listener configured.
		return func(context.Context) error { return nil }, nil
	}

	if cfg.requestPause <= 0 {
		cfg.requestPause = defaultProxyTimeout
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/resolve", proxyHandler(cfg))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})

	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.requestPause + 2*time.Second,
		WriteTimeout:      cfg.requestPause + 2*time.Second,
		IdleTimeout:       30 * time.Second,
	}

	log.Printf("common-api proxy: listening on %s → %s", cfg.listen, cfg.upstream)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("common-api proxy: ListenAndServe: %v", err)
		}
	}()

	return srv.Shutdown, nil
}

// proxyHandler returns the /resolve handler closed over the proxy
// config. Split out so a unit test can exercise it without standing
// up a full HTTP server.
func proxyHandler(cfg proxyConfig) http.HandlerFunc {
	httpClient := &http.Client{Timeout: cfg.requestPause}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if cfg.token != "" {
			auth := r.Header.Get("Authorization")
			expected := "Bearer " + cfg.token
			if auth != expected {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}

		var req resolveRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes))
		if err := dec.Decode(&req); err != nil {
			http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
			return
		}
		if req.RFID == "" {
			http.Error(w, `{"error":"rfid_required"}`, http.StatusBadRequest)
			return
		}

		// Upstream wants form-encoded ``rfid=<value>``. The kiosk does
		// the same dance — see ad-lookup-kiosk views.py.
		form := url.Values{}
		form.Set("rfid", req.RFID)
		upstreamReq, err := http.NewRequestWithContext(
			r.Context(), http.MethodPost, cfg.upstream, strings.NewReader(form.Encode()),
		)
		if err != nil {
			log.Printf("common-api proxy: build request: %v", err)
			http.Error(w, `{"error":"upstream_build_failed"}`, http.StatusBadGateway)
			return
		}
		upstreamReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := httpClient.Do(upstreamReq)
		if err != nil {
			log.Printf("common-api proxy: upstream %s: %v", cfg.upstream, err)
			http.Error(w, `{"error":"upstream_unreachable"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			log.Printf("common-api proxy: read upstream body: %v", err)
			http.Error(w, `{"error":"upstream_read_failed"}`, http.StatusBadGateway)
			return
		}

		// Pass the upstream status + body through verbatim so OMS can
		// disambiguate "no match" (200 with no user) from "AD down"
		// (5xx). We force JSON content-type because the upstream
		// sometimes mislabels with ``text/plain``.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(body); err != nil {
			log.Printf("common-api proxy: write response: %v", err)
		}
	}
}

// proxyConfigFromEnv reads the optional env triple. Returns a zero
// config when ``COMMON_API_PROXY_LISTEN`` is unset so the caller can
// skip startup without special-casing.
func proxyConfigFromEnv(env func(string) string) proxyConfig {
	return proxyConfig{
		listen:   env("COMMON_API_PROXY_LISTEN"),
		upstream: env("COMMON_API_URL"),
		token:    env("COMMON_API_PROXY_TOKEN"),
	}
}

// describeProxyConfig formats the active settings for the startup
// log without leaking the bearer token.
func describeProxyConfig(cfg proxyConfig) string {
	tokenState := "no token (open)"
	if cfg.token != "" {
		tokenState = "bearer required"
	}
	return fmt.Sprintf("listen=%s upstream=%s auth=%s", cfg.listen, cfg.upstream, tokenState)
}
