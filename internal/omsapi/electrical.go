package omsapi

import (
	"context"
	"fmt"
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
