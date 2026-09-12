package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// The shared fixtures for the asset meter and document screens.
//
// THEY CARRY THE LENGTHS AND THE STATES THE REAL SERVER SERVES, which is half of
// what makes a check about a bound mean anything. Every picker fixture in this
// package once drew `Widget 1` / `Bolt 1`, so no test had ever rendered a row at
// the width OMS really carries, and a whole class of clipping defects survived
// underneath. The names here are a real machine's and a real manual's; the
// readings are four-place decimals as `numeric(14,4)` serves them; and the
// values differ AT THE FRONT, because a fixture whose rows differ only past the
// clip cannot report a cursor that is moving perfectly.
//
// The states are the ones the grids have to keep apart: a value that is an
// ESTIMATE, a meter that is INACTIVE, one driven by the AUTO rollup, a ledger
// holding a CORRECTION with a negative delta, and a document library holding a
// superseded v1 beside its current v2.

const assetMeterFixtureAsset = "Haas VF-2 Vertical Machining Center"

func assetMeterFixtureRows() []omsapi.AssetMeter {
	return []omsapi.AssetMeter{
		{
			ID: "bceb18f6-288e-4789-b596-a98493833bb6", Asset: "a1",
			Name: "Spindle runtime", MeterType: omsapi.MeterTypeRuntimeHours,
			MeterTypeDisplay: "Runtime hours", Unit: "hours",
			Source: omsapi.MeterSourceManual, SourceDisplay: "Manual entry",
			CurrentValue: "1289.7500", IsActive: true,
		},
		{
			ID: "34ca60e8-3e98-4209-9d77-8ce555d3c858", Asset: "a1",
			Name: "Coolant dispensed", MeterType: omsapi.MeterTypeVolumeGallons,
			MeterTypeDisplay: "Volume (gallons)", Unit: "gallons",
			Source: omsapi.MeterSourceManual, SourceDisplay: "Manual entry",
			CurrentValue: "83.5000", CurrentIsEstimated: true, IsActive: true,
		},
		{
			ID: "f334a008-57fe-4dfb-8996-854cff10db05", Asset: "a1",
			Name: "Chiller compressor runtime", MeterType: omsapi.MeterTypeRuntimeHours,
			MeterTypeDisplay: "Runtime hours", Unit: "hours",
			Source: omsapi.MeterSourceAutoSession, SourceDisplay: "Auto — usage sessions",
			CurrentValue: "6.0833", IsActive: true,
		},
		{
			ID: "6689e41f-199f-4bee-b1ff-a9ff02da2007", Asset: "a1",
			Name: "Old tool-change counter", MeterType: omsapi.MeterTypeCycles,
			MeterTypeDisplay: "Cycles", Unit: "cycles",
			Source: omsapi.MeterSourceManual, SourceDisplay: "Manual entry",
			CurrentValue: "44120.0000", IsActive: false,
		},
	}
}

func assetMetersFixture() *AssetMetersScreen {
	s := NewAssetMetersScreen(Deps{}, "a1", assetMeterFixtureAsset)
	s.loading = false
	s.meters = assetMeterFixtureRows()
	return s
}

func assetMeterReadingFixtureRows() []omsapi.AssetMeterReading {
	base := time.Date(2026, 9, 11, 14, 5, 0, 0, time.UTC)
	who := 1
	return []omsapi.AssetMeterReading{
		{
			ID: "006e1cb4-f4e2-4f57-9d16-f312f2f43feb", Meter: "m1",
			Source: omsapi.MeterReadingSourceManualAdjust, SourceDisplay: "Manual correction",
			Delta: "-9.0000", ValueAfter: "1289.7500", ObservedAt: base,
			RecordedAt: base, RecordedBy: &who, RecordedByName: "shop.lead",
			SourceRef: "manual adjustment",
			Notes:     "recount against the control's own hour meter",
		},
		{
			ID: "97e807a4-e5ae-428a-b8e3-fd6fe8f9298e", Meter: "m1",
			Source: omsapi.MeterSourceManual, SourceDisplay: "Manual entry",
			Delta: "48.2500", ValueAfter: "1298.7500", ObservedAt: base.AddDate(0, 0, -6),
			RecordedAt: base.AddDate(0, 0, -6), RecordedBy: &who, RecordedByName: "shop.lead",
			SourceRef: "manual entry",
		},
		{
			ID: "b1d0c3aa-7f2e-4c31-9a5b-2d7e08c41f66", Meter: "m1",
			Source: omsapi.MeterSourceAutoSession, SourceDisplay: "Auto — usage sessions",
			Delta: "6.0833", ValueAfter: "1250.5000", ObservedAt: base.AddDate(0, 0, -20),
			RecordedAt: base.AddDate(0, 0, -20),
			// No recorder at all: an automatic rollup, whose only provenance is
			// its source_ref. The row has to stay attributed.
			SourceRef: "device_usage x12 ≤2026-08-22T09:00:00Z",
		},
		{
			ID: "4949e350-4be4-4a69-a8cd-5354f7b29099", Meter: "m1",
			Source: omsapi.MeterSourceManual, SourceDisplay: "Manual entry",
			Delta: "1244.4167", ValueAfter: "1244.4167", ObservedAt: base.AddDate(0, 0, -29),
			RecordedAt: base.AddDate(0, 0, -29), RecordedBy: &who, RecordedByName: "shop.lead",
			IsEstimated: true, SourceRef: "manual entry",
		},
	}
}

