package omsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Decode and request-shape tests for the four asset interlock endpoints, built
// from RECORDED OMS responses.
//
// The rule is testdata/README.md's: a fixture written by whoever wrote the struct
// agrees with the struct by construction and cannot report a disagreement with
// the server. Every body here came off a real backend on PostgreSQL, and the
// guards below read the RAW bytes alongside the decode, so a later edit "fixing"
// a fixture to match a struct fails rather than quietly restoring a defect.

// interlockServer answers one request with a recorded body at a recorded status,
// and hands back what the client actually SENT — the method, the path and the
// body — because on these four endpoints the request is half of what is being
// checked. lock must carry a reason and the other three must carry no body at
// all.
// The two pointers are filled in by the time any client call returns; the
// indirection is only so the caller can read them AFTER the call rather than
// during it.
func interlockServer(t *testing.T, status int, body []byte) (*Client, **http.Request, *string) {
	t.Helper()
	var got *http.Request
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = readAll(t, r)
		got = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &got, &sent
}

// rawAsset pulls the fields under test straight out of the fixture's bytes, so
// every assertion below compares the decode against the SERVER's own answer
// rather than against a second copy of the expectation.
func rawAsset(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// The requests
// ---------------------------------------------------------------------------

// LOCK CARRIES A REASON, always and as a plain key. The server reads
// `request.data.get("reason", "")` and refuses a falsy one, so an omitted key and
// an empty one are the same 400 — there is nothing for `omitempty` to protect and
// a request that dropped the key would be the web's broken Lock button (which
// posts no body at all and therefore always 400s).
func TestLockAsset_SendsTheReasonTheServerRequires(t *testing.T) {
	body := wireBody(t, "asset_lock.json")
	c, got, sent := interlockServer(t, http.StatusCreated, body)

	if _, err := c.LockAsset(context.Background(), "233eeb12-775f-44a3-9a6c-8cd28e35acd7",
		"spindle bearing seized"); err != nil {
		t.Fatalf("a recorded 201 did not decode: %v", err)
	}

	if (*got).Method != http.MethodPost {
		t.Errorf("method = %s, want POST", (*got).Method)
	}
	const want = "/api/inventory/assets/233eeb12-775f-44a3-9a6c-8cd28e35acd7/lock/"
	if (*got).URL.Path != want {
		t.Errorf("path = %q, want %q", (*got).URL.Path, want)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(*sent), &req); err != nil {
		t.Fatalf("lock body %q is not JSON: %v", *sent, err)
	}
	if req["reason"] != "spindle bearing seized" {
		t.Errorf("reason sent = %v, want the caller's sentence", req["reason"])
	}
	if len(req) != 1 {
		t.Errorf("lock sent %d keys (%v); the endpoint reads only `reason`, and a "+
			"key the server ignores is a claim about a contract it does not have", len(req), req)
	}
}

// THE OTHER THREE SEND NO BODY. Each reads nothing off request.data, so a key
// invented here would be a fiction about the endpoint — and `unlock` in
// particular takes no reason, which is a fact about the record: the close is
// stamped by the server from the authenticated user, not described by the caller.
func TestAssetInterlock_TheBodylessThreeSendNoBody(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture string
		path    string
		call    func(*Client, string) (*Asset, error)
	}{
		{"unlock", "asset_unlock.json", "unlock", func(c *Client, id string) (*Asset, error) {
			return c.UnlockAsset(context.Background(), id)
		}},
		{"disable", "asset_disable.json", "disable", func(c *Client, id string) (*Asset, error) {
			return c.DisableAsset(context.Background(), id)
		}},
		{"enable", "asset_enable.json", "enable", func(c *Client, id string) (*Asset, error) {
			return c.EnableAsset(context.Background(), id)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, got, sent := interlockServer(t, http.StatusOK, wireBody(t, tc.fixture))
			const id = "233eeb12-775f-44a3-9a6c-8cd28e35acd7"
			if _, err := tc.call(c, id); err != nil {
				t.Fatalf("a recorded 200 did not decode: %v", err)
			}
			if (*got).Method != http.MethodPost {
				t.Errorf("method = %s, want POST", (*got).Method)
			}
			want := "/api/inventory/assets/" + id + "/" + tc.path + "/"
			if (*got).URL.Path != want {
				t.Errorf("path = %q, want %q", (*got).URL.Path, want)
			}
			if body := strings.TrimSpace(*sent); body != "" && body != "null" {
				t.Errorf("%s sent a body %q; this endpoint reads none", tc.name, body)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The decodes, and the independence of the two axes
// ---------------------------------------------------------------------------

// LOCKING DOES NOT DISABLE, and this is the recorded proof of it rather than a
// reading of the view. The lock reply carries is_locked true AND is_active true,
// with the mode at locked_out — so a screen that presented the two as one
// "out of service" state would be describing an asset the server does not have.
func TestLockAsset_LeavesTheAssetActiveAndNamesTheLockout(t *testing.T) {
	body := wireBody(t, "asset_lock.json")
	c, _, _ := interlockServer(t, http.StatusCreated, body)
	a, err := c.LockAsset(context.Background(), "a1", "spindle bearing seized")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	raw := rawAsset(t, body)

	if !a.IsLocked {
		t.Error("IsLocked decoded false from a reply whose is_locked is true")
	}
	if raw["is_locked"] != true {
		t.Fatal("fixture no longer records a LOCKED asset, so this test proves nothing " +
			"about locking — re-record it rather than editing it")
	}
	if !a.IsActive {
		t.Error("IsActive decoded false; the recorded lock reply leaves is_active TRUE, " +
			"which is the whole distinction between locking and disabling")
	}
	if raw["is_active"] != true {
		t.Fatal("fixture no longer records an ACTIVE asset after a lock; that pairing is " +
			"the property under test")
	}
	if a.LockoutInfo == nil {
		t.Fatal("LockoutInfo is nil; the reply names the lockout and the unlock confirm " +
			"is built out of it")
	}
	wantInfo, _ := raw["lockout_info"].(map[string]any)
	if a.LockoutInfo.Reason != wantInfo["reason"] {
		t.Errorf("lockout reason = %q, want %q", a.LockoutInfo.Reason, wantInfo["reason"])
	}
	if a.LockoutInfo.LockedBy != wantInfo["locked_by"] {
		t.Errorf("locked_by = %q, want %q", a.LockoutInfo.LockedBy, wantInfo["locked_by"])
	}
	if a.LockoutInfo.LockoutLevel != wantInfo["lockout_level"] {
		t.Errorf("lockout_level = %q, want %q", a.LockoutInfo.LockoutLevel, wantInfo["lockout_level"])
	}
	if mode := assetMode(a); mode != "locked_out" {
		t.Errorf("operational mode = %q, want locked_out", mode)
	}
}

// DISABLING DOES NOT UNLOCK. The mirror of the above, off the reply recorded
// against an asset that was locked when it was disabled: is_active false with
// is_locked still true.
func TestDisableAsset_LeavesAnyLockoutInPlace(t *testing.T) {
	body := wireBody(t, "asset_disable.json")
	c, _, _ := interlockServer(t, http.StatusOK, body)
	a, err := c.DisableAsset(context.Background(), "a1")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	raw := rawAsset(t, body)
	if raw["is_active"] != false || raw["is_locked"] != true {
		t.Fatal("fixture no longer records a DISABLED-and-still-LOCKED asset, which is " +
			"the property under test — re-record it rather than editing it")
	}
	if a.IsActive {
		t.Error("IsActive decoded true from a reply whose is_active is false")
	}
	if !a.IsLocked {
		t.Error("IsLocked decoded false; disabling leaves the lockout alone and the " +
			"machine still denied")
	}
}

// AN UNLOCK THAT ANSWERS 200 CAN LEAVE THE ASSET LOCKED, and this is the reply
// that proves it: recorded with two lockouts stacked, the view cleared one and
// answered 200 with is_locked STILL TRUE.
//
// This is the single most important thing a caller must not predict. A screen
// that reported "unlocked" off the status code would tell an operator a machine
// was usable while the server still denies it.
func TestUnlockAsset_A200CanStillBeLocked(t *testing.T) {
	body := wireBody(t, "asset_unlock_still_locked.json")
	c, _, _ := interlockServer(t, http.StatusOK, body)
	a, err := c.UnlockAsset(context.Background(), "a1")
	if err != nil {
		t.Fatalf("unlock returned an error for a recorded 200: %v", err)
	}
	raw := rawAsset(t, body)
	if raw["is_locked"] != true {
		t.Fatal("fixture no longer records a 200 that stayed LOCKED; that is the entire " +
			"property under test — re-record it rather than editing it")
	}
	if !a.IsLocked {
		t.Error("IsLocked decoded false from a 200 whose is_locked is true")
	}
	if a.LockoutInfo == nil {
		t.Error("LockoutInfo is nil; the remaining lockout is what the operator has to be " +
			"told about")
	}
	if mode := assetMode(a); mode != "locked_out" {
		t.Errorf("operational mode = %q, want locked_out — the mode only falls back to "+
			"available when no lockout remains", mode)
	}
}

// AND THE CLEAN ONE COMES BACK CLEAN — without re-enabling a disabled asset.
// The recorded reply is an unlock of an asset that had been disabled meanwhile,
// so it pins the third direction of independence: is_locked false, is_active
// still false.
func TestUnlockAsset_DoesNotReEnableADisabledAsset(t *testing.T) {
	body := wireBody(t, "asset_unlock.json")
	c, _, _ := interlockServer(t, http.StatusOK, body)
	a, err := c.UnlockAsset(context.Background(), "a1")
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	raw := rawAsset(t, body)
	if raw["is_locked"] != false || raw["is_active"] != false {
		t.Fatal("fixture no longer records an UNLOCKED-but-still-DISABLED asset, which is " +
			"the property under test")
	}
	if a.IsLocked {
		t.Error("IsLocked decoded true from a reply whose is_locked is false")
	}
	if a.IsActive {
		t.Error("IsActive decoded true; unlocking does not enable, and saying it does " +
			"would promise an operator a state the server is not in")
	}
	if a.LockoutInfo != nil {
		t.Errorf("LockoutInfo = %+v, want nil once nothing is locked", a.LockoutInfo)
	}
}

// ENABLE comes back active, and leaves the lockout question to the lock axis.
func TestEnableAsset_ComesBackActive(t *testing.T) {
	body := wireBody(t, "asset_enable.json")
	c, _, _ := interlockServer(t, http.StatusOK, body)
	a, err := c.EnableAsset(context.Background(), "a1")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if raw := rawAsset(t, body); raw["is_active"] != true {
		t.Fatal("fixture no longer records an ACTIVE asset after enable")
	}
	if !a.IsActive {
		t.Error("IsActive decoded false from a reply whose is_active is true")
	}
}

// ---------------------------------------------------------------------------
// The refusals, in both shapes the server really sends
// ---------------------------------------------------------------------------

// THE CODED ENVELOPE reaches AsLineEntryError, because parseError understands it
// and puts the code on APIError. Both of lock's and unlock's validation refusals
// are this shape, and the operator-facing sentence is what must survive.
func TestAssetInterlock_TheCodedRefusalsKeepTheServersSentence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture string
		status  int
		want    string
		call    func(*Client) error
	}{
		{
			"lock without a reason", "asset_lock_no_reason.json",
			http.StatusBadRequest, "reason is required",
			func(c *Client) error { _, err := c.LockAsset(context.Background(), "a1", ""); return err },
		},
		{
			"unlock what is not locked", "asset_unlock_not_locked.json",
			http.StatusBadRequest, "Asset is not locked",
			func(c *Client) error { _, err := c.UnlockAsset(context.Background(), "a1"); return err },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := wireBody(t, tc.fixture)
			// The fixture must really carry the coded shape, or this test is
			// checking a recogniser against a body it was not written for.
			var env struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &env) != nil || env.Error.Code == "" {
				t.Fatalf("fixture %s is not the coded envelope any more: %s", tc.fixture, body)
			}
			if env.Error.Message != tc.want {
				t.Fatalf("fixture message = %q, want %q — re-record rather than edit",
					env.Error.Message, tc.want)
			}

			c, _, _ := interlockServer(t, tc.status, body)
			err := tc.call(c)
			if err == nil {
				t.Fatal("a recorded refusal decoded as a success")
			}
			entry, ok := AsLineEntryError(err)
			if !ok {
				t.Fatalf("AsLineEntryError did not recognise the coded envelope; the "+
					"operator would read the raw JSON instead: %v", err)
			}
			if entry.Message != tc.want {
				t.Errorf("recovered sentence = %q, want %q", entry.Message, tc.want)
			}
			if entry.Code != env.Error.Code {
				t.Errorf("code = %q, want %q", entry.Code, env.Error.Code)
			}
		})
	}
}

// THE FLAT SHAPE is the one the permission denials use, written by hand in the
// view body, so it never reaches DRF's exception handler and parseError hands the
// whole payload over. AsReceivingRefusal is the narrow recogniser for it.
func TestUnlockAsset_TheForbiddenRefusalKeepsTheServersSentence(t *testing.T) {
	body := wireBody(t, "asset_unlock_forbidden.json")
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || env.Error == "" {
		t.Fatalf("fixture is not the flat {\"error\": \"<prose>\"} shape any more: %s", body)
	}
	const want = "You do not have permission to unlock this asset"
	if env.Error != want {
		t.Fatalf("fixture sentence = %q, want %q — re-record rather than edit", env.Error, want)
	}

	c, _, _ := interlockServer(t, http.StatusForbidden, body)
	_, err := c.UnlockAsset(context.Background(), "a1")
	if err == nil {
		t.Fatal("a recorded 403 decoded as a success")
	}
	// It is NOT the coded shape, so the coded recogniser must leave it alone —
	// that separation is what lets one combiner read both without guessing.
	if entry, ok := AsLineEntryError(err); ok {
		t.Errorf("AsLineEntryError claimed a body with no code: %+v", entry)
	}
	prose, ok := AsReceivingRefusal(err)
	if !ok {
		t.Fatalf("AsReceivingRefusal did not recognise the flat shape; the operator "+
			"would read the raw JSON instead: %v", err)
	}
	if prose != want {
		t.Errorf("recovered sentence = %q, want %q", prose, want)
	}
}

// assetMode reads the mode out of the operational_mode map the serializer nests.
// It is a map[string]any on Asset, so this is the one place the key is spelled.
func assetMode(a *Asset) string {
	if a == nil || a.OperationalMode == nil {
		return ""
	}
	mode, _ := a.OperationalMode["mode"].(string)
	return mode
}
