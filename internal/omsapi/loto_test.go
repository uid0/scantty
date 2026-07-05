package omsapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListLOTODevicesDecodesIntID feeds the REAL LOTODeviceSerializer shape:
// the loto models have no id override, so with DEFAULT_AUTO_FIELD=BigAutoField
// the PK is an integer. The old `ID string` typing crashed every row.
func TestListLOTODevicesDecodesIntID(t *testing.T) {
	body := `{"count":1,"results":[{
		"id":3,"device_type":"padlock","device_type_display":"Padlock","label":"Lock 3",
		"location":5,"location_name":"Bay 5","assigned_to":null,"status":"available",
		"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListLOTODevices(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListLOTODevices: %v", err)
	}
	if len(page.Results) != 1 || fmt.Sprintf("%v", page.Results[0].ID) != "3" {
		t.Fatalf("unexpected devices: %+v", page.Results)
	}
}

// TestListAssetEnergySourcesDecodesIntShapes feeds the REAL shape: an integer
// PK, an M2M `required_devices` that is an ARRAY OF INTEGERS (not strings), and
// a `derived_from` integer FK. All three old string typings crashed the decode.
func TestListAssetEnergySourcesDecodesIntShapes(t *testing.T) {
	body := `{"count":1,"results":[{
		"id":8,"asset":"a1b2c3d4-0000-4444-8888-abcdef012345","asset_name":"Mill",
		"source_type":"electrical","source_type_display":"Electrical","magnitude":"480V",
		"required_devices":[3,7],
		"required_devices_detail":[{"id":3,"device_type":"padlock","label":"Lock 3","status":"available"}],
		"derived_from":5,"is_stale":false,
		"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListAssetEnergySources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListAssetEnergySources: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(page.Results))
	}
	es := page.Results[0]
	if len(es.RequiredDevices) != 2 {
		t.Errorf("required_devices = %v, want 2 int ids", es.RequiredDevices)
	}
	if es.DerivedFrom == nil || *es.DerivedFrom != 5 {
		t.Errorf("derived_from = %v, want 5", es.DerivedFrom)
	}
	if len(es.RequiredDevicesDetail) != 1 || fmt.Sprintf("%v", es.RequiredDevicesDetail[0].ID) != "3" {
		t.Errorf("required_devices_detail = %+v", es.RequiredDevicesDetail)
	}
}
