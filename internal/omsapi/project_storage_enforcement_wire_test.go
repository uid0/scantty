package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Request-shape and decode tests for the four project-storage enforcement
// endpoints, built from RECORDED OMS responses (testdata/README.md carries the
// provenance). The bodies came off a real backend on PostgreSQL, so a struct
// that disagrees with the server fails here rather than agreeing with a fixture
// written from itself.

// stintServer answers one request with a recorded body at a recorded status and
// hands back what the client SENT.
func stintServer(t *testing.T, status int, contentType string, body []byte) (*Client, **http.Request, *string) {
	t.Helper()
	var got *http.Request
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = readAll(t, r)
		got = r
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &got, &sent
}

// sentJSON decodes what the client posted, failing on a body that is not an
// object.
func sentJSON(t *testing.T, sent string) map[string]any {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal([]byte(sent), &req); err != nil {
		t.Fatalf("request body %q is not a JSON object: %v", sent, err)
	}
	return req
}

func TestSendProjectStorageViolationNotice_PostsAnEmptyObjectAndDecodes(t *testing.T) {
	body := wireBody(t, "project_storage_send_notice.json")
	c, got, sent := stintServer(t, http.StatusOK, "application/json", body)

	st, err := c.SendProjectStorageViolationNotice(context.Background(), "PS-JWBRFM4F")
	if err != nil {
		t.Fatalf("a recorded 200 did not decode: %v", err)
	}
	if (*got).Method != http.MethodPost {
		t.Errorf("method = %s, want POST", (*got).Method)
	}
	if want := "/api/project-storage/stints/PS-JWBRFM4F/send-violation-notice/"; (*got).URL.Path != want {
		t.Errorf("path = %q, want %q", (*got).URL.Path, want)
	}
	if req := sentJSON(t, *sent); len(req) != 0 {
		t.Errorf("notice sent %v; the view reads nothing off the body and the web posts {}", req)
	}
	raw := rawAsset(t, body)
	if st.Status != raw["status"] || st.Status != "purgatory_warned" {
		t.Errorf("status = %q, want the recorded %v", st.Status, raw["status"])
	}
	if st.NoticeSentAt == nil || st.PurgatoryAt == nil {
		t.Errorf("notice_sent_at / purgatory_at did not decode: %v / %v", st.NoticeSentAt, st.PurgatoryAt)
	}
	// The reply's events are the PRE-write prefetch — `[]` beside a stamped
	// notice — which is why the sheet re-fetches rather than drawing a reply.
	if evs, _ := raw["events"].([]any); len(evs) != len(st.Events) {
		t.Errorf("decoded %d events from a recorded %d", len(st.Events), len(evs))
	}
}

func TestMoveProjectStorageStintToPurgatory_AlwaysSendsTheLocationKey(t *testing.T) {
	for _, tc := range []struct{ name, location string }{
		{"typed", "Purgatory shelf 1"},
		// BLANK IS SENT, not omitted: the web sends it and the view reads blank as
		// "keep the stored location", so there is nothing for omitempty to protect.
		{"blank", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := wireBody(t, "project_storage_move_to_purgatory.json")
			c, got, sent := stintServer(t, http.StatusOK, "application/json", body)
			st, err := c.MoveProjectStorageStintToPurgatory(context.Background(), "PS-BCSSXENN", tc.location)
			if err != nil {
				t.Fatalf("a recorded 200 did not decode: %v", err)
			}
			if want := "/api/project-storage/stints/PS-BCSSXENN/move-to-purgatory/"; (*got).URL.Path != want {
				t.Errorf("path = %q, want %q", (*got).URL.Path, want)
			}
			req := sentJSON(t, *sent)
			if v, ok := req["purgatory_location_name"]; !ok || v != tc.location || len(req) != 1 {
				t.Errorf("sent %v, want exactly purgatory_location_name=%q", req, tc.location)
			}
			if st.Status != "purgatory" || st.MovedToPurgatoryAt == nil || st.PurgatoryLocationName != "Purgatory shelf 1" {
				t.Errorf("decoded %q / %v / %q, want the recorded purgatory reply",
					st.Status, st.MovedToPurgatoryAt, st.PurgatoryLocationName)
			}
		})
	}
}

func TestGenerateProjectStorageStintQR_SendsIncludeLogoAndDecodesTheURL(t *testing.T) {
	body := wireBody(t, "project_storage_generate_qr.json")
	c, got, sent := stintServer(t, http.StatusOK, "application/json", body)
	st, err := c.GenerateProjectStorageStintQR(context.Background(), "PS-BCSSXENN")
	if err != nil {
		t.Fatalf("a recorded 200 did not decode: %v", err)
	}
	if want := "/api/project-storage/stints/PS-BCSSXENN/generate-qr/"; (*got).URL.Path != want {
		t.Errorf("path = %q, want %q", (*got).URL.Path, want)
	}
	if req := sentJSON(t, *sent); req["include_logo"] != true || len(req) != 1 {
		t.Errorf("sent %v, want exactly include_logo=true (what the web sends)", req)
	}
	raw := rawAsset(t, body)
	if st.QRCodeURL == "" || st.QRCodeURL != raw["qr_code_url"] {
		t.Errorf("qr_code_url = %q, want the recorded %v", st.QRCodeURL, raw["qr_code_url"])
	}
	// The write's reply carries a RELATIVE path (no request in the serializer
	// context), which is why a screen shows the retrieve's value instead.
	if !strings.HasPrefix(st.QRCodeURL, "/media/") {
		t.Errorf("recorded generate-qr url %q is no longer the bare media path — "+
			"re-check where a screen should read it from", st.QRCodeURL)
	}
}

