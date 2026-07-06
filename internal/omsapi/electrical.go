package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// PowerPanel is the directory row returned by GET /api/electrical/panels/.
// Locations and breaker counts come pre-joined; the full breaker tree is
// pulled separately via GetPowerPanelTopology.
type PowerPanel struct {
	ID                  int    `json:"id"`
	Name                string `json:"name"`
	LocationID          int    `json:"location_id"`
	LocationName        string `json:"location_name"`
	PhaseConfiguration  string `json:"phase_configuration"`
	Voltage             int    `json:"voltage"`
	MainBreakerAmperage int    `json:"main_breaker_amperage"`
	BreakerCount        int    `json:"breaker_count"`
	NeedsReview         bool   `json:"needs_review"`
}

// PowerBreaker is the per-row breaker representation used inside topology
// and trip-impact responses.
type PowerBreaker struct {
	ID           int    `json:"id"`
	PanelID      int    `json:"panel_id"`
	PanelName    string `json:"panel_name"`
	Position     string `json:"position"`
	Amperage     int    `json:"amperage"`
	Phase        string `json:"phase"`
	PoleCount    int    `json:"pole_count"`
	Status       string `json:"status"`
	ReviewStatus string `json:"review_status,omitempty"`
	ReviewNote   string `json:"review_note,omitempty"`
	Label        string `json:"label,omitempty"`
}

// PowerCircuit is one branch off a breaker.
type PowerCircuit struct {
	ID            int    `json:"id"`
	Label         string `json:"label,omitempty"`
	BreakerID     int    `json:"breaker_id"`
	MaxLoadAmps   int    `json:"max_load_amps,omitempty"`
	ConductorSize string `json:"conductor_size,omitempty"`
}

// PowerOutlet is one outlet on a circuit. ConnectedAssets is populated by
// the topology endpoint; the legacy outlets-crud surface omits it.
type PowerOutlet struct {
	ID              int               `json:"id"`
	Label           string            `json:"label,omitempty"`
	OutletType      string            `json:"outlet_type,omitempty"`
	Status          string            `json:"status"`
	LocationID      *int              `json:"location_id"`
	LocationName    string            `json:"location_name,omitempty"`
	ConnectedAssets []PowerChainAsset `json:"connected_assets,omitempty"`
}

// PowerChainAsset is the slim asset slice the safety views return inside
// topology / trip-impact / power-chain responses. It deliberately doesn't
// share the heavy Asset struct from inventory.go since the safety endpoints
// only carry four fields.
type PowerChainAsset struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AssetTag   string `json:"asset_tag,omitempty"`
	IsCritical bool   `json:"is_critical"`
}

// PowerPanelTopology is the panel → breaker → circuit → outlet → asset
// tree returned by GET /api/electrical/panels/{id}/topology/.
type PowerPanelTopology struct {
	ID                  int                      `json:"id"`
	Name                string                   `json:"name"`
	LocationID          int                      `json:"location_id"`
	LocationName        string                   `json:"location_name"`
	PhaseConfiguration  string                   `json:"phase_configuration"`
	Voltage             int                      `json:"voltage"`
	MainBreakerAmperage int                      `json:"main_breaker_amperage"`
	BreakerType         string                   `json:"breaker_type,omitempty"`
	NumberingDirection  string                   `json:"numbering_direction,omitempty"`
	FedBySummary        *PanelFedBySummary       `json:"fed_by_summary"`
	DownstreamPanels    []PanelDownstreamSummary `json:"downstream_panels"`
	Breakers            []PowerBreakerWithChain  `json:"breakers"`
}

// PanelFedBySummary describes the upstream breaker + panel feeding this
// panel. Nil at the top of the tree (utility-fed mains).
type PanelFedBySummary struct {
	CircuitID        int    `json:"circuit_id"`
	CircuitLabel     string `json:"circuit_label,omitempty"`
	BreakerID        int    `json:"breaker_id"`
	BreakerPosition  string `json:"breaker_position"`
	BreakerAmperage  int    `json:"breaker_amperage"`
	BreakerPoleCount int    `json:"breaker_pole_count"`
	PanelID          int    `json:"panel_id"`
	PanelName        string `json:"panel_name"`
}

