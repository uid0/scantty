// Storage reservation slots — the physical project-storage racking.
//
// A StorageSlot is one addressable place in the pallet racking, named by a
// structured code (`1A1`): leading digits = the RACK, the letter = the LEVEL on
// that rack (early letters are ground-reachable, late ones are up high), and the
// trailing digits = the POSITION along the rack. The three components are
// authoritative and the model recomputes `code` from them on every save, so
// `code` is READ-ONLY on the wire — never send it.
//
// Contract verified against OMS backend/project_storage/{models,serializers,
// views}.py (PRs op-0xkl #990 model + generator, op-uw2q #991 printable cards,
// op-hfw5 #992 slot-aware claims), NOT a paraphrase. There is no web surface for
// any of this yet — `grep -rn storage_slot frontend/src` is empty — so ScanTTY is
// the FIRST interface and the serializer is the parity target.
//
// Endpoints (DefaultRouter under /api/project-storage/slots/, trailing slash),
// every verb gated IsStorageAdminOrStaff (staff/superuser OR a member of the
// "Storage Admin" group — the delegation the group exists for):
//
//	GET    /slots/                    paginated list; ?rack= ?level= ?is_active=
//	                                  ?requires_pallet_jack= ?occupied=
//	GET    /slots/{code}/             ONE slot — addressed by CODE, not pk
//	POST   /slots/                    create one slot (tag allocated on create)
//	PATCH  /slots/{code}/             update
//	DELETE /slots/{code}/             409 while a live stint sits in it
//	POST   /slots/generate/           bulk-generate a rack, idempotent
//	GET    /slots/{code}/card-preview/  base64 single-card PDF + what it encodes
//	POST   /slots/cards/              batch card sheet — returns the PDF BYTES
package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// StorageSlotCodePattern is the backend's StorageSlot.CODE_PATTERN verbatim —
// digits, one letter, digits. Shared by ParseStorageSlotCode and every caller
// that wants to reject a typo before spending a round trip, so "what is a valid
// code?" has one definition on this side too.
const StorageSlotCodePattern = `^(\d+)([A-Za-z])(\d+)$`

var storageSlotCodeRE = regexp.MustCompile(StorageSlotCodePattern)

