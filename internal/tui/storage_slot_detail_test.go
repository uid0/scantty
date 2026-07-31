package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func slotDetailKey(t *testing.T, s *StorageSlotDetailScreen, key string) *StorageSlotDetailScreen {
	t.Helper()
	next, _ := s.Update(namedKey(key))
	out, ok := next.(*StorageSlotDetailScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *StorageSlotDetailScreen", next)
	}
	return out
}

func loadedSlotDetail(slot omsapi.StorageSlot) *StorageSlotDetailScreen {
	s := NewStorageSlotDetailScreen(Deps{}, slot.Code)
	s.terminalHeight = 40
	next, _ := s.Update(storageSlotDetailLoadedMsg{slot: &slot})
	return next.(*StorageSlotDetailScreen)
}

// TestStorageSlotDetail_FreeSlotBody covers the read-out a warden scans: the
// placement, the permanent marker, and an unambiguous free/occupied line.
func TestStorageSlotDetail_FreeSlotBody(t *testing.T) {
	tag := 41
	s := loadedSlotDetail(omsapi.StorageSlot{
		ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true, AprilTagID: &tag,
	})
	body := s.renderBody()
	for _, want := range []string{"1A1", "Rack:", "Level:", "Position:", "AprilTag", "41", "Free"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "needs a pallet jack") {
		t.Errorf("a ground-reachable slot should not claim it needs a jack:\n%s", body)
	}
}

// TestStorageSlotDetail_TaglessSlotSaysWhy — a null april_tag_id means the tag
// family ran dry: the slot works by code but has nothing to scan, which is a
// fact somebody has to act on rather than a blank field.
func TestStorageSlotDetail_TaglessSlotSaysWhy(t *testing.T) {
	s := loadedSlotDetail(omsapi.StorageSlot{ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true})
	body := s.renderBody()
	if !strings.Contains(body, "No marker allocated") {
		t.Errorf("a tagless slot must explain itself:\n%s", body)
	}
}

// TestStorageSlotDetail_RetiredFreeSlot — free and retired is NOT "available";
// the two lines must not collapse into one.
func TestStorageSlotDetail_RetiredFreeSlot(t *testing.T) {
	s := loadedSlotDetail(omsapi.StorageSlot{ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1})
	body := s.renderBody()
	if !strings.Contains(body, "retired") {
		t.Errorf("a retired slot must say so:\n%s", body)
	}
	if strings.Contains(body, "available to reserve") {
		t.Errorf("a retired slot is not on offer:\n%s", body)
	}
}

// TestStorageSlotDetail_OccupantAndDrillIn — the occupant is the one thing on
// this screen with somewhere else to be.
func TestStorageSlotDetail_OccupantAndDrillIn(t *testing.T) {
	s := loadedSlotDetail(omsapi.StorageSlot{
		ID: 2, Code: "1A2", Rack: 1, Level: "A", Position: 2, IsActive: true, IsOccupied: true,
		CurrentStint: &omsapi.StorageSlotOccupant{
			StintID: "PS-AB23CDFG", Username: "alice", DisplayName: "Alice Smith", ProjectTitle: "CNC jig",
		},
	})
	body := s.renderBody()
	for _, want := range []string{"Alice Smith", "@alice", "PS-AB23CDFG", "CNC jig"} {
		if !strings.Contains(body, want) {
			t.Errorf("occupancy block missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(s.View(), "enter open stint") {
		t.Errorf("an occupied slot should advertise the drill-in")
	}

	var switched bool
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		if msg, ok := cmd().(SwitchScreenMsg); ok {
			if _, ok := msg.Screen.(*ProjectStorageDetailScreen); ok {
				switched = true
			}
		}
	}
	if !switched {
		t.Errorf("enter on an occupied slot should open the stint")
	}
}

// TestStorageSlotDetail_EnterOnFreeSlotIsInert — nothing to open, so nothing
// happens (rather than navigating somewhere arbitrary).
func TestStorageSlotDetail_EnterOnFreeSlotIsInert(t *testing.T) {
	s := loadedSlotDetail(omsapi.StorageSlot{ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true})
	if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Errorf("enter on a free slot should do nothing, got a command")
	}
}

