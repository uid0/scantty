package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Decode tests for the checklist lists a run is started from, built from
// RECORDED OMS responses (testdata/README.md carries the provenance and the
// seed). The guards read the RAW bytes, so a later edit "fixing" a fixture to
// match a struct fails rather than quietly restoring a defect.

// checklistWire serves one recorded body at `status` and records the request it
// was asked with, so a test can hold the ROUTE as well as the decode.
func checklistWire(t *testing.T, name string, status int) (*Client, *http.Request) {
	t.Helper()
	body := wireBody(t, name)
	seen := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), seen
}

// rawChecklists is the recorded array as the server's own JSON types.
func rawChecklists(t *testing.T, name string) []map[string]any {
	t.Helper()
	var raw []map[string]any
	if err := json.Unmarshal(wireBody(t, name), &raw); err != nil {
		t.Fatalf("%s is not a JSON array — every one of these routes answers a bare list, never a page: %v", name, err)
	}
	return raw
}

// THE DECODE AND THE ROUTE, per record kind. Each call is asked for the record the
// fixture was recorded against and must land on that route with the rows intact.
func TestRecordChecklists_DecodeTheBytesOMSReallySendsOffTheRouteTheWebCalls(t *testing.T) {
	cases := []struct {
		name, fixture, path, query string
		call                       func(*Client) ([]ChecklistSummary, error)
	}{
		{
			name: "asset", fixture: "checklists_asset_staff.json",
			path: "/api/inventory/assets/702421aa-97e5-4e54-8765-9a0e3a4ab301/checklists/",
			call: func(c *Client) ([]ChecklistSummary, error) {
				return c.ListAssetChecklists(context.Background(), "702421aa-97e5-4e54-8765-9a0e3a4ab301")
			},
		},
		{
			// A KIT, because it is the item the query exists for: without
			// include_kits its id is a 404 (checklists_item_kit_without_include_kits.json).
			name: "item/kit", fixture: "checklists_item_kit.json",
			path: "/api/inventory/items/1a3ccb1b-e843-4f6b-b58b-aa9763193698/checklists/", query: "include_kits=true",
			call: func(c *Client) ([]ChecklistSummary, error) {
				return c.ListItemChecklists(context.Background(), "1a3ccb1b-e843-4f6b-b58b-aa9763193698")
			},
		},
		{
			// A SEVEN-DIGIT pk, recorded that way on purpose: formatted through an
			// untyped float it would be spent as 1.200041e+06.
			name: "location", fixture: "checklists_location.json",
			path: "/api/inventory/locations/1200041/checklists/",
			call: func(c *Client) ([]ChecklistSummary, error) {
				return c.ListLocationChecklists(context.Background(), 1200041)
			},
		},
		{
			name: "available", fixture: "checklists_available_member.json",
			path: "/api/checklists/checklists/available/",
			call: func(c *Client) ([]ChecklistSummary, error) {
				return c.ListAvailableChecklists(context.Background())
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := checklistWire(t, tc.fixture, http.StatusOK)
			got, err := tc.call(c)
			if err != nil {
				t.Fatalf("a recorded OMS reply did not decode: %v", err)
			}
			if seen.URL.Path != tc.path || seen.URL.RawQuery != tc.query {
				t.Errorf("asked %s?%s, want %s?%s", seen.URL.Path, seen.URL.RawQuery, tc.path, tc.query)
			}
			raw := rawChecklists(t, tc.fixture)
			if len(raw) == 0 {
				t.Fatalf("%s holds no checklist — it would prove nothing about one", tc.fixture)
			}
			if len(got) != len(raw) {
				t.Fatalf("decoded %d checklists, the body holds %d", len(got), len(raw))
			}
			for i, want := range raw {
				if id, _ := want["id"].(string); got[i].ID != id {
					t.Errorf("[%d] id = %q, want %q", i, got[i].ID, id)
				}
				if n, _ := want["name"].(string); got[i].Name != n {
					t.Errorf("[%d] name = %q, want %q", i, got[i].Name, n)
				}
				if steps, _ := want["step_count"].(float64); got[i].StepCount != int(steps) {
					t.Errorf("[%d] step_count = %d, want %v", i, got[i].StepCount, steps)
				}
				if pub, _ := want["is_public"].(bool); got[i].IsPublic != pub {
					t.Errorf("[%d] is_public = %v, want %v", i, got[i].IsPublic, pub)
				}
			}
		})
	}
}

// THE EMPTY ANSWER IS AN EMPTY LIST, NOT A FAILURE: a location no checklist
// names answers 200 with `[]`.
func TestRecordChecklists_ALocationNoChecklistNamesDecodesEmpty(t *testing.T) {
	c, _ := checklistWire(t, "checklists_location_none.json", http.StatusOK)
	got, err := c.ListLocationChecklists(context.Background(), 1200042)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d rows, err %v; want an empty list and no error", len(got), err)
	}
}