func TestListProjectStorageStintsByMember_DecodesTheBareArray(t *testing.T) {
	body := wireBody(t, "project_storage_by_member.json")
	c, got, _ := stintServer(t, http.StatusOK, "application/json", body)
	rows, err := c.ListProjectStorageStintsByMember(context.Background(), "alice")
	if err != nil {
		t.Fatalf("a recorded 200 did not decode: %v", err)
	}
	if want := "/api/project-storage/stints/by-member/alice/"; (*got).URL.Path != want {
		t.Errorf("path = %q, want %q", (*got).URL.Path, want)
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not a JSON array: %v", err)
	}
	if len(rows) != len(raw) || len(rows) < 2 {
		t.Fatalf("decoded %d rows from a recorded %d; the fixture must carry more than one stint", len(rows), len(raw))
	}
	for i, r := range raw {
		if rows[i].StintID != r["stint_id"] || rows[i].Status != r["status"] {
			t.Errorf("row %d = %s/%s, want the recorded %v/%v", i, rows[i].StintID, rows[i].Status, r["stint_id"], r["status"])
		}
	}
	// The pk is a number on the wire; a fixture edited to a string would make the
	// decode above pass for the wrong reason.
	if _, isNumber := raw[0]["id"].(float64); !isNumber {
		t.Errorf("recorded id is %T, want a JSON number", raw[0]["id"])
	}

	empty, err := stintClient(t, http.StatusOK, "project_storage_by_member_empty.json").
		ListProjectStorageStintsByMember(context.Background(), "nobody")
	if err != nil || len(empty) != 0 {
		t.Errorf("a member with no stints answered %v / %v, want an empty list and no error", empty, err)
	}
}

// A DOTTED USERNAME NEVER REACHES THE VIEW. The recorded answer is the router's
// HTML 404, which is why ProjectStorageByMemberUnroutable exists.
func TestListProjectStorageStintsByMember_ADottedUsernameIsTheRoutersHTML404(t *testing.T) {
	c, got, _ := stintServer(t, http.StatusNotFound, "text/html", wireBody(t, "project_storage_by_member_dotted_404.html"))
	_, err := c.ListProjectStorageStintsByMember(context.Background(), "bob.jones")
	var api *APIError
	if !errors.As(err, &api) || !api.IsNotFound() {
		t.Fatalf("err = %v, want the recorded 404", err)
	}
	if want := "/api/project-storage/stints/by-member/bob.jones/"; (*got).URL.Path != want {
		t.Errorf("path = %q, want %q", (*got).URL.Path, want)
	}
	if !ProjectStorageByMemberUnroutable("bob.jones") || ProjectStorageByMemberUnroutable("alice_smith-2") {
		t.Error("the unroutable predicate must answer for a dot and only for the route's excluded characters")
	}
}

func stintClient(t *testing.T, status int, fixture string) *Client {
	t.Helper()
	c, _, _ := stintServer(t, status, "application/json", wireBody(t, fixture))
	return c
}

// EVERY REFUSAL ARRIVES AS THE SERVER'S OWN SENTENCE through a recogniser that
// already exists, and each shape reaches exactly one of them.
func TestProjectStorageEnforcement_EveryRecordedRefusalYieldsItsSentence(t *testing.T) {
	for _, tc := range []struct {
		name, fixture string
		status        int
		call          func(*Client) error
		want          string
		sentence      func(error) (string, bool)
	}{
		{"notice outside the notice states", "project_storage_notice_invalid_state.json", http.StatusConflict,
			func(c *Client) error {
				_, err := c.SendProjectStorageViolationNotice(context.Background(), "PS-DTR7MFEG")
				return err
			},
			"Violation notice is only valid for expiring/expired/already-warned stints.", AsDetailRefusal},
		{"notice with no email", "project_storage_notice_missing_email.json", http.StatusUnprocessableEntity,
			func(c *Client) error {
				_, err := c.SendProjectStorageViolationNotice(context.Background(), "PS-PN9KVJPU")
				return err
			},
			"Member has no on-file email; record the violation but the system can't send the notice.", AsDetailRefusal},
		{"purgatory before a notice", "project_storage_purgatory_notice_required.json", http.StatusConflict,
			func(c *Client) error {
				_, err := c.MoveProjectStorageStintToPurgatory(context.Background(), "PS-JWBRFM4F", "")
				return err
			},
			"Send the violation notice first — purgatory requires a 7-day notice period.", AsDetailRefusal},
		{"qr rate limit", "project_storage_qr_rate_limited.json", http.StatusTooManyRequests,
			func(c *Client) error {
				_, err := c.GenerateProjectStorageStintQR(context.Background(), "PS-JWBRFM4F")
				return err
			},
			"Rate limit exceeded: 5 requests per 1 minute allowed. Please try again later.", AsReceivingRefusal},
		{"notice by a non-staff storage admin", "project_storage_notice_forbidden.json", http.StatusForbidden,
			func(c *Client) error {
				_, err := c.SendProjectStorageViolationNotice(context.Background(), "PS-JWBRFM4F")
				return err
			},
			"You do not have permission to perform this action.",
			func(err error) (string, bool) {
				var api *APIError
				if errors.As(err, &api) && api.Code == "permission_denied" {
					return api.Message, true
				}
				return "", false
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(stintClient(t, tc.status, tc.fixture))
			if err == nil {
				t.Fatal("a recorded refusal came back as success")
			}
			got, ok := tc.sentence(err)
			if !ok || got != tc.want {
				t.Errorf("sentence = %q (%v), want the server's %q; raw error: %v", got, ok, tc.want, err)
			}
		})
	}
}
