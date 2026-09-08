package forgekeyapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An `any`-typed ForgeKey id must survive as the DIGITS the server sent, all the
// way to the URL path segment an action is spent on.
//
// THIS PACKAGE TALKS TO OPENMAKERSUITE. `/api/forgekey/...` is served by OMS's
// own `backend/forgekey` Django app — uid0/ForgeKey is the C++ device operating
// system, not the HTTP API — so these ids are decided by the same serializers
// every other ScanTTY wire type is, and the class closed for `internal/omsapi`
// was open here for exactly as long as "a different server" stood as the reason
// to skip it.
//
// The failure: several ids are typed `any` so the client carries whatever the
// serializer echoed, and the screens render them with fmt.Sprint. Decoded the
// default way a JSON number lands in an `any` as a float64 and fmt uses %g, so a
// seven-digit pk becomes "1e+06". Measured against a live backend with the
// correct id as the control, `.../operational-modes/1000000/enable_classroom_mode/`
// answers 200 and `.../1e+06/enable_classroom_mode/` answers 404 — an action on
// a row that exists, aimed at one that does not, with nothing on the pane saying
// the id was mangled.
//
// WHY THESE TWO AND NOT THE OTHERS. Of the ForgeKey models ScanTTY spends as a
// path segment, only AssetAuthorization and OperationalMode carry Django's
// implicit BigAutoField. ESP32Device, DeviceLockout, DeviceUsage, EPaperDisplay
// and FirmwareRollout all declare `id = models.UUIDField(primary_key=True)`, so
// their ids are strings on the wire and fmt.Sprint was never able to mangle one
// — asserting a numeric id for a rollout would be testing a payload the server
// cannot send. That derivation comes from reading each model class WHOLE:
// FirmwareRollout's `id =` line sits 26 lines into its class, so a grep window
// around the class name reports it as an integer pk and produces exactly the
// wrong set.
//
// BOTH LIST SHAPES ARE DRIVEN because both endpoints decode through MaybeList,
// whose UnmarshalJSON is handed raw bytes and so bypasses any outer decoder —
// the hole that made this reachable even once do() was fixed.
func TestAnyID_AForgeKeyNumericIDKeepsItsDigits(t *testing.T) {
	// Seven digits is where %g switches to exponent form. The small ids are here
	// so the boundary is exercised rather than assumed.
	ids := []int64{1, 42, 999999, 1000000, 1234567, 21000000, 900719925474099}

	shapes := map[string]string{
		"bare array": `[%s]`,
		"envelope":   `{"count": 1, "next": null, "previous": null, "results": [%s]}`,
	}

	cases := []struct {
		name string
		row  string
		// act lists through the real client, renders the row's id the way the
		// screen does, and spends it on the action's URL.
		act func(ctx context.Context, c *Client) error
		// path is what the server must receive for that id.
		path func(id string) string
	}{
		{
			name: "operational mode classroom mode",
			row:  `{"id": %d, "asset": "a-1", "mode": "normal", "classroom_mode_enabled": false}`,
			act: func(ctx context.Context, c *Client) error {
				rows, err := c.ListOperationalModes(ctx, nil)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return fmt.Errorf("rows = %d, want 1", len(rows))
				}
				// internal/tui/op_modes.go's `c` arm, verbatim.
				return c.EnableClassroomMode(ctx, fmt.Sprint(rows[0].ID))
			},
			path: func(id string) string {
				return "/api/forgekey/operational-modes/" + id + "/enable_classroom_mode/"
			},
		},
		{
			name: "authorization revoke",
			row:  `{"id": %d, "asset": "a-1", "user": 7, "is_active": true}`,
			act: func(ctx context.Context, c *Client) error {
				rows, err := c.ListAuthorizations(ctx, nil)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return fmt.Errorf("rows = %d, want 1", len(rows))
				}
				// internal/tui/auth_lockout.go's revoke confirm, verbatim.
				return c.RevokeAuthorization(ctx, fmt.Sprint(rows[0].ID), "")
			},
			path: func(id string) string {
				return "/api/forgekey/authorizations/" + id + "/revoke/"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for shape, envelope := range shapes {
				t.Run(shape, func(t *testing.T) {
					for _, id := range ids {
						t.Run(fmt.Sprint(id), func(t *testing.T) {
							var actionPath string
							srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								w.Header().Set("Content-Type", "application/json")
								if r.Method == http.MethodPost {
									actionPath = r.URL.Path
									_, _ = w.Write([]byte(`{}`))
									return
								}
								fmt.Fprintf(w, envelope, fmt.Sprintf(tc.row, id))
							}))
							defer srv.Close()

							c, err := New(Options{BaseURL: srv.URL})
							if err != nil {
								t.Fatalf("client: %v", err)
							}
							if err := tc.act(context.Background(), c); err != nil {
								t.Fatalf("act: %v", err)
							}
							if want := tc.path(fmt.Sprint(id)); actionPath != want {
								t.Errorf("the action reached %q, want %q — the id is spent "+
									"as a path segment, so a mangled one aims an operator's "+
									"keypress at a row that does not exist", actionPath, want)
							}
						})
					}
				})
			}
		})
	}
}

// The same property through do()'s OWN decode, which MaybeList sits in front of.
//
// Both live sites above are list-fed, so a test driving only them passes with
// do() still on a bare json.NewDecoder — the option would be set at one of the
// two places it has to be. GrantAuthorization decodes a single Authorization
// straight through do(), so this is the half of the choke point the list test
// cannot reach.
func TestAnyID_AForgeKeySingleObjectKeepsItsDigits(t *testing.T) {
	for _, id := range []int64{1, 42, 999999, 1000000, 1234567, 21000000, 900719925474099} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id": %d, "asset": "a-1", "user": 7, "is_active": true}`, id)
			}))
			defer srv.Close()

			c, err := New(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("client: %v", err)
			}
			got, err := c.GrantAuthorization(context.Background(),
				AuthorizationWrite{Asset: "a-1", User: 7})
			if err != nil {
				t.Fatalf("grant: %v", err)
			}
			want := fmt.Sprint(id)
			if rendered := fmt.Sprint(got.ID); rendered != want {
				t.Errorf("the granted authorization's id renders %q, want %q — this is "+
					"the value auth_lockout.go spends on the revoke path", rendered, want)
			}
		})
	}
}