// StorageSlotOccupant mirrors SlotOccupantSerializer — the live stint in a slot,
// trimmed to what a warden needs at a glance. Deliberately NOT the full stint
// serializer (that one drags the whole event log along, which would make a rack
// listing enormous), so a slot row carries who/what/when and nothing more.
type StorageSlotOccupant struct {
	ID           int        `json:"id"`
	StintID      string     `json:"stint_id"`
	Username     string     `json:"username"`
	DisplayName  string     `json:"display_name"`
	ProjectTitle string     `json:"project_title"`
	StartedAt    time.Time  `json:"started_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Status       string     `json:"status"`
}

// StorageSlot mirrors StorageSlotSerializer.
//
// AprilTagID is the slot's PERMANENT fiducial marker: it is allocated once (on
// create, or by the bulk generator) and is never released while the slot exists
// — a code has to keep meaning the same physical place. It is null only when
// the tag family ran dry mid-generate, so the slot is usable by code but has
// nothing to scan yet. OwningGroup is an optional auth.Group (SIG) pk.
type StorageSlot struct {
	ID                 int                  `json:"id"`
	Code               string               `json:"code"`
	Rack               int                  `json:"rack"`
	Level              string               `json:"level"`
	Position           int                  `json:"position"`
	RequiresPalletJack bool                 `json:"requires_pallet_jack"`
	IsActive           bool                 `json:"is_active"`
	OwningGroup        *int                 `json:"owning_group"`
	OwningGroupName    string               `json:"owning_group_name"`
	Notes              string               `json:"notes"`
	AprilTagID         *int                 `json:"april_tag_id"`
	CurrentStint       *StorageSlotOccupant `json:"current_stint"`
	IsOccupied         bool                 `json:"is_occupied"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

// StorageSlotWrite is the writable field set of StorageSlotSerializer, exactly.
// `code`, `april_tag_id`, `current_stint`, `is_occupied`, `owning_group_name`
// and the timestamps are read-only and must not be sent.
//
// OwningGroup and Notes carry NO omitempty so a PATCH can CLEAR them: a nil
// pointer marshals to JSON null (the SET_NULL FK's "unowned") and a blank Notes
// to "" (the TextField's blank=True). Rack/Level/Position are always sent —
// they are the slot's identity and the backend recomputes `code` from them.
type StorageSlotWrite struct {
	Rack               int    `json:"rack"`
	Level              string `json:"level"`
	Position           int    `json:"position"`
	RequiresPalletJack bool   `json:"requires_pallet_jack"`
	IsActive           bool   `json:"is_active"`
	OwningGroup        *int   `json:"owning_group"`
	Notes              string `json:"notes"`
}

// RackLevelSpec is one level of a rack in a bulk-generate request
// (RackLevelSpecSerializer). Positions is capped server-side at 1..100 so a
// fat-fingered "1000" can't swallow the whole tag family in one request.
type RackLevelSpec struct {
	Level              string `json:"level"`
	Positions          int    `json:"positions"`
	RequiresPalletJack bool   `json:"requires_pallet_jack"`
}

// GenerateRackRequest is the bulk-generate payload (GenerateRackSerializer):
// one rack, a spec per level, at most 26 levels and each letter at most once.
// OwningGroup/Notes are optional and apply to every slot the run creates —
// omitempty on a create is right (there is nothing to clear yet).
type GenerateRackRequest struct {
	Rack        int             `json:"rack"`
	Levels      []RackLevelSpec `json:"levels"`
	OwningGroup *int            `json:"owning_group,omitempty"`
	Notes       string          `json:"notes,omitempty"`
}

// GenerateRackResult is the generate action's report. The run is IDEMPOTENT:
// codes that already existed are left untouched and named in Skipped, so
// re-running after adding a level (or after a partial failure) is safe.
// WithoutTag is non-empty when the tag family ran dry mid-run.
type GenerateRackResult struct {
	Rack         int           `json:"rack"`
	Created      []string      `json:"created"`
	Skipped      []string      `json:"skipped"`
	CreatedCount int           `json:"created_count"`
	SkippedCount int           `json:"skipped_count"`
	WithoutTag   []string      `json:"without_tag"`
	Slots        []StorageSlot `json:"slots"`
}

// SlotCardBatchRequest selects which slots to print cards for
// (SlotCardBatchSerializer). EXACTLY ONE mode is allowed and the backend 400s
// on both or neither: either explicit SlotIDs (printed in the order given, ≤300)
// or a Rack filter (optionally narrowed to one Level, printed in code order).
// IncludeInactive only applies to the rack mode — a rack print covers the slots
// in service, and a retired slot keeps its marker but shouldn't ride along.
// Use SlotCardsForIDs / SlotCardsForRack rather than building this by hand.
type SlotCardBatchRequest struct {
	SlotIDs         []int  `json:"slot_ids,omitempty"`
	Rack            *int   `json:"rack,omitempty"`
	Level           string `json:"level,omitempty"`
	IncludeInactive bool   `json:"include_inactive,omitempty"`
}

// SlotCardsForIDs builds the explicit-selection print request.
func SlotCardsForIDs(ids []int) SlotCardBatchRequest {
	return SlotCardBatchRequest{SlotIDs: ids}
}

// SlotCardsForRack builds the rack print request. level "" prints the whole
// rack; a level narrows it to that one shelf.
func SlotCardsForRack(rack int, level string, includeInactive bool) SlotCardBatchRequest {
	return SlotCardBatchRequest{
		Rack:            &rack,
		Level:           strings.ToUpper(strings.TrimSpace(level)),
		IncludeInactive: includeInactive,
	}
}

// SlotCardPDF is a rendered Avery 5388 sheet (3 cards per page). The bytes are
// the PDF itself — the backend deliberately does NOT persist these (they are
// printed once and thrown away, so storing them would only grow MEDIA_ROOT),
// which is why this is the one OMS PDF ScanTTY holds in memory rather than
// following a URL. Filename is the server's Content-Disposition suggestion.
type SlotCardPDF struct {
	Filename string
	Data     []byte
}

// SlotCardPreview mirrors build_slot_card_preview — one slot's card as a
// base64 PDF, plus the kiosk URL and marker id the card encodes so a preview
// surface can report what was printed without decoding the PDF.
type SlotCardPreview struct {
	SlotID      int    `json:"slot_id"`
	Code        string `json:"code"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Preview     string `json:"preview"`
	KioskURL    string `json:"kiosk_url"`
	AprilTagID  *int   `json:"april_tag_id"`
}

const storageSlotsPath = "/api/project-storage/slots/"

// ParseStorageSlotCode splits a code into its (rack, level, position)
// components, mirroring StorageSlot.parse_code — including its case fold, so
// "1a1" resolves the same slot as "1A1". Callers use it to reject a malformed
// code locally instead of spending a round trip on a guaranteed 400.
func ParseStorageSlotCode(code string) (rack int, level string, position int, err error) {
	m := storageSlotCodeRE.FindStringSubmatch(strings.TrimSpace(code))
	if m == nil {
		return 0, "", 0, fmt.Errorf("invalid storage slot code %q: expected <rack><level><position>, e.g. 1A1", code)
	}
	// The regexp already proved both groups are digit runs, so the only way
	// these fail is an overflow — which is still a code no slot can have, and
	// must not silently become a different number.
	r, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", 0, fmt.Errorf("invalid storage slot code %q: rack number out of range", code)
	}
	p, err := strconv.Atoi(m[3])
	if err != nil {
		return 0, "", 0, fmt.Errorf("invalid storage slot code %q: position out of range", code)
	}
	return r, strings.ToUpper(m[2]), p, nil
}

// NormalizeStorageSlotCode canonicalizes a typed code (trim + upper-case the
// level) so a lookup by "1a1" hits "1A1". Returns an error for anything that
// isn't a code.
func NormalizeStorageSlotCode(code string) (string, error) {
	rack, level, position, err := ParseStorageSlotCode(code)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d%s%d", rack, level, position), nil
}

// ListStorageSlots returns one page of slots. Useful filters: `?rack=1` and
// `?level=A` for browsing a rack, `?occupied=false&is_active=true` for "what
// can I hand out?", `?occupied=true` for "what's on the rack right now?", and
// `?is_active=false` for the retired ones.
func (c *Client) ListStorageSlots(ctx context.Context, q url.Values) (*Page[StorageSlot], error) {
	return GetPage[StorageSlot](ctx, c, storageSlotsPath, q)
}

// ListAllStorageSlots walks every page of the filtered list. A rack easily
// exceeds the 50-row page size, so anything that has to reason about a whole
// rack (the free-slot picker, a print selection) must not stop at page 1.
func (c *Client) ListAllStorageSlots(ctx context.Context, q url.Values) ([]StorageSlot, error) {
	var out []StorageSlot
	err := IterPages(ctx, c, storageSlotsPath, cloneValues(q), func(batch []StorageSlot) error {
		out = append(out, batch...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetStorageSlot fetches one slot BY CODE — the viewset's lookup_field is
// `code`, not the pk, because the code is what is printed on the rack and what
// a scanner reads, so it is the identifier every caller already has. The code
// is normalized first so "1a1" works.
func (c *Client) GetStorageSlot(ctx context.Context, code string) (*StorageSlot, error) {
	canonical, err := NormalizeStorageSlotCode(code)
	if err != nil {
		return nil, err
	}
	var out StorageSlot
	if err := c.Get(ctx, storageSlotsPath+canonical+"/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateStorageSlot adds one slot. The response carries the AprilTag allocated
// for it (perform_create ensures the tag before DRF reads serializer.data), so
// the caller can report the marker immediately. A duplicate (rack, level,
// position) comes back as a 400 from the serializer's UniqueTogetherValidator
// rather than a 500 from the DB constraint.
func (c *Client) CreateStorageSlot(ctx context.Context, w StorageSlotWrite) (*StorageSlot, error) {
	var out StorageSlot
	if err := c.Post(ctx, storageSlotsPath, w, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateStorageSlot PATCHes a slot, addressed by its CURRENT code. Changing
// rack/level/position is legal and recomputes the code server-side, so the
// returned slot may answer at a different URL than the one just written to —
// callers should re-read Code from the response rather than reusing the old one.
func (c *Client) UpdateStorageSlot(ctx context.Context, code string, w StorageSlotWrite) (*StorageSlot, error) {
	canonical, err := NormalizeStorageSlotCode(code)
	if err != nil {
		return nil, err
	}
	var out StorageSlot
	if err := c.Patch(ctx, storageSlotsPath+canonical+"/", w, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteStorageSlot removes a slot and RELEASES its marker — the one case where
// the "a code always means the same physical place" promise ends. It 409s while
// a live stint occupies the slot (the stint FK is SET_NULL, so without that
// guard the delete would quietly strip the location off a member whose stuff is
// still physically on the rack); StorageSlotErrorDetail turns that into a
// sentence naming the occupant. Retiring (is_active=false) is the
// non-destructive option and keeps the tag.
func (c *Client) DeleteStorageSlot(ctx context.Context, code string) error {
	canonical, err := NormalizeStorageSlotCode(code)
	if err != nil {
		return err
	}
	return c.Delete(ctx, storageSlotsPath+canonical+"/")
}

// GenerateRackSlots bulk-creates one rack's slots from a per-level spec.
// Idempotent: existing codes are reported under Skipped instead of failing, so
// re-running after adding a level is safe. Returns 201 when anything was
// created and 200 when it was all skipped — both decode the same report.
func (c *Client) GenerateRackSlots(ctx context.Context, req GenerateRackRequest) (*GenerateRackResult, error) {
	var out GenerateRackResult
	if err := c.Post(ctx, storageSlotsPath+"generate/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RenderStorageSlotCards renders the batch card sheet and returns the PDF
// BYTES (the backend streams the file rather than storing it). The caller
// decides where they land — in a terminal that means writing them somewhere the
// operator can print from.
func (c *Client) RenderStorageSlotCards(ctx context.Context, req SlotCardBatchRequest) (*SlotCardPDF, error) {
	if len(req.SlotIDs) > 0 && req.Rack != nil {
		return nil, errors.New("slot cards: pass either slot_ids or a rack filter, not both")
	}
	if len(req.SlotIDs) == 0 && req.Rack == nil {
		return nil, errors.New("slot cards: pass slot_ids or a rack to print")
	}
	data, filename, err := c.PostBytes(ctx, storageSlotsPath+"cards/", req)
	if err != nil {
		return nil, err
	}
	return &SlotCardPDF{Filename: filename, Data: data}, nil
}

// PreviewStorageSlotCard renders ONE slot's card as a base64 PDF plus the kiosk
// URL and marker id it encodes.
func (c *Client) PreviewStorageSlotCard(ctx context.Context, code string) (*SlotCardPreview, error) {
	canonical, err := NormalizeStorageSlotCode(code)
	if err != nil {
		return nil, err
	}
	var out SlotCardPreview
	if err := c.Get(ctx, storageSlotsPath+canonical+"/card-preview/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StorageSlotErrorDetail extracts the human sentence out of the slot
// endpoints' HAND-ROLLED error responses — the occupied-slot 409, the
// unknown-slot_ids 404 and the empty-rack 404 are plain `Response({...},
// status)` returns, not raised exceptions, so the backend's standardized
// exception handler never wraps them in the `{"error": {...}}` envelope
// parseError understands. Without this the operator would be shown a raw JSON
// blob. Returns ("", "") for anything that isn't one of those bodies, so
// callers fall back to err.Error().
func StorageSlotErrorDetail(err error) (detail, code string) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return "", ""
	}
	var body struct {
		Detail string `json:"detail"`
		Code   string `json:"code"`
	}
	if json.Unmarshal([]byte(apiErr.Message), &body) != nil {
		return "", ""
	}
	return body.Detail, body.Code
}

// cloneValues copies query params so a helper that adds `page` can't mutate a
// caller's url.Values (IterPages sets it in place).
func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, vs := range q {
		for _, v := range vs {
			out.Add(k, v)
		}
	}
	return out
}
