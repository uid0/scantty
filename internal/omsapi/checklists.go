package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// ChecklistStep is one ordered step inside a Checklist definition. The
// step PK is UUID per [[scantty-api-field-drift]] note ("everything in
// inventory + checklists uses UUID"); the FKs to asset / inventory_item
// are UUIDs and the location is int.
type ChecklistStep struct {
	ID            string  `json:"id"`
	StepNumber    int     `json:"step_number"`
	Name          string  `json:"name"`
	Asset         *string `json:"asset"`
	Location      *int    `json:"location"`
	InventoryItem *string `json:"inventory_item"`
	Required      bool    `json:"required"`
	RequiresPhoto bool    `json:"requires_photo"`
	Notes         string  `json:"notes,omitempty"`
}

// ChecklistSummary mirrors ChecklistListSerializer — the lightweight
// shape returned by the list endpoint. `step_count` is a SerializerMethodField
// pre-joined for the list view.
type ChecklistSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SIG         *int   `json:"sig"`
	SIGName     string `json:"sig_name,omitempty"`
	IsActive    bool   `json:"is_active"`
	IsPublic    bool   `json:"is_public"`
	StepCount   int    `json:"step_count"`
}

// Checklist is the full detail shape — ChecklistSerializer with nested
// steps. The /detail/ action carries this; the plain GET on the list
// endpoint also returns this when fetched per-id.
type Checklist struct {
	ChecklistSummary
	CreatedBy         *int            `json:"created_by"`
	CreatedByUsername string          `json:"created_by_username,omitempty"`
	Steps             []ChecklistStep `json:"steps"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// ChecklistCompletion is one in-progress or finished run of a
// checklist. Fields mirror ChecklistCompletionSerializer's read shape;
// nested step_completions omitted from this client for v1 (the scan +
// finalize flows are queued for v2).
type ChecklistCompletion struct {
	ID                     string     `json:"id"`
	Checklist              string     `json:"checklist"`
	ChecklistName          string     `json:"checklist_name,omitempty"`
	User                   *int       `json:"user"`
	UserUsername           string     `json:"user_username,omitempty"`
	UserName               string     `json:"user_name,omitempty"`
	StartedAt              *time.Time `json:"started_at"`
	CompletedAt            *time.Time `json:"completed_at"`
	Status                 string     `json:"status"`
	CompletedStepsCount    int        `json:"completed_steps_count"`
	TotalStepsCount        int        `json:"total_steps_count"`
	RequiredStepsCompleted int        `json:"required_steps_completed"`
	RequiredStepsTotal     int        `json:"required_steps_total"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// ListChecklists returns the paginated checklist list. Useful filters:
// `?is_active=true`, `?sig=<id>`, `?search=<q>`.
func (c *Client) ListChecklists(ctx context.Context, q url.Values) (*Page[ChecklistSummary], error) {
	return GetPage[ChecklistSummary](ctx, c, "/api/checklists/checklists/", q)
}

// GetChecklist returns the full detail (with steps) for one checklist.
// Uses the /detail/ action which is AllowAny — viewing a checklist
// doesn't require auth, only modifying it does.
func (c *Client) GetChecklist(ctx context.Context, id string) (*Checklist, error) {
	var out Checklist
	if err := c.Get(ctx, fmt.Sprintf("/api/checklists/checklists/%s/detail/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartChecklist opens a new completion run for the given checklist.
// `userName` is the human-readable name to record on the run (anonymous
// flow); a signed-in user has their JWT applied server-side and can pass
// an empty string here.
func (c *Client) StartChecklist(ctx context.Context, checklistID, userName string) (*ChecklistCompletion, error) {
	body := map[string]string{}
	if userName != "" {
		body["user_name"] = userName
	}
	var out ChecklistCompletion
	if err := c.Post(ctx, fmt.Sprintf("/api/checklists/checklists/%s/start/", checklistID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListChecklistCompletions returns recent runs. Useful filters:
// `?checklist=<id>` for one definition's history, `?status=in_progress`
// for resumable runs.
func (c *Client) ListChecklistCompletions(ctx context.Context, q url.Values) (*Page[ChecklistCompletion], error) {
	return GetPage[ChecklistCompletion](ctx, c, "/api/checklists/completions/", q)
}
