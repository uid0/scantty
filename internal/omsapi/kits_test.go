// The kit client (op-8n0). These pin the four facts about the upstream API that
// every ScanTTY kit screen is built on, because every one of them is
// counter-intuitive and none of them is visible from a payload:
//
//	a kit is not on /items/      — the item viewset filters kits out of its
//	                               queryset, detail route included, so GetItem
//	                               MUST send include_kits=true or a kit is a 404
//	an item payload cannot say   — neither item serializer carries `is_kit`, so
//	whether it is a kit            "is this a kit?" is a /kits/ fetch whose 404
//	                               is the ANSWER, not an error
//	components are nested-write  — the bill of materials is saved with the kit;
//	                               there is no /kit-components/ endpoint
//	the reverse lookup is a bare — /items/{id}/kits/ returns an ARRAY, not the
//	array                          paginated envelope every list uses
package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetKit_ReadsTheBillOfMaterials pins the path and the decode, including
// the inherited item fields — a kit IS an inventory item upstream, and the
// embedded struct is what carries that through.
func TestGetKit_ReadsTheBillOfMaterials(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"kit-1","name":"Eufy Ink Kit","sku":"EIK-4","current_stock":0,
			"is_kit":true,"component_count":2,
			"components":[
				{"id":7,"component":"itm-c","component_name":"Cyan ink","component_sku":"CI-100",
				 "component_current_stock":0,"component_needs_reorder":true,"quantity":1,"notes":"CMYK"},
				{"id":8,"component":"itm-m","component_name":"Magenta ink","component_sku":"MI-100",
				 "component_current_stock":6,"quantity":2}
			]}`))
	}))
	defer srv.Close()

	kit, err := New(srv.URL).GetKit(context.Background(), "kit-1")
	if err != nil {
		t.Fatalf("GetKit: %v", err)
	}
	if gotPath != "/api/inventory/kits/kit-1/" {
		t.Errorf("path = %q", gotPath)
	}
	// The inherited half: a kit renders through the same code as any item.
	if kit.Name != "Eufy Ink Kit" || kit.SKU != "EIK-4" {
		t.Errorf("item fields did not decode: %+v", kit.Item)
	}
	if !kit.IsKit || kit.ComponentCount != 2 || len(kit.Components) != 2 {
		t.Fatalf("kit fields = %t/%d/%d", kit.IsKit, kit.ComponentCount, len(kit.Components))
	}
	first := kit.Components[0]
	if first.Component != "itm-c" || first.ComponentName != "Cyan ink" || first.Quantity != 1 {
		t.Errorf("component 1 = %+v", first)
	}
	// Zero stock has to survive as a READING, not as a missing field: a
	// component with none on the shelf is exactly the one worth ordering.
	if first.ComponentStock == nil || *first.ComponentStock != 0 {
		t.Errorf("component_current_stock 0 decoded as %v, want a pointer to 0", first.ComponentStock)
	}
	if !first.ComponentNeedsReorder {
		t.Errorf("component_needs_reorder did not decode")
	}
	if kit.Components[1].Quantity != 2 {
		t.Errorf("component 2 quantity = %d", kit.Components[1].Quantity)
	}
}

// TestIsNotKit_TreatsA404AsTheAnswer. This is the load-bearing one: a screen
// asking "is this a kit?" reads a 404 as "no" and everything else as "the
// question went unanswered". Getting that backwards would either hide every
// kit behind a transient error or claim every unreachable item is ordinary.
func TestIsNotKit_TreatsA404AsTheAnswer(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"not found is the answer no", http.StatusNotFound, `{"detail":"Not found."}`, true},
		{"envelope not_found is too", http.StatusNotFound, `{"error":{"code":"not_found","message":"nope"}}`, true},
		{"a server error is not an answer", http.StatusInternalServerError, `boom`, false},
		{"auth is not an answer", http.StatusUnauthorized, `{"error":{"code":"authentication_failed","message":"x"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := New(srv.URL).GetKit(context.Background(), "itm-1")
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := IsNotKit(err); got != tc.want {
				t.Errorf("IsNotKit(%v) = %t, want %t", err, got, tc.want)
			}
		})
	}
}

// TestGetItem_AsksForKitsToBeIncluded. Without this param a kit's id is a flat
// 404 on the item detail route, because InventoryItemViewSet.get_queryset
// excludes kits and get_object filters through it — so the item detail screen
// would report "not found" for a record that exists.
func TestGetItem_AsksForKitsToBeIncluded(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("include_kits")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"kit-1","name":"Eufy Ink Kit"}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).GetItem(context.Background(), "kit-1"); err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if gotQuery != "true" {
		t.Errorf("include_kits = %q, want \"true\" — a kit id would 404 without it", gotQuery)
	}
}

