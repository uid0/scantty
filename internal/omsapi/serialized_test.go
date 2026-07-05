package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestListSerializedComponents_EnvelopeAndFilters pins the list path and the
// three supported filters, and confirms the paginated envelope decodes.
func TestListSerializedComponents_EnvelopeAndFilters(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[
			{"id":"c1","item":"i1","serial_number":"SN-1","status":"in_stock",
			 "status_display":"In Stock","tracking_mode":"consumable",
			 "available_actions":["install","retire"]}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	units, err := c.ListSerializedComponents(context.Background(), url.Values{
		"item":               {"i1"},
		"status":             {"in_stock"},
		"installed_in_asset": {"a1"},
	})
	if err != nil {
		t.Fatalf("ListSerializedComponents: %v", err)
	}
	if gotPath != "/api/inventory/serialized-components/" {
		t.Fatalf("path = %q", gotPath)
	}
	for k, want := range map[string]string{"item": "i1", "status": "in_stock", "installed_in_asset": "a1"} {
		if got := gotQuery.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
	if len(units) != 1 || units[0].SerialNumber != "SN-1" {
		t.Fatalf("units = %+v", units)
	}
	if units[0].TrackingMode != "consumable" || len(units[0].AvailableActions) != 2 {
		t.Errorf("decoded fields wrong: %+v", units[0])
	}
}

// TestListSerializedComponents_BareArray confirms MaybeList tolerance for a
// non-paginated array (some deployments return a bare list).
func TestListSerializedComponents_BareArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"c1","serial_number":"SN-A"},{"id":"c2","serial_number":"SN-B"}]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	units, err := c.ListSerializedComponents(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListSerializedComponents: %v", err)
	}
	if len(units) != 2 || units[1].SerialNumber != "SN-B" {
		t.Fatalf("units = %+v", units)
	}
}

// TestCreateSerializedComponent_Contract pins the create body: item +
// serial_number required, provenance_purchase_order_item recorded, and the
// optional lot / provenance_delivery_item omitted when unset.
func TestCreateSerializedComponent_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new-uuid","serial_number":"SN-100","status":"received"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	comp, err := c.CreateSerializedComponent(context.Background(), SerializedComponentCreate{
		Item:                        "item-uuid",
		SerialNumber:                "SN-100",
		ProvenancePurchaseOrderItem: 42,
	})
	if err != nil {
		t.Fatalf("CreateSerializedComponent: %v", err)
	}
	if comp == nil || comp.Status != "received" {
		t.Fatalf("unexpected component: %+v", comp)
	}
	if captured.method != "POST" || captured.path != "/api/inventory/serialized-components/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["item"] != "item-uuid" {
		t.Errorf("item = %v", captured.body["item"])
	}
	if captured.body["serial_number"] != "SN-100" {
		t.Errorf("serial_number = %v", captured.body["serial_number"])
	}
	if captured.body["provenance_purchase_order_item"].(float64) != 42 {
		t.Errorf("provenance_purchase_order_item = %v", captured.body["provenance_purchase_order_item"])
	}
	if _, present := captured.body["lot"]; present {
		t.Errorf("lot should be omitted when empty, got %v", captured.body["lot"])
	}
	if _, present := captured.body["provenance_delivery_item"]; present {
		t.Errorf("provenance_delivery_item should be omitted when unset")
	}
}

