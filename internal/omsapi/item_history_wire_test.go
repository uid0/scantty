package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Decode tests for the item page's two readings, built from RECORDED OMS
// responses (testdata/item_stock_history*.json and testdata/item_usage_logs*.json,
// provenance in testdata/README.md).
//
// The guards read the RAW bytes beside the decode, so a later edit "fixing" a
// fixture to match a struct fails rather than quietly restoring whatever defect
// the edit was made to hide.

// itemHistoryServer answers GETs with a body chosen by the request, and records
// every request it saw.
func itemHistoryServer(t *testing.T, answer func(r *http.Request) []byte) (*Client, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(answer(r))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &seen
}

func TestGetItemStockHistory_DecodesTheRecordedBody(t *testing.T) {
	body := wireBody(t, "item_stock_history.json")
	c, seen := itemHistoryServer(t, func(*http.Request) []byte { return body })

	h, err := c.GetItemStockHistory(context.Background(), "4a2c5325-66fa-447d-8541-33627c903af8")
	if err != nil {
		t.Fatalf("a recorded stock history did not decode: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	r := (*seen)[0]
	if r.URL.Path != "/api/inventory/items/4a2c5325-66fa-447d-8541-33627c903af8/stock_history/" {
		t.Errorf("path = %q, want the underscored trailing-slashed action path", r.URL.Path)
	}
	if r.URL.Query().Get("include_kits") != "true" {
		t.Errorf("include_kits = %q, want \"true\" — a kit's id 404s on a detail action without it",
			r.URL.Query().Get("include_kits"))
	}

	if len(h.Series) != 4 || len(h.CycleCounts) != 1 || len(h.ReorderEvents) != 1 {
		t.Fatalf("series/cycle_counts/reorder_events = %d/%d/%d, want the recorded 4/1/1",
			len(h.Series), len(h.CycleCounts), len(h.ReorderEvents))
	}
	if got := h.Series[0].Date.Format("2006-01-02"); got != "2026-08-17" || h.Series[0].Count != 520 {
		t.Errorf("first snapshot = %s/%d, want 2026-08-17/520 (a bare DateField date)", got, h.Series[0].Count)
	}
	// THE LEVEL ON RECORD, NOT THE NUMBER COUNTED. The recording counted 380
	// against 393 on record; the view reads projected_count, so 393 is what
	// arrives. A renderer labelling this "counted" would be wrong by the delta.
	if h.CycleCounts[0].Count != 393 {
		t.Errorf("cycle count level = %d, want the recorded projected_count 393", h.CycleCounts[0].Count)
	}
	if got := h.ReorderEvents[0].Date.Format("2006-01-02"); got != "2026-09-14" {
		t.Errorf("reorder event date = %s, want 2026-09-14", got)
	}
	if h.Thresholds.ReorderPoint != 100 || h.Thresholds.Desired != 400 || h.CurrentStock != 360 {
		t.Errorf("thresholds/current = %d/%d/%d, want 100/400/360",
			h.Thresholds.ReorderPoint, h.Thresholds.Desired, h.CurrentStock)
	}

	raw := rawAsset(t, body)
	cycle := raw["cycle_counts"].([]any)[0].(map[string]any)
	if _, ok := cycle["date"].(string); !ok {
		t.Errorf("fixture's cycle count date = %#v, want the recorded JSON string", cycle["date"])
	}
	if _, ok := cycle["count"].(float64); !ok {
		t.Errorf("fixture's cycle count = %#v, want the recorded JSON number", cycle["count"])
	}
	if _, ok := raw["reorder_events"].([]any)[0].(map[string]any)["count"]; ok {
		t.Error("fixture's reorder event carries a count; the builder writes a date alone, so the fixture was edited")
	}
}

// AN ITEM WITH NO HISTORY IS A 200 WITH EMPTY LISTS, not an error — so a screen
// can say "no history" rather than "could not tell" (standing rule 3).
func TestGetItemStockHistory_NoHistoryIsAnAnswer(t *testing.T) {
	body := wireBody(t, "item_stock_history_empty.json")
	c, _ := itemHistoryServer(t, func(*http.Request) []byte { return body })

	h, err := c.GetItemStockHistory(context.Background(), "62f72d79-10b2-4546-b402-ea2a1a09d20d")
	if err != nil {
		t.Fatalf("a recorded empty history did not decode: %v", err)
	}
	if len(h.Series)+len(h.CycleCounts)+len(h.ReorderEvents) != 0 {
		t.Errorf("an item with no history decoded rows: %+v", h)
	}
	if h.CurrentStock != 6 || h.Thresholds.ReorderPoint != 2 || h.Thresholds.Desired != 6 {
		t.Errorf("current/thresholds = %d/%d/%d, want the recorded 6/2/6",
			h.CurrentStock, h.Thresholds.ReorderPoint, h.Thresholds.Desired)
	}
}

func TestListItemUsageLogs_DecodesTheRecordedRows(t *testing.T) {
	body := wireBody(t, "item_usage_logs.json")
	c, seen := itemHistoryServer(t, func(*http.Request) []byte { return body })

	logs, err := c.ListItemUsageLogs(context.Background(), "4a2c5325-66fa-447d-8541-33627c903af8")
	if err != nil {
		t.Fatalf("recorded usage logs did not decode: %v", err)
	}
	r := (*seen)[0]
	if r.URL.Path != "/api/inventory/usage-logs/" {
		t.Errorf("path = %q, want the usage-log list", r.URL.Path)
	}
	if got := r.URL.Query().Get("item_id"); got != "4a2c5325-66fa-447d-8541-33627c903af8" {
		t.Errorf("item_id = %q — without it the list is every item's usage", got)
	}
	if len(logs) != 3 {
		t.Fatalf("rows = %d, want the recorded 3", len(logs))
	}
	// Newest first, as the model orders it.
	if logs[0].ID != 3 || logs[2].ID != 1 {
		t.Errorf("ids = %d..%d, want the recorded newest-first 3..1", logs[0].ID, logs[2].ID)
	}
	if logs[1].QuantityUsed != 12 || logs[1].ChargedBy == nil || *logs[1].ChargedBy != 2 {
		t.Errorf("second row = %+v, want 12 used, recorded by user 2", logs[1])
	}
	if logs[1].Notes != "Laser cutter orientation, Saturday class\nTwo boxes opened at the bench" {
		t.Errorf("notes = %q, want the recorded two-line note with its newline intact", logs[1].Notes)
	}
	// NO RECORDER is a JSON null — an anonymous log_usage POST — and must stay
	// distinguishable from a recorded user.
	if logs[2].ChargedBy != nil {
		t.Errorf("charged_by = %d on the anonymous row, want nil", *logs[2].ChargedBy)
	}
	if got := logs[0].UsageDate.UTC().Format("2006-01-02 15:04"); got != "2026-09-14 04:54" {
		t.Errorf("usage_date = %s, want the recorded timestamp", got)
	}

	rows := rawAsset(t, body)["results"].([]any)
	first, last := rows[0].(map[string]any), rows[2].(map[string]any)
	if _, ok := first["id"].(float64); !ok {
		t.Errorf("fixture's id = %#v, want the recorded JSON number (UsageLog's BigAutoField)", first["id"])
	}
	if _, ok := first["charged_by"].(float64); !ok {
		t.Errorf("fixture's charged_by = %#v, want the recorded JSON number", first["charged_by"])
	}
	if v, ok := last["charged_by"]; !ok || v != nil {
		t.Errorf("fixture's anonymous charged_by = %#v (present %v), want a recorded JSON null", v, ok)
	}
}

// THE LIST WALKS EVERY PAGE. The recording is 51 uses of one item against OMS's
// page size of 50, which is the case the web's tab gets wrong: it reads page one
// and stops, so the 51st use is on no screen at all.
func TestListItemUsageLogs_WalksPastTheFiftiethUse(t *testing.T) {
	page1, page2 := wireBody(t, "item_usage_logs_page1.json"), wireBody(t, "item_usage_logs_page2.json")
	c, seen := itemHistoryServer(t, func(r *http.Request) []byte {
		if r.URL.Query().Get("page") == "2" {
			return page2
		}
		return page1
	})

	logs, err := c.ListItemUsageLogs(context.Background(), "998f2e58-dad4-4aad-835a-d9eb56213b6d")
	if err != nil {
		t.Fatalf("recorded pages did not decode: %v", err)
	}
	if len(*seen) != 2 {
		t.Errorf("requests = %d, want both pages", len(*seen))
	}
	if raw := rawAsset(t, page1); raw["count"] != float64(51) || raw["next"] == nil {
		t.Fatalf("fixture page 1 count/next = %#v/%#v, want the recorded 51 and a next link", raw["count"], raw["next"])
	}
	if len(logs) != 51 {
		t.Fatalf("rows = %d, want all 51 — the fiftieth-use cut is the defect this walk exists for", len(logs))
	}
	if logs[50].ID != 4 {
		t.Errorf("last row id = %d, want the recorded oldest use (4) off page 2", logs[50].ID)
	}
	for _, r := range *seen {
		if r.URL.Query().Get("item_id") != "998f2e58-dad4-4aad-835a-d9eb56213b6d" {
			t.Errorf("page %s lost the item_id filter", r.URL.Query().Get("page"))
		}
	}
}
