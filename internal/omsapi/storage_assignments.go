// Staff-assigned storage — committee (C), logistics (L) and class (E).
//
// The long-term half of the racking, and the counterpart to a project stint.
// A member claims a slot at the kiosk and a 30-day clock starts; a committee,
// the logistics crew or a class gets a slot because STAFF gave it to them, and
// it stays theirs until staff takes it back. So there is no expiry, no violation
// notice, no purgatory and no cool-down — deliberately. The whole lifecycle is
// assign → release.
//
// Contract verified against OMS backend/project_storage/{models,serializers,
// views}.py (PR op-wgc8 #994), NOT a paraphrase. The web has no assignment page
// at all, so the serializer + viewset are the parity target.
//
// Endpoints (DefaultRouter under /api/project-storage/assignments/, trailing
// slash), every verb gated IsStorageAdminOrStaff:
//
//	GET  /assignments/            paginated; ?active= ?storage_type= ?rack= ?slot_code=
//	POST /assignments/assign/     hand a slot to a committee / crew / class
//	POST /assignments/{id}/release/   take it back
//
// The viewset is deliberately READ-ONLY for CRUD: the two actions are the only
// way in and out, which keeps `assigned_by` meaningful and stops a PATCH from
// quietly reopening a released assignment. So there is no Update/Delete here,
// and that is the contract rather than an omission.
package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// The three storage types. Their single-letter grid codes are the
// StorageTypeLetter* constants in storage_overview.go.
const (
	StorageAssignmentTypeCommittee = "committee"
	StorageAssignmentTypeLogistics = "logistics"
	StorageAssignmentTypeClass     = "class"
)

// StorageAssignment mirrors StorageAssignmentSerializer — one C/L/E holding.
// Every field is read-only on the wire.
//
// Who holds it is recorded one of two ways, because the three types name their
// occupants differently: a committee IS a Django group (OwningGroup, the SIG),
// while logistics and class carry free text (OccupantLabel — "Winter welding
// cohort", "Ana's CNC class"), neither being a durable object in the system.
// OccupantDisplay picks whichever is there and falls back to the type's own
// label, so it is always the right thing to show.
//
// ReleasedAt nil is what "active" MEANS (it is the partial unique constraint's
// condition, and IsActive is derived from it); a released row stays as the
// history of who used to be in the slot.
type StorageAssignment struct {
	ID                 int        `json:"id"`
	Slot               int        `json:"slot"`
	SlotCode           string     `json:"slot_code"`
	StorageType        string     `json:"storage_type"`
	StorageTypeDisplay string     `json:"storage_type_display"`
	TypeLetter         string     `json:"type_letter"`
	OwningGroup        *int       `json:"owning_group"`
	OwningGroupName    string     `json:"owning_group_name"`
	OccupantLabel      string     `json:"occupant_label"`
	OccupantDisplay    string     `json:"occupant_display"`
	AssignedBy         *int       `json:"assigned_by"`
	AssignedByName     string     `json:"assigned_by_name"`
	AssignedAt         time.Time  `json:"assigned_at"`
	ReleasedAt         *time.Time `json:"released_at"`
	IsActive           bool       `json:"is_active"`
	Notes              string     `json:"notes"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// StorageAssignmentWrite is the assign action's payload (AssignSlotSerializer),
// exactly. There is no update counterpart, so every optional field carries
// omitempty: this is a CREATE and there is nothing to clear yet (the
// GenerateRackRequest precedent). Blank stays blank server-side by default.
//
// The slot is named by CODE under `slot_code` rather than by pk under `slot`.
// Both spellings work (the serializer resolves a code-or-pk), but the code is
// what is printed on the upright, what a scanner reads and what every other
// slot call in ScanTTY is addressed by — and it can be validated locally, so a
// typo costs no round trip.
//
// OwningGroup is REQUIRED IN EFFECT for a committee assignment: the serializer
// refuses a committee that names neither a group nor an occupant label, because
// a committee holding that doesn't say which committee is just a blocked slot.
type StorageAssignmentWrite struct {
	SlotCode      string `json:"slot_code"`
	StorageType   string `json:"storage_type"`
	OwningGroup   *int   `json:"owning_group,omitempty"`
	OccupantLabel string `json:"occupant_label,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

const storageAssignmentsPath = "/api/project-storage/assignments/"

// ListStorageAssignments returns one page of holdings. Useful filters:
// `?active=true` ("who holds what right now?", the one a console lives on),
// `?storage_type=committee`, `?rack=1` and `?slot_code=1A1`. Unfiltered, the
// list is the history of every holding the racking has ever had.
func (c *Client) ListStorageAssignments(ctx context.Context, q url.Values) (*Page[StorageAssignment], error) {
	return GetPage[StorageAssignment](ctx, c, storageAssignmentsPath, q)
}

// ActiveStorageAssignmentForSlot answers "who holds this slot, if anyone?" —
// (nil, nil) when nobody does, which is a normal answer and not an error.
//
// This lookup exists because the overview grid's cell carries the OCCUPANT but
// not the assignment id, and release is addressed by that id. The partial
// unique constraint guarantees at most one active row per slot, so taking the
// first result is exact rather than a heuristic.
func (c *Client) ActiveStorageAssignmentForSlot(ctx context.Context, code string) (*StorageAssignment, error) {
	canonical, err := NormalizeStorageSlotCode(code)
	if err != nil {
		return nil, err
	}
	page, err := c.ListStorageAssignments(ctx, url.Values{
		"slot_code": {canonical},
		"active":    {"true"},
	})
	if err != nil {
		return nil, err
	}
	if page == nil || len(page.Results) == 0 {
		return nil, nil
	}
	a := page.Results[0]
	return &a, nil
}

// AssignStorageSlot hands a slot to a committee, the logistics crew or a class.
//
// Refuses a slot that already holds anything — a member's live stint (409
// `slot_occupied`) or another assignment (409 `slot_assigned`) — and a retired
// slot (400): a retired slot keeps its marker but is not offered for new
// reservations, and an assignment outlasts every stint. Those 409s are
// hand-rolled Response bodies, so read them through StorageSlotErrorDetail.
func (c *Client) AssignStorageSlot(ctx context.Context, w StorageAssignmentWrite) (*StorageAssignment, error) {
	canonical, err := NormalizeStorageSlotCode(w.SlotCode)
	if err != nil {
		return nil, err
	}
	w.SlotCode = canonical
	var out StorageAssignment
	if err := c.Post(ctx, storageAssignmentsPath+"assign/", w, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReleaseStorageAssignment takes the slot back. Like resolving a stint this
// frees the slot by ceasing to match the partial unique constraint rather than
// by deleting anything — the code is immediately assignable (or claimable at
// the kiosk) again, and the record of the two years the welding SIG held it
// survives. Releasing an already-released row is a 409 `already_released`.
func (c *Client) ReleaseStorageAssignment(ctx context.Context, id int) (*StorageAssignment, error) {
	var out StorageAssignment
	path := fmt.Sprintf("%s%d/release/", storageAssignmentsPath, id)
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
