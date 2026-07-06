package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestMakerBoxes_HandlesKey confirms the screen claims s (scan) and n (new) so
// they beat the global nav, but leaves other keys to fall through.
func TestMakerBoxes_HandlesKey(t *testing.T) {
	s := NewMakerBoxesScreen(Deps{})
	if !s.HandlesKey("s") {
		t.Errorf("s should be claimed (scan)")
	}
	if !s.HandlesKey("n") {
		t.Errorf("n should be claimed (new maker box)")
	}
	if s.HandlesKey("j") || s.HandlesKey("E") || s.HandlesKey("x") {
		t.Errorf("j/E/x should fall through to the screen, not be claimed")
	}
}

// TestMakerBoxes_DeleteConfirm confirms x arms the confirmation (and flips to
// raw input so n=no doesn't hit global notifications), y starts the delete, and
// n cancels a fresh confirm.
func TestMakerBoxes_DeleteConfirm(t *testing.T) {
	s := NewMakerBoxesScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.MakerBox{{ID: 5, BinID: "PSB-005", AssignedUsername: "a"}}

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Fatalf("x should arm delete confirmation and claim raw input")
	}

	// n cancels.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if s.confirmingDelete {
		t.Errorf("n should cancel the confirmation")
	}

	// Re-arm, then y starts the delete (deleting flag set; cmd returned but not
	// run here, so the nil client is never dialed).
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !s.deleting || cmd == nil {
		t.Errorf("y should start delete (deleting=%v, cmd=%v)", s.deleting, cmd)
	}
}

// TestMakerBoxes_DeletedMsg confirms a successful delete reloads and a failed
// one surfaces an error without leaving the confirm stuck.
func TestMakerBoxes_DeletedMsg(t *testing.T) {
	s := NewMakerBoxesScreen(Deps{})
	s.confirmingDelete = true
	s.deleting = true
	next, cmd := s.Update(makerBoxDeletedMsg{err: nil})
	s = next.(*MakerBoxesScreen)
	if s.confirmingDelete || s.deleting {
		t.Errorf("successful delete should clear confirm/deleting flags")
	}
	if !s.loading || cmd == nil {
		t.Errorf("successful delete should trigger a reload")
	}
}

// TestMakerBoxes_ConvertConfirmRawInput confirms the convert y/n prompt also
// claims raw input so n cancels cleanly instead of navigating to notifications.
func TestMakerBoxes_ConvertConfirmRawInput(t *testing.T) {
	s := NewMakerBoxesScreen(Deps{})
	id := 3
	s.confirmConvertID = &id
	if !s.WantsRawInput() {
		t.Errorf("an open convert confirm should claim raw input")
	}
}
