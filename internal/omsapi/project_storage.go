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

// ProjectStoragePrintQueueEntry is one stint pending a label print on the
// Pi-side claim-tag daemon. LabelURL is supplied absolute by the backend
// — callers should honor it verbatim instead of reconstructing the path
// so a future rename can't break the daemon.
type ProjectStoragePrintQueueEntry struct {
	StintID     string `json:"stint_id"`
	PrintTarget string `json:"print_target"`
	CreatedAt   string `json:"created_at"`
	LabelURL    string `json:"label_url"`
}

// ListProjectStoragePrintQueue returns the stints waiting to print.
// AllowAny on the backend — the Pi daemon hits this without a JWT.
func (c *Client) ListProjectStoragePrintQueue(ctx context.Context) ([]ProjectStoragePrintQueueEntry, error) {
	var out []ProjectStoragePrintQueueEntry
	if err := c.Get(ctx, "/api/project-storage/stints/print-queue/", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetProjectStorageLabelBytes fetches the rendered label PNG. labelURL
// is the absolute URL from a print-queue entry; we honor it verbatim.
func (c *Client) GetProjectStorageLabelBytes(ctx context.Context, labelURL string) ([]byte, error) {
	return c.GetBytes(ctx, labelURL)
}

// MarkProjectStorageStintPrinted drains a stint from the print queue
// after a successful print. note is surfaced in the OMS audit log.
func (c *Client) MarkProjectStorageStintPrinted(ctx context.Context, stintID, note string) error {
	path := "/api/project-storage/stints/" + stintID + "/mark-printed/"
	return c.Post(ctx, path, map[string]string{"note": note}, nil)
}