func assetMeterReadingsFixture() *AssetMeterReadingsScreen {
	s := NewAssetMeterReadingsScreen(Deps{}, "a1", assetMeterFixtureAsset,
		assetMeterFixtureRows()[0])
	s.loading = false
	s.readings = assetMeterReadingFixtureRows()
	return s
}

func assetDocumentFixtureRows() []omsapi.AssetDocument {
	base := time.Date(2026, 9, 11, 14, 5, 0, 0, time.UTC)
	first := "77b65ced-9aaa-4883-9ca3-d382093f4f6b"
	who := 1
	return []omsapi.AssetDocument{
		{
			ID: "6b1a3f2c-0c44-4d18-9a1e-77f0d2a9b311", Asset: "a1",
			Title:    "Soft-jaw fixture plate — 6in vise, 0.75in stock",
			Category: "cut_ready_template", CategoryDisplay: "Cut-Ready Template",
			Version: 1, IsCurrent: true, UploadedBy: &who, UploadedByName: "shop.lead",
			UploadedAt: base,
		},
		{
			ID: "c97ef2d5-5978-4739-8452-c955f8ad9f6b", Asset: "a1",
			Title: "VF-2 Operator Manual", Category: "manual",
			CategoryDisplay: "Manual / Documentation",
			Description:     "Rev B, corrected the spindle warm-up table",
			Version:         2, IsCurrent: true, Supersedes: &first,
			SupersedesTitle: "VF-2 Operator Manual (v1)",
			UploadedBy:      &who, UploadedByName: "shop.lead", UploadedAt: base.AddDate(0, 0, -2),
		},
		{
			// The superseded one, LAST so the delete-confirm fixture reaches the
			// branch that warns about breaking the chain by pointing at it.
			ID: first, Asset: "a1",
			Title: "VF-2 Operator Manual", Category: "manual",
			CategoryDisplay: "Manual / Documentation",
			Description:     "Scanned from the binder at the machine",
			Version:         1, IsCurrent: false,
			UploadedBy: &who, UploadedByName: "shop.lead", UploadedAt: base.AddDate(0, 0, -40),
		},
	}
}

func assetDocumentsFixture() *AssetDocumentsScreen {
	s := NewAssetDocumentsScreen(Deps{}, "a1", assetMeterFixtureAsset)
	s.loading = false
	s.docs = assetDocumentFixtureRows()
	return s
}

// assetFlatPane is the CLIPPED pane with its whitespace collapsed.
//
// Flattened because every caveat on these frames FOLDS: a sentence crossing a
// fold is absent from the raw pane and plainly present on screen, so a raw
// substring check reports a warning as missing when it is drawn. It is the same
// instrument poRemoveFlatPane is, and it carries the same limit — a collapsed
// pane cannot see a headline lose its second row, so a claim about ROWS is
// asserted against the layer's own fold rather than against this.
func assetFlatPane(s Screen, width, height int) string {
	pane := clampToBox(s.View(), screenBodyWidth(width), screenBodyRows(height))
	return strings.Join(strings.Fields(stripANSI(pane)), " ")
}

// ---------------------------------------------------------------------------
// The asset INTERLOCK screen
// ---------------------------------------------------------------------------

// assetInterlockFixtureAsset carries a lockout at the lengths OMS really serves:
// a real machine's name, a real username, a maintainer-level lockout, and a
// REASON long enough to fold — which is the length that matters, because the
// reason is the sentence the unlock confirm is built out of and the one value on
// these frames that an operator supplied rather than a schema.
func assetInterlockFixtureAsset(locked, active bool) *omsapi.Asset {
	a := &omsapi.Asset{
		ID:           "233eeb12-775f-44a3-9a6c-8cd28e35acd7",
		Name:         assetMeterFixtureAsset,
		AssetTag:     "DMS-26A0011E",
		LocationName: "Machine Shop — Bay 3",
		IsLocked:     locked,
		IsActive:     active,
		CanUnlock:    true,
		CanEnable:    true,
	}
	if locked {
		a.LockoutInfo = &omsapi.AssetLockout{
			LockedBy:     "shop.lead",
			LockedAt:     "2026-09-12T07:21:29.488098+00:00",
			LockoutLevel: "maintainer",
			Reason: "spindle bearing seized — do not run until the bearing has been " +
				"replaced and the head re-trammed",
		}
		a.OperationalMode = map[string]any{"mode": "locked_out"}
	} else {
		a.OperationalMode = map[string]any{"mode": "available"}
	}
	return a
}

