package forgekeyapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSetRelayChannel_PostsChannelAndAction verifies the per-channel power-relay
// control (ga-40w) POSTs to the OMS relay-channel endpoint with the channel + on
// body the backend turns into a signed power_set command.
func TestSetRelayChannel_PostsChannelAndAction(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody RelayChannelRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.SetRelayChannel(context.Background(), "dev-1", RelayChannelRequest{Channel: 2, On: false}); err != nil {
		t.Fatalf("SetRelayChannel: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/api/forgekey/devices/dev-1/relay-channel"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody.Channel != 2 || gotBody.On {
		t.Errorf("body = %+v, want {Channel:2 On:false}", gotBody)
	}
}

// TestRecentCommandsDecodesSentByShape feeds the REAL DeviceCommandSerializer
// shape: the fields are sent_by (integer user FK) / sent_by_username / sent_at
// / ack_status / ack_at — the old issued_by(string)/issued_at/status tags
// matched none of them, so the recent-commands table rendered blank rows.
func TestRecentCommandsDecodesSentByShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[{
			"id":"d1d1d1d1-1111-2222-3333-444444444444","command":"restart",
			"sent_by":5,"sent_by_username":"ada","sent_at":"2026-01-02T03:04:05Z",
			"ack_status":"pending","effective_ack_status":"acked","ack_at":null,"ack_payload":{}
		}]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmds, err := c.RecentCommands(context.Background(), "dev-1", 10)
	if err != nil {
		t.Fatalf("RecentCommands: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("commands = %d, want 1", len(cmds))
	}
	cmd := cmds[0]
	if cmd.Command != "restart" || cmd.SentByUsername != "ada" || cmd.SentAt.IsZero() || cmd.EffectiveAckStatus != "acked" {
		t.Fatalf("unexpected command: %+v", cmd)
	}
}
