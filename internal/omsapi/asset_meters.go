// Asset usage meters and their append-only reading ledger.
//
// An OMS AssetMeter is a named cumulative counter on an asset — spindle runtime
// hours, gallons through a coolant line, cycles on a press, kWh on a kiln. It is
// the machine-side half of the EAM story the web drives from
// AssetMetersSection.tsx, and it is what an operator standing AT the machine
// with a number on a display needs a terminal route to.
//
// THE TWO WRITES ARE DIFFERENT OPERATIONS AND THE RECORD KEEPS THEM APART.
// `record-reading` says what the machine reads NOW; `adjust` corrects the
// history and REQUIRES a reason, which the server stores in the reading's
// `notes`. They land in the same append-only ledger with different `source`
// values (`manual` vs `manual_adjust`), so the distinction survives in the
// record long after the operator has gone home — which is why nothing on this
// side collapses them into one call with a flag.
//
// WHAT THE SERVER DOES NOT REFUSE, measured against a real backend rather than
// inferred (2026-09-11, remote `main` tree 2d9c8f9c):
//
//   - A reading that goes BACKWARDS. `apply_reading` computes
//     `delta = value_after - current_value` and stores whatever arrives, so an
//     absolute 120 against a meter reading 1250.5 is accepted with 201 and a
//     delta of -1130.5.
//   - A reading off by an ORDER OF MAGNITUDE. 12505000 against the same meter
//     is a 201.
//   - A NEGATIVE cumulative total. -5 is a 201.
//
// AND THE DAMAGE IS ONE-WAY. For a `runtime_hours` meter `apply_reading` adds a
// positive advance to `Asset.hours_used` through an `F()` update and
// deliberately does NOT decrement on a downward correction ("it is a monotonic
// cumulative counter"), and `Asset.hours_used` is what the existing maintenance
// forecast reads. Measured: 1250.5 → 120 → 12505000 → -5 left `hours_used` at
// 12506130, and no call in this file can bring it back. So a magnitude typo on a
// runtime meter permanently inflates the forecast input, the `adjust` endpoint
// notwithstanding — which is why internal/tui/asset_meters.go confirms a
// backwards or order-of-magnitude entry on the terminal side rather than
// trusting the 201.
//
// The refusals it DOES make are DRF `{"detail": ...}` bodies written by hand in
// the view (`value must be a number`, `target must be a number`,
// `reason is required for an adjustment`), so they never reach OMS's exception
// handler and `parseError` hands the whole raw JSON over. `AsDetailRefusal`
// recovers the sentence — the same recogniser the reorder lifecycle uses.
//
// EVERY ID HERE IS A UUID AND THEREFORE A STRING ON THE WIRE. `AssetMeter`,
// `AssetMeterReading`, `Asset` and `AssetDocument` all declare
// `id = models.UUIDField(primary_key=True, ...)` read from the whole model
// class, and every one of these payloads is built by a plain `ModelSerializer`
// rather than a hand-rolled dict — so there is no untyped landing spot for a
// number and nothing here is spent through `anyIDString`. The recorded fixtures
// in testdata/ are what pin that, and they are recorded rather than written so
// they can contradict these structs.
package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// AssetMeter mirrors OMS's AssetMeterSerializer.
//
// CurrentValue / CurrentIsEstimated / RollupWatermarkAt are server-controlled:
// they move only when a reading is applied, so nothing on this side writes them.
// They are DecimalString because DRF serializes a DecimalField as a JSON string
// ("1289.7500", four places — the column is numeric(14,4)), and DecimalString
// keeps the server's own digits verbatim rather than routing a meter reading
// through a float.
//
// UNIT IS PART OF THE VALUE. A runtime-hour reading and a cycle count are not
// interchangeable and an unlabelled number invites the wrong entry, so every
// surface that shows or takes one of these values shows Unit beside it. Unit is
// free text on the server (`blank=True`), so it can be empty; MeterTypeDisplay
// is the server's own label for the TYPE and is what a screen falls back to.
type AssetMeter struct {
	ID                 string        `json:"id"`
	Asset              string        `json:"asset"`
	Name               string        `json:"name"`
	MeterType          string        `json:"meter_type"`
	MeterTypeDisplay   string        `json:"meter_type_display,omitempty"`
	Unit               string        `json:"unit"`
	Source             string        `json:"source"`
	SourceDisplay      string        `json:"source_display,omitempty"`
	CurrentValue       DecimalString `json:"current_value"`
	CurrentIsEstimated bool          `json:"current_is_estimated"`
	RollupWatermarkAt  *time.Time    `json:"rollup_watermark_at"`
	IsActive           bool          `json:"is_active"`
	CreatedAt          time.Time     `json:"created_at,omitempty"`
	UpdatedAt          time.Time     `json:"updated_at,omitempty"`
}

