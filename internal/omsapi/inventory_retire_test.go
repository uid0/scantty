package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSetItemRetired_Contract pins the retire/un-retire actions (op-jv7r): a
// bodyless POST to the item's …/retire/ or …/unretire/ detail action depending
// on the direction, decoding the echoed item.
func TestSetItemRetired_Contract(t *testing.T) {
	cases := []struct {
		name     string
		retired  bool
		wantPath string
		respBody string
		wantFlag bool
	}{
		{"retire", true, "/api/inventory/items/itm-1/retire/", `{"id":"itm-1","name":"Widget","is_retired":true}`, true},
		{"unretire", false, "/api/inventory/items/itm-1/unretire/", `{"id":"itm-1","name":"Widget","is_retired":false}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured struct {
				method string
				path   string
				body   []byte
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.method = r.Method
				captured.path = r.URL.Path
				captured.body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.respBody))
			}))
			defer srv.Close()

			c := New(srv.URL)
			item, err := c.SetItemRetired(context.Background(), "itm-1", tc.retired)
			if err != nil {
				t.Fatalf("SetItemRetired: %v", err)
			}
			if captured.method != http.MethodPost || captured.path != tc.wantPath {
				t.Fatalf("method/path = %q %q, want POST %q", captured.method, captured.path, tc.wantPath)
			}
			// The retire/unretire actions take no request body.
			if body := strings.TrimSpace(string(captured.body)); body != "" {
				t.Errorf("body = %q, want empty", body)
			}
			if item == nil || item.IsRetired != tc.wantFlag {
				t.Fatalf("returned item = %+v, want IsRetired=%v", item, tc.wantFlag)
			}
		})
	}
}

// TestListItemsWithMetrics_IncludesRetired confirms the warden's inventory list
// asks the backend to keep retired items visible (?include_retired=true) — so a
// retired-and-empty item, which the default list hides, still shows up with its
// (retired) tag — alongside the existing ?with_metrics=1.
func TestListItemsWithMetrics_IncludesRetired(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[
			{"id":"a1","name":"Widget","sku":"W-1","current_stock":0,"is_retired":true}
		]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListItemsWithMetrics(context.Background())
	if err != nil {
		t.Fatalf("ListItemsWithMetrics: %v", err)
	}
	// The backend matches the literal string "true" (case-insensitive).
	if got := getQueryParam(query, "include_retired"); got != "true" {
		t.Fatalf("include_retired param = %q, want true (raw query %q)", got, query)
	}
	if got := getQueryParam(query, "with_metrics"); got != "1" {
		t.Fatalf("with_metrics param = %q, want 1 (raw query %q)", got, query)
	}
	if len(page.Results) != 1 || !page.Results[0].IsRetired {
		t.Fatalf("expected one retired item, got %+v", page.Results)
	}
}

// TestItemWrite_IsRetiredPayload confirms is_retired always rides in the item
// form's PATCH/POST body (no omitempty), so toggling an item back to un-retired
// (is_retired=false) actually reaches the backend rather than being dropped.
func TestItemWrite_IsRetiredPayload(t *testing.T) {
	for _, retired := range []bool{false, true} {
		raw, err := json.Marshal(ItemWrite{Name: "Widget", IsActive: true, IsRetired: retired})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		v, present := m["is_retired"]
		if !present {
			t.Fatalf("is_retired should be present even when %v", retired)
		}
		want := "false"
		if retired {
			want = "true"
		}
		if string(v) != want {
			t.Errorf("is_retired = %s, want %s", v, want)
		}
	}
}

// TestItem_IsRetiredDecodes pins that the read-side Item decodes is_retired off
// both the list and detail serializers, so the (retired)/[retired] markers key
// off a real value.
func TestItem_IsRetiredDecodes(t *testing.T) {
	var it Item
	if err := json.Unmarshal([]byte(`{"id":"itm-1","name":"Widget","is_retired":true}`), &it); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !it.IsRetired {
		t.Errorf("IsRetired = false, want true")
	}
	// Absent key → false (older backend / never-retired item).
	var it2 Item
	if err := json.Unmarshal([]byte(`{"id":"itm-2","name":"Gadget"}`), &it2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if it2.IsRetired {
		t.Errorf("IsRetired = true for absent key, want false")
	}
}
