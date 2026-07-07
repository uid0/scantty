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

// TestGetDeviceDecodesLiveSubState feeds the op-2cr live sub-state the __all__
// serializer now exposes on device detail: relay_channels (list of
// {channel,on}) and indicator_state ({color,pattern}). Confirms both decode.
func TestGetDeviceDecodesLiveSubState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"dev-1","name":"Relay A","mac_address":"AA:BB:CC:DD:EE:01",
			"capabilities":["power_relay","status_led"],
			"relay_channels":[{"channel":1,"on":true},{"channel":2,"on":false}],
			"indicator_state":{"color":"green","pattern":"solid"}
		}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dev, err := c.GetDevice(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if len(dev.RelayChannels) != 2 {
		t.Fatalf("relay channels = %d, want 2", len(dev.RelayChannels))
	}
	if dev.RelayChannels[0].Channel != 1 || !dev.RelayChannels[0].On {
		t.Errorf("ch1 = %+v, want {Channel:1 On:true}", dev.RelayChannels[0])
	}
	if dev.RelayChannels[1].Channel != 2 || dev.RelayChannels[1].On {
		t.Errorf("ch2 = %+v, want {Channel:2 On:false}", dev.RelayChannels[1])
	}
	if dev.IndicatorState.Color != "green" || dev.IndicatorState.Pattern != "solid" {
		t.Errorf("indicator = %+v, want {Color:green Pattern:solid}", dev.IndicatorState)
	}
}

// TestGetDeviceHandlesEmptyLiveSubState: before a device reports, relay_channels
// is an empty list and indicator_state's color/pattern arrive as JSON null. Both
// must decode gracefully (no error, empty values) so the detail can render "—".
func TestGetDeviceHandlesEmptyLiveSubState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"dev-2","name":"Relay B","mac_address":"AA:BB:CC:DD:EE:02",
			"relay_channels":[],"indicator_state":{"color":null,"pattern":null}
		}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dev, err := c.GetDevice(context.Background(), "dev-2")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if len(dev.RelayChannels) != 0 {
		t.Errorf("relay channels = %d, want 0", len(dev.RelayChannels))
	}
	if dev.IndicatorState.Color != "" || dev.IndicatorState.Pattern != "" {
		t.Errorf("indicator = %+v, want empty", dev.IndicatorState)
	}
}

// TestIndicatorTest_PostsBodyAndPath verifies the indicator preview POSTs to
// the trailing-slash device action path and that omitempty drops the fields the
// caller left blank (color for an "off" pattern, period_ms for a steady one).
func TestIndicatorTest_PostsBodyAndPath(t *testing.T) {
	t.Run("blink sends color+period", func(t *testing.T) {
		var gotPath, gotMethod string
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotMethod = r.URL.Path, r.Method
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"indicator_test command sent","device":"AA:BB","command_id":"cmd-9","payload":{"pattern":"blink"}}`))
		}))
		defer srv.Close()
		c, err := New(Options{BaseURL: srv.URL})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		resp, err := c.IndicatorTest(context.Background(), "dev-1", IndicatorTestRequest{
			Color: "green", Brightness: "high", Pattern: "blink", PeriodMS: 1500,
		})
		if err != nil {
			t.Fatalf("IndicatorTest: %v", err)
		}
		if gotMethod != http.MethodPost || gotPath != "/api/forgekey/devices/dev-1/indicator/test/" {
			t.Fatalf("%s %s, want POST /api/forgekey/devices/dev-1/indicator/test/", gotMethod, gotPath)
		}
		if gotBody["color"] != "green" || gotBody["brightness"] != "high" || gotBody["pattern"] != "blink" {
			t.Errorf("body = %v", gotBody)
		}
		if gotBody["period_ms"] != float64(1500) {
			t.Errorf("period_ms = %v, want 1500", gotBody["period_ms"])
		}
		if resp.CommandID != "cmd-9" {
			t.Errorf("command_id = %q", resp.CommandID)
		}
	})

	t.Run("off omits color and period", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"command_id":"cmd-10","payload":{}}`))
		}))
		defer srv.Close()
		c, err := New(Options{BaseURL: srv.URL})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.IndicatorTest(context.Background(), "dev-1", IndicatorTestRequest{
			Brightness: "low", Pattern: "off",
		}); err != nil {
			t.Fatalf("IndicatorTest: %v", err)
		}
		if _, present := gotBody["color"]; present {
			t.Errorf("color must be omitted for an off pattern, got %v", gotBody["color"])
		}
		if _, present := gotBody["period_ms"]; present {
			t.Errorf("period_ms must be omitted for a steady pattern, got %v", gotBody["period_ms"])
		}
		if gotBody["brightness"] != "low" || gotBody["pattern"] != "off" {
			t.Errorf("body = %v", gotBody)
		}
	})
}

// TestUpdateDevice_PatchesLocation verifies the record-edit PATCH hits the
// device-detail endpoint with the {location} body the web inline editor sends,
// and decodes the refreshed device back.
func TestUpdateDevice_PatchesLocation(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev-1","name":"Relay A","mac_address":"AA:BB:CC:DD:EE:01","location":7}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	loc := 7
	dev, err := c.UpdateDevice(context.Background(), "dev-1", DeviceWrite{Location: &loc})
	if err != nil {
		t.Fatalf("UpdateDevice: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if want := "/api/forgekey/devices/dev-1/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody["location"] != float64(7) {
		t.Errorf("location = %v, want 7", gotBody["location"])
	}
	if dev == nil || dev.Location == nil || *dev.Location != 7 {
		t.Errorf("decoded device location = %+v, want 7", dev)
	}
}

// TestUpdateDevice_ClearsLocationWithExplicitNull pins the no-omitempty contract:
// clearing the location must serialize {location: null} (key present) so the
// backend actually unassigns the FK. With omitempty the key would drop and
// "unassign" would silently no-op.
func TestUpdateDevice_ClearsLocationWithExplicitNull(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev-1","name":"Relay A","mac_address":"AA:BB:CC:DD:EE:01"}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.UpdateDevice(context.Background(), "dev-1", DeviceWrite{Location: nil}); err != nil {
		t.Fatalf("UpdateDevice: %v", err)
	}
	if _, present := gotBody["location"]; !present {
		t.Errorf("location key must be present as explicit null, body = %v", gotBody)
	}
	if gotBody["location"] != nil {
		t.Errorf("location = %v, want null", gotBody["location"])
	}
}

// TestDeleteDevice_DeletesPath verifies delete hits DELETE on the device-detail
// endpoint and tolerates the 204 No Content the viewset returns.
func TestDeleteDevice_DeletesPath(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.DeleteDevice(context.Background(), "dev-1"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if want := "/api/forgekey/devices/dev-1/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// TestUpdateDevice_SurfacesBackendError confirms a 4xx becomes a Go error the
// TUI can show instead of crashing.
func TestUpdateDevice_SurfacesBackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"location":["Invalid pk \"999\" - object does not exist."]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	loc := 999
	if _, err := c.UpdateDevice(context.Background(), "dev-1", DeviceWrite{Location: &loc}); err == nil {
		t.Fatalf("expected an error for a 400 response")
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