// PanelDownstreamSummary names a sub-panel fed by THIS panel's circuits.
type PanelDownstreamSummary struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// PowerBreakerWithChain embeds the flat breaker fields and nests its
// circuits — each carries its outlets and connected assets.
type PowerBreakerWithChain struct {
	PowerBreaker
	Circuits []PowerCircuitWithOutlets `json:"circuits"`
}

// PowerCircuitWithOutlets is a circuit + its outlets.
type PowerCircuitWithOutlets struct {
	PowerCircuit
	Outlets []PowerOutlet `json:"outlets"`
}

// BreakerTripImpact is the response to GET /api/electrical/breakers/{id}/trip-impact/.
// Tells you what goes dark if this breaker trips.
type BreakerTripImpact struct {
	Breaker       PowerBreaker      `json:"breaker"`
	Assets        []PowerChainAsset `json:"assets"`
	CriticalLoads []PowerChainAsset `json:"critical_loads"`
}

// CircuitLoad is the response to GET /api/electrical/circuits/{id}/load/.
// Estimated draw, capacity, and per-asset breakdown.
type CircuitLoad struct {
	Circuit                    PowerCircuit      `json:"circuit"`
	ConnectedDeviceCount       int               `json:"connected_device_count"`
	ConnectedDevices           []PowerChainAsset `json:"connected_devices"`
	EstimatedMaxDrawAmps       float64           `json:"estimated_max_draw_amps"`
	CapacityAmps               *int              `json:"capacity_amps"`
	CapacityUtilizationPercent *float64          `json:"capacity_utilization_percent"`
}

