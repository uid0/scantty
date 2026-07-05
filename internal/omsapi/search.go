package omsapi

import (
	"context"
	"net/url"
)

type SearchResult struct {
	Type string `json:"type"`
	// ID is `any` because backend search returns heterogeneous IDs:
	// integers for items/locations/SIGs, UUID strings for assets and
	// work orders. The TUI fmt.Sprints these into detail-screen URLs
	// so the underlying type doesn't matter — but Go's decoder needs
	// the breathing room to accept either shape.
	ID       any    `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	URL      string `json:"url,omitempty"`
}

type SearchResponse struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
	Count   int            `json:"count,omitempty"`
}

func (c *Client) Search(ctx context.Context, query string) (*SearchResponse, error) {
	if query == "" {
		return &SearchResponse{Query: query}, nil
	}
	var out SearchResponse
	q := url.Values{"q": []string{query}}
	if err := c.Get(ctx, "/api/search/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type InventorySummary struct {
	TotalItems      int `json:"total_items"`
	LowStockCount   int `json:"low_stock_count"`
	OutOfStockCount int `json:"out_of_stock_count"`
	PendingReorders int `json:"pending_reorders"`
}

func (c *Client) GetInventorySummary(ctx context.Context) (*InventorySummary, error) {
	// /api/dashboard/inventory-summary/ nests the counts under "inventory"
	// (not at the top level) and names the reorder count
	// items_with_pending_reorders — the flat top-level decode read all zeros.
	// out_of_stock_count is not emitted by the backend, so it stays 0.
	var raw struct {
		Inventory struct {
			TotalItems               int `json:"total_items"`
			LowStockCount            int `json:"low_stock_count"`
			OutOfStockCount          int `json:"out_of_stock_count"`
			ItemsWithPendingReorders int `json:"items_with_pending_reorders"`
		} `json:"inventory"`
	}
	if err := c.Get(ctx, "/api/dashboard/inventory-summary/", nil, &raw); err != nil {
		return nil, err
	}
	return &InventorySummary{
		TotalItems:      raw.Inventory.TotalItems,
		LowStockCount:   raw.Inventory.LowStockCount,
		OutOfStockCount: raw.Inventory.OutOfStockCount,
		PendingReorders: raw.Inventory.ItemsWithPendingReorders,
	}, nil
}
