package omsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Decode and request-shape tests for the fixture refill endpoints, built from
// RECORDED OMS responses (testdata/README.md, "Fixtures and refill requests").
//
// The client these replace is the reason they are recorded: ScanFixture decoded
// the scan reply into a Fixture and its test fed it a body shaped like that
// struct, so it passed while the server was sending a refill request.

// fixtureServer answers every request with one recorded body at one status and
// hands back what the client SENT — method, path, query and body — because on the
// write endpoints the request is half of what is checked.
func fixtureServer(t *testing.T, status int, body []byte) (*Client, **http.Request, *string) {
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

const (
	wireFixtureID = "352067b3-6d6a-4a9b-ac83-90a0e1958fa3"
	wireRequestID = "9bd248a2-f660-40e0-872a-2abc24be5c34"
)

// EVERY ID IS A STRING AND THE LOCATION IS A NUMBER, read off the raw bytes as
// well as the decode, so a fixture "corrected" to agree with a struct fails here.
func TestGetFixture_DecodesTheRecordedDetail(t *testing.T) {
	body := wireBody(t, "fixture_detail.json")
	c, got, _ := fixtureServer(t, http.StatusOK, body)
	fix, err := c.GetFixture(context.Background(), wireFixtureID)
	if err != nil {
		t.Fatalf("a recorded 200 did not decode: %v", err)
	}
	if want := "/api/inventory/fixtures/" + wireFixtureID + "/"; (*got).URL.Path != want {
		t.Errorf("path = %q, want %q", (*got).URL.Path, want)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["id"].(string); !ok {
		t.Fatalf("the recording's id is %T, not a string — re-record rather than edit", raw["id"])
	}
	if _, ok := raw["location"].(float64); !ok {
		t.Fatalf("the recording's location is %T, not a number — re-record rather than edit", raw["location"])
	}
	if _, ok := raw["refill_item"].(string); !ok {
		t.Fatalf("the recording's refill_item is %T, not a string", raw["refill_item"])
	}

	if fix.ID != raw["id"] || float64(fix.Location) != raw["location"] || fix.RefillItem != raw["refill_item"] {
		t.Errorf("ids decoded as %q / %d / %q against the recording's %v / %v / %v",
			fix.ID, fix.Location, fix.RefillItem, raw["id"], raw["location"], raw["refill_item"])
	}
	if fix.AssetTag == nil || *fix.AssetTag != "FIX-0001" || !fix.IsActive || fix.PendingRequestsCount != 2 {
		t.Errorf("decoded fixture = %+v", fix)
	}
	if fix.RefillItemDetails == nil || fix.RefillItemDetails.CurrentStock != 4 || fix.RefillItemDetails.MinimumStock != 6 {
		t.Errorf("refill item details = %+v", fix.RefillItemDetails)
	}
	// RECENT IS NOT PENDING: the recording carries a completed request beside
	// the two pending ones, which is why the screen reads the queue separately.
	statuses := map[string]int{}
	for _, r := range fix.RecentRefillRequests {
		statuses[r.Status]++
	}
	if statuses[FixtureRefillCompleted] == 0 || statuses[FixtureRefillPending] != 2 {
		t.Errorf("recent requests by status = %v; the recording holds one completed and two pending", statuses)
	}
}

// AN ANONYMOUS REQUEST HAS A BLANK requested_by AND AN "Anonymous" ACTOR, so the
// requester is read from the actor.
func TestListFixtureRefillRequests_SendsTheFilterAndReadsTheActor(t *testing.T) {
	c, got, _ := fixtureServer(t, http.StatusOK, wireBody(t, "fixture_refill_requests_for_fixture.json"))
	rows, err := c.ListFixtureRefillRequests(context.Background(), FixtureRefillRequestFilter{
		Fixture: wireFixtureID, Status: FixtureRefillPending,
	})
	if err != nil {
		t.Fatalf("a recorded page did not decode: %v", err)
	}
	q := (*got).URL.Query()
	if q.Get("fixture") != wireFixtureID || q.Get("status") != FixtureRefillPending || q.Has("location") {
		t.Errorf("query = %v, want fixture and status and no location", q)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded %d rows, the recording holds 2", len(rows))
	}
	var anon *FixtureRefillRequest
	for i := range rows {
		if rows[i].RequestedBy == "" {
			anon = &rows[i]
		}
		if rows[i].ResolvedAt != nil || rows[i].ResolvedActor != nil {
			t.Errorf("a pending request decoded a resolver: %+v", rows[i])
		}
	}
	if anon == nil || anon.Requester() != "Anonymous" || anon.RequestedUsername != nil {
		t.Errorf("the anonymous request decoded as %+v", anon)
	}
}

func TestListFixtureRefillRequests_WalksEveryPage(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"count":2,"next":"http://x/?page=2","previous":null,"results":[{"id":"a","status":"pending"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":"http://x/?page=1","results":[{"id":"b","status":"pending"}]}`))
	}))
	t.Cleanup(srv.Close)
	rows, err := New(srv.URL).ListFixtureRefillRequests(context.Background(), FixtureRefillRequestFilter{Status: FixtureRefillPending})
	if err != nil || len(rows) != 2 || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("rows=%d pages=%v err=%v; the second page was not read", len(rows), pages, err)
	}
}

