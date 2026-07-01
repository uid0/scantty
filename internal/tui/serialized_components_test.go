package tui

import (
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

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
