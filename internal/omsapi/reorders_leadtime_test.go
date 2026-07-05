package omsapi

import (
	"encoding/json"
	"testing"
)

// TestReorderDataSupplierDecodesFloatAvgLeadTime guards the field-drift fix:
// the backend computes avg_lead_time as an AVERAGE → a JSON float (e.g. 3.0),
// but AvgLeadTime was typed int and crashed the "new PO from a supplier with
// reorder-queue items" flow with:
//
//	cannot unmarshal number 3.0 into ... avg_lead_time of type int
//
// AvgLeadTime is now float64.
func TestReorderDataSupplierDecodesFloatAvgLeadTime(t *testing.T) {
	var s ReorderDataSupplier
	if err := json.Unmarshal([]byte(`{"id":1,"name":"Acme","avg_lead_time":3.0,"total_items":2}`), &s); err != nil {
		t.Fatalf("unmarshal ReorderDataSupplier: %v", err)
	}
	if s.AvgLeadTime != 3.0 {
		t.Errorf("avg_lead_time = %v, want 3.0", s.AvgLeadTime)
	}
	var frac ReorderDataSupplier
	if err := json.Unmarshal([]byte(`{"avg_lead_time":2.5}`), &frac); err != nil {
		t.Fatalf("unmarshal fractional avg_lead_time: %v", err)
	}
	if frac.AvgLeadTime != 2.5 {
		t.Errorf("avg_lead_time = %v, want 2.5", frac.AvgLeadTime)
	}
}

// TestItemSupplierDecodesFloatAverageLeadTime guards the same class on the
// item-supplier struct — average_lead_time is likewise a float average.
func TestItemSupplierDecodesFloatAverageLeadTime(t *testing.T) {
	var it ItemSupplier
	if err := json.Unmarshal([]byte(`{"average_lead_time":4.0}`), &it); err != nil {
		t.Fatalf("unmarshal ItemSupplier: %v", err)
	}
	if it.LeadTimeDays != 4.0 {
		t.Errorf("LeadTimeDays = %v, want 4.0", it.LeadTimeDays)
	}
}