// PowerChainHop is one step in the asset → outlet → circuit → breaker →
// panel → (sub-panel feeder) → main path returned by AssetPowerChainView.
type PowerChainHop struct {
	Kind  string `json:"kind"`
	ID    any    `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

// AssetPowerChain is the response to GET /api/assets/{id}/power-chain/.
// Answers "which breaker feeds this thing" — the most common floor question.
type AssetPowerChain struct {
	Asset PowerChainAsset `json:"asset"`
	Chain []PowerChainHop `json:"chain"`
}

// ListPowerPanels pulls the panel directory. PowerPanelListView returns a
// plain {results: [...]} envelope rather than DRF pagination, so unwrap
// into the typed slice here.
func (c *Client) ListPowerPanels(ctx context.Context) ([]PowerPanel, error) {
	var out struct {
		Results []PowerPanel `json:"results"`
	}
	if err := c.Get(ctx, "/api/electrical/panels/", nil, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// GetPowerPanelTopology pulls the full panel tree.
func (c *Client) GetPowerPanelTopology(ctx context.Context, panelID int) (*PowerPanelTopology, error) {
	var out PowerPanelTopology
	if err := c.Get(ctx, fmt.Sprintf("/api/electrical/panels/%d/topology/", panelID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBreakerTripImpact lists every asset that loses power if the breaker
// trips, separating critical loads.
func (c *Client) GetBreakerTripImpact(ctx context.Context, breakerID int) (*BreakerTripImpact, error) {
	var out BreakerTripImpact
	if err := c.Get(ctx, fmt.Sprintf("/api/electrical/breakers/%d/trip-impact/", breakerID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCircuitLoad estimates draw against capacity for a single circuit.
func (c *Client) GetCircuitLoad(ctx context.Context, circuitID int) (*CircuitLoad, error) {
	var out CircuitLoad
	if err := c.Get(ctx, fmt.Sprintf("/api/electrical/circuits/%d/load/", circuitID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAssetPowerChain walks the path from an asset back to its main feed.
// Asset IDs are UUIDs (string) — see [[scantty-api-field-drift]] PK note.
func (c *Client) GetAssetPowerChain(ctx context.Context, assetID string) (*AssetPowerChain, error) {
	var out AssetPowerChain
	if err := c.Get(ctx, fmt.Sprintf("/api/assets/%s/power-chain/", assetID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ===========================================================================
// PowerPanel / PowerBreaker / PowerCircuit CRUD (panels-crud / breakers-crud /
// circuits-crud). These are the WRITE + management surface, distinct from the
// read-only /panels/ directory (PowerPanel) and /panels/{id}/topology/
// (PowerPanelTopology) shapes above. All three viewsets are staff-gated
// (IsStaffUser) and DRF-paginated.
//
// DECODE-DRIFT NOTE: the *-crud serializers emit DIFFERENT field names than the
// topology tree — a breaker's panel is `panel` (int id) here vs `panel_id` in
// topology, position is a string ("12" or a "14/16" tandem), and the CRUD rows
// carry needs_review / is_critical / critical_* / *_count / created_at fields
// topology never returns. So these use dedicated *Detail read structs rather
// than reusing PowerBreaker / PowerCircuit, keeping the topology decode intact.
// FK fields (location, fed_by, panel, breaker) serialize as int ids; the
// matching *_name / *_label strings are separate read-only fields. install_date
// is a date-only "YYYY-MM-DD" string; created_at/updated_at are full datetimes.
// ===========================================================================

// PowerPanelDetail is the full CRUD representation of a panel returned by
// GET/POST/PATCH /api/electrical/panels-crud/[{id}/]. main_breaker_amperage,
// install_date and fed_by are nullable.
type PowerPanelDetail struct {
	ID                   int                `json:"id"`
	Location             int                `json:"location"`
	LocationName         string             `json:"location_name"`
	Name                 string             `json:"name"`
	PhaseConfiguration   string             `json:"phase_configuration"`
	Voltage              int                `json:"voltage"`
	MainBreakerAmperage  *int               `json:"main_breaker_amperage"`
	BreakerType          string             `json:"breaker_type"`
	NumberingDirection   string             `json:"numbering_direction"`
	Manufacturer         string             `json:"manufacturer"`
	Model                string             `json:"model"`
	InstallDate          string             `json:"install_date"`
	Notes                string             `json:"notes"`
	NeedsReview          bool               `json:"needs_review"`
	FedBy                *int               `json:"fed_by"`
	FedBySummary         *PanelFedBySummary `json:"fed_by_summary"`
	BreakerCount         int                `json:"breaker_count"`
	DownstreamPanelCount int                `json:"downstream_panel_count"`
	CreatedAt            time.Time          `json:"created_at,omitempty"`
	UpdatedAt            time.Time          `json:"updated_at,omitempty"`
}

// PowerBreakerDetail is the full CRUD representation of a breaker returned by
// GET/POST/PATCH /api/electrical/breakers-crud/[{id}/] (and the ?panel= list).
type PowerBreakerDetail struct {
	ID               int       `json:"id"`
	Panel            int       `json:"panel"`
	PanelName        string    `json:"panel_name"`
	Position         string    `json:"position"`
	PoleCount        int       `json:"pole_count"`
	Amperage         int       `json:"amperage"`
	Phase            string    `json:"phase"`
	Status           string    `json:"status"`
	ReviewStatus     string    `json:"review_status"`
	ReviewNote       string    `json:"review_note"`
	Label            string    `json:"label"`
	Notes            string    `json:"notes"`
	NeedsReview      bool      `json:"needs_review"`
	IsCritical       bool      `json:"is_critical"`
	CriticalCategory string    `json:"critical_category"`
	CriticalNote     string    `json:"critical_note"`
	CircuitCount     int       `json:"circuit_count"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

// PowerCircuitDetail is the full CRUD representation of a circuit returned by
// GET/POST/PATCH /api/electrical/circuits-crud/[{id}/] (and the ?breaker= list).
// conductor_length_ft and max_load_amps are nullable; the backend derates
// max_load_amps to breaker.amperage × 0.8 on save when left null.
type PowerCircuitDetail struct {
	ID                int       `json:"id"`
	Breaker           int       `json:"breaker"`
	BreakerLabel      string    `json:"breaker_label"`
	PanelID           int       `json:"panel_id"`
	PanelName         string    `json:"panel_name"`
	Label             string    `json:"label"`
	ConductorSize     string    `json:"conductor_size"`
	ConductorLengthFt *int      `json:"conductor_length_ft"`
	MaxLoadAmps       *int      `json:"max_load_amps"`
	Notes             string    `json:"notes"`
	NeedsReview       bool      `json:"needs_review"`
	OutletCount       int       `json:"outlet_count"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

// PowerPanelWrite is the create/edit payload for a panel. The form owns every
// field, so the full representation is sent each save (create = POST, edit =
// PATCH). Booleans + choice/string fields always serialize (so an edit can
// clear a text field or flip needs_review off); the three nullable fields
// (main_breaker_amperage, install_date, fed_by) are pointers with NO omitempty
// so a nil marshals to JSON null — accepted on create and clearing on edit.
//
// Choice codes (exact, from the serializer): phase_configuration
// single|split|three; breaker_type ""|SQUARE_D_QO|SQUARE_D_HOMELINE|EATON_CH|
// EATON_BR|SIEMENS_QP|GE_Q_LINE|FEDERAL_PACIFIC|PUSHMATIC|DIN_RAIL|OTHER;
// numbering_direction top_down|bottom_up. Constraint: unique(location, name);
// on EDIT the backend also rejects a fed_by circuit that belongs to this panel
// (self-feed) — the form filters those out of the picker to avoid the 400.
type PowerPanelWrite struct {
	Location            int     `json:"location"`
	Name                string  `json:"name"`
	PhaseConfiguration  string  `json:"phase_configuration"`
	Voltage             int     `json:"voltage"`
	MainBreakerAmperage *int    `json:"main_breaker_amperage"`
	BreakerType         string  `json:"breaker_type"`
	NumberingDirection  string  `json:"numbering_direction"`
	Manufacturer        string  `json:"manufacturer"`
	Model               string  `json:"model"`
	InstallDate         *string `json:"install_date"`
	Notes               string  `json:"notes"`
	NeedsReview         bool    `json:"needs_review"`
	FedBy               *int    `json:"fed_by"`
}

// PowerBreakerWrite is the create/edit payload for a breaker. amperage and
// position are required by the serializer. pole_count is an INT choice (1|2|3),
// NOT a string. phase (A|B|C|AB|BC|AC|ABC) is stored verbatim — the server never
// derives it, so the form computes it client-side (computePhase) and sends the
// result. is_critical and critical_category are paired: the backend 400s if
// is_critical=true with an empty category, or a non-empty category with
// is_critical=false — the form enforces the pairing before submit.
// required_loto_devices is intentionally absent (not in the serializer's fields;
// deferred to the outlet/disconnect bead). Constraint: unique(panel, position).
type PowerBreakerWrite struct {
	Panel            int    `json:"panel"`
	Position         string `json:"position"`
	PoleCount        int    `json:"pole_count"`
	Amperage         int    `json:"amperage"`
	Phase            string `json:"phase"`
	Status           string `json:"status"`
	ReviewStatus     string `json:"review_status"`
	ReviewNote       string `json:"review_note"`
	Label            string `json:"label"`
	Notes            string `json:"notes"`
	NeedsReview      bool   `json:"needs_review"`
	IsCritical       bool   `json:"is_critical"`
	CriticalCategory string `json:"critical_category"`
	CriticalNote     string `json:"critical_note"`
}

// PowerCircuitWrite is the create/edit payload for a circuit. breaker is
// required. conductor_length_ft is nullable (pointer, no omitempty → clears on
// edit). max_load_amps is deliberately OMITTED (omitempty + always nil): the
// form never prompts it so the backend auto-derates it to breaker.amperage ×
// 0.8 on create, and an edit leaves the persisted value untouched.
type PowerCircuitWrite struct {
	Breaker           int    `json:"breaker"`
	Label             string `json:"label"`
	ConductorSize     string `json:"conductor_size"`
	ConductorLengthFt *int   `json:"conductor_length_ft"`
	MaxLoadAmps       *int   `json:"max_load_amps,omitempty"`
	Notes             string `json:"notes"`
	NeedsReview       bool   `json:"needs_review"`
}

// --- PowerPanel CRUD ---

// GetPowerPanelRecord fetches the full CRUD panel record for edit-mode
// hydration. Distinct from GetPowerPanelTopology (the read tree) — this returns
// the flat writable field set from /panels-crud/{id}/.
func (c *Client) GetPowerPanelRecord(ctx context.Context, id int) (*PowerPanelDetail, error) {
	var out PowerPanelDetail
	if err := c.Get(ctx, fmt.Sprintf("/api/electrical/panels-crud/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreatePowerPanel(ctx context.Context, body PowerPanelWrite) (*PowerPanelDetail, error) {
	var out PowerPanelDetail
	if err := c.Post(ctx, "/api/electrical/panels-crud/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdatePowerPanel PATCHes an existing panel (partial-update mixin; the form
// sends the full representation so this behaves like a replace).
func (c *Client) UpdatePowerPanel(ctx context.Context, id int, body PowerPanelWrite) (*PowerPanelDetail, error) {
	var out PowerPanelDetail
	if err := c.Patch(ctx, fmt.Sprintf("/api/electrical/panels-crud/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePowerPanel removes a panel. Returns an APIError (409/400) when the panel
// still has breakers (FK-protected) — the caller surfaces that as a clear
// "remove its breakers first" message rather than crashing.
func (c *Client) DeletePowerPanel(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/electrical/panels-crud/%d/", id))
}

// --- PowerBreaker CRUD ---

// ListPowerBreakers returns the breakers on one panel (GET
// /breakers-crud/?panel=<id>), unwrapping the DRF page envelope.
func (c *Client) ListPowerBreakers(ctx context.Context, panelID int) ([]PowerBreakerDetail, error) {
	q := url.Values{}
	q.Set("panel", strconv.Itoa(panelID))
	var all []PowerBreakerDetail
	if err := IterPages[PowerBreakerDetail](ctx, c, "/api/electrical/breakers-crud/", q, func(batch []PowerBreakerDetail) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

func (c *Client) GetPowerBreaker(ctx context.Context, id int) (*PowerBreakerDetail, error) {
	var out PowerBreakerDetail
	if err := c.Get(ctx, fmt.Sprintf("/api/electrical/breakers-crud/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreatePowerBreaker(ctx context.Context, body PowerBreakerWrite) (*PowerBreakerDetail, error) {
	var out PowerBreakerDetail
	if err := c.Post(ctx, "/api/electrical/breakers-crud/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdatePowerBreaker(ctx context.Context, id int, body PowerBreakerWrite) (*PowerBreakerDetail, error) {
	var out PowerBreakerDetail
	if err := c.Patch(ctx, fmt.Sprintf("/api/electrical/breakers-crud/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePowerBreaker removes a breaker. FK-protected when it still has circuits.
func (c *Client) DeletePowerBreaker(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/electrical/breakers-crud/%d/", id))
}

// --- PowerCircuit CRUD ---

// ListPowerCircuits returns the circuits on one breaker (GET
// /circuits-crud/?breaker=<id>).
func (c *Client) ListPowerCircuits(ctx context.Context, breakerID int) ([]PowerCircuitDetail, error) {
	q := url.Values{}
	q.Set("breaker", strconv.Itoa(breakerID))
	var all []PowerCircuitDetail
	if err := IterPages[PowerCircuitDetail](ctx, c, "/api/electrical/circuits-crud/", q, func(batch []PowerCircuitDetail) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// ListAllPowerCircuits pages through every circuit in the building (no breaker
// filter). Used by the panel form's fed_by picker, which chooses the upstream
// circuit feeding a sub-panel from the full set. Circuit counts are bounded per
// install, so the extra pages are cheap (mirrors ListAllAssets).
func (c *Client) ListAllPowerCircuits(ctx context.Context) ([]PowerCircuitDetail, error) {
	var all []PowerCircuitDetail
	if err := IterPages[PowerCircuitDetail](ctx, c, "/api/electrical/circuits-crud/", nil, func(batch []PowerCircuitDetail) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

func (c *Client) GetPowerCircuit(ctx context.Context, id int) (*PowerCircuitDetail, error) {
	var out PowerCircuitDetail
	if err := c.Get(ctx, fmt.Sprintf("/api/electrical/circuits-crud/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreatePowerCircuit(ctx context.Context, body PowerCircuitWrite) (*PowerCircuitDetail, error) {
	var out PowerCircuitDetail
	if err := c.Post(ctx, "/api/electrical/circuits-crud/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdatePowerCircuit(ctx context.Context, id int, body PowerCircuitWrite) (*PowerCircuitDetail, error) {
	var out PowerCircuitDetail
	if err := c.Patch(ctx, fmt.Sprintf("/api/electrical/circuits-crud/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePowerCircuit removes a circuit (its outlets cascade per the model).
func (c *Client) DeletePowerCircuit(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/electrical/circuits-crud/%d/", id))
}