// The recorded bodies carry the server's own types — `id` a UUID STRING (Checklist
// declares a UUIDField pk), `sig` a NUMBER (auth.Group's AutoField), `step_count`
// a NUMBER (a SerializerMethodField returning .count(), which spectacular would
// type as a string) — and they reach the states the reader filter keeps apart.
func TestChecklistFixtures_CarryTheServersOwnTypesAndStates(t *testing.T) {
	for _, name := range []string{
		"checklists_asset_staff.json", "checklists_asset_member.json", "checklists_item.json",
		"checklists_item_kit.json", "checklists_location.json",
		"checklists_available_staff.json", "checklists_available_member.json",
	} {
		for i, row := range rawChecklists(t, name) {
			if _, ok := row["id"].(string); !ok {
				t.Errorf("%s[%d].id is %T, want a JSON string", name, i, row["id"])
			}
			if _, ok := row["sig"].(float64); !ok {
				t.Errorf("%s[%d].sig is %T, want a JSON number", name, i, row["sig"])
			}
			if _, ok := row["step_count"].(float64); !ok {
				t.Errorf("%s[%d].step_count is %T, want a JSON number", name, i, row["step_count"])
			}
		}
	}

	// THE READER FILTER IS THE SERVER'S. One asset, two readers: staff see the
	// private SIG checklist, an ordinary member does not — so a checklist the
	// reader may not run is absent, never served and refused.
	private := func(name string) bool {
		for _, row := range rawChecklists(t, name) {
			if pub, _ := row["is_public"].(bool); !pub {
				return true
			}
		}
		return false
	}
	if !private("checklists_asset_staff.json") || private("checklists_asset_member.json") {
		t.Error("the asset recordings no longer show staff seeing a private checklist a member does not")
	}
	// And an INACTIVE checklist with a step on the same asset is in neither.
	for _, name := range []string{"checklists_asset_staff.json", "checklists_available_staff.json"} {
		for _, row := range rawChecklists(t, name) {
			if active, _ := row["is_active"].(bool); !active {
				t.Errorf("%s serves an inactive checklist; the recording was made to show it absent", name)
			}
		}
	}
}

// THE PREMISE OF MOVING THE GLOBAL LIST OFF THE MANAGEMENT ENDPOINT, recorded off
// one database: the same member reads an EMPTY page from the list endpoint and two
// runnable checklists from available/.
func TestChecklistFixtures_TheManagementListHidesWhatAMemberCanRun(t *testing.T) {
	var page struct {
		Count   int   `json:"count"`
		Results []any `json:"results"`
	}
	if err := json.Unmarshal(wireBody(t, "checklists_list_member.json"), &page); err != nil {
		t.Fatalf("checklists_list_member.json: %v", err)
	}
	if page.Count != 0 || len(page.Results) != 0 {
		t.Errorf("the member's management list holds %d rows; the recording was made to show it empty", page.Count)
	}
	if n := len(rawChecklists(t, "checklists_available_member.json")); n == 0 {
		t.Error("the member's available/ list is empty; the recording was made to show what the list hides")
	}
}

// A REFUSED START IS THE SERVER'S SENTENCE. Both of the start action's own
// refusals are hand-written {"detail": …} bodies, so AsDetailRefusal must recover
// each one verbatim off the error StartChecklist returns.
func TestStartChecklist_ARefusalCarriesTheServersSentence(t *testing.T) {
	cases := []struct {
		fixture string
		status  int
		want    string
	}{
		{"checklist_start_forbidden.json", http.StatusForbidden, "You do not have permission to start this checklist."},
		{"checklist_start_inactive.json", http.StatusBadRequest, "This checklist is not currently active."},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			c, _ := checklistWire(t, tc.fixture, tc.status)
			_, err := c.StartChecklist(context.Background(), "311d9d30-fee5-4c4b-b520-4f8ed57cbd73", "")
			var api *APIError
			if !errors.As(err, &api) || api.Status != tc.status {
				t.Fatalf("err = %v, want an APIError with status %d", err, tc.status)
			}
			if got, ok := AsDetailRefusal(err); !ok || got != tc.want {
				t.Errorf("AsDetailRefusal = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
}

// A started run decodes, and its id is the string the run screen is opened on.
func TestStartChecklist_DecodesTheRecordedRun(t *testing.T) {
	c, seen := checklistWire(t, "checklist_start.json", http.StatusCreated)
	run, err := c.StartChecklist(context.Background(), "463fa56f-6454-4b37-b3b1-02cfe4063d81", "")
	if err != nil {
		t.Fatalf("a recorded start did not decode: %v", err)
	}
	if seen.Method != http.MethodPost || seen.URL.Path != "/api/checklists/checklists/463fa56f-6454-4b37-b3b1-02cfe4063d81/start/" {
		t.Errorf("asked %s %s", seen.Method, seen.URL.Path)
	}
	if run.ID != "89fa43c1-c5b6-4de4-bd07-5c511b8c6398" || run.Status != "in_progress" || run.TotalStepsCount != 3 {
		t.Errorf("run = %+v", run)
	}
}