// TestStorageSlotDetail_PrintPromptIsSingleCard — p prints exactly this slot,
// via the ids mode (so the include-retired toggle is correctly absent).
func TestStorageSlotDetail_PrintPromptIsSingleCard(t *testing.T) {
	s := loadedSlotDetail(omsapi.StorageSlot{ID: 7, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true})
	s = slotDetailKey(t, s, "p")
	if !s.card.active {
		t.Fatalf("p should open the print prompt")
	}
	req := s.card.request()
	if len(req.SlotIDs) != 1 || req.SlotIDs[0] != 7 || req.Rack != nil {
		t.Errorf("a single-card print should be ids-only, got %+v", req)
	}
	if !s.WantsRawInput() {
		t.Errorf("the print prompt must claim raw input so a typed path lands here")
	}
}

// TestStorageSlotDetail_HandlesKey — only G collides with a global here.
func TestStorageSlotDetail_HandlesKey(t *testing.T) {
	s := loadedSlotDetail(omsapi.StorageSlot{ID: 1, Code: "1A1", IsActive: true})
	if !s.HandlesKey("G") {
		t.Errorf("G (bottom of scroll) collides with the global categories hotkey and must be claimed")
	}
	for _, k := range []string{"E", "x", "p", "v", "r", "enter"} {
		if s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true, want false", k)
		}
	}
}

// TestStorageSlotDetail_PreviewPanelReportsWhatIsEncoded — a terminal can't
// draw the PDF, so the panel surfaces the facts a scan can be checked against
// instead of pretending to render it.
func TestStorageSlotDetail_PreviewPanelReportsWhatIsEncoded(t *testing.T) {
	tag := 41
	s := loadedSlotDetail(omsapi.StorageSlot{ID: 7, Code: "1A1", IsActive: true})
	s = slotDetailKey(t, s, "v")
	if !s.previewing {
		t.Fatalf("v should open the card panel")
	}
	next, _ := s.Update(storageSlotPreviewMsg{preview: &omsapi.SlotCardPreview{
		SlotID: 7, Code: "1A1", Filename: "storage_slot_1A1_card.pdf",
		Preview: "JVBERi0=", KioskURL: "https://oms.example/kiosk?slot=1A1", AprilTagID: &tag,
	}})
	s = next.(*StorageSlotDetailScreen)
	panel := s.previewPanel()
	if !strings.Contains(panel, "kiosk?slot=1A1") {
		t.Errorf("the panel must report the QR target:\n%s", panel)
	}
	if !strings.Contains(panel, "41") {
		t.Errorf("the panel must report the marker id:\n%s", panel)
	}
	if !strings.Contains(panel, "press p") {
		t.Errorf("the panel should point at the way to actually get the sheet:\n%s", panel)
	}

	// Any key dismisses it.
	s = slotDetailKey(t, s, "j")
	if s.previewing {
		t.Errorf("any key should close the panel")
	}
}

// TestStorageSlotDetail_RekeysOnRename — editing rack/level/position recomputes
// the code server-side, so a refresh after a rename must follow the new code
// rather than 404ing on the old one.
func TestStorageSlotDetail_RekeysOnRename(t *testing.T) {
	s := NewStorageSlotDetailScreen(Deps{}, "1A1")
	next, _ := s.Update(storageSlotDetailLoadedMsg{slot: &omsapi.StorageSlot{ID: 1, Code: "2B3", Rack: 2, Level: "B", Position: 3}})
	s = next.(*StorageSlotDetailScreen)
	if s.code != "2B3" {
		t.Errorf("code = %q, want the code the server computed", s.code)
	}
}
