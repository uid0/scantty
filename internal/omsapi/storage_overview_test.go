package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newOverviewTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// A rack with a hole in it, one cell of every type, and the two coloured
// project states — the whole contract in one payload.
const overviewFixture = `{
  "generated_at": "2026-08-01T02:00:00Z",
  "racks": [
    {"rack": 1, "levels": ["C", "A"], "max_position": 3,
     "rows": [
       {"level": "C", "cells": [
         {"code": "1C1", "slot_id": 11, "position": 1, "type": "C", "status": "occupied",
          "color": null, "occupant": "Welding SIG", "is_active": true},
         null,
         {"code": "1C3", "slot_id": 13, "position": 3, "type": "L", "status": "occupied",
          "color": null, "occupant": "Dock crew", "is_active": true}
       ]},
       {"level": "A", "cells": [
         {"code": "1A1", "slot_id": 1, "position": 1, "type": "P", "status": "expiring_soon",
          "color": "yellow", "occupant": "Ada Byron", "is_active": true},
         {"code": "1A2", "slot_id": 2, "position": 2, "type": "P", "status": "expired",
          "color": "red", "occupant": "Grace Hopper", "is_active": true},
         {"code": "1A3", "slot_id": 3, "position": 3, "type": null, "status": "empty",
          "color": null, "occupant": "", "is_active": false}
       ]}
     ]},
    {"rack": 2, "levels": ["A"], "max_position": 1,
     "rows": [
       {"level": "A", "cells": [
         {"code": "2A1", "slot_id": 21, "position": 1, "type": "E", "status": "occupied",
          "color": null, "occupant": "Ana's CNC class", "is_active": true}
       ]}
     ]}
  ]}`

// TestStorageOverview_Decode pins the grid's read shape: the null `type`/`color`
// of an empty cell (which a naive struct would reject or mis-read), the null
// CELL that stands for a hole in the racking, and the descending level order
// that makes the payload read like the steel.
func TestStorageOverview_Decode(t *testing.T) {
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/project-storage/overview/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("rack"); got != "" {
			t.Errorf("unfiltered call sent ?rack=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(overviewFixture))
	}))

	ov, err := c.GetStorageOverview(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetStorageOverview: %v", err)
	}
	if len(ov.Racks) != 2 {
		t.Fatalf("got %d racks, want 2", len(ov.Racks))
	}
	if ov.GeneratedAt.IsZero() {
		t.Errorf("generated_at did not decode")
	}

	rack := ov.Racks[0]
	if rack.Rack != 1 || rack.MaxPosition != 3 {
		t.Errorf("rack header decoded wrong: %+v", rack)
	}
	// High levels first — C above A, the way you read a rack.
	if len(rack.Levels) != 2 || rack.Levels[0] != "C" || rack.Levels[1] != "A" {
		t.Errorf("levels = %v, want [C A]", rack.Levels)
	}
	if len(rack.Rows) != 2 || rack.Rows[0].Level != "C" || rack.Rows[1].Level != "A" {
		t.Errorf("rows are not in level order: %+v", rack.Rows)
	}

	top := rack.Rows[0]
	// A C/L/E holding has no clock to be late against, so its status is a flat
	// "occupied" rather than one of the stint states.
	if got := top.Cells[0]; got.Status != StorageOverviewStatusOccupied {
		t.Errorf("committee cell status = %q, want occupied", got.Status)
	}

	// A hole in the racking is a null CELL, and it must stay in place rather
	// than closing the gap — position 3 is still the third column.
	if len(top.Cells) != 3 {
		t.Fatalf("row C has %d cells, want 3 (padded to max_position)", len(top.Cells))
	}
	if top.Cells[1] != nil {
		t.Errorf("position 2 should be a hole, got %+v", top.Cells[1])
	}
	if top.Cells[2] == nil || top.Cells[2].Code != "1C3" {
		t.Errorf("position 3 shifted: %+v", top.Cells[2])
	}

	ground := rack.Rows[1]
	if got := ground.Cells[0]; got.Type != StorageTypeLetterProject || got.Color != StorageOverviewColorYellow {
		t.Errorf("expiring cell = %+v, want P/yellow", got)
	}
	if got := ground.Cells[1]; got.Color != StorageOverviewColorRed || got.Occupant != "Grace Hopper" {
		t.Errorf("expired cell = %+v, want red/Grace Hopper", got)
	}

	// The empty cell: `type` and `color` arrive as JSON null and must read as
	// "" rather than tripping the decode.
	empty := ground.Cells[2]
	if empty.Type != "" || empty.Color != "" {
		t.Errorf("null type/color decoded to %q/%q, want empty", empty.Type, empty.Color)
	}
	if empty.Status != StorageOverviewStatusEmpty {
		t.Errorf("status = %q, want empty", empty.Status)
	}
	// A retired slot is empty but NOT available — the flag is the only thing
	// that distinguishes it, so it has to survive the decode.
	if empty.IsActive {
		t.Errorf("is_active = true for a retired slot")
	}
}

