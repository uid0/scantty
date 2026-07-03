package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

// TestCreateInventoryItem_Contract pins the create body: POST to the items
// collection, booleans always present, the serialized flag + mode carried
// through, location serialized as a string and category as a number, an NFPA
// rating of 0 preserved (not dropped), and the optional case fields omitted
// when case-based reordering is off.
func TestCreateInventoryItem_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"new-uuid","name":"Widget","is_serialized":true,"serial_tracking_mode":"consumable"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.CreateInventoryItem(context.Background(), ItemWrite{
		Name:               "Widget",
		SKU:                strptr("W-1"),
		CurrentStock:       10,
		MinimumStock:       2,
		ReorderQuantity:    1,
		Category:           intptr(3),
		Location:           strptr("5"),
		IsHazardous:        true,
		MSDSURL:            strptr("https://example.test/msds.pdf"),
		NFPAHealthHazard:   intptr(0), // 0 is a real rating, must survive
		IsSerialized:       true,
		SerialTrackingMode: strptr("consumable"),
		IsActive:           true,
	})
	if err != nil {
		t.Fatalf("CreateInventoryItem: %v", err)
	}
	if item == nil || item.ID != "new-uuid" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/items/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}

	// Required + always-present fields.
	if captured.body["name"] != "Widget" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["current_stock"].(float64) != 10 {
		t.Errorf("current_stock = %v", captured.body["current_stock"])
	}
	if captured.body["is_serialized"] != true {
		t.Errorf("is_serialized = %v", captured.body["is_serialized"])
	}
	if captured.body["serial_tracking_mode"] != "consumable" {
		t.Errorf("serial_tracking_mode = %v", captured.body["serial_tracking_mode"])
	}
	if captured.body["is_hazardous"] != true {
		t.Errorf("is_hazardous = %v", captured.body["is_hazardous"])
	}
	if captured.body["is_active"] != true {
		t.Errorf("is_active = %v", captured.body["is_active"])
	}
	if captured.body["msds_url"] != "https://example.test/msds.pdf" {
		t.Errorf("msds_url = %v", captured.body["msds_url"])
	}
	// category is a number, location is a string (viewset resolves it as a pk).
	if captured.body["category"].(float64) != 3 {
		t.Errorf("category = %v (%T)", captured.body["category"], captured.body["category"])
	}
	if captured.body["location"] != "5" {
		t.Errorf("location = %v (%T)", captured.body["location"], captured.body["location"])
	}
	// NFPA 0 must be present as 0, not omitted.
	v, present := captured.body["nfpa_health_hazard"]
	if !present || v.(float64) != 0 {
		t.Errorf("nfpa_health_hazard = %v (present=%v)", v, present)
	}
	// Case fields omitted when use_case_based_reorder is false.
	if _, present := captured.body["minimum_cases"]; present {
		t.Errorf("minimum_cases should be omitted when not case-based")
	}
	if _, present := captured.body["reorder_cases"]; present {
		t.Errorf("reorder_cases should be omitted when not case-based")
	}
}

// TestCreateInventoryItem_OmitsUnsetOptionals confirms that a minimal item
// still sends the booleans (false), and omits the optional strings/ids and —
// critically — serial_tracking_mode when the item isn't serialized (the model
// column is NOT NULL, so a null there would 400).
func TestCreateInventoryItem_OmitsUnsetOptionals(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"i2","name":"Basic"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateInventoryItem(context.Background(), ItemWrite{
		Name:            "Basic",
		CurrentStock:    0,
		MinimumStock:    0,
		ReorderQuantity: 1,
		IsActive:        true,
	}); err != nil {
		t.Fatalf("CreateInventoryItem: %v", err)
	}

	// Booleans present even when false / default.
	if body["is_serialized"] != false {
		t.Errorf("is_serialized = %v, want false", body["is_serialized"])
	}
	if body["is_hazardous"] != false {
		t.Errorf("is_hazardous = %v, want false", body["is_hazardous"])
	}
	if body["current_stock"].(float64) != 0 {
		t.Errorf("current_stock = %v, want 0", body["current_stock"])
	}
	// Optionals omitted.
	for _, k := range []string{"serial_tracking_mode", "category", "location", "sku", "description", "msds_url", "minimum_cases", "shelf_position"} {
		if _, present := body[k]; present {
			t.Errorf("%s should be omitted when unset, got %v", k, body[k])
		}
	}
}

