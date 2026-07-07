package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// reportSrv spins up a server that records the requested path and returns the
// given JSON body, and a client pointed at it.
func reportSrv(t *testing.T, body string) (*Client, *string) {
	t.Helper()
	got := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), got
}

func TestInventoryReports_Decode(t *testing.T) {
	c, path := reportSrv(t, `[{"category_id":3,"category_name":"Filament","total_items":12,
		"total_stock":340,"total_value":1234.5,"low_stock_count":2},
		{"category_id":null,"category_name":"Uncategorized","total_items":1,
		"total_stock":0,"total_value":0.0,"low_stock_count":1}]`)
	rows, err := c.InventoryStockByCategory(context.Background())
	if err != nil {
		t.Fatalf("StockByCategory: %v", err)
	}
	if *path != "/api/inventory/reports/inventory/stock_by_category/" {
		t.Fatalf("path = %q", *path)
	}
	if len(rows) != 2 || rows[0].TotalValue != 1234.5 || rows[0].TotalItems != 12 {
		t.Errorf("stock_by_category decode wrong: %+v", rows)
	}
	if rows[0].CategoryID == nil || *rows[0].CategoryID != 3 {
		t.Errorf("category_id[0] = %v", rows[0].CategoryID)
	}
	if rows[1].CategoryID != nil {
		t.Errorf("category_id[1] should be nil")
	}

	c2, path2 := reportSrv(t, `[{"item_id":"i-1","item_name":"PLA","item_sku":"PLA-1",
		"category_name":"Filament","reorder_count":5}]`)
	rf, err := c2.InventoryReorderFrequency(context.Background())
	if err != nil {
		t.Fatalf("ReorderFrequency: %v", err)
	}
	if *path2 != "/api/inventory/reports/inventory/reorder_frequency/" {
		t.Fatalf("path = %q", *path2)
	}
	if len(rf) != 1 || rf[0].ReorderCount != 5 || rf[0].ItemSKU != "PLA-1" {
		t.Errorf("reorder_frequency decode wrong: %+v", rf)
	}

	c3, path3 := reportSrv(t, `[{"location_id":7,"location_name":"Shelf A","total_items":4,
		"total_stock":40,"total_value":99.99}]`)
	vl, err := c3.InventoryValueByLocation(context.Background())
	if err != nil {
		t.Fatalf("ValueByLocation: %v", err)
	}
	if *path3 != "/api/inventory/reports/inventory/value_by_location/" {
		t.Fatalf("path = %q", *path3)
	}
	if len(vl) != 1 || vl[0].TotalValue != 99.99 || vl[0].LocationName != "Shelf A" {
		t.Errorf("value_by_location decode wrong: %+v", vl)
	}
}

func TestPurchasingReports_Decode(t *testing.T) {
	c, path := reportSrv(t, `[{"supplier_id":2,"supplier_name":"Acme","total_orders":9,
		"total_spend":4200.75,"avg_order_value":466.75}]`)
	ss, err := c.PurchasingSpendBySupplier(context.Background())
	if err != nil {
		t.Fatalf("SpendBySupplier: %v", err)
	}
	if *path != "/api/reorders/reports/purchasing/spend_by_supplier/" {
		t.Fatalf("path = %q", *path)
	}
	if len(ss) != 1 || ss[0].TotalSpend != 4200.75 || ss[0].AvgOrderValue != 466.75 {
		t.Errorf("spend_by_supplier decode wrong: %+v", ss)
	}

	c2, path2 := reportSrv(t, `[{"category_id":1,"category_name":"Tools","total_items":3,
		"total_quantity":30,"total_spend":150.0}]`)
	sc, err := c2.PurchasingSpendByCategory(context.Background())
	if err != nil {
		t.Fatalf("SpendByCategory: %v", err)
	}
	if *path2 != "/api/reorders/reports/purchasing/spend_by_category/" {
		t.Fatalf("path = %q", *path2)
	}
	if len(sc) != 1 || sc[0].TotalSpend != 150.0 || sc[0].TotalQuantity != 30 {
		t.Errorf("spend_by_category decode wrong: %+v", sc)
	}

	c3, path3 := reportSrv(t, `[{"supplier_id":2,"supplier_name":"Acme","item_name":"Bolt",
		"total_orders":4,"avg_estimated_lead_time":5.0,"avg_actual_lead_time":6.5,
		"avg_variance":1.5,"on_time_rate":0.75}]`)
	lt, err := c3.PurchasingLeadTimeAnalysis(context.Background())
	if err != nil {
		t.Fatalf("LeadTime: %v", err)
	}
	if *path3 != "/api/reorders/reports/purchasing/lead_time_analysis/" {
		t.Fatalf("path = %q", *path3)
	}
	if len(lt) != 1 || lt[0].AvgActualLeadTime != 6.5 || lt[0].OnTimeRate != 0.75 {
		t.Errorf("lead_time decode wrong: %+v", lt)
	}

	// price_trends: null price_change_percentage → nil pointer.
	c4, path4 := reportSrv(t, `[{"item_id":"i-1","item_name":"Bolt","supplier_name":"Acme",
		"price_changes":3,"min_unit_cost":0.10,"max_unit_cost":0.25,"latest_unit_cost":0.20,
		"price_change_percentage":100.0},
		{"item_id":"i-2","item_name":"Nut","supplier_name":"Acme",
		"price_changes":1,"min_unit_cost":0.05,"max_unit_cost":0.05,"latest_unit_cost":0.05,
		"price_change_percentage":null}]`)
	pt, err := c4.PurchasingPriceTrends(context.Background())
	if err != nil {
		t.Fatalf("PriceTrends: %v", err)
	}
	if *path4 != "/api/reorders/reports/purchasing/price_trends/" {
		t.Fatalf("path = %q", *path4)
	}
	if len(pt) != 2 || pt[0].LatestUnitCost != 0.20 {
		t.Errorf("price_trends decode wrong: %+v", pt)
	}
	if pt[0].PriceChangePercentage == nil || *pt[0].PriceChangePercentage != 100.0 {
		t.Errorf("price_change_percentage[0] = %v", pt[0].PriceChangePercentage)
	}
	if pt[1].PriceChangePercentage != nil {
		t.Errorf("price_change_percentage[1] should be nil")
	}
}