// AssetMeter.MeterType values, from OMS's AssetMeter.MeterType choices.
const (
	MeterTypeRuntimeHours  = "runtime_hours"
	MeterTypeVolumeGallons = "volume_gallons"
	MeterTypeCycles        = "cycles"
	MeterTypeKWh           = "kwh"
	MeterTypeGenericCount  = "generic_count"
)

// AssetMeter.Source values. A meter whose source is one of the AUTO ones is
// advanced by the server's own rollup; a manual reading against one is still
// accepted, which is why nothing here refuses it — the screen says whose number
// it is instead.
const (
	MeterSourceAutoSession   = "auto_session"
	MeterSourceAutoTelemetry = "auto_telemetry"
	MeterSourceManual        = "manual"
)

// AssetMeterReading.Source adds a fourth value the meter itself cannot have:
// a manual CORRECTION, which is how `adjust` is told apart from `record-reading`
// once both are rows in the same ledger.
const MeterReadingSourceManualAdjust = "manual_adjust"

// AssetMeterReading is one immutable row of a meter's ledger.
//
// Delta is the SIGNED change and ValueAfter the meter total straight after this
// row, so the history reconstructs without recomputation. Both are decimals and
// both are carried verbatim.
//
// RecordedBy is a *int because it is NULL for an automatic rollup — nobody
// recorded it — and that is a different fact from a reading recorded by a user
// whose name the serializer could not resolve. IsEstimated is the server's
// measured-vs-eyeballed flag and rides on the ROW, so a meter's current value
// being an estimate is a fact about the newest reading.
type AssetMeterReading struct {
	ID             string        `json:"id"`
	Meter          string        `json:"meter"`
	Source         string        `json:"source"`
	SourceDisplay  string        `json:"source_display,omitempty"`
	Delta          DecimalString `json:"delta"`
	ValueAfter     DecimalString `json:"value_after"`
	IsEstimated    bool          `json:"is_estimated"`
	ObservedAt     time.Time     `json:"observed_at"`
	RecordedAt     time.Time     `json:"recorded_at"`
	RecordedBy     *int          `json:"recorded_by"`
	RecordedByName string        `json:"recorded_by_name,omitempty"`
	SourceRef      string        `json:"source_ref,omitempty"`
	Notes          string        `json:"notes,omitempty"`
}

// MeterReadingResult is the envelope BOTH write actions return: the meter as it
// now stands and the ledger row that moved it. It is a 201 on both.
//
// The reply carrying the meter is what lets a screen show the new total without
// a second round trip — and, more to the point, lets it show what the SERVER
// stored rather than what the client thinks it asked for.
// A failed later list refresh leaves that displayed total stale; the TUI carries
// that uncertainty into its sanity verdict and confirms every such write while
// saying that the comparison could not be checked against the server.
type MeterReadingResult struct {
	Meter   AssetMeter        `json:"meter"`
	Reading AssetMeterReading `json:"reading"`
}

// CreateAssetMeterRequest is the body for POST /api/inventory/asset-meters/.
//
// Source is sent explicitly rather than left to the server's default so a meter
// created from the terminal is unambiguously a manual one: the terminal has no
// way to attach a rollup source, and a meter silently created as something else
// would advance from two directions at once.
type CreateAssetMeterRequest struct {
	Asset     string `json:"asset"`
	Name      string `json:"name"`
	MeterType string `json:"meter_type"`
	Unit      string `json:"unit"`
	Source    string `json:"source"`
}

