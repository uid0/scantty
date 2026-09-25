package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// item_history.go — the two READINGS behind the web item page's "Stock History"
// and "Usage Logs" tabs (frontend/src/pages/InventoryItemDetailPage.tsx). ScanTTY
// could WRITE a usage log from the item sheet and could read neither, so an
// operator at a terminal could not see how an item's stock moved or who used
// what. Both are read-only; nothing here writes.
//
// The wire shapes are pinned by RECORDED bodies (testdata/item_stock_history*.json,
// testdata/item_usage_logs*.json; provenance in testdata/README.md), and three
// facts about them are worth knowing before rendering either one.
//
//   - EVERY COUNT ON THE STOCK HISTORY IS BASE UNITS, AND THE TWO THRESHOLDS ARE
//     NOT. `series[].count` is `StockLevelSnapshot.count`, a copy of
//     `current_stock`; `cycle_counts[].count` is `StockReconciliation.projected_count`,
//     also `current_stock`; `current_stock` is itself. But `thresholds.reorder_point`
//     is `minimum_stock` and `desired` is `minimum_stock + reorder_quantity`, and
//     both of those are in the item's COUNT unit for a pack-counting item
//     (AGENTS.md). The web chart draws all five on one axis; a terminal must not.
//
//   - A CYCLE COUNT'S `count` IS THE LEVEL ON RECORD WHEN THE COUNT WAS TAKEN,
//     NOT THE NUMBER COUNTED. The view reads `rec.projected_count`, which the
//     model documents as "current_stock at the time of reconciliation" — the
//     figure BEFORE the physical count replaced it. Measured: a count of 380
//     against 393 on record is served as `{"count": 393}`. The web chart's
//     comment calls these "real physical counts"; the builder says otherwise, and
//     the builder is the contract.
//
//   - A USAGE LOG'S `quantity_used` IS BASE UNITS WHATEVER UNIT IT WAS ENTERED IN.
//     `log_usage` converts a pack count through `resolve_base_quantity` before it
//     writes, and echoes the entered unit only on its own POST reply — the unit an
//     operator typed is not stored on the row. So the list can say what was USED
//     in base units and cannot say what was typed.

// StockHistory is GET /api/inventory/items/{id}/stock_history/ — the payload the
// web's StockHistoryChart draws.
type StockHistory struct {
	// Series is the WEEKLY snapshot of current_stock written by the
	// snapshot_stock_levels beat task, oldest first. It has no backfill, so a new
	// deployment serves none for weeks.
	Series []StockHistoryPoint `json:"series"`
	// ReorderEvents is when a reorder request was RAISED for the item, oldest
	// first. It carries a date and nothing else — no quantity, no status.
	ReorderEvents []StockHistoryEvent `json:"reorder_events"`
	// CycleCounts is one row per StockReconciliation, oldest first, carrying the
	// level ON RECORD when the count was taken (see the file comment).
	CycleCounts []StockHistoryPoint `json:"cycle_counts"`
	// Thresholds are in the item's COUNT unit (see the file comment).
	Thresholds StockHistoryThresholds `json:"thresholds"`
	// CurrentStock is the live base-unit level.
	CurrentStock int `json:"current_stock"`
}

// StockHistoryPoint is one dated level. The date is a bare "2006-01-02": a
// snapshot's snapshot_date is a DateField, and a reconciliation's reconciled_at
// is cut to .date() by the view, in the server's time zone.
type StockHistoryPoint struct {
	Date  DateOnly `json:"date"`
	Count int      `json:"count"`
}

// StockHistoryEvent is one dated event with no level of its own.
type StockHistoryEvent struct {
	Date DateOnly `json:"date"`
}

// StockHistoryThresholds are the chart's two reference lines.
type StockHistoryThresholds struct {
	// ReorderPoint is minimum_stock.
	ReorderPoint int `json:"reorder_point"`
	// Desired is minimum_stock + reorder_quantity.
	Desired int `json:"desired"`
}

// GetItemStockHistory fetches an item's stock history
// (GET /api/inventory/items/{id}/stock_history/ — underscored, as DRF derives an
// action path from the method name). Auth-required.
//
// include_kits for the reason GetPurchaseHistory sends it: a detail action runs
// through the item viewset's kit-excluding get_queryset, so a kit's id is a flat
// 404 without it.
func (c *Client) GetItemStockHistory(ctx context.Context, id string) (*StockHistory, error) {
	var out StockHistory
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/items/%s/stock_history/", id), includeKitsValues(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UsageLog is one row of GET /api/inventory/usage-logs/ (UsageLogSerializer,
// `fields = "__all__"`).
//
// ONLY WHAT THE TERMINAL DRAWS IS DECODED. The cost snapshot (unit_cost,
// total_cost), the committee charged and the ledger transaction also ship; the
// web's Usage Logs tab shows none of them, and decoding a vendor figure nothing
// renders would only invite a render nobody has checked against the withholding
// rule (vendor_data_withheld).
type UsageLog struct {
	// ID is UsageLog's BigAutoField pk — a JSON NUMBER.
	ID int `json:"id"`
	// QuantityUsed is BASE UNITS (see the file comment).
	QuantityUsed int `json:"quantity_used"`
	// UsageDate is when the row was written (auto_now_add), a full timestamp.
	UsageDate time.Time `json:"usage_date"`
	Notes     string    `json:"notes"`
	// ChargedBy is the pk of the signed-in user who recorded it, and nil where
	// nobody was: log_usage is AllowAny (the QR-scan flow), and the work-order
	// material path writes no actor here at all — it puts the actor in Notes. So
	// nil is "no recorder on this row", never "anonymous" as a fact about who
	// took the stock. The web does not draw it; the terminal does, as a number,
	// because the only endpoint that turns a pk into a name
	// (/api/membership/users/) is staff-only.
	ChargedBy *int `json:"charged_by"`
}

// ListItemUsageLogs is every usage log for one item, newest first (the model's
// ordering is -usage_date).
//
// IT WALKS EVERY PAGE, where the web reads page one. The endpoint is paginated
// at 50, so the web's tab silently stops at the fiftieth use with nothing on the
// page saying there were more — the "reads page one and calls it all" defect
// AGENTS.md records against the reorder dashboard. A shop consumable passes fifty
// uses in a month.
func (c *Client) ListItemUsageLogs(ctx context.Context, itemID string) ([]UsageLog, error) {
	var all []UsageLog
	q := url.Values{"item_id": []string{itemID}}
	if err := IterPages[UsageLog](ctx, c, "/api/inventory/usage-logs/", q, func(batch []UsageLog) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}