// assetInterlockFixture is the state frame past its load, which is the frame the
// operator arrives on. locked/active pick which of the four states the two axes
// make, because the BAR changes shape in every one of them and a fixture in one
// state proves nothing about the others.
func assetInterlockFixture(locked, active bool) *AssetInterlockScreen {
	s := NewAssetInterlockScreen(Deps{}, "233eeb12-775f-44a3-9a6c-8cd28e35acd7",
		assetMeterFixtureAsset)
	s.loading = false
	s.asset = assetInterlockFixtureAsset(locked, active)
	return s
}

// assetInterlockConfirmFixture opens the confirm for one action, through the same
// key path an operator takes — openAction rather than by setting the phase — so a
// fixture cannot reach a state the keys cannot.
func assetInterlockConfirmFixture(a interlockAction, locked, active bool) *AssetInterlockScreen {
	s := assetInterlockFixture(locked, active)
	s.openAction(a, interlockFixtureKey(a))
	if a == interlockLock {
		// Lock lands on the reason form first; fill it and step on, because the
		// confirm repeats the sentence back and an empty one would take Ctrl-X off
		// the bar.
		s.reason.SetValue("spindle bearing seized — do not run until the bearing has " +
			"been replaced and the head re-trammed")
		s.phase = interlockPhaseConfirm
	}
	return s
}

func interlockFixtureKey(a interlockAction) string {
	switch a {
	case interlockLock:
		return "l"
	case interlockUnlock:
		return "u"
	case interlockDisable:
		return "d"
	case interlockEnable:
		return "e"
	}
	return "?"
}

// ---------------------------------------------------------------------------
// The two read-only GRID BANDS that ride on an edit form
// ---------------------------------------------------------------------------

// itemFormSupplierBandFixture is the inventory item form in EDIT mode with
// supplier links on it, which is the only state that draws the supplier band at
// all (supplierRows returns nil in create mode).
//
// The FACT cells are at the lengths OMS really serves, and that is deliberate:
// itemSupplierCostW is nine cells and a six-figure pack cost is ten, so this is
// the fixture that reaches the bound. Every earlier fixture for this screen was
// the bare create form, so the band was drawn by nothing and the width sweeps
// reported the screen as fitting every pane without having rendered one row of
// it.
func itemFormSupplierBandFixture() *InventoryItemFormScreen {
	s := NewInventoryItemFormScreen(Deps{}, "i1")
	s.loading = false
	s.edit = true
	s.item = &omsapi.Item{
		ID:   "1",
		Name: "Hex bolt M8x40 zinc plated grade 8.8",
		SKU:  "HB-M8X40-ZP-88",
	}
	for i := 0; i < 4; i++ {
		s.item.Suppliers = append(s.item.Suppliers, omsapi.ItemSupplier{
			ID:           i + 1,
			SupplierName: "Northern Tool & Die Supply Co",
			SupplierSKU:  fmt.Sprintf("NT-884422-%04d", i),
			UnitCost:     omsapi.DecimalString("123456.7800"),
			PackageCost:  omsapi.DecimalString("987654.3200"),
			LeadTimeDays: 10.25,
			IsPreferred:  i == 0,
		})
	}
	return s
}

// assetFormSupplyBandFixture is the asset form in EDIT mode with parts on it —
// the only state that draws the supply band — carrying a QuantityNeeded that
// outruns the three cells assetSupplyQtyW budgets for it.
func assetFormSupplyBandFixture() *AssetFormScreen {
	s := NewAssetFormScreen(Deps{}, "a1")
	s.loading = false
	s.edit = true
	s.asset = &omsapi.Asset{
		ID:       1,
		Name:     "Bridgeport Series I vertical mill",
		AssetTag: "DMS-7F3A9C21",
	}
	for i := 0; i < 4; i++ {
		s.asset.Parts = append(s.asset.Parts, omsapi.AssetPart{
			ID:             i + 1,
			PartName:       "Way oil, Mobil Vactra No. 2, 1 gallon",
			PartSKU:        fmt.Sprintf("WAYOIL-VACTRA2-%04d", i),
			QuantityNeeded: 12345,
			IsRequired:     i%2 == 0,
		})
	}
	return s
}
