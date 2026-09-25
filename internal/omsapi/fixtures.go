package omsapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Fixtures and their refill requests — a soap dispenser, a towel holder: a
// thing installed at a location that is refilled from one inventory item, and
// the queue of "this needs refilling" reports its QR label files.
//
// OMS's `inventory.views.FixtureViewSet` and `FixtureRefillRequestViewSet` are
// the contract, and the web's FixtureScanPage / LocationFixturesList are what
// parity is measured against. Every wire shape below is pinned by a body
// RECORDED off a real backend (testdata/README.md, "Fixtures and refill
// requests"), because the one client this file replaces was not: `ScanFixture`
// decoded the scan reply into a Fixture with a name, a location and a status,
// and the scan action returns a REFILL REQUEST, whose name key is
// `fixture_name`. Its test fed it a hand-written body shaped like the struct and
// passed.
//
// WHAT IS WORTH KNOWING BEFORE TOUCHING ANY OF IT, all of it measured:
//
//   - EVERY ID IS A STRING UUID except `location`, which is an integer
//     (Location declares no pk). Fixture, FixtureRefillRequest and the refill
//     InventoryItem all declare `UUIDField(primary_key=True)`, so nothing here is
//     decoded into an `any` and nothing is spent through anyIDString.
//   - PERMISSIONS ARE SIGNED-IN, NOT STAFF. Both viewsets are
//     IsAuthenticatedOrReadOnly (the scan action and a request CREATE are
//     AllowAny), and neither resolve action checks anything more. The web shows
//     "Resolve all" only where its localStorage says `is_staff`, which is a
//     presentation choice and not a rule the server holds; OMS refuses a
//     sign-in without a membership or role before any of this is reached.
//   - A RESOLVE'S NOTES REPLACE THE REQUEST'S OWN NOTE, and absent and empty are
//     different writes. `resolve` does `notes = request.data.get("notes",
//     stored)`, so an absent key keeps the reporter's note and `""` ERASES it
//     (fixture_refill_request_resolve_empty_notes.json); `resolve_all` writes
//     the notes over EVERY pending request when they are non-blank
//     (fixture_resolve_all_with_notes.json, where two reporters' notes became
//     one resolution sentence). So the notes key is sent only when there are
//     notes, and a caller offering notes must say what they overwrite.
//   - `resolve_all` RESOLVES WHAT IS PENDING WHEN IT ARRIVES, not what the caller
//     last saw: it is one UPDATE over `status=pending`, so a report filed after
//     the list loaded is closed with the rest, and an `in_progress` one is not
//     touched. It refuses nothing — resolving nothing is a 200 reading "Resolved
//     0 pending refill request(s)".
//   - `resolve` refuses ONLY a request already `completed`, with the
//     hand-written `{"error": "This request is already completed"}`. It does not
//     refuse a CANCELLED one, which it turns into completed; nothing on the
//     client should present that as a rule the server keeps.
//   - REFUSALS COME IN TWO SHAPES from one viewset: the view bodies write
//     `{"error": "<prose>"}` by hand (scan on an inactive fixture, resolve on a
//     completed request) and everything DRF raises — a 404, a 401 — arrives as
//     the standardized coded envelope. FixtureRefusal reads both.
//   - The location's fixture list is a BARE ARRAY of ACTIVE fixtures only
//     (`location.fixtures.filter(is_active=True)`), and the refill-request list
//     is PAGINATED and filters server-side on `fixture`, `status` and
//     `location` — unlike the reorder-request list, those query parameters are
//     honoured.

// Refill-request states, `FixtureRefillRequest.Status`. Nothing in OMS moves a
// request to `in_progress` on its own; only a direct PATCH of the model does.
const (
	FixtureRefillPending    = "pending"
	FixtureRefillInProgress = "in_progress"
	FixtureRefillCompleted  = "completed"
	FixtureRefillCancelled  = "cancelled"
)

