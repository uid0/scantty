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
