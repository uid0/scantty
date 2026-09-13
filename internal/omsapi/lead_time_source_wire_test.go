package omsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Decode tests for `average_lead_time_source`, built from RECORDED OMS
// responses (testdata/README.md carries the provenance). The `_pre1085` bodies
// came off the same database with OMS at #1085's parent, so each pair differs
// only by whether the server serves the marker.
//
// Every assertion compares the decode against the RAW bytes of the same
// fixture rather than against a second copy of the expectation, and the
// coverage checks read the raw bytes too: a fixture later "fixed" to match the
// struct, or trimmed of the rows that reach a value, fails here rather than
// quietly narrowing what is proved.

// rawRows decodes a fixture into untyped rows for comparison.
func rawRows(t *testing.T, body []byte, key string) []map[string]any {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("fixture is not a JSON object: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(envelope[key], &rows); err != nil {
		t.Fatalf("fixture %q is not a list of objects: %v", key, err)
	}
	return rows
}

// knownLeadTimeSources is every value OMS's LeadTimeSource defines, as this
// client spells them.
var knownLeadTimeSources = []LeadTimeSource{
	LeadTimeSourceUnknown, LeadTimeSourceDefault, LeadTimeSourceRecorded, LeadTimeSourceMeasured,
}

// checkSourcesAgainstRaw asserts the decoded sources equal the raw key on every
// row, and returns which values the raw rows reached.
func checkSourcesAgainstRaw(t *testing.T, raw []map[string]any, got []ItemSupplier) map[LeadTimeSource]bool {
	t.Helper()
	if len(raw) != len(got) {
		t.Fatalf("decoded %d rows, fixture has %d", len(got), len(raw))
	}
	reached := map[LeadTimeSource]bool{}
	for i, row := range raw {
		want, _ := row["average_lead_time_source"].(string)
		if LeadTimeSource(want) != got[i].LeadTimeSource {
			t.Errorf("row %d: source = %q, the server sent %q", i, got[i].LeadTimeSource, want)
		}
		if days, _ := row["average_lead_time"].(float64); days != got[i].LeadTimeDays {
			t.Errorf("row %d: lead = %v, the server sent %v", i, got[i].LeadTimeDays, days)
		}
		reached[LeadTimeSource(want)] = true
	}
	return reached
}

func requireEverySource(t *testing.T, reached map[LeadTimeSource]bool) {
	t.Helper()
	for _, src := range knownLeadTimeSources {
		if !reached[src] {
			t.Errorf("the recording reaches no row whose source is %q, so nothing here proves it decodes", src)
		}
	}
	for src := range reached {
		known := false
		for _, k := range knownLeadTimeSources {
			known = known || k == src
		}
		if !known {
			t.Errorf("the server sent source %q, which this client has no constant for", src)
		}
	}
}

// THE MARKER ARRIVES, on the list the item's Suppliers screen reads, with every
// value OMS defines — including a quoted 7 and a defaulted 7 on the same item,
// which are the two readings this exists to tell apart.
func TestLeadTimeSource_TheItemSupplierListCarriesEveryValue(t *testing.T) {
	body := wireBody(t, "lead_time_source_item_suppliers.json")
	got, err := serveWire(t, body).ListItemSuppliersForItem(context.Background(), "dd8ac252-fe7d-4dd9-824f-1f0dfc1864a2")
	if err != nil {
		t.Fatalf("a recorded reply did not decode: %v", err)
	}
	requireEverySource(t, checkSourcesAgainstRaw(t, rawRows(t, body, "results"), got))

	sevens := map[LeadTimeSource]bool{}
	for _, sup := range got {
		if sup.LeadTimeDays == 7 {
			sevens[sup.LeadTimeSource] = true
		}
	}
	if !sevens[LeadTimeSourceDefault] || !sevens[LeadTimeSourceRecorded] {
		t.Errorf("the recording should carry a defaulted 7 AND a quoted 7; the sevens it has: %v", sevens)
	}
}

// A SERVER THAT PREDATES THE MARKER decodes exactly as before: the number is
// there, and the source is "" — nothing said — rather than any of the four.
func TestLeadTimeSource_AnOlderServerDecodesWithNoSource(t *testing.T) {
	body := wireBody(t, "lead_time_source_item_suppliers_pre1085.json")
	for i, row := range rawRows(t, body, "results") {
		if _, ok := row["average_lead_time_source"]; ok {
			t.Fatalf("row %d of the pre-#1085 recording carries the marker; it is not the recording it claims to be", i)
		}
	}
	got, err := serveWire(t, body).ListItemSuppliersForItem(context.Background(), "dd8ac252-fe7d-4dd9-824f-1f0dfc1864a2")
	if err != nil {
		t.Fatalf("a recorded reply did not decode: %v", err)
	}
	checkSourcesAgainstRaw(t, rawRows(t, body, "results"), got)
	for i, sup := range got {
		if sup.LeadTimeSource != "" {
			t.Errorf("row %d: source = %q from a server that sent none", i, sup.LeadTimeSource)
		}
		if sup.LeadTimeDays <= 0 {
			t.Errorf("row %d: the lead time itself did not decode", i)
		}
	}
}