// TestStorageOverview_RackFilter checks the ?rack= narrowing, including that a
// zero means "every rack" rather than "rack 0".
func TestStorageOverview_RackFilter(t *testing.T) {
	var seen []string
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"racks": [], "generated_at": "2026-08-01T02:00:00Z"}`))
	}))

	if _, err := c.GetStorageOverview(context.Background(), 2); err != nil {
		t.Fatalf("GetStorageOverview(2): %v", err)
	}
	if _, err := c.GetStorageOverview(context.Background(), 0); err != nil {
		t.Fatalf("GetStorageOverview(0): %v", err)
	}
	if len(seen) != 2 || seen[0] != "rack=2" || seen[1] != "" {
		t.Errorf("queries = %q, want [rack=2 <empty>]", seen)
	}
}

func TestStorageOverview_FindRackAndCellAt(t *testing.T) {
	var ov StorageOverview
	if err := json.Unmarshal([]byte(overviewFixture), &ov); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Re-keying by rack NUMBER, not by index: a refresh can add or drop a rack.
	if r := ov.FindRack(2); r == nil || r.Rows[0].Cells[0].Code != "2A1" {
		t.Errorf("FindRack(2) = %+v", r)
	}
	if r := ov.FindRack(99); r != nil {
		t.Errorf("FindRack(99) = %+v, want nil", r)
	}
	if r := (*StorageOverview)(nil).FindRack(1); r != nil {
		t.Errorf("FindRack on a nil overview should be nil")
	}

	rack := ov.FindRack(1)
	if cell := rack.CellAt("A", 2); cell == nil || cell.Code != "1A2" {
		t.Errorf("CellAt(A,2) = %+v, want 1A2", cell)
	}
	if cell := rack.CellAt("C", 2); cell != nil {
		t.Errorf("CellAt(C,2) is a hole, got %+v", cell)
	}
	for _, tc := range []struct {
		level string
		pos   int
	}{{"A", 0}, {"A", 4}, {"Z", 1}} {
		if cell := rack.CellAt(tc.level, tc.pos); cell != nil {
			t.Errorf("CellAt(%s,%d) = %+v, want nil", tc.level, tc.pos, cell)
		}
	}
}

// TestStorageAssignment_Decode pins StorageAssignmentSerializer, including the
// two ways an occupant is named (a committee's group vs free text) and the
// released_at null that IS the definition of active.
func TestStorageAssignment_Decode(t *testing.T) {
	body := `{"count": 2, "next": null, "previous": null, "results": [
	  {"id": 5, "slot": 11, "slot_code": "1C1", "storage_type": "committee",
	   "storage_type_display": "Committee", "type_letter": "C",
	   "owning_group": 3, "owning_group_name": "Welding SIG",
	   "occupant_label": "", "occupant_display": "Welding SIG",
	   "assigned_by": 9, "assigned_by_name": "warden",
	   "assigned_at": "2024-02-01T00:00:00Z", "released_at": null,
	   "is_active": true, "notes": "back corner",
	   "created_at": "2024-02-01T00:00:00Z", "updated_at": "2024-02-01T00:00:00Z"},
	  {"id": 6, "slot": 13, "slot_code": "1C3", "storage_type": "logistics",
	   "storage_type_display": "Logistics", "type_letter": "L",
	   "owning_group": null, "owning_group_name": "",
	   "occupant_label": "Dock crew", "occupant_display": "Dock crew",
	   "assigned_by": null, "assigned_by_name": "",
	   "assigned_at": "2024-03-01T00:00:00Z", "released_at": "2026-01-01T00:00:00Z",
	   "is_active": false, "notes": "",
	   "created_at": "2024-03-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
	]}`
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/project-storage/assignments/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	page, err := c.ListStorageAssignments(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListStorageAssignments: %v", err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("got %d rows, want 2", len(page.Results))
	}

	committee := page.Results[0]
	if committee.TypeLetter != StorageTypeLetterCommittee || committee.OccupantDisplay != "Welding SIG" {
		t.Errorf("committee row decoded wrong: %+v", committee)
	}
	if committee.OwningGroup == nil || *committee.OwningGroup != 3 {
		t.Errorf("owning_group = %v, want 3", committee.OwningGroup)
	}
	if committee.ReleasedAt != nil || !committee.IsActive {
		t.Errorf("a live holding must have a null released_at: %+v", committee)
	}

	released := page.Results[1]
	if released.OwningGroup != nil || released.OccupantLabel != "Dock crew" {
		t.Errorf("logistics row decoded wrong: %+v", released)
	}
	if released.ReleasedAt == nil || released.IsActive {
		t.Errorf("a released holding must carry released_at: %+v", released)
	}
	if released.AssignedBy != nil || released.AssignedByName != "" {
		t.Errorf("an unattributed assignment should decode with no staff member: %+v", released)
	}
}

// TestAssignStorageSlot_Payload pins the wire: the slot goes by CODE, the code
// is normalized before it is sent, and the optional fields are OMITTED rather
// than sent blank (there is no update action, so a blank has nothing to clear).
func TestAssignStorageSlot_Payload(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 7, "slot_code": "1A1", "storage_type": "logistics",
		  "type_letter": "L", "occupant_display": "Dock crew", "is_active": true}`))
	}))

	got, err := c.AssignStorageSlot(context.Background(), StorageAssignmentWrite{
		SlotCode:      "1a1", // typed lower-case, as a warden would
		StorageType:   StorageAssignmentTypeLogistics,
		OccupantLabel: "Dock crew",
	})
	if err != nil {
		t.Fatalf("AssignStorageSlot: %v", err)
	}
	if got.ID != 7 || got.TypeLetter != StorageTypeLetterLogistics {
		t.Errorf("response decoded wrong: %+v", got)
	}
	if gotPath != "/api/project-storage/assignments/assign/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["slot_code"] != "1A1" {
		t.Errorf("slot_code = %v, want the normalized 1A1", gotBody["slot_code"])
	}
	if _, ok := gotBody["owning_group"]; ok {
		t.Errorf("owning_group should be omitted when unset, got %v", gotBody["owning_group"])
	}
	if _, ok := gotBody["notes"]; ok {
		t.Errorf("blank notes should be omitted, got %v", gotBody["notes"])
	}
}