// THE LOCATION LIST IS A BARE ARRAY AND HOLDS NO INACTIVE FIXTURE: the recording's
// location has three fixtures, one of them inactive, and the body carries two.
func TestListLocationFixtures_DecodesTheBareArray(t *testing.T) {
	c, got, _ := fixtureServer(t, http.StatusOK, wireBody(t, "location_fixtures.json"))
	rows, err := c.ListLocationFixtures(context.Background(), 1200041)
	if err != nil {
		t.Fatalf("a recorded array did not decode: %v", err)
	}
	if want := "/api/inventory/locations/1200041/fixtures/"; (*got).URL.Path != want {
		t.Errorf("path = %q, want %q", (*got).URL.Path, want)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded %d fixtures, the recording holds 2", len(rows))
	}
	for _, f := range rows {
		if !f.IsActive || f.RecentRefillRequests != nil || f.RefillItemDetails != nil {
			t.Errorf("a list row decoded as %+v", f)
		}
	}
	if rows[1].AssetTag != nil {
		t.Errorf("a null asset tag decoded as %q", *rows[1].AssetTag)
	}
}

// BLANK NOTES SEND NO KEY, on all three writes. On `resolve` that is the
// difference between keeping the reporter's note and erasing it — the recording
// beside this test is a resolve sent `{"notes": ""}`, and its notes came back
// empty.
func TestFixtureWrites_BlankNotesSendNoKey(t *testing.T) {
	erased := wireBody(t, "fixture_refill_request_resolve_empty_notes.json")
	var raw map[string]any
	if err := json.Unmarshal(erased, &raw); err != nil || raw["notes"] != "" {
		t.Fatalf("the empty-notes recording no longer shows the erasure (notes=%v)", raw["notes"])
	}
	for _, tc := range []struct {
		name, fixture, path string
		call                func(*Client, string) error
	}{
		{"scan", "fixture_scan.json", "/api/inventory/fixtures/" + wireFixtureID + "/scan/", func(c *Client, notes string) error {
			_, err := c.ScanFixture(context.Background(), wireFixtureID, notes)
			return err
		}},
		{"resolve", "fixture_refill_request_resolve.json", "/api/inventory/fixture-refill-requests/" + wireRequestID + "/resolve/", func(c *Client, notes string) error {
			_, err := c.ResolveFixtureRefillRequest(context.Background(), wireRequestID, notes)
			return err
		}},
		{"resolve all", "fixture_resolve_all.json", "/api/inventory/fixtures/" + wireFixtureID + "/resolve_all/", func(c *Client, notes string) error {
			_, err := c.ResolveAllFixtureRefillRequests(context.Background(), wireFixtureID, notes)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, notes := range []string{"", "   "} {
				c, got, sent := fixtureServer(t, http.StatusOK, wireBody(t, tc.fixture))
				if err := tc.call(c, notes); err != nil {
					t.Fatalf("a recorded reply did not decode: %v", err)
				}
				if (*got).Method != http.MethodPost || (*got).URL.Path != tc.path {
					t.Errorf("%s %s, want POST %s", (*got).Method, (*got).URL.Path, tc.path)
				}
				if strings.Contains(*sent, "notes") {
					t.Errorf("blank notes %q sent %s; an absent key is the only body that keeps the reporter's note", notes, *sent)
				}
			}
			c, _, sent := fixtureServer(t, http.StatusOK, wireBody(t, tc.fixture))
			if err := tc.call(c, "  Refilled from the storeroom "); err != nil {
				t.Fatal(err)
			}
			var body map[string]string
			if err := json.Unmarshal([]byte(*sent), &body); err != nil || body["notes"] != "Refilled from the storeroom" || len(body) != 1 {
				t.Errorf("notes sent as %s, want exactly the trimmed notes", *sent)
			}
		})
	}
}