// Fixture mirrors `FixtureSerializer`, and `FixtureDetailSerializer` where the
// detail route adds RecentRefillRequests and RefillItemDetails (absent on a list
// row, hence the pointer and the nil slice).
type Fixture struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	Location             int       `json:"location"`
	LocationName         string    `json:"location_name"`
	RefillItem           string    `json:"refill_item"`
	RefillItemName       string    `json:"refill_item_name"`
	RefillItemSKU        string    `json:"refill_item_sku"`
	AssetTag             *string   `json:"asset_tag"`
	IsActive             bool      `json:"is_active"`
	PendingRequestsCount int       `json:"pending_requests_count"`
	QRCodeURL            string    `json:"qr_code_url"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`

	// RecentRefillRequests is the detail route's last TEN requests of EVERY
	// status, newest first (`to_representation` slices it). It is not the
	// pending queue — a fixture with eleven pending reports shows ten here — so
	// the queue is read from ListFixtureRefillRequests, as the web does.
	RecentRefillRequests []FixtureRefillRequest `json:"recent_refill_requests,omitempty"`
	RefillItemDetails    *FixtureRefillItem     `json:"refill_item_details,omitempty"`
}

// FixtureRefillItem is the detail route's hand-built `refill_item_details` dict.
type FixtureRefillItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SKU          string `json:"sku"`
	CurrentStock int    `json:"current_stock"`
	MinimumStock int    `json:"minimum_stock"`
	NeedsReorder bool   `json:"needs_reorder"`
}

// FixtureRefillRequest mirrors `FixtureRefillRequestSerializer`.
//
// The requester is a PAIR (#888): RequestedActor is the collapsed display name
// and is never blank — an anonymous kiosk scan reads "Anonymous" — while
// RequestedBy is the legacy string, blank for that same scan. Draw the actor.
// ResolvedActor is null until someone resolves the request.
type FixtureRefillRequest struct {
	ID                string     `json:"id"`
	Fixture           string     `json:"fixture"`
	FixtureName       string     `json:"fixture_name"`
	FixtureLocation   string     `json:"fixture_location"`
	RefillItemName    string     `json:"refill_item_name"`
	RefillItemSKU     string     `json:"refill_item_sku"`
	Status            string     `json:"status"`
	RequestedAt       time.Time  `json:"requested_at"`
	RequestedBy       string     `json:"requested_by"`
	RequestedActor    string     `json:"requested_actor"`
	RequestedUsername *string    `json:"requested_username"`
	ResolvedAt        *time.Time `json:"resolved_at"`
	ResolvedBy        string     `json:"resolved_by"`
	ResolvedActor     *string    `json:"resolved_actor"`
	ResolvedUsername  *string    `json:"resolved_username"`
	Notes             string     `json:"notes"`
	TimeToResolve     *int       `json:"time_to_resolve"`
}

// Requester is who filed the request, as the server collapses it.
func (r FixtureRefillRequest) Requester() string {
	if s := strings.TrimSpace(r.RequestedActor); s != "" {
		return s
	}
	if s := strings.TrimSpace(r.RequestedBy); s != "" {
		return s
	}
	return "Anonymous"
}

// FixtureRefillRequestFilter is the list route's three server-side filters.
// Blank fields are not sent; Location is the integer Location pk.
type FixtureRefillRequestFilter struct {
	Fixture  string
	Status   string
	Location int
}