// TestUpdateKit_SendsTheWholeBillOfMaterials pins the write contract: PATCH to
// /kits/ (never /items/, which 404s for a kit and has no `components` field),
// with the components nested in the same request as the item fields.
func TestUpdateKit_SendsTheWholeBillOfMaterials(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method, captured.path = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"kit-1","name":"Eufy Ink Kit","is_kit":true}`))
	}))
	defer srv.Close()

	comps := []KitComponentWrite{
		{Component: "itm-c", Quantity: 1, Notes: "CMYK"},
		{Component: "itm-m", Quantity: 2},
	}
	kit, err := New(srv.URL).UpdateKit(context.Background(), "kit-1", KitWrite{
		ItemWrite:  ItemWrite{Name: "Eufy Ink Kit", IsActive: true},
		Components: &comps,
	})
	if err != nil {
		t.Fatalf("UpdateKit: %v", err)
	}
	if kit == nil || !kit.IsKit {
		t.Fatalf("unexpected kit: %+v", kit)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/inventory/kits/kit-1/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	// The embedded item fields have to marshal INLINE, not under a nested key —
	// which is the whole point of embedding ItemWrite rather than nesting it.
	if captured.body["name"] != "Eufy Ink Kit" {
		t.Errorf("item fields did not inline: %v", captured.body)
	}
	rows, ok := captured.body["components"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("components = %v", captured.body["components"])
	}
	first := rows[0].(map[string]any)
	if first["component"] != "itm-c" || first["quantity"].(float64) != 1 || first["notes"] != "CMYK" {
		t.Errorf("component row 1 = %v", first)
	}
	// The row pk is deliberately absent: the serializer upserts on (kit,
	// component), so sending an id would only be something to keep in sync.
	if _, present := first["id"]; present {
		t.Errorf("component row carried an id: %v", first)
	}
}

// TestUpdateKit_OmitsComponentsWhenUnset. A save that only touched the item
// fields must leave the bill of materials alone — and it CANNOT do that by
// sending an empty list, which upstream rejects as "a kit must contain at least
// one component".
func TestUpdateKit_OmitsComponentsWhenUnset(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"kit-1","is_kit":true}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).UpdateKit(context.Background(), "kit-1", KitWrite{
		ItemWrite: ItemWrite{Name: "Eufy Ink Kit"},
	}); err != nil {
		t.Fatalf("UpdateKit: %v", err)
	}
	if _, present := body["components"]; present {
		t.Errorf("components key was sent for an untouched bill of materials: %v", body)
	}
}

// TestListItemKits_DecodesTheBareArray. The action returns a plain list rather
// than the paginated envelope every other list uses, so a Page[T] decode here
// would silently yield nothing.
func TestListItemKits_DecodesTheBareArray(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"kit-1","name":"Eufy Ink Kit","sku":"EIK-4","is_active":true,
			 "quantity_in_kit":2,"supplier_name":"Acme","supplier_sku":"ACM-1",
			 "unit_cost":"34.99","component_count":5},
			{"id":"kit-2","name":"Retired bundle","is_active":false,"quantity_in_kit":null,"unit_cost":null}
		]`))
	}))
	defer srv.Close()

	kits, err := New(srv.URL).ListItemKits(context.Background(), "itm-c")
	if err != nil {
		t.Fatalf("ListItemKits: %v", err)
	}
	if gotPath != "/api/inventory/items/itm-c/kits/" {
		t.Errorf("path = %q", gotPath)
	}
	if len(kits) != 2 {
		t.Fatalf("got %d kits", len(kits))
	}
	if kits[0].QuantityInKit == nil || *kits[0].QuantityInKit != 2 {
		t.Errorf("quantity_in_kit = %v", kits[0].QuantityInKit)
	}
	if kits[0].UnitCost.Empty() || kits[0].ComponentCount != 5 || !kits[0].IsActive {
		t.Errorf("row 1 = %+v", kits[0])
	}
	// null must stay distinguishable from zero in both nullable fields: "the
	// view was not asked which component" is not "this kit holds none of it",
	// and "no price recorded" is not "free".
	if kits[1].QuantityInKit != nil {
		t.Errorf("null quantity_in_kit decoded as %v", *kits[1].QuantityInKit)
	}
	if !kits[1].UnitCost.Empty() {
		t.Errorf("null unit_cost decoded as %q", kits[1].UnitCost)
	}
}
