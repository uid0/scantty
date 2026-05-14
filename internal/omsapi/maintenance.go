package omsapi

import (
	"context"
	"net/url"
	"time"
)

type MaintenanceItem struct {
	ID                  string     `json:"id"`
	Asset               string     `json:"asset"`
	AssetName           string     `json:"asset_name,omitempty"`
	Title               string     `json:"title"`
	Description         string     `json:"description,omitempty"`
	Instructions        string     `json:"instructions,omitempty"`
	IntervalDays        *int       `json:"interval_days,omitempty"`
	EstimatedCost       string     `json:"estimated_cost,omitempty"`
	EstimatedTimeMin    *int       `json:"estimated_time_minutes,omitempty"`
	IsOverdue           bool       `json:"is_overdue,omitempty"`
	DaysOverdue         *int       `json:"days_overdue,omitempty"`
	NextDueAt           *time.Time `json:"next_due_at,omitempty"`
	LastCompletedAt     *time.Time `json:"last_completed_at,omitempty"`
}

func (c *Client) ListMaintenanceItems(ctx context.Context, q url.Values) (*Page[MaintenanceItem], error) {
	return GetPage[MaintenanceItem](ctx, c, "/api/inventory/maintenance-items/", q)
}

type AssetProblem struct {
	ID          string    `json:"id"`
	Asset       string    `json:"asset"`
	AssetName   string    `json:"asset_name,omitempty"`
	AssetTag    string    `json:"asset_tag,omitempty"`
	ReportedBy  string    `json:"reported_by,omitempty"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type AssetProblemCreate struct {
	Asset       string `json:"asset"`
	Description string `json:"description"`
	ReportedBy  string `json:"reported_by,omitempty"`
}

func (c *Client) ListAssetProblems(ctx context.Context, q url.Values) (*Page[AssetProblem], error) {
	return GetPage[AssetProblem](ctx, c, "/api/inventory/asset-problems/", q)
}

func (c *Client) CreateAssetProblem(ctx context.Context, req AssetProblemCreate) (*AssetProblem, error) {
	var out AssetProblem
	if err := c.Post(ctx, "/api/inventory/asset-problems/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
