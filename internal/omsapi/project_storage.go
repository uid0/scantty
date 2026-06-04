package omsapi

import (
	"context"
	"net/url"
	"time"
)

// ProjectStorageEvent mirrors backend ProjectStorageEventSerializer —
// one audit-log row inside a stint's lifecycle (notice sent, moved to
// purgatory, marked removed).
type ProjectStorageEvent struct {
	ID            int       `json:"id"`
	Stint         int       `json:"stint"`
	Action        string    `json:"action"`
	Actor         *int      `json:"actor"`
	ActorUsername string    `json:"actor_username,omitempty"`
	Notes         string    `json:"notes,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// ProjectStorageStint mirrors backend ProjectStorageStintSerializer —
// one member's project-storage assignment from start through expiry,
// purgatory, and removal. status is a computed field: "active" /
// "warning" / "expired" / "purgatory" / "removed".
type ProjectStorageStint struct {
	ID                    int                   `json:"id"`
	StintID               string                `json:"stint_id"`
	Username              string                `json:"username"`
	FirstName             string                `json:"first_name,omitempty"`
	LastName              string                `json:"last_name,omitempty"`
	Email                 string                `json:"email,omitempty"`
	DisplayName           string                `json:"display_name,omitempty"`
	ProjectTitle          string                `json:"project_title"`
	StartedAt             time.Time             `json:"started_at"`
	ExpiresAt             *time.Time            `json:"expires_at"`
	RemovedAt             *time.Time            `json:"removed_at"`
	NoticeSentAt          *time.Time            `json:"notice_sent_at"`
	MovedToPurgatoryAt    *time.Time            `json:"moved_to_purgatory_at"`
	StorageLocationName   string                `json:"storage_location_name,omitempty"`
	PurgatoryLocationName string                `json:"purgatory_location_name,omitempty"`
	Notes                 string                `json:"notes,omitempty"`
	Status                string                `json:"status"`
	PurgatoryAt           *time.Time            `json:"purgatory_at"`
	ExpiryWeek            int                   `json:"expiry_week"`
	ExpiryDayOfYear       int                   `json:"expiry_day_of_year"`
	Events                []ProjectStorageEvent `json:"events"`
	CreatedAt             time.Time             `json:"created_at"`
	UpdatedAt             time.Time             `json:"updated_at"`
}

// ListProjectStorageStints returns the paginated stint list. Useful
// filters: `?status=expired` for the renewal queue, `?username=<u>`
// for member lookup, `?ordering=expires_at` for "due soonest".
func (c *Client) ListProjectStorageStints(ctx context.Context, q url.Values) (*Page[ProjectStorageStint], error) {
	return GetPage[ProjectStorageStint](ctx, c, "/api/project-storage/stints/", q)
}