func TestAssetReports_Decode(t *testing.T) {
	c, path := reportSrv(t, `[{"status":"operational","status_display":"Operational","count":14},
		{"status":"retired","status_display":"Retired","count":3}]`)
	bs, err := c.AssetsByStatus(context.Background())
	if err != nil {
		t.Fatalf("AssetsByStatus: %v", err)
	}
	if *path != "/api/inventory/reports/assets/assets_by_status/" {
		t.Fatalf("path = %q", *path)
	}
	if len(bs) != 2 || bs[0].Count != 14 || bs[0].StatusDisplay != "Operational" {
		t.Errorf("assets_by_status decode wrong: %+v", bs)
	}

	// maintenance_due: a part row + an in-maintenance row with null intervals.
	c2, path2 := reportSrv(t, `[{"asset_id":"a-1","asset_name":"Lathe","asset_tag":"DMS-260010101",
		"part_id":"p-1","part_name":"Belt","part_sku":"B-1","maintenance_interval_days":90,
		"days_since_replacement":120,"days_overdue":30,"last_replaced_at":"2026-03-01T00:00:00Z"},
		{"asset_id":"a-2","asset_name":"Mill","asset_tag":"DMS-260010102",
		"maintenance_interval_days":null,"days_since_replacement":null,"days_overdue":null,
		"last_replaced_at":null,"status":"in_maintenance"}]`)
	md, err := c2.AssetMaintenanceDue(context.Background())
	if err != nil {
		t.Fatalf("MaintenanceDue: %v", err)
	}
	if *path2 != "/api/inventory/reports/assets/maintenance_due/" {
		t.Fatalf("path = %q", *path2)
	}
	if len(md) != 2 {
		t.Fatalf("maintenance_due len = %d", len(md))
	}
	if md[0].DaysOverdue == nil || *md[0].DaysOverdue != 30 || md[0].PartName != "Belt" {
		t.Errorf("maintenance_due[0] wrong: %+v", md[0])
	}
	if md[1].DaysOverdue != nil || md[1].MaintenanceIntervalDays != nil || md[1].Status != "in_maintenance" {
		t.Errorf("maintenance_due[1] in-maintenance row wrong: %+v", md[1])
	}

	c3, path3 := reportSrv(t, `[{"asset_id":"a-1","asset_name":"Lathe","asset_tag":"DMS-260010101",
		"total_sessions":8,"total_hours":40.5,"avg_hours_per_session":5.0625}]`)
	au, err := c3.AssetUtilization(context.Background())
	if err != nil {
		t.Fatalf("AssetUtilization: %v", err)
	}
	if *path3 != "/api/inventory/reports/assets/utilization/" {
		t.Fatalf("path = %q", *path3)
	}
	if len(au) != 1 || au[0].TotalHours != 40.5 || au[0].TotalSessions != 8 {
		t.Errorf("asset utilization decode wrong: %+v", au)
	}

	// tco: cost fields are Decimal-as-STRING (not float).
	c4, path4 := reportSrv(t, `[{"asset_id":"a-1","asset_name":"Lathe","asset_tag":"DMS-260010101",
		"maintenance_days_last_90":6,"scheduled_maintenance_cost":"100.00",
		"unscheduled_maintenance_cost":"50.00","repair_cost":"25.00","tco":"175.00",
		"preventive_maintenance_cost":"100.00","vendor_maintenance_cost":"0.00",
		"total_maintenance_cost_90d":"175.00"}]`)
	tco, err := c4.AssetTCO(context.Background())
	if err != nil {
		t.Fatalf("AssetTCO: %v", err)
	}
	if *path4 != "/api/inventory/reports/assets/tco/" {
		t.Fatalf("path = %q", *path4)
	}
	if len(tco) != 1 || tco[0].TotalMaintenanceCost90d != "175.00" || tco[0].ScheduledMaintenanceCost != "100.00" {
		t.Errorf("tco decode wrong: %+v", tco)
	}
	if tco[0].MaintenanceDaysLast90 != 6 {
		t.Errorf("tco maintenance_days_last_90 = %d", tco[0].MaintenanceDaysLast90)
	}
}

// TestReports_EmptyArray: every report tolerates an empty array (no rows) and a
// pagination envelope (defensive — some DRF configs wrap even @action lists).
func TestReports_EmptyArray(t *testing.T) {
	c, _ := reportSrv(t, `[]`)
	if rows, err := c.InventoryStockByCategory(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("empty array should decode to 0 rows: %v %v", rows, err)
	}

	c2, _ := reportSrv(t, `{"count":0,"next":null,"previous":null,"results":[]}`)
	if rows, err := c2.AssetTCO(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("envelope form should decode to 0 rows: %v %v", rows, err)
	}
}
