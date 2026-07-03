package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

func sampleItem() *omsapi.MaintenanceItem {
	iv := 30
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	return &omsapi.MaintenanceItem{
		ID: "mi-1", Asset: "a1", AssetName: "Lathe", AssetTag: "LT-1",
		Title: "Filter change", Description: "airflow", Instructions: "swap it",
		IntervalDays: &iv, EstimatedCost: "5.00", IsActive: true,
		LastCompletedAt: &now,
		Tasks: []omsapi.MaintenanceTask{
			{ID: "t1", Order: 0, Title: "Remove cover", IsRequired: true},
			{ID: "t2", Order: 1, Title: "Swap filter", IsRequired: false},
		},
		Materials: []omsapi.MaintenanceMaterial{
			{ID: "m1", Name: "Filter", Quantity: "1", Unit: "ea", EstimatedCostPerUnit: "5.00"},
		},
	}
}

// TestMaintenanceItemDetail_RenderBody confirms the body shows the item, its
// task steps, and materials.
func TestMaintenanceItemDetail_RenderBody(t *testing.T) {
	s := NewMaintenanceItemDetailScreen(Deps{}, "mi-1")
	s.loading = false
	s.terminalHeight = 40
	s.item = sampleItem()
	s.scroller.Set(s.renderBody())

	out := s.View()
	for _, want := range []string{"Filter change", "Task steps", "Remove cover", "Materials", "Filter"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail view missing %q:\n%s", want, out)
		}
	}
	// Action hints advertise every per-item action.
	for _, want := range []string{"complete", "gen-WO", "clone", "check-stock", "edit", "delete"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail footer missing action %q", want)
		}
	}
}

// TestMaintenanceItemDetail_RawInputGating: the screen only claims raw input
// while a modal is open.
func TestMaintenanceItemDetail_RawInputGating(t *testing.T) {
	s := NewMaintenanceItemDetailScreen(Deps{}, "mi-1")
	s.loading = false
	s.item = sampleItem()
	if s.WantsRawInput() {
		t.Errorf("view phase should not claim raw input")
	}
	s.phase = mDetailPhaseComplete
	if !s.WantsRawInput() {
		t.Errorf("modal phase should claim raw input")
	}
}

// TestMaintenanceItemDetail_DeleteConfirmFlow: x opens the confirm, n cancels.
func TestMaintenanceItemDetail_DeleteConfirmFlow(t *testing.T) {
	s := NewMaintenanceItemDetailScreen(Deps{}, "mi-1")
	s.loading = false
	s.terminalHeight = 40
	s.item = sampleItem()
	s.scroller.Set(s.renderBody())

	s.Update(mtKey("x"))
	if s.phase != mDetailPhaseConfirmDelete {
		t.Fatalf("x should open delete confirm, phase=%d", s.phase)
	}
	if out := s.View(); !strings.Contains(out, "Delete") {
		t.Errorf("confirm view missing prompt: %q", out)
	}
	s.Update(mtKey("n"))
	if s.phase != mDetailPhaseView {
		t.Errorf("n should cancel back to view, phase=%d", s.phase)
	}
}

// TestMaintenanceItemDetail_ModalOpens: c/w/L open their modals and render.
func TestMaintenanceItemDetail_ModalOpens(t *testing.T) {
	s := NewMaintenanceItemDetailScreen(Deps{}, "mi-1")
	s.loading = false
	s.terminalHeight = 40
	s.item = sampleItem()
	s.scroller.Set(s.renderBody())

	s.Update(mtKey("c"))
	if s.phase != mDetailPhaseComplete {
		t.Fatalf("c should open complete modal")
	}
	if out := s.View(); !strings.Contains(out, "Mark complete") {
		t.Errorf("complete view = %q", out)
	}

	s.phase = mDetailPhaseView
	s.Update(mtKey("w"))
	if s.phase != mDetailPhaseGenerate {
		t.Fatalf("w should open generate modal")
	}
	if out := s.View(); !strings.Contains(out, "Generate work order") {
		t.Errorf("generate view = %q", out)
	}

	// Clone view with assets pre-loaded.
	s.phase = mDetailPhaseClone
	s.assetsLoaded = true
	s.assets = []omsapi.Asset{{ID: "a2", Name: "Mill", AssetTag: "ML-1"}}
	s.applyCloneFilter()
	if out := s.View(); !strings.Contains(out, "Clone to asset") || !strings.Contains(out, "Mill") {
		t.Errorf("clone view = %q", out)
	}
}

// TestStockSummary pins the check-material-stock summariser + level.
func TestStockSummary(t *testing.T) {
	if got := stockSummary(nil); !strings.Contains(got, "OK") {
		t.Errorf("nil summary = %q", got)
	}
	if lvl := stockLevel(nil); lvl != StatusOK {
		t.Errorf("nil level = %v", lvl)
	}
	resp := &omsapi.MaterialStockResponse{LowStockAlerts: []omsapi.MaterialStockAlert{
		{Name: "Oil", Current: 1, Minimum: 5},
	}}
	if got := stockSummary(resp); !strings.Contains(got, "Oil 1/5") {
		t.Errorf("alert summary = %q", got)
	}
	if lvl := stockLevel(resp); lvl != StatusWarn {
		t.Errorf("alert level = %v", lvl)
	}
}