// THE ITEM DETAIL carries the marker twice: flat beside the flat lead time
// (the PRIMARY link's), and on every nested supplier row.
func TestLeadTimeSource_TheItemDetailCarriesItFlatAndNested(t *testing.T) {
	body := wireBody(t, "lead_time_source_item_detail.json")
	it, err := serveWire(t, body).GetItem(context.Background(), "dd8ac252-fe7d-4dd9-824f-1f0dfc1864a2")
	if err != nil {
		t.Fatalf("a recorded reply did not decode: %v", err)
	}
	requireEverySource(t, checkSourcesAgainstRaw(t, rawRows(t, body, "suppliers"), it.Suppliers))

	var flat map[string]any
	if err := json.Unmarshal(body, &flat); err != nil {
		t.Fatal(err)
	}
	if want, _ := flat["average_lead_time_source"].(string); it.AverageLeadTimeSource != LeadTimeSource(want) || want == "" {
		t.Errorf("flat source = %q, the server sent %q", it.AverageLeadTimeSource, want)
	}
}

// The flat source on each of its other served shapes: a primary link on its
// planning default, an item with no link at all (JSON null beside a null lead
// time), an anonymous reader (both keys WITHHELD), and a server predating the
// marker. Each is compared with the raw key rather than with a restated value.
func TestLeadTimeSource_TheFlatSourceOnEveryServedShape(t *testing.T) {
	cases := []struct {
		file string
		// present is whether the raw body must carry the key at all.
		present bool
		want    LeadTimeSource
	}{
		{"lead_time_source_item_detail_primary_default.json", true, LeadTimeSourceDefault},
		{"lead_time_source_item_detail_no_supplier.json", true, ""},
		{"lead_time_source_item_detail_anonymous.json", false, ""},
		{"lead_time_source_item_detail_pre1085.json", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			body := wireBody(t, tc.file)
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			rawSrc, present := raw["average_lead_time_source"]
			if present != tc.present {
				t.Fatalf("key present = %v in the recording, want %v", present, tc.present)
			}
			if s, _ := rawSrc.(string); LeadTimeSource(s) != tc.want {
				t.Fatalf("the recording's source is %v, want %q", rawSrc, tc.want)
			}
			it, err := serveWire(t, body).GetItem(context.Background(), "x")
			if err != nil {
				t.Fatalf("a recorded reply did not decode: %v", err)
			}
			if it.AverageLeadTimeSource != tc.want {
				t.Errorf("flat source = %q, want %q", it.AverageLeadTimeSource, tc.want)
			}
		})
	}
}

// THE SUPPLIER DETAIL's item rows are the same serializer and carry the same
// key; its pre-#1085 twin carries none.
func TestLeadTimeSource_TheSupplierDetailItemsCarryIt(t *testing.T) {
	for _, tc := range []struct {
		file   string
		served bool
	}{
		{"lead_time_source_supplier_detail.json", true},
		{"lead_time_source_supplier_detail_pre1085.json", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			body := wireBody(t, tc.file)
			sup, err := serveWire(t, body).GetSupplier(context.Background(), "1")
			if err != nil {
				t.Fatalf("a recorded reply did not decode: %v", err)
			}
			if len(sup.Items) == 0 {
				t.Fatal("the recording carries no item rows, so it proves nothing")
			}
			reached := checkSourcesAgainstRaw(t, rawRows(t, body, "items"), sup.Items)
			if tc.served {
				requireEverySource(t, reached)
			}
			for i, row := range sup.Items {
				if (row.LeadTimeSource != "") != tc.served {
					t.Errorf("item %d: source = %q, served = %v", i, row.LeadTimeSource, tc.served)
				}
			}
		})
	}
}

// THE WRITE: a nil lead time OMITS the key, which is what makes OMS store its
// planning default labelled `default`; a set one is sent, which OMS labels a
// recorded quote. The recording's `default` and `recorded` sevens were made by
// a POST omitting the key and a POST sending 7 (testdata/README.md).
func TestCreateItemSupplier_ANilLeadTimeOmitsTheKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		lead *int
		sent bool
	}{
		{"untouched", nil, false},
		{"typed", intptr(7), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.Unmarshal([]byte(readAll(t, r)), &body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}))
			t.Cleanup(srv.Close)
			if _, err := New(srv.URL).CreateItemSupplier(context.Background(), ItemSupplierWrite{
				Item: "itm-1", Supplier: 3, SupplierSKU: "S", QuantityPerPackage: 1, AverageLeadTime: tc.lead,
			}); err != nil {
				t.Fatal(err)
			}
			v, ok := body["average_lead_time"]
			if ok != tc.sent {
				t.Fatalf("average_lead_time present = %v (%v), want %v", ok, v, tc.sent)
			}
			if ok && v.(float64) != 7 {
				t.Errorf("average_lead_time = %v, want 7", v)
			}
		})
	}
}
