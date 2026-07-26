package tui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestInventoryDetail_UsedByAssets_QueryAndRender drives the real Init fan-out
// against a fake OMS and asserts (a) the asset list is fetched with the
// consumable_for_item filter — the AssetPart through-model relation, not
// inventory_item — and (b) the returned assets render in the detail body with
// their through-model detail for THIS item.
func TestInventoryDetail_UsedByAssets_QueryAndRender(t *testing.T) {
	const itemID = "item-1"
	var assetQueries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/inventory/assets/"):
			assetQueries = append(assetQueries, r.URL.Query())
			// Two consuming assets; each nests its AssetPart rows, including one
			// for an unrelated part that must not be mistaken for this item's.
			_, _ = w.Write([]byte(`{"count":2,"results":[
				{"id":"asset-1","name":"Laser Cutter","asset_tag":"LC-001",
				 "status":"active","location_name":"Main Shop",
				 "parts":[{"id":1,"asset":"asset-1","part":"other-item","quantity_needed":9,"is_required":true},
				          {"id":2,"asset":"asset-1","part":"item-1","quantity_needed":2,"is_required":true}]},
				{"id":"asset-2","name":"Vinyl Cutter","asset_tag":"VC-002",
				 "status":"maintenance","location_name":"Annex",
				 "parts":[{"id":3,"asset":"asset-2","part":"item-1","quantity_needed":1}]}
			]}`))
		case strings.HasSuffix(r.URL.Path, "/metrics/"):
			_, _ = w.Write([]byte(`{"current_stock":4}`))
		default:
			_, _ = w.Write([]byte(`{"id":"item-1","name":"Ink Cartridge","sku":"INK-1","current_stock":4}`))
		}
	}))
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL)}, itemID)
	for _, msg := range ccDrainCmd(s.Init()) {
		s.Update(msg)
	}

	if len(assetQueries) != 1 {
		t.Fatalf("expected exactly one asset query, got %d: %v", len(assetQueries), assetQueries)
	}
	if got := assetQueries[0].Get("consumable_for_item"); got != itemID {
		t.Errorf("consumable_for_item = %q, want %q (full query %v)", got, itemID, assetQueries[0])
	}
	// The two relations are distinct: this section must NOT filter by
	// inventory_item (assets that ARE an instance of the item type).
	if assetQueries[0].Has("inventory_item") {
		t.Errorf("used-by query must not filter by inventory_item: %v", assetQueries[0])
	}

	if s.usedByLoading {
		t.Errorf("load should clear the loading flag")
	}
	if s.usedByErr != "" {
		t.Fatalf("unexpected load error: %s", s.usedByErr)
	}
	if len(s.usedBy) != 2 {
		t.Fatalf("expected 2 consuming assets, got %d", len(s.usedBy))
	}

	body := s.renderBody()
	for _, want := range []string{
		"Assets that use this item (2)",
		"Laser Cutter", "tag LC-001", "qty 2", "required", "active", "Main Shop", "ID asset-1",
		"Vinyl Cutter", "tag VC-002", "qty 1", "optional", "maintenance", "Annex", "ID asset-2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	// The unrelated part row belongs to a different item and must not be read.
	if strings.Contains(body, "qty 9") {
		t.Errorf("meta line used another item's AssetPart row:\n%s", body)
	}
}

// TestInventoryDetail_UsedByAssets_EmptyAndError covers the two non-list
// states. Empty is meaningful information ("nothing uses this"), so it gets a
// real message rather than a vanished section; an error says so instead of
// silently claiming the item is unused.
func TestInventoryDetail_UsedByAssets_EmptyAndError(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "item-1")
	s.item = &omsapi.Item{ID: "item-1", Name: "Widget", SKU: "W-1"}
	s.loading = false

	// While loading, the header is already drawn so the section can't pop in
	// later and shift the scroll position out from under the operator.
	if got := s.renderUsedBySection(); !strings.Contains(got, "Assets that use this item") {
		t.Errorf("loading state should still draw the header: %q", got)
	}

	s.Update(inventoryUsedByLoadedMsg{assets: nil})
	empty := s.renderUsedBySection()
	if !strings.Contains(empty, "No assets use this item as a part.") {
		t.Errorf("empty state missing its message: %q", empty)
	}
	if strings.Contains(empty, "(0)") {
		t.Errorf("empty state should not show a zero count: %q", empty)
	}

	s.Update(inventoryUsedByLoadedMsg{err: errFake("boom")})
	failed := s.renderUsedBySection()
	if !strings.Contains(failed, "unavailable: boom") {
		t.Errorf("error state should surface the reason: %q", failed)
	}
	if strings.Contains(failed, "No assets use this item") {
		t.Errorf("a failed fetch must not read as 'nothing uses this': %q", failed)
	}
}

// errFake is a minimal error for the load-failure branch.
type errFake string

func (e errFake) Error() string { return string(e) }
