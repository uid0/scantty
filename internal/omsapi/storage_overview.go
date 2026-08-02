// The storage overview — one glanceable status grid per rack.
//
// The read this exists for: stand in the aisle, pull the rack up, look for the
// coloured cells, go deal with those. So the payload is shaped like the racking
// itself rather than like the tables behind it — a row per level with the HIGH
// levels first (you read a rack top-down), a dense run of positions per row, and
// one flat cell per slot carrying everything a renderer needs.
//
// Contract verified against OMS backend/project_storage/services/
// storage_overview.py + serializers.py + views.py (PR op-wgc8 #994), NOT a
// paraphrase. There is no web surface for it — `grep -rn project-storage/overview
// frontend/src` is empty, and the frontend has no assignment page either — so
// ScanTTY is the FIRST interface for the grid and the serializer is the parity
// target (the sc-ofem / sc-iz0s precedent).
//
//	GET /api/project-storage/overview/   ?rack=N narrows to one rack
//
// Gated IsStorageAdminOrStaff, the same as the racking it describes: the grid
// names every member with something on the shelves.
package omsapi

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// The single-letter storage types a cell can carry. P is a member's project
// stint; C/L/E are the staff-assigned long-term holdings (see
// storage_assignments.go). "E" is class because "C" is already committee's —
// the grid has one column of characters to work with.
const (
	StorageTypeLetterProject   = "P"
	StorageTypeLetterCommittee = "C"
	StorageTypeLetterLogistics = "L"
	StorageTypeLetterClass     = "E"
)

// The colour names the backend hands back. ONLY Project storage is ever
// coloured: a committee slot has been the committee's for two years and will be
// tomorrow, so colouring it would mean the grid is mostly coloured and the one
// expired member project stops standing out — which is the entire point of the
// screen. A renderer maps these names to a style and makes no status judgement
// of its own; the mapping from stint status to colour lives server-side in
// PROJECT_STATUS_COLORS.
const (
	StorageOverviewColorYellow = "yellow" // expiring soon
	StorageOverviewColorRed    = "red"    // expired, warned, or in purgatory
)

// StorageOverviewStatusEmpty / Occupied are the two statuses that are not a
// stint status: an empty slot, and a C/L/E holding (which has no clock to be
// late against, so it is simply in use).
const (
	StorageOverviewStatusEmpty    = "empty"
	StorageOverviewStatusOccupied = "occupied"
)

// StorageOverviewCell mirrors StorageOverviewCellSerializer — one slot in the
// grid.
//
// Type and Color are `allow_null` server-side and decode to "" for an empty
// slot / an uncoloured cell: a fresh struct starts zeroed and encoding/json
// leaves a non-pointer field untouched on null, so "" IS the null reading here.
// The backend never sends an empty string for either, so "" is unambiguous and
// a pointer would only push the same nil check onto every caller.
//
// IsActive is the slot's own in-service flag. A retired slot is empty but is
// NOT available, and a grid that can't tell those apart invites the warden to
// hand out a slot that isn't there any more.
type StorageOverviewCell struct {
	Code     string `json:"code"`
	SlotID   int    `json:"slot_id"`
	Position int    `json:"position"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Color    string `json:"color"`
	Occupant string `json:"occupant"`
	IsActive bool   `json:"is_active"`
}

// StorageOverviewRow is one level of one rack: a dense, 1-INDEXED run of
// positions. Cells is MaxPosition long and holds nil where the racking has no
// slot at that position, so every row of a rack lines up without the renderer
// re-deriving which columns exist. Entry i is position i+1.
type StorageOverviewRow struct {
	Level string                 `json:"level"`
	Cells []*StorageOverviewCell `json:"cells"`
}

// StorageOverviewRack is one rack. Levels is DESCENDING — Z overhead, A at your
// feet — so the payload reads the way the steel does, and Rows is in the same
// order.
type StorageOverviewRack struct {
	Rack        int                  `json:"rack"`
	Levels      []string             `json:"levels"`
	MaxPosition int                  `json:"max_position"`
	Rows        []StorageOverviewRow `json:"rows"`
}

// StorageOverview is the whole grid, rack by rack in numeric order. It is NOT
// paginated — the view is a plain APIView, so one request is the whole racking.
type StorageOverview struct {
	Racks       []StorageOverviewRack `json:"racks"`
	GeneratedAt time.Time             `json:"generated_at"`
}

const storageOverviewPath = "/api/project-storage/overview/"

// GetStorageOverview fetches the grid. rack 0 means every rack — the builder is
// three queries whatever the slot count, so pulling the whole racking in one
// request is cheaper than a round trip per rack, and a client that already has
// every rack can page between them locally.
//
// A junk rack filter matches nothing server-side rather than erroring, so an
// empty Racks slice means "no racking here", not "something went wrong".
func (c *Client) GetStorageOverview(ctx context.Context, rack int) (*StorageOverview, error) {
	var q url.Values
	if rack > 0 {
		q = url.Values{"rack": {strconv.Itoa(rack)}}
	}
	var out StorageOverview
	if err := c.Get(ctx, storageOverviewPath, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FindRack returns the rack with that number, or nil. Callers page by INDEX but
// re-key by rack NUMBER across a refresh: a rack can appear or disappear between
// two fetches (the last slot of a rack retired, a new aisle generated), and an
// index kept over that lands the operator on someone else's rack.
func (o *StorageOverview) FindRack(number int) *StorageOverviewRack {
	if o == nil {
		return nil
	}
	for i := range o.Racks {
		if o.Racks[i].Rack == number {
			return &o.Racks[i]
		}
	}
	return nil
}

// CellAt returns the cell at (level, position), or nil for a hole in the
// racking. Position is 1-based, as printed on the card.
func (r *StorageOverviewRack) CellAt(level string, position int) *StorageOverviewCell {
	if r == nil {
		return nil
	}
	for _, row := range r.Rows {
		if row.Level != level {
			continue
		}
		if position < 1 || position > len(row.Cells) {
			return nil
		}
		return row.Cells[position-1]
	}
	return nil
}
