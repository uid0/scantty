package omsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// Tests for the supplier-link version token, built from RECORDED OMS responses
// (testdata/README.md carries the provenance and the sequence that produced
// them). Every expectation is read off the raw bytes of the same fixture rather
// than written out a second time, so a fixture later "fixed" to agree with a
// struct fails here instead of quietly re-certifying it.

// staleServer answers every request with one recorded body at one status, and
// records what the client sent.
type staleServer struct {
	method, path, query string
	body                map[string]any
}

func serveRecorded(t *testing.T, status int, body []byte, seen *staleServer) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method, seen.path, seen.query = r.Method, r.URL.Path, r.URL.RawQuery
		if raw := readAll(t, r); raw != "" {
			if err := json.Unmarshal([]byte(raw), &seen.body); err != nil {
				t.Errorf("request body is not JSON: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// rawVersion is a recorded row's `version`, asserting it is a JSON NUMBER — the
// type the Go field has to decode, and the one a hand-written fixture would be
// free to get wrong.
func rawVersion(t *testing.T, row map[string]any) int {
	t.Helper()
	v, ok := row["version"].(float64)
	if !ok {
		t.Fatalf("recorded row carries version %#v, not a JSON number", row["version"])
	}
	return int(v)
}

func TestItemSupplierVersion_DecodesOnEveryRecordedRepresentation(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		body := wireBody(t, "supplier_link_version_item_suppliers.json")
		raw := rawRows(t, body, "results")
		got, err := serveWire(t, body).ListItemSuppliersForItem(context.Background(), "itm")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(raw) {
			t.Fatalf("decoded %d rows, fixture has %d", len(got), len(raw))
		}
		moved := false
		for i, row := range raw {
			want := rawVersion(t, row)
			if got[i].Version != want {
				t.Errorf("row %d: version = %d, the server sent %d", i, got[i].Version, want)
			}
			moved = moved || want > 1
		}
		// A list of first versions cannot tell a decoded token from a default
		// of one; the recording holds a link written once since it was created.
		if !moved {
			t.Errorf("no recorded row has moved past version 1, so nothing proves the token is read")
		}
	})
	for _, name := range []string{"supplier_link_version_detail.json", "supplier_link_version_patch.json"} {
		t.Run(name, func(t *testing.T) {
			body := wireBody(t, name)
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			got, err := serveWire(t, body).GetItemSupplier(context.Background(), int(raw["id"].(float64)))
			if err != nil {
				t.Fatal(err)
			}
			if want := rawVersion(t, raw); got.Version != want {
				t.Errorf("version = %d, the server sent %d", got.Version, want)
			}
		})
	}
}

// A server before #1091 serves no token, and a row decoded from it must send
// none: the older server would ignore the key, but a zero is not a version any
// server issued. The recording is the lead-time provenance session's, made
// before the token existed.
func TestItemSupplierVersion_AServerWithoutTheTokenIsSentNone(t *testing.T) {
	body := wireBody(t, "lead_time_source_item_suppliers.json")
	if strings.Contains(string(body), `"version"`) {
		t.Fatalf("the pre-token recording carries a version key, so it cannot prove this")
	}
	rows, err := serveWire(t, body).ListItemSuppliersForItem(context.Background(), "itm")
	if err != nil || len(rows) == 0 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	row := rows[0]
	var seen staleServer
	c := serveRecorded(t, http.StatusOK, wireBody(t, "supplier_link_version_patch.json"), &seen)

	if _, err := c.UpdateItemSupplier(context.Background(), row.ID, ItemSupplierWrite{
		Item: row.Item, Supplier: row.Supplier, SupplierSKU: row.SupplierSKU,
		QuantityPerPackage: 1, Version: row.Version,
	}); err != nil {
		t.Fatal(err)
	}
	if _, present := seen.body["version"]; present {
		t.Errorf("PATCH sent version %v for a row the server gave no token", seen.body["version"])
	}
	seen = staleServer{}
	if _, err := c.SetItemSupplierPrimary(context.Background(), row.ID, row.Version); err != nil {
		t.Fatal(err)
	}
	if _, present := seen.body["version"]; present {
		t.Errorf("set-primary sent version %v for a row the server gave no token", seen.body["version"])
	}
	seen = staleServer{}
	if err := New(serveURL(t, http.StatusNoContent, &seen)).DeleteItemSupplier(context.Background(), row.ID, row.Version); err != nil {
		t.Fatal(err)
	}
	if seen.query != "" {
		t.Errorf("DELETE sent query %q for a row the server gave no token", seen.query)
	}
}

// serveURL is a server answering one status with no body — a 204 DELETE.
func serveURL(t *testing.T, status int, seen *staleServer) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method, seen.path, seen.query = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// THE ADOPTION. Each write states the version its copy was loaded at, in the
// place OMS reads it — the PATCH body, the DELETE query — and each recorded
// refusal comes back recognised, carrying the server's own sentence and whether
// the link still exists.
func TestItemSupplierVersion_EveryWriteSendsItsVersionAndReadsTheRefusal(t *testing.T) {
	cases := []struct {
		file  string
		write func(c *Client, id, version int) error
		sent  func(t *testing.T, seen staleServer) int
	}{
		{
			file: "supplier_link_stale_version_patch.json",
			write: func(c *Client, id, version int) error {
				_, err := c.UpdateItemSupplier(context.Background(), id, ItemSupplierWrite{
					Item: "itm", Supplier: 1, SupplierSKU: "4NUE9-B", QuantityPerPackage: 1, Version: version,
				})
				return err
			},
			sent: bodyVersion,
		},
		{
			file: "supplier_link_stale_version_deleted.json",
			write: func(c *Client, id, version int) error {
				_, err := c.UpdateItemSupplier(context.Background(), id, ItemSupplierWrite{
					Item: "itm", Supplier: 3, SupplierSKU: "94645A101-B", QuantityPerPackage: 100, Version: version,
				})
				return err
			},
			sent: bodyVersion,
		},
		{
			file: "supplier_link_stale_version_set_primary.json",
			write: func(c *Client, id, version int) error {
				_, err := c.SetItemSupplierPrimary(context.Background(), id, version)
				return err
			},
			sent: func(t *testing.T, seen staleServer) int {
				t.Helper()
				if len(seen.body) != 2 || seen.body["is_primary"] != true {
					t.Errorf("set-primary body = %v, want is_primary and version alone", seen.body)
				}
				return bodyVersion(t, seen)
			},
		},
		{
			file: "supplier_link_stale_version_delete.json",
			write: func(c *Client, id, version int) error {
				return c.DeleteItemSupplier(context.Background(), id, version)
			},
			sent: func(t *testing.T, seen staleServer) int {
				t.Helper()
				if seen.method != http.MethodDelete {
					t.Errorf("method = %s, want DELETE", seen.method)
				}
				if seen.body != nil {
					t.Errorf("DELETE carried a body %v; OMS reads the version off the query", seen.body)
				}
				q, err := url.ParseQuery(seen.query)
				if err != nil {
					t.Fatalf("DELETE query %q: %v", seen.query, err)
				}
				v, err := strconv.Atoi(q.Get("version"))
				if err != nil {
					t.Fatalf("DELETE query %q carries no integer version: %v", seen.query, err)
				}
				return v
			},
		},
	}
	reachedDeleted, reachedChanged := false, false
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			body := wireBody(t, tc.file)
			var raw struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
					Details struct {
						ID             int  `json:"id"`
						SentVersion    int  `json:"sent_version"`
						CurrentVersion *int `json:"current_version"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			d := raw.Error.Details

			var seen staleServer
			err := tc.write(serveRecorded(t, http.StatusConflict, body, &seen), d.ID, d.SentVersion)
			if err == nil {
				t.Fatal("a recorded 409 came back as a success")
			}
			if seen.path == "" || !strings.HasSuffix(seen.path, "/item-suppliers/"+strconv.Itoa(d.ID)+"/") {
				t.Errorf("path = %q, want the link's own route", seen.path)
			}
			if got := tc.sent(t, seen); got != d.SentVersion {
				t.Errorf("the write stated version %d, the refused one carried %d", got, d.SentVersion)
			}

			refusal, ok := AsStaleSupplierLink(err)
			if !ok {
				t.Fatalf("the recorded stale_version refusal was not recognised: %v", err)
			}
			if refusal.Message != raw.Error.Message {
				t.Errorf("message = %q, the server said %q", refusal.Message, raw.Error.Message)
			}
			if refusal.ID == nil || *refusal.ID != d.ID || refusal.SentVersion != d.SentVersion {
				t.Errorf("details = id %v sent %d, the server sent id %d sent %d",
					refusal.ID, refusal.SentVersion, d.ID, d.SentVersion)
			}
			switch {
			case d.CurrentVersion == nil:
				reachedDeleted = true
				if !refusal.Deleted() || refusal.CurrentVersion != nil {
					t.Errorf("current_version was null and the refusal reads as a live link: %+v", refusal)
				}
			default:
				reachedChanged = true
				if refusal.Deleted() || refusal.CurrentVersion == nil || *refusal.CurrentVersion != *d.CurrentVersion {
					t.Errorf("current_version = %v, the server sent %d", refusal.CurrentVersion, *d.CurrentVersion)
				}
			}
		})
	}
	if !reachedDeleted || !reachedChanged {
		t.Errorf("recordings reached deleted=%v changed=%v; a side never reached is a side never proved",
			reachedDeleted, reachedChanged)
	}
}

func bodyVersion(t *testing.T, seen staleServer) int {
	t.Helper()
	v, ok := seen.body["version"].(float64)
	if !ok {
		t.Fatalf("the write sent version %#v, not a number (body %v)", seen.body["version"], seen.body)
	}
	return int(v)
}

// The recogniser is as narrow as the contract: a recorded 404 off the same
// route, a stale_version code at another status, and any other 409 are not a
// stale refusal — and a zero-version write body carries no key at all.
func TestItemSupplierVersion_OnlyTheRefusalIsRecognised(t *testing.T) {
	gone := wireBody(t, "supplier_link_version_detail_gone.json")
	var seen staleServer
	_, err := serveRecorded(t, http.StatusNotFound, gone, &seen).GetItemSupplier(context.Background(), 6)
	if _, ok := AsStaleSupplierLink(err); ok {
		t.Errorf("a 404 was read as a stale refusal: %v", err)
	}
	if api, ok := err.(*APIError); !ok || !api.IsNotFound() {
		t.Errorf("the recorded 404 is not reported as not-found: %#v", err)
	}

	stale := wireBody(t, "supplier_link_stale_version_patch.json")
	_, err = serveRecorded(t, http.StatusBadRequest, stale, &seen).SetItemSupplierPrimary(context.Background(), 4, 1)
	if _, ok := AsStaleSupplierLink(err); ok {
		t.Errorf("the code at a 400 was read as a 409 refusal")
	}
	other := []byte(`{"error":{"code":"ambiguous","message":"pick one"}}`)
	_, err = serveRecorded(t, http.StatusConflict, other, &seen).SetItemSupplierPrimary(context.Background(), 4, 1)
	if _, ok := AsStaleSupplierLink(err); ok {
		t.Errorf("another 409 code was read as a stale refusal")
	}

	raw, err := json.Marshal(ItemSupplierWrite{Item: "itm", Supplier: 1, SupplierSKU: "S", QuantityPerPackage: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"version"`) {
		t.Errorf("a write with no loaded version sent the key: %s", raw)
	}
}