// TestSerializedComponentAction_InstallContract pins the action URL shape
// (/{id}/{action}/), the asset field on install, and that the response
// decodes both the updated component and the recorded event.
func TestSerializedComponentAction_InstallContract(t *testing.T) {
	var captured struct {
		path string
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","status":"installed","installed_in_asset":"a1",
			"event":{"id":"e1","action":"install","action_display":"Install","asset_name":"Printer 3"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.SerializedComponentAction(context.Background(), "c1", SerialActionInstall, SerializedComponentAction{
		Asset: "a1",
		Notes: "into printer",
	})
	if err != nil {
		t.Fatalf("SerializedComponentAction: %v", err)
	}
	if captured.path != "/api/inventory/serialized-components/c1/install/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["asset"] != "a1" {
		t.Errorf("asset = %v", captured.body["asset"])
	}
	if _, present := captured.body["disposal_reason"]; present {
		t.Errorf("disposal_reason should be omitted for install")
	}
	if res.Status != "installed" || res.InstalledInAsset != "a1" {
		t.Errorf("component not decoded: %+v", res.SerializedComponent)
	}
	if res.Event == nil || res.Event.Action != "install" || res.Event.AssetName != "Printer 3" {
		t.Errorf("event not decoded: %+v", res.Event)
	}
}

// TestSerializedComponentAction_LifecycleVerbs pins the four no-argument /
// reason-only lifecycle actions the TUI drives (receive / consume / retire /
// dispose): each hits /{id}/{action}/, receive/consume/retire carry only the
// optional notes (never asset or disposal_reason), and dispose carries the
// required disposal_reason. Every response decodes the updated status + event.
func TestSerializedComponentAction_LifecycleVerbs(t *testing.T) {
	cases := []struct {
		action     string
		req        SerializedComponentAction
		wantStatus string
		wantBody   map[string]string // fields that must be present + equal
		absent     []string          // fields that must be omitted
	}{
		{
			action: SerialActionReceive, req: SerializedComponentAction{Notes: "off the truck"},
			wantStatus: SerialStatusInStock,
			wantBody:   map[string]string{"notes": "off the truck"},
			absent:     []string{"asset", "disposal_reason"},
		},
		{
			action: SerialActionConsume, req: SerializedComponentAction{},
			wantStatus: SerialStatusConsumed,
			wantBody:   map[string]string{},
			absent:     []string{"asset", "disposal_reason", "notes"},
		},
		{
			action: SerialActionRetire, req: SerializedComponentAction{Notes: "worn out"},
			wantStatus: SerialStatusRetired,
			wantBody:   map[string]string{"notes": "worn out"},
			absent:     []string{"asset", "disposal_reason"},
		},
		{
			action: SerialActionDispose, req: SerializedComponentAction{DisposalReason: "cracked housing"},
			wantStatus: SerialStatusDisposed,
			wantBody:   map[string]string{"disposal_reason": "cracked housing"},
			absent:     []string{"asset"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"c1","status":"` + tc.wantStatus + `",
					"event":{"id":"e1","action":"` + tc.action + `","action_display":"X"}}`))
			}))
			defer srv.Close()

			c := New(srv.URL)
			res, err := c.SerializedComponentAction(context.Background(), "c1", tc.action, tc.req)
			if err != nil {
				t.Fatalf("SerializedComponentAction(%s): %v", tc.action, err)
			}
			wantPath := "/api/inventory/serialized-components/c1/" + tc.action + "/"
			if gotPath != wantPath {
				t.Fatalf("path = %q, want %q", gotPath, wantPath)
			}
			for k, want := range tc.wantBody {
				if got, _ := gotBody[k].(string); got != want {
					t.Errorf("body[%q] = %v, want %q", k, gotBody[k], want)
				}
			}
			for _, k := range tc.absent {
				if _, present := gotBody[k]; present {
					t.Errorf("body[%q] should be omitted for %s, got %v", k, tc.action, gotBody[k])
				}
			}
			if res.Status != tc.wantStatus {
				t.Errorf("res.Status = %q, want %q", res.Status, tc.wantStatus)
			}
			if res.Event == nil || res.Event.Action != tc.action {
				t.Errorf("event not decoded for %s: %+v", tc.action, res.Event)
			}
		})
	}
}

// TestSerializedForecast_NullFields confirms the forecast decodes a bare
// array and that null days_until_stockout / lead_time_days land as nil
// pointers (rather than a decode error).
func TestSerializedForecast_NullFields(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"item_id":"i1","item_name":"Filament","sku":"F-1","available_stock":3,
			 "avg_daily_use":0.4286,"days_until_stockout":7.0,"reorder_point":5,
			 "needs_reorder":true,"projected_stockout_date":"2026-07-08","lead_time_days":2.0},
			{"item_id":"i2","item_name":"Bearing","available_stock":10,
			 "avg_daily_use":0.0,"days_until_stockout":null,"reorder_point":2,
			 "needs_reorder":false,"lead_time_days":null}]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	rows, err := c.SerializedForecast(context.Background(), url.Values{"low_stock_only": {"true"}})
	if err != nil {
		t.Fatalf("SerializedForecast: %v", err)
	}
	if gotPath != "/api/inventory/reports/inventory/serialized_forecast/" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery.Get("low_stock_only") != "true" {
		t.Errorf("low_stock_only = %q", gotQuery.Get("low_stock_only"))
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].DaysUntilStockout == nil || *rows[0].DaysUntilStockout != 7.0 {
		t.Errorf("row0 days_until_stockout = %v", rows[0].DaysUntilStockout)
	}
	if !rows[0].NeedsReorder || rows[0].ProjectedStockoutDate != "2026-07-08" {
		t.Errorf("row0 fields wrong: %+v", rows[0])
	}
	if rows[1].DaysUntilStockout != nil {
		t.Errorf("row1 days_until_stockout should be nil, got %v", *rows[1].DaysUntilStockout)
	}
	if rows[1].LeadTimeDays != nil {
		t.Errorf("row1 lead_time_days should be nil")
	}
}

// TestListComponentUsageEvents_Filter pins the events path + component filter.
func TestListComponentUsageEvents_Filter(t *testing.T) {
	var gotPath, gotComponent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotComponent = r.URL.Query().Get("component")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"e1","component":"c1","action":"receive"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	events, err := c.ListComponentUsageEvents(context.Background(), url.Values{"component": {"c1"}})
	if err != nil {
		t.Fatalf("ListComponentUsageEvents: %v", err)
	}
	if gotPath != "/api/inventory/component-usage-events/" || gotComponent != "c1" {
		t.Fatalf("path/component = %q %q", gotPath, gotComponent)
	}
	if len(events) != 1 || events[0].Action != "receive" {
		t.Fatalf("events = %+v", events)
	}
}
