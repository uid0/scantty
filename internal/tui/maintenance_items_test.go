package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestMaintenanceItemsScreen_RenderSmoke covers the loaded / empty / error
// render paths and the overdue badge.
func TestMaintenanceItemsScreen_RenderSmoke(t *testing.T) {
	iv := 30
	d := 4
	s := NewMaintenanceItemsScreen(Deps{})
	s.loading = false
	s.terminalHeight = 30
	s.items = []omsapi.MaintenanceItem{
		{ID: "1", Title: "Filter change", AssetName: "Lathe", IntervalDays: &iv, IsActive: true, IsOverdue: true, DaysOverdue: &d},
		{ID: "2", Title: "Lube spindle", AssetName: "Mill", IsActive: false},
	}
	s.windowSize = s.computeWindowSize()

	out := s.View()
	if !strings.Contains(out, "Filter change") || !strings.Contains(out, "OVERDUE") {
		t.Errorf("list view missing item/overdue: %q", out)
	}
	if !strings.Contains(out, "inactive") {
		t.Errorf("inactive item should be flagged: %q", out)
	}

	// Empty + error variants must not panic.
	s.items = nil
	if out := s.View(); !strings.Contains(out, "No PM items") {
		t.Errorf("empty view = %q", out)
	}
	s.loadErr = "boom"
	if out := s.View(); !strings.Contains(out, "boom") {
		t.Errorf("error view = %q", out)
	}
}

// TestMaintenanceItemsScreen_CreateKey confirms `c` routes to the create form
// (in the Maintenance workspace).
func TestMaintenanceItemsScreen_CreateKey(t *testing.T) {
	s := NewMaintenanceItemsScreen(Deps{})
	s.loading = false
	_, cmd := s.Update(mtKey("c"))
	if cmd == nil {
		t.Fatalf("c should return a switch command")
	}
	msg := cmd()
	sw, ok := msg.(SwitchScreenMsg)
	if !ok {
		t.Fatalf("expected SwitchScreenMsg, got %T", msg)
	}
	if sw.Workspace != WSMaintenance {
		t.Errorf("workspace = %v", sw.Workspace)
	}
	if _, ok := sw.Screen.(*MaintenanceItemFormScreen); !ok {
		t.Errorf("expected form screen, got %T", sw.Screen)
	}
}

// TestMaintenanceItemsScreen_EnterOpensDetail confirms enter routes to the
// detail screen for the selected row.
func TestMaintenanceItemsScreen_EnterOpensDetail(t *testing.T) {
	s := NewMaintenanceItemsScreen(Deps{})
	s.loading = false
	s.items = []omsapi.MaintenanceItem{{ID: "mi-9", Title: "X"}}
	s.cursor = 0
	_, cmd := s.Update(mtKey("enter"))
	if cmd == nil {
		t.Fatalf("enter should return a command")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("expected SwitchScreenMsg, got %T", cmd())
	}
	if _, ok := sw.Screen.(*MaintenanceItemDetailScreen); !ok {
		t.Errorf("expected detail screen, got %T", sw.Screen)
	}
}