// A code that isn't a code must not become a request — the same local guard the
// slot calls use, so a typo costs no round trip and can never address a slot
// other than the one meant.
func TestAssignStorageSlot_RejectsJunkCode(t *testing.T) {
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a malformed code reached the server: %s", r.URL.Path)
	}))
	if _, err := c.AssignStorageSlot(context.Background(), StorageAssignmentWrite{
		SlotCode:    "nope",
		StorageType: StorageAssignmentTypeClass,
	}); err == nil {
		t.Fatal("want an error for a malformed slot code")
	}
}

func TestReleaseStorageAssignment(t *testing.T) {
	var gotPath, gotCT string
	var gotLen int64
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCT, gotLen = r.URL.Path, r.Header.Get("Content-Type"), r.ContentLength
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 5, "slot_code": "1C1", "released_at": "2026-08-01T02:00:00Z",
		  "is_active": false}`))
	}))

	got, err := c.ReleaseStorageAssignment(context.Background(), 5)
	if err != nil {
		t.Fatalf("ReleaseStorageAssignment: %v", err)
	}
	if gotPath != "/api/project-storage/assignments/5/release/" {
		t.Errorf("path = %q — the DRF router action needs the trailing slash", gotPath)
	}
	// Bodyless, so no Content-Type either: a `null` body would be a document
	// the serializer never asked for.
	if gotCT != "" || gotLen > 0 {
		t.Errorf("release sent a body: content-type=%q length=%d", gotCT, gotLen)
	}
	if got.ReleasedAt == nil || got.IsActive {
		t.Errorf("released assignment decoded wrong: %+v", got)
	}
}

// TestActiveStorageAssignmentForSlot covers the lookup release depends on: the
// grid cell carries the occupant but not the assignment id, so release has to
// resolve it from the slot code.
func TestActiveStorageAssignmentForSlot(t *testing.T) {
	var gotQuery string
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Encode()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count": 1, "results": [
		  {"id": 5, "slot_code": "1C1", "storage_type": "committee", "type_letter": "C",
		   "occupant_display": "Welding SIG", "is_active": true}]}`))
	}))

	got, err := c.ActiveStorageAssignmentForSlot(context.Background(), "1c1")
	if err != nil {
		t.Fatalf("ActiveStorageAssignmentForSlot: %v", err)
	}
	if got == nil || got.ID != 5 {
		t.Fatalf("got %+v, want the live holding", got)
	}
	if gotQuery != "active=true&slot_code=1C1" {
		t.Errorf("query = %q, want the normalized code and active=true", gotQuery)
	}
}

// Nobody holding the slot is a normal answer, not an error — the caller shows
// "not assigned" rather than a failure.
func TestActiveStorageAssignmentForSlot_None(t *testing.T) {
	c := newOverviewTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count": 0, "results": []}`))
	}))

	got, err := c.ActiveStorageAssignmentForSlot(context.Background(), "1C1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for an unheld slot", got)
	}
}
