package omsapi

import (
	"context"
	"net/url"
	"time"
)

// ProjectStorageEvent mirrors backend ProjectStorageEventSerializer —
// one audit-log row inside a stint's lifecycle (notice sent, moved to
// purgatory, marked removed).
// ProjectStorageEvent mirrors ProjectStorageEventSerializer, which emits
// event_type / note / created_at (there is no `action`, `notes`, or
// `occurred_at` key) — the old tags left the event log blank.
type ProjectStorageEvent struct {
	ID            int       `json:"id"`
	Action        string    `json:"event_type"`
	ActorUsername string    `json:"actor_username,omitempty"`
	Notes         string    `json:"note,omitempty"`
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

// GetProjectStorageStint fetches a single stint by its public stint_id
// (e.g. "PS-AB23CDFG"), mirroring the frontend's .get(stint_id) against
// GET /api/project-storage/stints/{stint_id}/. Returns the same
// serializer shape as the list — including the computed status field and
// the embedded Events audit log — so the detail view can render the full
// lifecycle. Follows GetItem's *T convention: a 404 comes back as an
// error, nil stint.
func (c *Client) GetProjectStorageStint(ctx context.Context, stintID string) (*ProjectStorageStint, error) {
	var out ProjectStorageStint
	if err := c.Get(ctx, "/api/project-storage/stints/"+stintID+"/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProjectStorageStintStart is the intake (create) payload — the EXACT
// StartStintSerializer field set for the only create path the backend exposes,
// POST /api/project-storage/stints/start/. The stint viewset is a
// ReadOnlyModelViewSet (there is no POST /stints/ and no PATCH/PUT — those 405),
// so this self-issue "start" action is how a stint is created, mirroring the web
// projectStorageAPI.start + ProjectStorageKioskPage intake form.
//
// Every field is a plain string: the backend stores the member as a denormalized
// `username` and the location as a `storage_location_name` name string — NEITHER
// is a foreign key (see the model's own comment: it deliberately avoids FKs to
// inventory.Location). So there are no owner/location pickers to resolve — the
// form sends free text. Only username is required; the optionals carry omitempty
// so a blank one is dropped rather than sent as empty noise. started_at,
// expires_at (= started_at + 30d), stint_id, the QR image, and the AprilTag are
// all server-generated and must NOT be sent.
type ProjectStorageStintStart struct {
	Username            string `json:"username"`
	FirstName           string `json:"first_name,omitempty"`
	LastName            string `json:"last_name,omitempty"`
	Email               string `json:"email,omitempty"`
	ProjectTitle        string `json:"project_title,omitempty"`
	StorageLocationName string `json:"storage_location_name,omitempty"`
}

// StartProjectStorageStint intakes (creates) a new stint via the kiosk
// self-issue action — again, the ONLY create path (the collection has no POST).
// The endpoint is AllowAny + throttled (5/hr/IP). On success it returns the
// created stint (201); within a 5-minute idempotency window a duplicate submit
// returns the existing stint (200) instead — either way the caller gets a stint
// back to navigate to. Backend business-rule 409s (active_stint_exists when the
// member already holds a live stint, cooldown_active during the 3-day re-entry
// cooldown) come back as APIErrors the caller surfaces as a clear message rather
// than crashing.
func (c *Client) StartProjectStorageStint(ctx context.Context, body ProjectStorageStintStart) (*ProjectStorageStint, error) {
	var out ProjectStorageStint
	if err := c.Post(ctx, "/api/project-storage/stints/start/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkProjectStorageStintRemoved marks the WHOLE stint removed. The backend has
// no per-item model (a stint is a single flat row = one member's occupancy, with
// only an append-only event log beside it), so there is no "remove one item"
// operation anywhere in OMS — the sole removal is this terminal mark-removed
// transition, mirroring the web's markRemoved warden action:
//
//	POST /api/project-storage/stints/{stintID}/mark-removed/
//
// It stamps removed_at, appends a "removed" event, and frees the stint's
// AprilTag. note is surfaced verbatim in the OMS audit log. The action requires
// IsAdminUser, and a repeat call returns 409 already_removed — both come back as
// APIErrors the caller shows as a "remove failed" status instead of panicking.
func (c *Client) MarkProjectStorageStintRemoved(ctx context.Context, stintID, note string) error {
	path := "/api/project-storage/stints/" + stintID + "/mark-removed/"
	return c.Post(ctx, path, map[string]string{"note": note}, nil)
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

// ReprintProjectStorageStint re-surfaces a stint in the Pi-daemon print
// queue so a warden can re-print its claim ticket. The daemon only prints
// stints whose printed_at is NULL, and mark-printed sets it; reprint is
// the symmetric inverse — it clears printed_at so the next daemon poll
// picks the stint back up and re-prints the label. note is surfaced in the
// OMS audit log.
//
// This posts to the mirror of mark-printed:
//
//	POST /api/project-storage/stints/{stintID}/reprint/
//
// NOTE: that backend @action does not exist yet. The stint viewset is a
// ReadOnlyModelViewSet, printed_at is not an exposed serializer field, and
// the only current reprint path is a warden clearing printed_at by hand in
// the Django admin (see the Pi print-daemon README). Landing this endpoint
// is tracked as an OMS follow-up; until then the call returns the server's
// 404/405, which the caller surfaces as a "reprint failed" status.
func (c *Client) ReprintProjectStorageStint(ctx context.Context, stintID, note string) error {
	path := "/api/project-storage/stints/" + stintID + "/reprint/"
	return c.Post(ctx, path, map[string]string{"note": note}, nil)
}
