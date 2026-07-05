package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestSerializedCreateForm_HandlesKeyScope pins the sc-k7p claim: the item
// scope grabs 'a' (add) off the global nav, the asset scope leaves it alone
// (no create surface there — mirrors the web, where Add-unit lives on the item
// panel only).
func TestSerializedCreateForm_HandlesKeyScope(t *testing.T) {
	item := NewItemInstancesScreen(Deps{}, "item-uuid", "Filament")
	if !item.HandlesKey("a") {
		t.Error("item scope should claim 'a' for add")
	}
	if item.HandlesKey("z") {
		t.Error("item scope should only claim 'a', not other keys")
	}
	asset := NewAssetComponentsScreen(Deps{}, "asset-uuid", "Printer")
	if asset.HandlesKey("a") {
		t.Error("asset scope must not claim 'a' (no create form there)")
	}
}

// TestSerializedCreateForm_PayloadAndValidation walks the add form: opening it,
// the serial-required rule, and that item (from screen context) + serial + lot
// map onto the create body.
func TestSerializedCreateForm_PayloadAndValidation(t *testing.T) {
	s := NewItemInstancesScreen(Deps{}, "item-uuid", "Filament")
	s.openCreateForm()
	if s.form != serialFormCreate {
		t.Fatalf("form = %v, want serialFormCreate", s.form)
	}
	// Serial is required.
	if _, err := s.buildCreatePayload(); err == nil {
		t.Error("expected error for empty serial number")
	}
	s.createInputs[scfSerial].SetValue("SN-42")
	s.createInputs[scfLot].SetValue("batch-7")
	w, err := s.buildCreatePayload()
	if err != nil {
		t.Fatalf("buildCreatePayload: %v", err)
	}
	if w.Item != "item-uuid" {
		t.Errorf("item = %q, want item-uuid", w.Item)
	}
	if w.SerialNumber != "SN-42" {
		t.Errorf("serial_number = %q, want SN-42", w.SerialNumber)
	}
	if w.Lot != "batch-7" {
		t.Errorf("lot = %q, want batch-7", w.Lot)
	}

	// Lot is optional — a blank lot still builds a valid payload.
	s.createInputs[scfLot].SetValue("")
	if w, err := s.buildCreatePayload(); err != nil || w.Lot != "" {
		t.Errorf("blank lot should be valid: w=%+v err=%v", w, err)
	}
}

// TestSerializedCreateForm_FieldNavigation drives the two-field form: tab moves
// serial→lot and wraps, esc closes it.
func TestSerializedCreateForm_FieldNavigation(t *testing.T) {
	s := NewItemInstancesScreen(Deps{}, "item-uuid", "Filament")
	s.openCreateForm()
	if s.createFocus != scfSerial {
		t.Fatalf("initial focus = %d, want serial", s.createFocus)
	}
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab}); s.createFocus != scfLot {
		t.Errorf("after tab focus = %d, want lot", s.createFocus)
	}
	// Two fields → tab wraps back to serial.
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab}); s.createFocus != scfSerial {
		t.Errorf("after 2nd tab focus = %d, want serial (wrap)", s.createFocus)
	}
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyEsc}); s.form != serialFormNone {
		t.Errorf("esc should close the form, form = %v", s.form)
	}
}

func TestContainsStr(t *testing.T) {
	acts := []string{"install", "retire"}
	if !containsStr(acts, "install") {
		t.Error("expected install to be present")
	}
	if containsStr(acts, "consume") {
		t.Error("consume should not be present")
	}
	if containsStr(nil, "install") {
		t.Error("nil slice should contain nothing")
	}
}

func TestStatusLabel(t *testing.T) {
	withDisplay := &omsapi.SerializedComponent{Status: "in_stock", StatusDisplay: "In Stock"}
	if got := statusLabel(withDisplay); got != "In Stock" {
		t.Errorf("statusLabel = %q, want In Stock", got)
	}
	// Falls back to the raw code when the server omitted the display string.
	raw := &omsapi.SerializedComponent{Status: "in_stock"}
	if got := statusLabel(raw); got != "in_stock" {
		t.Errorf("statusLabel fallback = %q, want in_stock", got)
	}
}

func TestActionPastTense(t *testing.T) {
	cases := map[string]string{
		omsapi.SerialActionReceive: "received",
		omsapi.SerialActionInstall: "installed",
		omsapi.SerialActionRemove:  "removed",
		omsapi.SerialActionConsume: "consumed",
		omsapi.SerialActionRetire:  "retired",
		omsapi.SerialActionDispose: "disposed",
	}
	for action, want := range cases {
		if got := actionPastTense(action); got != want {
			t.Errorf("actionPastTense(%q) = %q, want %q", action, got, want)
		}
	}
	if got := actionPastTense("frobnicate"); got != "frobnicate done" {
		t.Errorf("unknown action fallback = %q", got)
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("abcd1234-5678-90ab-cdef-1234567890ab"); got != "abcd1234" {
		t.Errorf("shortID uuid = %q, want abcd1234", got)
	}
	if got := shortID("short"); got != "short" {
		t.Errorf("shortID short = %q, want short", got)
	}
	if got := shortID("0123456789abcdef"); got != "01234567" {
		t.Errorf("shortID no-dash long = %q, want 01234567", got)
	}
}

func TestTrimFloat(t *testing.T) {
	cases := map[float64]string{
		7.5:    "7.5",
		3.0:    "3",
		0.4286: "0.43",
		0.0:    "0",
		12.0:   "12",
	}
	for in, want := range cases {
		if got := trimFloat(in); got != want {
			t.Errorf("trimFloat(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestPOLineSerialized(t *testing.T) {
	serialized := omsapi.PurchaseOrderItem{
		ItemDetails: map[string]any{"id": "item-uuid", "is_serialized": true},
	}
	if id, ok := poLineSerialized(serialized); !ok || id != "item-uuid" {
		t.Errorf("serialized line = (%q, %v), want (item-uuid, true)", id, ok)
	}

	notSerialized := omsapi.PurchaseOrderItem{
		ItemDetails: map[string]any{"id": "item-uuid", "is_serialized": false},
	}
	if _, ok := poLineSerialized(notSerialized); ok {
		t.Error("non-serialized line should return ok=false")
	}

	// Serialized but missing the item id — can't create units, so skip.
	noID := omsapi.PurchaseOrderItem{
		ItemDetails: map[string]any{"is_serialized": true},
	}
	if _, ok := poLineSerialized(noID); ok {
		t.Error("serialized line without id should return ok=false")
	}

	// Freeform / asset line with no item_details must not panic.
	freeform := omsapi.PurchaseOrderItem{Description: "custom bolts"}
	if _, ok := poLineSerialized(freeform); ok {
		t.Error("freeform line should return ok=false")
	}
}
