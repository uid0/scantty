package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	s.confirm = partsConfirmReplaceSerial
	if !s.WantsRawInput() {
		t.Error("should want raw input while the serial prompt is up")
	}
}

// TestAssetPartsScreen_SerializedOpensSerialPrompt gates the prompt on
// part_details.is_serialized: confirming replace on a serialized part opens the
// serial-entry step instead of firing immediately.
func TestAssetPartsScreen_SerializedOpensSerialPrompt(t *testing.T) {
	s := NewAssetPartsScreen(Deps{Ctx: context.Background()}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{
		ID: float64(1), Part: "item-1", PartName: "Magenta ink",
		PartDetails: omsapi.AssetPartDetails{IsSerialized: true},
	}}

	s.Update(partsRuneKey("R"))
	if s.confirm != partsConfirmReplace {
		t.Fatalf("R should arm replace confirm, got %v", s.confirm)
	}
	_, cmd := s.Update(partsRuneKey("y"))
	if s.confirm != partsConfirmReplaceSerial {
		t.Fatalf("y on a serialized part should open the serial prompt, got %v", s.confirm)
	}
	if s.working {
		t.Errorf("must not fire the request before the serial is entered")
	}
	if cmd == nil {
		t.Errorf("opening the prompt should return the textinput.Blink cmd")
	}
	if !s.serialInput.Focused() {
		t.Errorf("serial input should be focused")
	}
	if !strings.Contains(s.View(), "Replacement serial number") {
		t.Errorf("prompt view missing the serial field label: %q", s.View())
	}
}

// TestAssetPartsScreen_NonSerializedNoPrompt keeps the one-click behavior for a
// non-serialized part: confirming replace fires immediately, no serial prompt.
func TestAssetPartsScreen_NonSerializedNoPrompt(t *testing.T) {
	s := NewAssetPartsScreen(Deps{Ctx: context.Background()}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{ID: float64(1), Part: "item-1", PartName: "Belt"}}

	s.Update(partsRuneKey("R"))
	_, cmd := s.Update(partsRuneKey("y"))
	if s.confirm == partsConfirmReplaceSerial {
		t.Fatalf("non-serialized part must not open the serial prompt")
	}
	if !s.working {
		t.Errorf("non-serialized replace should fire immediately (working=true)")
	}
	if cmd == nil {
		t.Errorf("non-serialized replace should return the request cmd")
	}
}

// TestAssetPartsScreen_SerialPromptSubmit drives the full serialized flow against
// a fake server and asserts the typed serial reaches the mark_replaced body.
func TestAssetPartsScreen_SerialPromptSubmit(t *testing.T) {
	var gotBody map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &gotBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"asset":"asset-9","part":"item-1","replacement_serial_number":"MG-1"}`))
	}))
	defer srv.Close()

	s := NewAssetPartsScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{
		ID: float64(1), Part: "item-1", PartName: "Magenta ink",
		PartDetails: omsapi.AssetPartDetails{IsSerialized: true},
	}}

	s.Update(partsRuneKey("R"))
	s.Update(partsRuneKey("y"))
	s.Update(partsRuneKey("MG-1")) // keystrokes route to the focused input
	if got := s.serialInput.Value(); got != "MG-1" {
		t.Fatalf("serial input value = %q, want MG-1", got)
	}
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !s.working || cmd == nil {
		t.Fatalf("enter should submit (working=true, cmd non-nil)")
	}
	msg := cmd() // execute the request
	rm, ok := msg.(assetPartReplacedMsg)
	if !ok || rm.err != nil {
		t.Fatalf("expected a successful assetPartReplacedMsg, got %#v", msg)
	}
	if gotPath != "/api/inventory/asset-parts/1/mark_replaced/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody == nil || gotBody["replacement_serial_number"] != "MG-1" {
		t.Errorf("mark_replaced body = %v, want replacement_serial_number=MG-1", gotBody)
	}
}

// TestAssetPartsScreen_SerialPromptBlankSubmitNoBody confirms a blank serial
// submit is allowed and sends NO body (the operator isn't blocked; back-compat).
func TestAssetPartsScreen_SerialPromptBlankSubmitNoBody(t *testing.T) {
	sawBody := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sawBody = len(raw) > 0
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"asset":"asset-9","part":"item-1"}`))
	}))
	defer srv.Close()

	s := NewAssetPartsScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{
		ID: float64(1), Part: "item-1", PartName: "Magenta ink",
		PartDetails: omsapi.AssetPartDetails{IsSerialized: true},
	}}

	s.Update(partsRuneKey("R"))
	s.Update(partsRuneKey("y"))
	// submit with the field left blank
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("blank enter should still submit")
	}
	cmd()
	if sawBody {
		t.Errorf("blank serial must send no request body")
	}
}

// TestAssetPartsScreen_SerialPromptEscCancels backs the serial prompt all the
// way out without firing a request.
func TestAssetPartsScreen_SerialPromptEscCancels(t *testing.T) {
	s := NewAssetPartsScreen(Deps{Ctx: context.Background()}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{
		ID: float64(1), Part: "item-1", PartName: "Magenta ink",
		PartDetails: omsapi.AssetPartDetails{IsSerialized: true},
	}}

	s.Update(partsRuneKey("R"))
	s.Update(partsRuneKey("y"))
	if s.confirm != partsConfirmReplaceSerial {
		t.Fatalf("precondition: serial prompt should be open")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.confirm != partsConfirmNone {
		t.Errorf("esc should clear the serial prompt, got %v", s.confirm)
	}
	if s.working {
		t.Errorf("esc must not fire a request")
	}
	if s.serialInput.Focused() {
		t.Errorf("serial input should be blurred after cancel")
	}
}

// TestAssetPartsScreen_RenderRowShowsSerial surfaces a recorded replacement
// serial in the row meta.
func TestAssetPartsScreen_RenderRowShowsSerial(t *testing.T) {
	s := NewAssetPartsScreen(Deps{}, "asset-9", "Lathe")
	s.loading = false
	s.rows = []omsapi.AssetPart{{
		ID: float64(1), Part: "item-1", PartName: "Magenta ink",
		ReplacementSerialNumber: "MG-2024-XYZ",
	}}
	if out := s.renderRow(0); !strings.Contains(out, "s/n MG-2024-XYZ") {
		t.Errorf("row should surface the recorded serial: %q", out)
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
