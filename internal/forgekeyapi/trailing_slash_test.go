package forgekeyapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestActionPathsEndWithSlash guards every forgekey @action client call against
// the trailing-slash regression (sc-amjg): the backend is DRF with a
// DefaultRouter (routes registered WITH a trailing slash) + APPEND_SLASH, so a
// POST to an un-slashed action 301-redirects to the slashed route and net/http
// downgrades the redirected POST to a GET, dropping the body — the action then
// silently no-ops. Each method must post/get the slashed path the web api.ts
// uses. The captured path is asserted both exact and ends-with-"/" so a future
// dropped slash fails loudly.
func TestActionPathsEndWithSlash(t *testing.T) {
	cases := []struct {
		name string
		want string
		call func(ctx context.Context, c *Client) error
	}{
		{"RevokeAuthorization", "/api/forgekey/authorizations/a1/revoke/", func(ctx context.Context, c *Client) error {
			return c.RevokeAuthorization(ctx, "a1")
		}},
		{"Unlock", "/api/forgekey/lockouts/l1/unlock/", func(ctx context.Context, c *Client) error {
			return c.Unlock(ctx, "l1")
		}},
		{"EnableClassroomMode", "/api/forgekey/operational-modes/o1/enable_classroom_mode/", func(ctx context.Context, c *Client) error {
			return c.EnableClassroomMode(ctx, "o1")
		}},
		{"DisableClassroomMode", "/api/forgekey/operational-modes/o1/disable_classroom_mode/", func(ctx context.Context, c *Client) error {
			return c.DisableClassroomMode(ctx, "o1")
		}},
		{"EndSession", "/api/forgekey/usage/u1/end_session/", func(ctx context.Context, c *Client) error {
			return c.EndSession(ctx, "u1")
		}},
		{"EnableDevice", "/api/forgekey/devices/d1/enable/", func(ctx context.Context, c *Client) error {
			return c.EnableDevice(ctx, "d1")
		}},
		{"DisableDevice", "/api/forgekey/devices/d1/disable/", func(ctx context.Context, c *Client) error {
			return c.DisableDevice(ctx, "d1", DisableRequest{DelaySeconds: 30})
		}},
		{"SetRelayChannel", "/api/forgekey/devices/d1/relay-channel/", func(ctx context.Context, c *Client) error {
			return c.SetRelayChannel(ctx, "d1", RelayChannelRequest{Channel: 1, On: true})
		}},
		{"RequestStatus", "/api/forgekey/devices/d1/status/", func(ctx context.Context, c *Client) error {
			return c.RequestStatus(ctx, "d1")
		}},
		{"IdentifyDevice", "/api/forgekey/devices/d1/command/identify/", func(ctx context.Context, c *Client) error {
			return c.IdentifyDevice(ctx, "d1", IdentifyRequest{DurationS: 5})
		}},
		{"RestartDevice", "/api/forgekey/devices/d1/command/restart/", func(ctx context.Context, c *Client) error {
			return c.RestartDevice(ctx, "d1")
		}},
		{"PingDevice", "/api/forgekey/devices/d1/command/ping/", func(ctx context.Context, c *Client) error {
			return c.PingDevice(ctx, "d1")
		}},
		{"BlinkDevice", "/api/forgekey/devices/d1/command/blink/", func(ctx context.Context, c *Client) error {
			return c.BlinkDevice(ctx, "d1", BlinkRequest{Pattern: "sos"})
		}},
		{"UpdateDeviceFirmware", "/api/forgekey/devices/d1/command/firmware-update/", func(ctx context.Context, c *Client) error {
			return c.UpdateDeviceFirmware(ctx, "d1", FirmwareUpdateRequest{Version: "1.2.3"})
		}},
		{"RecentCommands", "/api/forgekey/devices/d1/recent-commands/", func(ctx context.Context, c *Client) error {
			_, err := c.RecentCommands(ctx, "d1", 5)
			return err
		}},
		{"DeviceOccupancy", "/api/forgekey/devices/d1/occupancy/", func(ctx context.Context, c *Client) error {
			_, err := c.DeviceOccupancy(ctx, "d1", "")
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				// {} decodes into every out shape here: a nil-out action ignores
				// it, MaybeList falls back to the empty envelope, and
				// OccupancyResponse is all-zero.
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			c, err := New(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := tc.call(context.Background(), c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !strings.HasSuffix(gotPath, "/") {
				t.Errorf("%s path = %q, must end with a trailing slash", tc.name, gotPath)
			}
			if gotPath != tc.want {
				t.Errorf("%s path = %q, want %q", tc.name, gotPath, tc.want)
			}
		})
	}
}
