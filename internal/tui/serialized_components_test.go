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
	item := NewItemInstancesScreen(Deps{}, "item-uuid", "Filament", nil)
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
	s := NewItemInstancesScreen(Deps{}, "item-uuid", "Filament", nil)
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

	// Expiration is optional; a valid YYYY-MM-DD round-trips onto the payload.
	s.createInputs[scfExpiration].SetValue("2026-12-31")
	if w, err := s.buildCreatePayload(); err != nil || w.ExpirationDate.String() != "2026-12-31" {
		t.Errorf("expiration should map: w.ExpirationDate=%q err=%v", w.ExpirationDate.String(), err)
	}
	// A malformed expiration is rejected before submit.
	s.createInputs[scfExpiration].SetValue("not-a-date")
	if _, err := s.buildCreatePayload(); err == nil {
		t.Error("expected error for malformed expiration date")
	}

	// Lot + expiration are optional — blank both still builds a valid payload
	// with a zero (null-serializing) expiration.
	s.createInputs[scfLot].SetValue("")
	s.createInputs[scfExpiration].SetValue("")
	if w, err := s.buildCreatePayload(); err != nil || w.Lot != "" || !w.ExpirationDate.IsZero() {
		t.Errorf("blank lot+expiration should be valid: w=%+v err=%v", w, err)
	}
}

// TestSerializedCreateForm_FieldNavigation drives the three-field form: tab
// moves serial→lot→expiration and wraps, esc closes it.
func TestSerializedCreateForm_FieldNavigation(t *testing.T) {
	s := NewItemInstancesScreen(Deps{}, "item-uuid", "Filament", nil)
	s.openCreateForm()
	if s.createFocus != scfSerial {
		t.Fatalf("initial focus = %d, want serial", s.createFocus)
	}
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab}); s.createFocus != scfLot {
		t.Errorf("after tab focus = %d, want lot", s.createFocus)
	}
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab}); s.createFocus != scfExpiration {
		t.Errorf("after 2nd tab focus = %d, want expiration", s.createFocus)
	}
	// Three fields → tab wraps back to serial.
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab}); s.createFocus != scfSerial {
		t.Errorf("after 3rd tab focus = %d, want serial (wrap)", s.createFocus)
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

// TestSerialTargetUnits pins the scaling a receipt's capture list is built
// from, which is what replaced poLineSerialized.
//
// That predicate asked the LINE's own item whether it was serialized, which on
// a kit line describes the kit — the identity a serial may never name. The
// question is the server's now (`serial_targets`), and the only arithmetic left
// on this side is scaling a target to a PARTIAL receipt, which the contract
// publishes precisely so a client can size its capture list without a round
// trip per keystroke.
func TestSerialTargetUnits(t *testing.T) {
	target := omsapi.SerialTarget{Item: "itm-c", Quantity: 6} // 3 per kit × 2 ordered
	cases := []struct {
		name                        string
		ordered, received, wantUnit int
	}{
		{"the whole order", 2, 2, 6},
		{"half of it", 2, 1, 3},
		{"an over-receipt credits what turned up", 2, 3, 9},
		{"nothing received", 2, 0, 0},
		{"nothing ordered cannot be scaled", 0, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := target.Units(tc.ordered, tc.received); got != tc.wantUnit {
				t.Errorf("Units(%d, %d) = %d, want %d",
					tc.ordered, tc.received, got, tc.wantUnit)
			}
		})
	}
	// A target with nothing behind it credits nothing, whatever arrives.
	if got := (omsapi.SerialTarget{}).Units(4, 4); got != 0 {
		t.Errorf("an empty target credits %d units, want 0", got)
	}
}