// RecordMeterReadingRequest is the body for the record-reading action.
//
// IsAbsolute is NOT omitempty and is not a pointer: the server defaults it to
// TRUE, and a `false` dropped from the payload would turn "add 8.25 hours" into
// "the meter now reads 8.25" — the one confusion on this endpoint that silently
// destroys a number rather than refusing. Sending it always is what makes the
// basis the client's statement rather than a default it inherited.
//
// ObservedAt is optional (the server stamps `timezone.now()` when it is blank),
// so it is omitempty; Value rides as a string so the operator's own digits reach
// the server unrounded.
type RecordMeterReadingRequest struct {
	Value       string `json:"value"`
	IsAbsolute  bool   `json:"is_absolute"`
	IsEstimated bool   `json:"is_estimated"`
	ObservedAt  string `json:"observed_at,omitempty"`
}

// AdjustMeterRequest is the body for the adjust action. Reason is REQUIRED by
// the server (a blank one is a 400) and is not omitempty for that reason: an
// omitted key and an empty one are the same refusal here, and sending it always
// means the refusal the operator reads is the server's own sentence rather than
// a decode surprise.
type AdjustMeterRequest struct {
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// ListAssetMeters returns every meter on one asset, walking `next` to the end.
//
// It pages because the list is CLIENT-side complete or it is wrong: the grid
// numbers its rows and offers a cursor over all of them, so a second page left
// on the server would be a meter an operator cannot reach and cannot see is
// missing. An asset has a handful of meters, so the walk is cheap.
//
// The endpoint is staff/SIG-admin gated (`IsStaffOrSigAdmin`) for LIST as well
// as for the writes, which is why a screen that cannot read it must say "could
// not tell" rather than "this asset has no meters".
func (c *Client) ListAssetMeters(ctx context.Context, assetID string) ([]AssetMeter, error) {
	q := url.Values{"asset": {assetID}}
	var all []AssetMeter
	if err := IterPages[AssetMeter](ctx, c, "/api/inventory/asset-meters/", q, func(batch []AssetMeter) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// CreateAssetMeter defines a new meter on an asset.
func (c *Client) CreateAssetMeter(ctx context.Context, req CreateAssetMeterRequest) (*AssetMeter, error) {
	var out AssetMeter
	if err := c.Post(ctx, "/api/inventory/asset-meters/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RecordMeterReading enters what the machine reads NOW —
// POST /api/inventory/asset-meters/{id}/record-reading/.
//
// Absolute ("the counter reads N") and delta ("add N") collapse into the same
// ledger row server-side; which one was meant is IsAbsolute's whole job.
func (c *Client) RecordMeterReading(
	ctx context.Context, meterID string, req RecordMeterReadingRequest,
) (*MeterReadingResult, error) {
	var out MeterReadingResult
	path := fmt.Sprintf("/api/inventory/asset-meters/%s/record-reading/", meterID)
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdjustMeter CORRECTS the history —
// POST /api/inventory/asset-meters/{id}/adjust/.
//
// It is a different operation from RecordMeterReading and not a convenience
// wrapper over it: the row it writes carries source `manual_adjust` and the
// operator's reason in `notes`, so an audit can tell a measurement from a
// correction. The earlier reading is NOT edited — the ledger is append-only, so
// the mistake and its correction both stay on the record.
func (c *Client) AdjustMeter(
	ctx context.Context, meterID string, req AdjustMeterRequest,
) (*MeterReadingResult, error) {
	var out MeterReadingResult
	path := fmt.Sprintf("/api/inventory/asset-meters/%s/adjust/", meterID)
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListMeterReadings returns one meter's ledger, newest first (the model's own
// `ordering = ["-recorded_at"]`), walking `next` to the end.
//
// The endpoint is read-only and merely `IsAuthenticated`, so a viewer who cannot
// see the meter LIST can still read a ledger they have an id for.
func (c *Client) ListMeterReadings(ctx context.Context, meterID string) ([]AssetMeterReading, error) {
	q := url.Values{"meter": {meterID}}
	var all []AssetMeterReading
	if err := IterPages[AssetMeterReading](ctx, c, "/api/inventory/asset-meter-readings/", q,
		func(batch []AssetMeterReading) error {
			all = append(all, batch...)
			return nil
		}); err != nil {
		return nil, err
	}
	return all, nil
}
