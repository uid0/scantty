package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func partsRuneKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestAssetPartsScreen_HandlesKey(t *testing.T) {
	s := NewAssetPartsScreen(Deps{}, "asset-9", "Lathe")
	// n (Notifications) and G (Categories) collide with globals — the screen
	// must claim them; every other key stays with the global fallback.
	for _, k := range []string{"n", "G"} {
		if !s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"x", "R", "E", "enter", "r", "j", "k", "g"} {
		if s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true, want false", k)
		}
	}
}

func TestAssetPartsScreen_WantsRawInput(t *testing.T) {
	s := NewAssetPartsScreen(Deps{}, "asset-9", "Lathe")
	if s.WantsRawInput() {
		t.Error("should not want raw input in the normal list view")
	}
	s.confirm = partsConfirmDelete
	if !s.WantsRawInput() {
		t.Error("should want raw input while a delete confirm is up")
	}
	s.confirm = partsConfirmReplace
	if !s.WantsRawInput() {
		t.Error("should want raw input while a mark-replaced confirm is up")
	}
}

func TestAssetPartsScreen_ConfirmFlow(t *testing.T) {
	s := NewAssetPartsScreen(Deps{}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{ID: float64(1), Part: "item-1", PartName: "Belt"}}

	// x opens the delete confirm; esc dismisses it.
	s.Update(partsRuneKey("x"))
	if s.confirm != partsConfirmDelete {
		t.Fatalf("x should arm delete confirm, got %v", s.confirm)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.confirm != partsConfirmNone {
		t.Fatalf("esc should clear confirm, got %v", s.confirm)
	}

	// R opens the mark-replaced confirm; n dismisses it.
	s.Update(partsRuneKey("R"))
	if s.confirm != partsConfirmReplace {
		t.Fatalf("R should arm replace confirm, got %v", s.confirm)
	}
	s.Update(partsRuneKey("n"))
	if s.confirm != partsConfirmNone {
		t.Fatalf("n should clear confirm, got %v", s.confirm)
	}
}

func TestAssetPartsScreen_NavAndEmpty(t *testing.T) {
	s := NewAssetPartsScreen(Deps{}, "asset-9", "Lathe")
	s.loading = false
	// Empty list renders the "no parts" hint, not a crash.
	if out := s.View(); !strings.Contains(out, "No parts") {
		t.Errorf("empty view = %q", out)
	}

	s.rows = []omsapi.AssetPart{
		{ID: float64(1), Part: "item-1", PartName: "Belt", PartSKU: "B-1"},
		{ID: float64(2), Part: "item-2", PartName: "Filter"},
	}
	// j moves down within bounds and never past the last row.
	s.Update(partsRuneKey("j"))
	if s.cursor != 1 {
		t.Errorf("cursor after j = %d, want 1", s.cursor)
	}
	s.Update(partsRuneKey("j"))
	if s.cursor != 1 {
		t.Errorf("cursor should clamp at last row, got %d", s.cursor)
	}
	if out := s.View(); !strings.Contains(out, "Belt") || !strings.Contains(out, "Filter") {
		t.Errorf("populated view missing rows: %q", out)
	}
}

func TestAssetPartsScreen_RenderRowReplacementState(t *testing.T) {
	interval := 30
	days := 45
	s := NewAssetPartsScreen(Deps{}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{
		ID:                      float64(1),
		Part:                    "item-1",
		PartName:                "Belt",
		QuantityNeeded:          2,
		IsRequired:              true,
		MaintenanceIntervalDays: &interval,
		DaysSinceReplacement:    &days,
		NeedsReplacement:        true,
	}}
	out := s.renderRow(0)
	if !strings.Contains(out, "NEEDS REPLACEMENT") {
		t.Errorf("overdue row should flag NEEDS REPLACEMENT: %q", out)
	}
	if !strings.Contains(out, "every 30d") || !strings.Contains(out, "qty 2") {
		t.Errorf("row meta missing interval/qty: %q", out)
	}
	if !strings.Contains(out, "never replaced") {
		t.Errorf("row with nil last_replaced_at should say 'never replaced': %q", out)
	}
}
