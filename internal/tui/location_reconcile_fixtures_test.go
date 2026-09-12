package tui

import (
	"fmt"

	"github.com/uid0/scantty/internal/omsapi"
)

// Fixtures for the location count, shared by the drive tests, the key-space
// sweep and the derived pane sweeps in jde_pane_fit_test.go.
//
// EVERY NAME AND SKU HERE IS THE LENGTH OMS REALLY SERVES. A fixture that draws
// "Bolt 1" cannot report a row that overruns the pane, and this project has
// already shipped a whole class of clipping defects behind exactly that — so the
// first row's name is a full-length MRO description, on the FIRST row, where a
// window that draws only one row still meets it.
//
// The unit mix is the other half. A grid of each-counted items alone would let
// every unit-rule assertion pass on the one shape where base units and count
// units are the same number, which is the state the whole rule is invisible in.

// reconGridFixture is a room holding one case-counted item, one each-counted
// item and one open/closed item — the three count modes, which are the three
// shapes the payload's unit fields take.
func reconGridFixture() *omsapi.LocationReconcileGrid {
	return &omsapi.LocationReconcileGrid{
		LocationID:   "7",
		LocationName: "Machine shop mezzanine",
		Items: []omsapi.LocationReconcileItem{
			{
				ItemID: "11111111-1111-1111-1111-111111111111",
				Name:   "Nitrile gloves, powder-free, blue, medium",
				SKU:    "GLV-NIT-BLU-M-100",
				// 14 boxes of 100. The two figures differ by the pack size,
				// which is what makes reading the wrong one a factor of 100.
				Projected: 1400, ProjectedAtUnit: 14,
				MinimumStock: 12, ReorderQuantity: 6,
				OwningGroupName: "Metal shop",
				CountMode:       omsapi.CountModeByLevel,
				CountUnit:       "box",
			},
			{
				ItemID:    "22222222-2222-2222-2222-222222222222",
				Name:      "Hex head cap screw M8x40 zinc-plated",
				SKU:       "BOLT-M8X40-ZP",
				Projected: 250, ProjectedAtUnit: 250,
				MinimumStock: 50, ReorderQuantity: 100,
				CountMode: omsapi.CountModeEach,
				CountUnit: "bolt",
			},
			{
				ItemID:    "33333333-3333-3333-3333-333333333333",
				Name:      "Isopropyl alcohol 99%, 1L bottle",
				SKU:       "IPA-99-1L",
				Projected: 24, ProjectedAtUnit: 4,
				MinimumStock: 2, ReorderQuantity: 4,
				CountMode: omsapi.CountModeOpenClosed,
				CountUnit: "carton",
				// One carton is already open, which is the fact an open/closed
				// item's sealed count alone does not carry.
				OpenContainerCount: 1,
			},
		},
	}
}

// reconLongGridFixture is the same room with enough rows to outrun any pane, so
// the paging and windowing claims are made against a body that really scrolls.
func reconLongGridFixture() *omsapi.LocationReconcileGrid {
	grid := reconGridFixture()
	for i := 0; i < 20; i++ {
		grid.Items = append(grid.Items, omsapi.LocationReconcileItem{
			ItemID:    fmt.Sprintf("aaaaaaaa-0000-0000-0000-%012d", i),
			Name:      fmt.Sprintf("Shoulder bolt %d, hardened, socket head", i),
			SKU:       fmt.Sprintf("SHB-%04d-HSK", i),
			Projected: 40 + i, ProjectedAtUnit: 40 + i,
			MinimumStock: 10, ReorderQuantity: 20,
			CountMode: omsapi.CountModeEach,
			CountUnit: "bolt",
		})
	}
	return grid
}

// reconFixture is the count form as an operator reaches it: the grid landed, the
// cursor on the scan row.
func reconFixture(grid *omsapi.LocationReconcileGrid) *LocationReconcileScreen {
	s := NewLocationReconcileScreen(Deps{}, "7", "Machine shop mezzanine")
	if grid == nil {
		grid = reconGridFixture()
	}
	s.Update(reconGridMsg{grid: grid})
	return s
}

// reconCountedFixture is the count form with numbers in the boxes: the case row
// counted BELOW its minimum (so it forecasts a reorder), the each row counted
// above its own, and the open/closed row left alone.
func reconCountedFixture() *LocationReconcileScreen {
	s := reconFixture(nil)
	s.counts[0].SetValue("9")
	s.counts[1].SetValue("240")
	return s
}

// reconFailedSubmitFixture is the state a room count must survive: the write
// went out, the server refused it, and every typed count is still in its box.
//
// The detail is an OMS refusal in the hand-written {"detail": …} shape these
// endpoints really use, so the header is folding the sentence an operator acts
// on rather than a string a test invented.
func reconFailedSubmitFixture() *LocationReconcileScreen {
	s := reconCountedFixture()
	s.openReview(0)
	s.Update(reconSubmittedMsg{err: &omsapi.APIError{
		Status: 403,
		Message: `{"detail": "You do not have permission to reconcile item ` +
			`Nitrile gloves, powder-free, blue, medium."}`,
	}})
	return s
}

// reconDoneFixture is the summary of a landed batch that filed a reorder — the
// state the done frame draws its most content in.
func reconDoneFixture() *LocationReconcileScreen {
	s := reconCountedFixture()
	s.openReview(0)
	reorder := 4
	s.Update(reconSubmittedMsg{result: &omsapi.ReconciliationBatchResult{
		Reconciled: 2, ReordersCreated: 1,
		Reconciliations: []omsapi.StockReconciliation{{
			ItemName: "Nitrile gloves, powder-free, blue, medium",
			ItemSKU:  "GLV-NIT-BLU-M-100", Delta: -500,
			TriggeredReorderID: &reorder,
		}},
	}})
	return s
}
