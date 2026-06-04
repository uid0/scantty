package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// PMSchedule mirrors backend PMScheduleSerializer — one recurring PM
// task tied to an asset. id and asset are UUID; the computed fields
// (status, days_until_due, days_since_last) are pre-projected by
// SerializerMethodFields so the dashboard can render without
// re-computing intervals client-side.
type PMSchedule struct {
	ID              string     `json:"id"`
	Asset           string     `json:"asset"`
	AssetName       string     `json:"asset_name,omitempty"`
	LocationName    *string    `json:"location_name"`
	TaskName        string     `json:"task_name"`
	Description     string     `json:"description,omitempty"`
	IntervalDays    int        `json:"interval_days"`
	IsActive        bool       `json:"is_active"`
	LastPerformedAt *time.Time `json:"last_performed_at"`
	DaysSinceLast   *int       `json:"days_since_last"`
	DaysUntilDue    *int       `json:"days_until_due"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// PMServiceLog is one "I performed this task at this time" record.
type PMServiceLog struct {
	ID                  string    `json:"id"`
	Schedule            string    `json:"schedule"`
	PerformedAt         time.Time `json:"performed_at"`
	PerformedBy         *int      `json:"performed_by"`
	PerformedByUsername string    `json:"performed_by_username,omitempty"`
	Notes               string    `json:"notes,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// PMBoardEntry mirrors PMBoardEntrySerializer — the compact projection
// returned by the /board/ action. Same fields as PMSchedule minus the
// audit timestamps and description, plus the urgency-sorted ordering
// the backend pre-applies (overdue → warning → ok → never, then by
// days_until_due ascending).
type PMBoardEntry struct {
	ID              string     `json:"id"`
	AssetID         string     `json:"asset_id"`
	AssetName       string     `json:"asset_name"`
	LocationName    *string    `json:"location_name"`
	TaskName        string     `json:"task_name"`
	IntervalDays    int        `json:"interval_days"`
	LastPerformedAt *time.Time `json:"last_performed_at"`
	DaysSinceLast   *int       `json:"days_since_last"`
	DaysUntilDue    *int       `json:"days_until_due"`
	Status          string     `json:"status"`
}

// PMBoard wraps the /board/ response with the generated_at stamp +
// count + the urgency-sorted schedule slice.
type PMBoard struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Count       int            `json:"count"`
	Schedules   []PMBoardEntry `json:"schedules"`
}

// GetPMBoard pulls the urgency-sorted board. AllowAny on the backend
// (so the Inkplate firmware can call without a JWT), which means
// scantty can show the board even before login completes.
func (c *Client) GetPMBoard(ctx context.Context) (*PMBoard, error) {
	var out PMBoard
	if err := c.Get(ctx, "/api/preventive-maintenance/schedules/board/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPMSchedules returns the paginated full schedule list (the board
// is more useful for the dashboard view; the list is better for
// per-asset filtering).
func (c *Client) ListPMSchedules(ctx context.Context, q url.Values) (*Page[PMSchedule], error) {
	return GetPage[PMSchedule](ctx, c, "/api/preventive-maintenance/schedules/", q)
}

// LogPMService posts the convenience "I just performed this task"
// action. Performed_at = now, performed_by = the auth'd user.
// Backend response is the new PMServiceLog row.
func (c *Client) LogPMService(ctx context.Context, scheduleID, notes string) (*PMServiceLog, error) {
	body := map[string]string{}
	if notes != "" {
		body["notes"] = notes
	}
	var out PMServiceLog
	if err := c.Post(ctx, fmt.Sprintf("/api/preventive-maintenance/schedules/%s/log_service/", scheduleID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPMServiceLogs returns the paginated history. Useful filter:
// `?schedule=<uuid>` for one task's history.
func (c *Client) ListPMServiceLogs(ctx context.Context, q url.Values) (*Page[PMServiceLog], error) {
	return GetPage[PMServiceLog](ctx, c, "/api/preventive-maintenance/service-logs/", q)
}
