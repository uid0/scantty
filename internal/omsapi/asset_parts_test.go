package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListAllItems_Paginates confirms the part-picker's item loader follows the
// DRF `next` link across pages, so an item beyond page 1 is still selectable.
func TestListAllItems_Paginates(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[{"id":"item-2","name":"Filter"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":2,"next":"` + base + `/api/inventory/items/?page=2","previous":null,"results":[{"id":"item-1","name":"Belt"}]}`))
	}))
	defer srv.Close()
	base = srv.URL

	c := New(srv.URL)
	items, err := c.ListAllItems(context.Background())
	if err != nil {
		t.Fatalf("ListAllItems: %v", err)
	}
	if len(items) != 2 || items[0].ID != "item-1" || items[1].ID != "item-2" {
		t.Fatalf("items = %+v", items)
	}
}

// TestCreateAssetPart_Contract confirms the create path, method, and the full
// writable field set (asset, part, quantity_needed, is_required,
// maintenance_interval_days, notes) reach the backend, and the returned part
// decodes.
func TestCreateAssetPart_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated,
		`{"id":12,"asset":"asset-9","part":"item-1","part_name":"Drive belt","part_sku":"BELT-1","quantity_needed":2,"is_required":true,"maintenance_interval_days":90,"notes":"OEM only"}`,
		&cap)
	defer srv.Close()

	interval := 90
	c := New(srv.URL)
	part, err := c.CreateAssetPart(context.Background(), AssetPartWrite{
		Asset:                   "asset-9",
		Part:                    "item-1",
		QuantityNeeded:          2,
		IsRequired:              true,
		MaintenanceIntervalDays: &interval,
		Notes:                   "OEM only",
	})
	if err != nil {
		t.Fatalf("CreateAssetPart: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/asset-parts/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.body["asset"] != "asset-9" {
		t.Errorf("asset = %v", cap.body["asset"])
	}
	if cap.body["part"] != "item-1" {
		t.Errorf("part = %v", cap.body["part"])
	}
	if cap.body["quantity_needed"].(float64) != 2 {
		t.Errorf("quantity_needed = %v", cap.body["quantity_needed"])
	}
	if cap.body["is_required"] != true {
		t.Errorf("is_required = %v", cap.body["is_required"])
	}
	if cap.body["maintenance_interval_days"].(float64) != 90 {
		t.Errorf("maintenance_interval_days = %v", cap.body["maintenance_interval_days"])
	}
	if cap.body["notes"] != "OEM only" {
		t.Errorf("notes = %v", cap.body["notes"])
	}
	if part == nil || part.PartName != "Drive belt" || part.QuantityNeeded != 2 {
		t.Fatalf("unexpected part: %+v", part)
	}
}