// TestCreateInventoryItem_CaseBased confirms the case fields ride along when
// case-based reordering is enabled.
func TestCreateInventoryItem_CaseBased(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"i3","name":"Cased"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateInventoryItem(context.Background(), ItemWrite{
		Name:                "Cased",
		ReorderQuantity:     1,
		UseCaseBasedReorder: true,
		MinimumCases:        intptr(2),
		ReorderCases:        intptr(4),
		IsActive:            true,
	}); err != nil {
		t.Fatalf("CreateInventoryItem: %v", err)
	}
	if body["use_case_based_reorder"] != true {
		t.Errorf("use_case_based_reorder = %v", body["use_case_based_reorder"])
	}
	if body["minimum_cases"].(float64) != 2 {
		t.Errorf("minimum_cases = %v", body["minimum_cases"])
	}
	if body["reorder_cases"].(float64) != 4 {
		t.Errorf("reorder_cases = %v", body["reorder_cases"])
	}
}

// TestUpdateInventoryItem_Contract pins the edit path + method (PATCH to the
// item detail URL) and that the serialized flag/mode round-trip on update too.
func TestUpdateInventoryItem_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"abc","name":"Renamed","is_serialized":true,"serial_tracking_mode":"reusable"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.UpdateInventoryItem(context.Background(), "abc", ItemWrite{
		Name:               "Renamed",
		CurrentStock:       5,
		MinimumStock:       1,
		ReorderQuantity:    1,
		IsSerialized:       true,
		SerialTrackingMode: strptr("reusable"),
		IsActive:           false,
	})
	if err != nil {
		t.Fatalf("UpdateInventoryItem: %v", err)
	}
	if item == nil || item.SerialTrackingMode != "reusable" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/inventory/items/abc/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Renamed" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["is_serialized"] != true {
		t.Errorf("is_serialized = %v", captured.body["is_serialized"])
	}
	if captured.body["serial_tracking_mode"] != "reusable" {
		t.Errorf("serial_tracking_mode = %v", captured.body["serial_tracking_mode"])
	}
	// is_active=false must reach the server (no omitempty on the bool).
	if captured.body["is_active"] != false {
		t.Errorf("is_active = %v, want false", captured.body["is_active"])
	}
}

// TestDeleteInventoryItem_Contract pins the delete path + method and that a 204
// with no body is treated as success.
func TestDeleteInventoryItem_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteInventoryItem(context.Background(), "abc"); err != nil {
		t.Fatalf("DeleteInventoryItem: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/inventory/items/abc/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestGetItem_HydratesWriteFields confirms the fields the edit form needs to
// prefill (hazmat, ownership, is_active, notes) decode off the detail payload.
func TestGetItem_HydratesWriteFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"i9","name":"Hazmat Widget","sku":"HZ-1","current_stock":7,
			"is_hazardous":true,"msds_url":"https://x/msds","nfpa_health_hazard":0,
			"nfpa_fire_hazard":3,"is_serialized":true,"serial_tracking_mode":"reusable",
			"is_active":false,"notes":"handle with care"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	it, err := c.GetItem(context.Background(), "i9")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !it.IsHazardous || it.MSDSURL != "https://x/msds" {
		t.Errorf("hazmat not decoded: %+v", it)
	}
	if it.NFPAHealthHazard == nil || *it.NFPAHealthHazard != 0 {
		t.Errorf("nfpa_health_hazard = %v", it.NFPAHealthHazard)
	}
	if it.NFPAFireHazard == nil || *it.NFPAFireHazard != 3 {
		t.Errorf("nfpa_fire_hazard = %v", it.NFPAFireHazard)
	}
	if !it.IsSerialized || it.SerialTrackingMode != "reusable" {
		t.Errorf("serial fields not decoded: %+v", it)
	}
	if it.IsActive {
		t.Errorf("is_active = true, want false")
	}
	if it.Notes != "handle with care" {
		t.Errorf("notes = %q", it.Notes)
	}
}
