package tui

import (
	"fmt"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// asset_maintenance_history_fixtures_test.go — the columnar-layer fixtures for
// the asset maintenance history sheet, in every phase and in the states its bar
// changes shape in: staff or not (the write keys), a logged row or a work-order
// row under the cursor (Ctrl-E), and an empty range.

var histFixtureNow = time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)

// histFixtureRows is a history at the lengths OMS really serves: a real vendor
// name, an invoice, notes, both sources, and one row with no cost. The titles
// differ at the FRONT so a clipped grid still tells rows apart.
func histFixtureRows(n int) []omsapi.MaintenanceHistoryEntry {
	out := make([]omsapi.MaintenanceHistoryEntry, 0, n)
	for i := 0; i < n; i++ {
		completed := omsapi.DateOnly{Time: histFixtureNow.AddDate(0, -i*2, 0)}
		r := omsapi.MaintenanceHistoryEntry{
			ID:          fmt.Sprintf("rec-%d", i+1),
			Source:      omsapi.MaintenanceSourceHistorical,
			Title:       fmt.Sprintf("%02d Arbor bearing replacement and trunnion lubrication", i+1),
			Description: "Replaced both arbor bearings; cleaned and greased the trunnions.",
			CompletedOn: completed,
			Cost:        omsapi.DecimalString(fmt.Sprintf("%d.00", 400+i*25)),
			PerformedBy: omsapi.MaintenancePerformedBy{
				Vendor: &omsapi.MaintenanceVendorRef{ID: "v1", Name: "Hill Country Saw Service & Industrial Repair"},
			},
			InvoiceNumber: fmt.Sprintf("HC-%04d", 1100+i),
			Notes:         "Backdated from the 2023 paper maintenance log, page 14",
		}
		switch i % 3 {
		case 1:
			url := "/work-orders/wo-" + fmt.Sprint(i)
			r.Source, r.DetailURL, r.InvoiceNumber = omsapi.MaintenanceSourceWorkOrder, &url, ""
		case 2:
			r.PerformedBy = omsapi.MaintenancePerformedBy{InternalUser: &omsapi.MaintenanceUserRef{ID: 1, Username: "shop.lead"}}
			r.Cost = ""
		}
		out = append(out, r)
	}
	return out
}

func assetMaintenanceHistoryFixtureN(staff bool, n int) *AssetMaintenanceHistoryScreen {
	s := NewAssetMaintenanceHistoryScreen(Deps{InitialStaff: staff}, "a1", assetMeterFixtureAsset)
	s.now = func() time.Time { return histFixtureNow }
	rows := histFixtureRows(n)
	next, _ := s.Update(histLoadedMsg{history: &omsapi.MaintenanceHistory{
		Count: len(rows), TotalCost: "12345.67", Results: rows,
	}, query: s.query, seq: s.loadSeq})
	s = next.(*AssetMaintenanceHistoryScreen)
	s.vendors = []omsapi.Vendor{
		{ID: "v1", Name: "Hill Country Saw Service & Industrial Repair"},
		{ID: "v2", Name: "Austin Laser Tube Replacement"},
	}
	return s
}

func assetMaintenanceHistoryFixture() *AssetMaintenanceHistoryScreen {
	return assetMaintenanceHistoryFixtureN(true, 9)
}

// histFixtureStates is every state of the sheet beyond the loaded, staff list.
func histFixtureStates() map[string]func() Screen {
	return map[string]func() Screen{
		"AssetMaintenanceHistoryScreen/not staff": func() Screen {
			return assetMaintenanceHistoryFixtureN(false, 9)
		},
		// The cursor on an outsourced work order: Ctrl-E comes off.
		"AssetMaintenanceHistoryScreen/work order row": func() Screen {
			s := assetMaintenanceHistoryFixture()
			s.cursor = 1
			return s
		},
		"AssetMaintenanceHistoryScreen/empty range": func() Screen {
			return assetMaintenanceHistoryFixtureN(true, 0)
		},
		"AssetMaintenanceHistoryScreen/filter": func() Screen {
			s := assetMaintenanceHistoryFixture()
			s.openFilter()
			return s
		},
		"AssetMaintenanceHistoryScreen/create": func() Screen {
			s := assetMaintenanceHistoryFixture()
			s.openCreate()
			return s
		},
		"AssetMaintenanceHistoryScreen/create internal": func() Screen {
			s := assetMaintenanceHistoryFixture()
			s.openCreate()
			s.internal = true
			return s
		},
		"AssetMaintenanceHistoryScreen/edit notes": func() Screen {
			s := assetMaintenanceHistoryFixture()
			row, _ := s.addressed()
			s.openEdit(row)
			return s
		},
	}
}
