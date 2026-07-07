package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocationProblem mirrors LocationProblemSerializer (backend/inventory) — a
// problem reported against a Location (leak, broken door, HVAC complaint) that
// has no home in AssetProblem. Served read-only at /api/inventory/location-
// problems/; reports are CREATED via the Location.report_problem @action and
// closed out via the resolve @action (both below).
//
// DECODE-DRIFT NOTE ([[scantty-api-field-drift]]): the pk is a UUID string. The
// raw ImageField/FileField (`photo`, `paper_form_attachment`) are omitted — the
// serializer also exposes absolute `photo_url` / `paper_form_url` which is all a
// read-only TUI view can act on. `work_order` / `third_party_work_order` are
// nullable UUID FKs (both models use UUIDField pks); their human short ids come
// from `*_short_id` SerializerMethodFields (null until promoted). `resolved_at`
// is a nullable DateTimeField (*time.Time); `reported_at` / `updated_at` are
// non-null auto timestamps. `status_display` / `severity_display` are the human
// labels for the status / severity codes.
type LocationProblem struct {
	ID                         string     `json:"id"`
	Location                   int        `json:"location"`
	LocationName               string     `json:"location_name"`
	ReportedBy                 string     `json:"reported_by"`
	Description                string     `json:"description"`
	Status                     string     `json:"status"`
	StatusDisplay              string     `json:"status_display"`
	Severity                   string     `json:"severity"`
	SeverityDisplay            string     `json:"severity_display"`
	PhotoURL                   string     `json:"photo_url"`
	PaperFormURL               string     `json:"paper_form_url"`
	WorkOrder                  *string    `json:"work_order"`
	WorkOrderShortID           string     `json:"work_order_short_id"`
	ThirdPartyWorkOrder        *string    `json:"third_party_work_order"`
	ThirdPartyWorkOrderShortID string     `json:"third_party_work_order_short_id"`
	ResolutionNotes            string     `json:"resolution_notes"`
	ReportedAt                 time.Time  `json:"reported_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
	ResolvedAt                 *time.Time `json:"resolved_at"`
	ResolvedBy                 string     `json:"resolved_by"`
}

// Location-problem status + severity codes (backend LocationProblem.*_CHOICES).
// A report starts at "reported"; the resolve @action only accepts "resolved" or
// "closed" as terminal statuses (a promote-to-WO moves it to "in_progress").
const (
	LocationProblemReported   = "reported"
	LocationProblemInProgress = "in_progress"
	LocationProblemResolved   = "resolved"
	LocationProblemClosed     = "closed"

	LocationProblemSeverityLow    = "low"
	LocationProblemSeverityMedium = "medium"
	LocationProblemSeverityHigh   = "high"
	LocationProblemSeverityUrgent = "urgent"
)

// LocationProblemSeverities is the ordered severity code list the report form
// cycles through (matches the web ReportLocationProblemModal's SEVERITY_OPTIONS,
// least→most urgent, default medium).
var LocationProblemSeverities = []string{
	LocationProblemSeverityLow,
	LocationProblemSeverityMedium,
	LocationProblemSeverityHigh,
	LocationProblemSeverityUrgent,
}

// IsResolved reports whether the problem has reached a terminal state (resolved
// or closed) — the web treats both as "no longer open" for its filter + gates
// the resolve action off once either is set.
func (p LocationProblem) IsResolved() bool {
	return p.Status == LocationProblemResolved || p.Status == LocationProblemClosed
}

// IsPromoted reports whether the problem has already been promoted to a standard
// or third-party work order (either FK set).
func (p LocationProblem) IsPromoted() bool {
	return (p.WorkOrder != nil && *p.WorkOrder != "") ||
		(p.ThirdPartyWorkOrder != nil && *p.ThirdPartyWorkOrder != "")
}

// LocationProblemListParams filters the location-problems list. All are optional;
// an empty field is omitted from the query. Location is the int Location pk.
type LocationProblemListParams struct {
	Location int    // 0 → omit (all locations)
	Status   string // "" → omit
	Severity string // "" → omit
}

func (p LocationProblemListParams) query() url.Values {
	q := url.Values{}
	if p.Location > 0 {
		q.Set("location", fmt.Sprintf("%d", p.Location))
	}
	if s := strings.TrimSpace(p.Status); s != "" {
		q.Set("status", s)
	}
	if s := strings.TrimSpace(p.Severity); s != "" {
		q.Set("severity", s)
	}
	return q
}

// ListLocationProblems returns location problems matching params
// (GET /api/inventory/location-problems/?location=&status=&severity=), paging
// through the DRF envelope. Mirrors the web locationProblemsAPI.list. Reports
// per location are few, so eager full-paging is cheap.
func (c *Client) ListLocationProblems(ctx context.Context, params LocationProblemListParams) ([]LocationProblem, error) {
	var all []LocationProblem
	if err := IterPages[LocationProblem](ctx, c, "/api/inventory/location-problems/", params.query(), func(batch []LocationProblem) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// GetLocationProblem fetches one report by id — used to refresh the row after a
// resolve so the updated status / resolution notes show.
func (c *Client) GetLocationProblem(ctx context.Context, id string) (*LocationProblem, error) {
	var out LocationProblem
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/location-problems/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocationProblemReport is the report_problem @action payload. Description is
// required; Severity defaults to "medium" server-side when blank; PhotoPath is
// an optional local image file uploaded as the reporter photo.
type LocationProblemReport struct {
	Description string
	Severity    string
	PhotoPath   string
}

// ReportLocationProblem files a new problem against a location
// (POST /api/inventory/locations/{id}/report_problem/, trailing slash). The
// backend @action only accepts MultiPartParser/FormParser (no JSON), so the
// call is ALWAYS multipart — description + severity as form fields, plus an
// optional in-memory photo part. AllowAny on the backend, so any operator can
// report. Mirrors the web reportForLocation FormData post. Returns the created
// LocationProblem (HTTP 201).
func (c *Client) ReportLocationProblem(ctx context.Context, locationID string, body LocationProblemReport) (*LocationProblem, error) {
	fields := map[string][]string{"description": {body.Description}}
	if s := strings.TrimSpace(body.Severity); s != "" {
		fields["severity"] = []string{s}
	}
	var files []MultipartFile
	if p := strings.TrimSpace(body.PhotoPath); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("oms: read %s: %w", p, err)
		}
		files = append(files, MultipartFile{Field: "photo", Filename: filepath.Base(p), Data: data})
	}
	var out LocationProblem
	path := fmt.Sprintf("/api/inventory/locations/%s/report_problem/", locationID)
	if err := c.PostMultipart(ctx, path, fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocationProblemResolve is the resolve @action JSON body. Status must be
// "resolved" or "closed" (the backend rejects anything else). ResolutionNotes is
// omitempty so a blank submit is dropped and the stored notes are kept — exactly
// as the web sends `resolution_notes || undefined`.
type LocationProblemResolve struct {
	Status          string `json:"status"`
	ResolutionNotes string `json:"resolution_notes,omitempty"`
}

// ResolveLocationProblem marks a report resolved or closed
// (POST /api/inventory/location-problems/{id}/resolve/, trailing slash). The
// backend stamps resolved_at / resolved_by on first resolve and records a
// location_problem_resolve maintenance audit event. Requires an authenticated
// user (IsAuthenticated on the @action). Returns the updated LocationProblem.
func (c *Client) ResolveLocationProblem(ctx context.Context, id string, body LocationProblemResolve) (*LocationProblem, error) {
	var out LocationProblem
	path := fmt.Sprintf("/api/inventory/location-problems/%s/resolve/", id)
	if err := c.Post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
