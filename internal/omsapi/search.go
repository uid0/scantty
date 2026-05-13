package omsapi

import (
	"context"
	"net/url"
)

type SearchResult struct {
	Type     string `json:"type"`
	ID       int    `json:"id"`
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
	var out InventorySummary
	if err := c.Get(ctx, "/api/dashboard/inventory-summary/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