// TestCreateAssetPart_NullableAndFalse confirms a nil maintenance interval rides
// as explicit JSON null (so "blank = on demand" clears the field) and that
// is_required:false is sent, not omitted — both need the no-omitempty tags.
func TestCreateAssetPart_NullableAndFalse(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated,
		`{"id":13,"asset":"asset-9","part":"item-2"}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.CreateAssetPart(context.Background(), AssetPartWrite{
		Asset:                   "asset-9",
		Part:                    "item-2",
		QuantityNeeded:          1,
		IsRequired:              false,
		MaintenanceIntervalDays: nil,
		Notes:                   "",
	})
	if err != nil {
		t.Fatalf("CreateAssetPart: %v", err)
	}
	v, present := cap.body["maintenance_interval_days"]
	if !present {
		t.Errorf("maintenance_interval_days must be present as null, was omitted")
	}
	if v != nil {
		t.Errorf("maintenance_interval_days = %v, want null", v)
	}
	if got, present := cap.body["is_required"]; !present || got != false {
		t.Errorf("is_required = %v present=%v, want false present", got, present)
	}
	if got, present := cap.body["notes"]; !present || got != "" {
		t.Errorf("notes = %v present=%v, want empty-string present", got, present)
	}
}

// TestUpdateAssetPart_Contract confirms edit is a PATCH to the /{id}/ detail URL
// carrying the changed fields.
func TestUpdateAssetPart_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":12,"asset":"asset-9","part":"item-1","quantity_needed":5,"is_required":false}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	part, err := c.UpdateAssetPart(context.Background(), "12", AssetPartWrite{
		Asset:          "asset-9",
		Part:           "item-1",
		QuantityNeeded: 5,
		IsRequired:     false,
		Notes:          "changed",
	})
	if err != nil {
		t.Fatalf("UpdateAssetPart: %v", err)
	}
	if cap.method != http.MethodPatch || cap.path != "/api/inventory/asset-parts/12/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.body["quantity_needed"].(float64) != 5 {
		t.Errorf("quantity_needed = %v", cap.body["quantity_needed"])
	}
	if part == nil || part.QuantityNeeded != 5 {
		t.Fatalf("unexpected part: %+v", part)
	}
}

// TestDeleteAssetPart_Contract confirms the DELETE hits the detail URL and a 204
// with no body is treated as success.
func TestDeleteAssetPart_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusNoContent, "", &cap)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteAssetPart(context.Background(), "12"); err != nil {
		t.Fatalf("DeleteAssetPart: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/inventory/asset-parts/12/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
}

// TestMarkAssetPartReplaced_Contract confirms the action is a POST to the
// underscore url_path and — for a non-serialized part (empty serial) — sends NO
// request body (back-compat), and that the refreshed part decodes
// (days_since_replacement → 0, needs_replacement → false, last_replaced_at set).
func TestMarkAssetPartReplaced_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":12,"asset":"asset-9","part":"item-1","last_replaced_at":"2026-07-06T11:00:00Z","days_since_replacement":0,"needs_replacement":false}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	part, err := c.MarkAssetPartReplaced(context.Background(), "12", "")
	if err != nil {
		t.Fatalf("MarkAssetPartReplaced: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/asset-parts/12/mark_replaced/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.body != nil {
		t.Errorf("mark_replaced with empty serial must send no body, got %v", cap.body)
	}
	if part == nil || part.LastReplacedAt == nil {
		t.Fatalf("expected last_replaced_at set: %+v", part)
	}
	if part.NeedsReplacement {
		t.Errorf("needs_replacement should be false after replace")
	}
	if part.DaysSinceReplacement == nil || *part.DaysSinceReplacement != 0 {
		t.Errorf("days_since_replacement = %v, want 0", part.DaysSinceReplacement)
	}
}

// TestMarkAssetPartReplaced_WithSerial confirms a non-empty replacement serial
// rides as the optional body {"replacement_serial_number": <serial>} (op-8nxe
// contract), and the returned part surfaces the recorded serial.
func TestMarkAssetPartReplaced_WithSerial(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":12,"asset":"asset-9","part":"item-1","last_replaced_at":"2026-07-06T11:00:00Z","days_since_replacement":0,"needs_replacement":false,"replacement_serial_number":"MG-2024-XYZ"}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	part, err := c.MarkAssetPartReplaced(context.Background(), "12", "MG-2024-XYZ")
	if err != nil {
		t.Fatalf("MarkAssetPartReplaced: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/asset-parts/12/mark_replaced/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.body == nil {
		t.Fatalf("mark_replaced with a serial must send a body")
	}
	if got := cap.body["replacement_serial_number"]; got != "MG-2024-XYZ" {
		t.Errorf("replacement_serial_number = %v, want MG-2024-XYZ", got)
	}
	if part == nil || part.ReplacementSerialNumber != "MG-2024-XYZ" {
		t.Fatalf("expected recorded serial on part: %+v", part)
	}
}

// TestAssetPart_PartDetailsSerialized covers decoding the nested part_details
// projection the TUI gates on: is_serialized:true lands on the typed field, and
// absent/false part_details decodes to the zero value (no prompt).
func TestAssetPart_PartDetailsSerialized(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":7,"asset":"asset-9","part":"item-1","part_name":"Magenta ink","part_details":{"is_serialized":true}}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	part, err := c.GetAssetPart(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetAssetPart: %v", err)
	}
	if !part.PartDetails.IsSerialized {
		t.Errorf("part_details.is_serialized should decode true: %+v", part.PartDetails)
	}

	// absent part_details → zero value → not serialized (no prompt).
	var cap2 capture
	srv2 := captureServer(t, http.StatusOK,
		`{"id":8,"asset":"asset-9","part":"item-2","part_name":"Belt"}`, &cap2)
	defer srv2.Close()
	c2 := New(srv2.URL)
	part2, err := c2.GetAssetPart(context.Background(), "8")
	if err != nil {
		t.Fatalf("GetAssetPart: %v", err)
	}
	if part2.PartDetails.IsSerialized {
		t.Errorf("absent part_details must decode is_serialized=false: %+v", part2.PartDetails)
	}
}

// TestGetAssetPart_Decode covers the read struct round-trip, including the
// nullable pointers left absent (maintenance_interval_days / last_replaced_at /
// days_since_replacement all null) and the integer pk landing in the any-typed
// ID as a float64.
func TestGetAssetPart_Decode(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":7,"asset":"asset-9","asset_name":"Lathe","part":"item-1","part_name":"Belt","part_sku":"B-1","quantity_needed":1,"is_required":true,"maintenance_interval_days":null,"last_replaced_at":null,"days_since_replacement":null,"needs_replacement":false,"notes":"n"}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	part, err := c.GetAssetPart(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetAssetPart: %v", err)
	}
	if cap.path != "/api/inventory/asset-parts/7/" {
		t.Fatalf("path = %q", cap.path)
	}
	if part.PartName != "Belt" || part.AssetName != "Lathe" {
		t.Errorf("name decode: %+v", part)
	}
	if part.MaintenanceIntervalDays != nil || part.LastReplacedAt != nil || part.DaysSinceReplacement != nil {
		t.Errorf("nullables should decode to nil: %+v", part)
	}
	if got := part.IDString(); got != "7" {
		t.Errorf("IDString = %q, want 7", got)
	}
}

// TestAssetPartIDString covers the pk coercion the detail/action URLs depend on:
// a large integer pk (arriving as float64) must render as a plain decimal, not
// scientific notation, and the string / nil shapes degrade gracefully.
func TestAssetPartIDString(t *testing.T) {
	cases := []struct {
		id   any
		want string
	}{
		{float64(12), "12"},
		{float64(1234567), "1234567"},
		{float64(987654321), "987654321"},
		{"abc", "abc"},
		{int(5), "5"},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := (AssetPart{ID: tc.id}).IDString(); got != tc.want {
			t.Errorf("IDString(%v[%T]) = %q, want %q", tc.id, tc.id, got, tc.want)
		}
	}
}