// THE SCAN REPLY IS A REFILL REQUEST, which the client this replaced decoded as a
// fixture.
func TestScanFixture_DecodesARefillRequest(t *testing.T) {
	c, _, _ := fixtureServer(t, http.StatusCreated, wireBody(t, "fixture_scan.json"))
	req, err := c.ScanFixture(context.Background(), wireFixtureID, "Towels ran out during the class")
	if err != nil {
		t.Fatalf("a recorded 201 did not decode: %v", err)
	}
	if req.Status != FixtureRefillPending || req.FixtureName == "" || req.Notes != "Towels ran out during the class" || req.Requester() != "coo" {
		t.Errorf("scan reply decoded as %+v", req)
	}
}

// RESOLVE ALL RELAYS THE SERVER'S SENTENCE AND THE FIXTURE AFTERWARDS, including
// the zero case, which is a 200 and not a refusal.
func TestResolveAllFixtureRefillRequests_DecodesTheSentence(t *testing.T) {
	for name, want := range map[string]string{
		"fixture_resolve_all.json":                 "Resolved 1 pending refill request(s)",
		"fixture_resolve_all_nothing_pending.json": "Resolved 0 pending refill request(s)",
		"fixture_resolve_all_with_notes.json":      "Resolved 2 pending refill request(s)",
	} {
		c, _, _ := fixtureServer(t, http.StatusOK, wireBody(t, name))
		res, err := c.ResolveAllFixtureRefillRequests(context.Background(), wireFixtureID, "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Message != want || res.Fixture.PendingRequestsCount != 0 || res.Fixture.ID == "" {
			t.Errorf("%s decoded as %q with %d pending", name, res.Message, res.Fixture.PendingRequestsCount)
		}
	}
	// The notes recording is the proof the overwrite is real: both reporters'
	// notes came back as the one resolution sentence.
	c, _, _ := fixtureServer(t, http.StatusOK, wireBody(t, "fixture_resolve_all_with_notes.json"))
	res, _ := c.ResolveAllFixtureRefillRequests(context.Background(), wireFixtureID, "")
	overwritten := 0
	for _, r := range res.Fixture.RecentRefillRequests {
		if r.Notes == "Refilled from the storeroom" {
			overwritten++
		}
	}
	if overwritten != 2 {
		t.Errorf("the with-notes recording shows %d overwritten notes, want 2", overwritten)
	}
}

// BOTH REFUSAL SHAPES YIELD THE SERVER'S OWN SENTENCE, and a gateway page yields
// none.
func TestFixtureRefusal_ReadsBothShapes(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		status  int
		want    string
	}{
		{"fixture_scan_inactive.json", http.StatusBadRequest, "This fixture is inactive"},
		{"fixture_refill_request_resolve_completed.json", http.StatusBadRequest, "This request is already completed"},
		{"fixture_resolve_all_unauthenticated.json", http.StatusUnauthorized, "Authentication credentials were not provided."},
		{"fixture_not_found.json", http.StatusNotFound, "No Fixture matches the given query."},
		{"fixture_not_a_uuid.json", http.StatusNotFound, "Not found."},
	} {
		c, _, _ := fixtureServer(t, tc.status, wireBody(t, tc.fixture))
		_, err := c.GetFixture(context.Background(), wireFixtureID)
		if err == nil {
			t.Fatalf("%s: a %d decoded as success", tc.fixture, tc.status)
		}
		got, ok := FixtureRefusal(err)
		if !ok || got != tc.want {
			t.Errorf("%s: refusal = %q, %v; want the server's %q", tc.fixture, got, ok, tc.want)
		}
	}
	c, _, _ := fixtureServer(t, http.StatusBadGateway, []byte("<html><body>502 Bad Gateway</body></html>"))
	_, err := c.GetFixture(context.Background(), wireFixtureID)
	if s, ok := FixtureRefusal(err); ok {
		t.Errorf("a gateway page was coerced into the sentence %q", s)
	}
}