func (f FixtureRefillRequestFilter) query() url.Values {
	q := url.Values{}
	if f.Fixture != "" {
		q.Set("fixture", f.Fixture)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.Location > 0 {
		q.Set("location", strconv.Itoa(f.Location))
	}
	return q
}

// FixtureResolveAllResult is `resolve_all`'s reply: the server's own sentence,
// and the fixture as it stands afterwards.
type FixtureResolveAllResult struct {
	Message string  `json:"message"`
	Fixture Fixture `json:"fixture"`
}

// fixtureNotesBody is a write body carrying notes only when there are some. The
// absent key is what keeps a reporter's note on `resolve` (see the file note);
// on `scan` and `resolve_all` it is what the web sends for blank notes.
func fixtureNotesBody(notes string) map[string]string {
	body := map[string]string{}
	if n := strings.TrimSpace(notes); n != "" {
		body["notes"] = n
	}
	return body
}

// fixtureSegment escapes an id for use as a path segment. Every id here is a
// UUID, which escapes to itself; an operator-typed or scanned value is what this
// protects.
func fixtureSegment(id string) string { return url.PathEscape(strings.TrimSpace(id)) }

// GetFixture reads one fixture through the DETAIL serializer.
func (c *Client) GetFixture(ctx context.Context, id string) (*Fixture, error) {
	var out Fixture
	if err := c.Get(ctx, "/api/inventory/fixtures/"+fixtureSegment(id)+"/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListLocationFixtures is `GET /api/inventory/locations/{id}/fixtures/`: the
// ACTIVE fixtures installed at one location, as a bare array. An inactive
// fixture is not listed here at all.
func (c *Client) ListLocationFixtures(ctx context.Context, locationID int) ([]Fixture, error) {
	var out MaybeList[Fixture]
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/locations/%d/fixtures/", locationID), nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ListFixtureRefillRequests walks every page of the refill-request list under
// the filter, newest first. Every page, because a screen that says "3 pending"
// off page one of a paginated list is stating a count it did not read.
func (c *Client) ListFixtureRefillRequests(ctx context.Context, filter FixtureRefillRequestFilter) ([]FixtureRefillRequest, error) {
	var all []FixtureRefillRequest
	if err := IterPages[FixtureRefillRequest](ctx, c, "/api/inventory/fixture-refill-requests/", filter.query(), func(batch []FixtureRefillRequest) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// ScanFixture files a refill request against a fixture — what scanning its QR
// label does on the web. Blank notes send no key. A signed-in caller is stamped
// as the requester by the server. Refused on an inactive fixture.
func (c *Client) ScanFixture(ctx context.Context, fixtureID, notes string) (*FixtureRefillRequest, error) {
	var out FixtureRefillRequest
	if err := c.Post(ctx, "/api/inventory/fixtures/"+fixtureSegment(fixtureID)+"/scan/", fixtureNotesBody(notes), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResolveFixtureRefillRequest marks one request completed. Blank notes send NO
// key, which keeps the reporter's note; non-blank notes replace it.
func (c *Client) ResolveFixtureRefillRequest(ctx context.Context, requestID, notes string) (*FixtureRefillRequest, error) {
	var out FixtureRefillRequest
	if err := c.Post(ctx, "/api/inventory/fixture-refill-requests/"+fixtureSegment(requestID)+"/resolve/", fixtureNotesBody(notes), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResolveAllFixtureRefillRequests marks every request PENDING on the fixture at
// the moment the server receives this completed. Non-blank notes replace every
// one of those requests' notes.
func (c *Client) ResolveAllFixtureRefillRequests(ctx context.Context, fixtureID, notes string) (*FixtureResolveAllResult, error) {
	var out FixtureResolveAllResult
	if err := c.Post(ctx, "/api/inventory/fixtures/"+fixtureSegment(fixtureID)+"/resolve_all/", fixtureNotesBody(notes), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FixtureRefusal recovers the server's own sentence from a failed fixture call,
// and reports false where there is none to recover.
//
// Both shapes this viewset refuses in (see the file note): the hand-written
// `{"error": "<prose>"}` through AsReceivingRefusal, and the coded envelope
// parseError has already decoded, whose Message IS the sentence. Anything else —
// a gateway page, a transport error — is not coerced into one, for the reason
// AsReceivingRefusal gives about inventing a sentence the server never said.
func FixtureRefusal(err error) (string, bool) {
	if prose, ok := AsReceivingRefusal(err); ok {
		return prose, true
	}
	var api *APIError
	if errors.As(err, &api) && api.Code != "" && strings.TrimSpace(api.Message) != "" {
		return api.Message, true
	}
	return "", false
}
