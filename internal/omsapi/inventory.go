package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type LookupResult struct {
	Type     string `json:"type"`
	ID       int    `json:"id"`
	Name     string `json:"name"`
	SKU      string `json:"sku,omitempty"`
	Location string `json:"location,omitempty"`
	Code     string `json:"code,omitempty"`
}

func (c *Client) LookupCode(ctx context.Context, code string) (*LookupResult, error) {
	if code == "" {
		return nil, &APIError{Code: "invalid_code", Message: "code is empty"}
	}
	var out LookupResult
	q := url.Values{"code": []string{strings.ToUpper(code)}}
	if err := c.Get(ctx, "/api/inventory/lookup-code/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Item struct {
	ID            int      `json:"id"`
	Name          string   `json:"name"`
	SKU           string   `json:"sku"`
	Description   string   `json:"description,omitempty"`
	Category      *int     `json:"category,omitempty"`
	Location      *int     `json:"location,omitempty"`
	Stock         int      `json:"stock"`
	ReorderLevel  int      `json:"reorder_level,omitempty"`
	NeedsReorder  bool     `json:"needs_reorder,omitempty"`
	ReorderStatus string   `json:"reorder_status,omitempty"`
	Suppliers     []int    `json:"suppliers,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

func (c *Client) ListItems(ctx context.Context, q url.Values) (*Page[Item], error) {
	return GetPage[Item](ctx, c, "/api/inventory/items/", q)
}

func (c *Client) GetItem(ctx context.Context, id int) (*Item, error) {
	var out Item
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/items/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ScanItem(ctx context.Context, id int) (*Item, error) {
	var out Item
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/items/%d/scan/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Asset struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Location    *int   `json:"location,omitempty"`
	Status      string `json:"status,omitempty"`
}

func (c *Client) ListAssets(ctx context.Context, q url.Values) (*Page[Asset], error) {
	return GetPage[Asset](ctx, c, "/api/inventory/assets/", q)
}

func (c *Client) GetAsset(ctx context.Context, id int) (*Asset, error) {
	var out Asset
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/assets/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ScanAsset(ctx context.Context, id int) (*Asset, error) {
	var out Asset
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/assets/%d/scan/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Location struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Parent   *int   `json:"parent,omitempty"`
	Code     string `json:"code,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

func (c *Client) ListLocations(ctx context.Context, q url.Values) (*Page[Location], error) {
	return GetPage[Location](ctx, c, "/api/inventory/locations/", q)
}

type Category struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Parent *int   `json:"parent,omitempty"`
}

func (c *Client) ListCategories(ctx context.Context, q url.Values) (*Page[Category], error) {
	return GetPage[Category](ctx, c, "/api/inventory/categories/", q)
}

type Supplier struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Contact string `json:"contact,omitempty"`
}

func (c *Client) ListSuppliers(ctx context.Context, q url.Values) (*Page[Supplier], error) {
	return GetPage[Supplier](ctx, c, "/api/inventory/suppliers/", q)
}

type ItemSupplier struct {
	ID            int     `json:"id"`
	Item          int     `json:"item"`
	Supplier      int     `json:"supplier"`
	SupplierSKU   string  `json:"supplier_sku,omitempty"`
	PackQuantity  int     `json:"pack_quantity,omitempty"`
	UnitCost      float64 `json:"unit_cost,omitempty"`
	LeadTimeDays  int     `json:"lead_time_days,omitempty"`
	IsPreferred   bool    `json:"is_preferred,omitempty"`
	URL           string  `json:"url,omitempty"`
}

func (c *Client) ListItemSuppliers(ctx context.Context, q url.Values) (*Page[ItemSupplier], error) {
	return GetPage[ItemSupplier](ctx, c, "/api/inventory/item-suppliers/", q)
}

type Fixture struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Location *int   `json:"location,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (c *Client) ScanFixture(ctx context.Context, id int) (*Fixture, error) {
	var out Fixture
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/fixtures/%d/scan/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
