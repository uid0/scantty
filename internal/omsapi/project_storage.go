package omsapi

import (
	"context"
	"net/url"
	"strings"
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
// purgatory, and removal. status is computed server-side
// (ProjectStorageStint.compute_status, terminal state wins): "active" /
// "expiring_soon" / "expired" / "purgatory_warned" / "purgatory" / "removed".
type ProjectStorageStint struct {
	ID                    int        `json:"id"`
	StintID               string     `json:"stint_id"`
	Username              string     `json:"username"`
	FirstName             string     `json:"first_name,omitempty"`
	LastName              string     `json:"last_name,omitempty"`
	Email                 string     `json:"email,omitempty"`
	DisplayName           string     `json:"display_name,omitempty"`
	ProjectTitle          string     `json:"project_title"`
	StartedAt             time.Time  `json:"started_at"`
	ExpiresAt             *time.Time `json:"expires_at"`
	RemovedAt             *time.Time `json:"removed_at"`
	NoticeSentAt          *time.Time `json:"notice_sent_at"`
	MovedToPurgatoryAt    *time.Time `json:"moved_to_purgatory_at"`
	StorageLocationName   string     `json:"storage_location_name,omitempty"`
	PurgatoryLocationName string     `json:"purgatory_location_name,omitempty"`
	// Slot is the racking slot this stint occupies (StorageSlot pk), null for
	// ad-hoc non-rack storage. All three are READ-ONLY on the stint serializer:
	// a stint's slot is chosen at claim time (start) and afterwards only the
	// lifecycle actions move it. SlotCode is the printed code ("1A1") and
	// LocationDisplay is the backend's own precedence — the slot wins when set,
	// falling back to the free-text StorageLocationName — so prefer it over
	// re-deriving that rule client-side.
	Slot            *int                  `json:"slot"`
	SlotCode        string                `json:"slot_code,omitempty"`
	LocationDisplay string                `json:"location_display,omitempty"`
	Notes           string                `json:"notes,omitempty"`
	Status          string                `json:"status"`
	PurgatoryAt     *time.Time            `json:"purgatory_at"`
	ExpiryWeek      int                   `json:"expiry_week"`
	ExpiryDayOfYear int                   `json:"expiry_day_of_year"`
	Events          []ProjectStorageEvent `json:"events"`
	// QRCodeURL is where the stint's stored QR PNG is served, or "" (the wire's
	// null) when none has been generated. Its FORM depends on the endpoint: the
	// retrieve builds it absolute from the request, while generate-qr, the
	// other warden writes and by-member serialize without a request and send
	// the bare /media/… path. So a screen shows the value off a fresh GET rather
	// than off a write's reply.
	QRCodeURL string    `json:"qr_code_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
//
// A stint may also CLAIM a racking slot (op-hfw5). The serializer takes the
// slot two ways — `slot` (pk OR code, disambiguated by whether the value
// contains a letter) and `slot_code` (the code, explicitly) — and 400s when
// both are sent naming different slots. ScanTTY always has the CODE (it is what
// is printed on the rack card and what a scanner reads), so it sends exactly one
// spelling, `slot_code`, and never the ambiguous `slot`. Blank means ad-hoc
// storage: a stint still starts with just StorageLocationName, or with nothing.
type ProjectStorageStintStart struct {
	Username            string `json:"username"`
	FirstName           string `json:"first_name,omitempty"`
	LastName            string `json:"last_name,omitempty"`
	Email               string `json:"email,omitempty"`
	ProjectTitle        string `json:"project_title,omitempty"`
	StorageLocationName string `json:"storage_location_name,omitempty"`
	SlotCode            string `json:"slot_code,omitempty"`
}

// StartProjectStorageStint intakes (creates) a new stint via the kiosk
// self-issue action — again, the ONLY create path (the collection has no POST).
// The endpoint is AllowAny + throttled (5/hr/IP). On success it returns the
// created stint (201); within a 5-minute idempotency window a duplicate submit
// returns the existing stint (200) instead — either way the caller gets a stint
// back to navigate to. Backend business-rule 409s (active_stint_exists when the
// member already holds a live stint, cooldown_active during the 3-day re-entry
// cooldown, slot_occupied when someone else's project is already in the claimed
// slot) come back as APIErrors the caller surfaces as a clear message rather
// than crashing — see StorageSlotErrorDetail for the slot one, whose body is
// hand-rolled rather than exception-wrapped.
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

// The warden's enforcement actions — the web's FacilitiesProjectStoragePage
// buttons, one client call each. The contract is OMS's
// project_storage.views.ProjectStorageStintViewSet, recorded off a real backend
// into testdata/ (README.md carries the provenance), and what is worth knowing
// before calling any of them:
//
//   - WHO MAY ACT DIFFERS BY ACTION. send-violation-notice and move-to-purgatory
//     are IsAdminUser (is_staff); by-member and generate-qr are
//     IsStorageAdminOrStaff, so a volunteer in the "Storage Admin" group may
//     list a member's stints and regenerate a QR but not notify or purgatory
//     anybody. The refusal is OMS's standardized envelope (APIError.Code
//     `permission_denied`) carrying the server's sentence.
//   - THE STATE RULES ARE THE SERVER'S AND ARE RELAYED, not re-implemented as
//     refusals: a notice outside expiring_soon / expired / purgatory_warned is a
//     409 `invalid_state_for_notice`, a notice to a stint with no email is a 422
//     `missing_email` (nothing is stamped), and purgatory before a notice is a
//     409 `notice_required`. Those three bodies are hand-built
//     `{"detail": …, "code": …}` and never reach the exception handler, so
//     parseError hands over the raw JSON and AsDetailRefusal recovers the
//     sentence. generate-qr's rate limit is a 429 `{"error": "<prose>"}`, which
//     is AsReceivingRefusal's shape.
//   - A WRITE'S REPLY CARRIES THE EVENTS AS THEY WERE BEFORE THE WRITE. The
//     viewset prefetches `events` in get_object and appends the new event
//     afterwards, so the recorded notice reply says `"events": []` beside a
//     stamped notice_sent_at. A caller that wants the audit trail re-fetches.

// SendProjectStorageViolationNotice emails the member the "your stint expired"
// notice and stamps notice_sent_at, which is what dates the purgatory deadline —
// so sending it again on an already-warned stint RESTARTS that clock.
//
//	POST /api/project-storage/stints/{stintID}/send-violation-notice/
//
// The web posts an empty object and the view reads nothing off the body.
func (c *Client) SendProjectStorageViolationNotice(ctx context.Context, stintID string) (*ProjectStorageStint, error) {
	var out ProjectStorageStint
	path := "/api/project-storage/stints/" + url.PathEscape(stintID) + "/send-violation-notice/"
	if err := c.Post(ctx, path, map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MoveProjectStorageStintToPurgatory records that the warden has taken the
// items out of project storage. It frees the racking slot at once — the stint
// keeps `slot` as the history of where the items came out of — and stamps
// moved_to_purgatory_at.
//
//	POST /api/project-storage/stints/{stintID}/move-to-purgatory/
//	{"purgatory_location_name": "<where they went>"}
//
// The key is ALWAYS sent, blank included, because that is what the web sends
// and because the view treats blank as "keep the stored location" (`if
// location:`) — a blank here never erases one.
func (c *Client) MoveProjectStorageStintToPurgatory(ctx context.Context, stintID, purgatoryLocation string) (*ProjectStorageStint, error) {
	var out ProjectStorageStint
	path := "/api/project-storage/stints/" + url.PathEscape(stintID) + "/move-to-purgatory/"
	body := map[string]string{"purgatory_location_name": purgatoryLocation}
	if err := c.Post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListProjectStorageStintsByMember returns every stint a member has held, most
// recent first — the warden's "is this a serial offender?" view. A bare JSON
// array, not a page.
//
//	GET /api/project-storage/stints/by-member/{username}/
//
// THE ROUTE CANNOT MATCH EVERY USERNAME. Its url_path is
// `by-member/(?P<username>[^/.]+)`, so a username with a dot in it (Django's
// validator allows `.`) never reaches the view: the recorded answer for
// `bob.jones` is the router's HTML 404 page, not an empty list. The web's
// byMember has the same hole, so this is an OMS limitation to report rather
// than something a client can route around; callers should say so rather than
// relay a bare 404.
func (c *Client) ListProjectStorageStintsByMember(ctx context.Context, username string) ([]ProjectStorageStint, error) {
	var out []ProjectStorageStint
	path := "/api/project-storage/stints/by-member/" + url.PathEscape(username) + "/"
	if err := c.Get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ProjectStorageByMemberUnroutable reports whether a username is one the
// by-member route's pattern cannot match, so a 404 for it is the route and not
// an answer about the member. It reads the same character class the url_path
// excludes.
func ProjectStorageByMemberUnroutable(username string) bool {
	return strings.ContainsAny(username, "./")
}

// GenerateProjectStorageStintQR regenerates the QR PNG stored on the stint (the
// warden page's "Generate QR" / "Regenerate QR"), replacing any previous one.
// The PNG encodes the stint's /scan/project-storage/<id> URL; the Pi label
// daemon does not read it — labels are rendered at print time — so this is the
// stored image the web previews, not a reprint.
//
//	POST /api/project-storage/stints/{stintID}/generate-qr/
//	{"include_logo": true}
//
// include_logo is what the web sends. Staff are not rate limited; anyone else
// is held to five a minute per stint and gets the 429 `{"error": …}`.
func (c *Client) GenerateProjectStorageStintQR(ctx context.Context, stintID string) (*ProjectStorageStint, error) {
	var out ProjectStorageStint
	path := "/api/project-storage/stints/" + url.PathEscape(stintID) + "/generate-qr/"
	if err := c.Post(ctx, path, map[string]bool{"include_logo": true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
