package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestSaveSlotCardPDF_WritesFile — the backend streams the sheet rather than
// storing it, so the file the operator names IS the deliverable.
func TestSaveSlotCardPDF_WritesFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cards.pdf")
	pdf := &omsapi.SlotCardPDF{Filename: "storage_slot_cards_rack1.pdf", Data: []byte("%PDF-1.4")}

	got, replaced, err := saveSlotCardPDF(target, pdf, "fallback.pdf")
	if err != nil {
		t.Fatalf("saveSlotCardPDF: %v", err)
	}
	if got != target {
		t.Errorf("path = %q, want %q", got, target)
	}
	if replaced {
		t.Errorf("a fresh path is not a replacement")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "%PDF-1.4" {
		t.Errorf("wrote %q", data)
	}
}

// TestSaveSlotCardPDF_DirectoryTarget — naming a directory is a valid answer,
// and the file then gets the name a browser download would have given it.
func TestSaveSlotCardPDF_DirectoryTarget(t *testing.T) {
	dir := t.TempDir()
	pdf := &omsapi.SlotCardPDF{Filename: "storage_slot_cards_rack1.pdf", Data: []byte("x")}

	got, _, err := saveSlotCardPDF(dir, pdf, "fallback.pdf")
	if err != nil {
		t.Fatalf("saveSlotCardPDF: %v", err)
	}
	if got != filepath.Join(dir, "storage_slot_cards_rack1.pdf") {
		t.Errorf("path = %q, want the server's filename inside the directory", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("file not written: %v", err)
	}
}

// TestSaveSlotCardPDF_FallbackName — the server may send no
// Content-Disposition; the caller's own name is then used rather than writing
// to a directory path.
func TestSaveSlotCardPDF_FallbackName(t *testing.T) {
	dir := t.TempDir()
	got, _, err := saveSlotCardPDF(dir+string(os.PathSeparator), &omsapi.SlotCardPDF{Data: []byte("x")}, "fallback.pdf")
	if err != nil {
		t.Fatalf("saveSlotCardPDF: %v", err)
	}
	if filepath.Base(got) != "fallback.pdf" {
		t.Errorf("path = %q, want the fallback name", got)
	}
}

// TestSaveSlotCardPDF_Guards — an empty path and a nil PDF are operator-facing
// errors, not panics.
func TestSaveSlotCardPDF_Guards(t *testing.T) {
	if _, _, err := saveSlotCardPDF("", &omsapi.SlotCardPDF{Data: []byte("x")}, "f.pdf"); err == nil {
		t.Errorf("an empty path should be rejected")
	}
	if _, _, err := saveSlotCardPDF(t.TempDir(), nil, "f.pdf"); err == nil {
		t.Errorf("a nil PDF should be rejected")
	}
}

// TestSlotCardSavedSummary reports the size too — "did it actually render?" is
// the question a zero-byte file would otherwise answer wrong.
func TestSlotCardSavedSummary(t *testing.T) {
	got := slotCardSavedSummary("/tmp/cards.pdf", 2048, false)
	if !strings.Contains(got, "/tmp/cards.pdf") || !strings.Contains(got, "2.0 kB") {
		t.Errorf("summary = %q, want path + size", got)
	}
	if !strings.HasPrefix(got, "saved") {
		t.Errorf("a fresh write should read as saved, got %q", got)
	}
	if got := slotCardSavedSummary("/tmp/cards.pdf", 2048, true); !strings.HasPrefix(got, "replaced") {
		t.Errorf("overwriting an existing file should say so, got %q", got)
	}
	if got := humanBytes(12); got != "12 B" {
		t.Errorf("humanBytes(12) = %q", got)
	}
	if got := humanBytes(3 << 20); got != "3.0 MB" {
		t.Errorf("humanBytes(3MB) = %q", got)
	}
}

// TestSlotCardPrompt_ToggleOnlyForRack — the backend ignores include_inactive
// for an ids print, so offering the row there would suggest it did something.
func TestSlotCardPrompt_ToggleOnlyForRack(t *testing.T) {
	p := newSlotCardPrompt()
	p.open("3 selected", omsapi.SlotCardsForIDs([]int{1, 2, 3}), "cards.pdf")
	if p.hasToggle() || p.rowCount() != 1 {
		t.Errorf("an ids print should have no include-retired row")
	}
	p.includeInactive = true // even if something set it
	if p.request().IncludeInactive {
		t.Errorf("include_inactive must be dropped for an ids print")
	}

	p.open("rack 1", omsapi.SlotCardsForRack(1, "", false), "cards.pdf")
	if !p.hasToggle() || p.rowCount() != 2 {
		t.Errorf("a rack print should offer the include-retired row")
	}
}

// TestSlotCardPrompt_DefaultPath suggests the name the backend itself would use.
func TestSlotCardPrompt_DefaultPath(t *testing.T) {
	p := newSlotCardPrompt()
	p.open("rack 1", omsapi.SlotCardsForRack(1, "A", false), "storage_slot_cards_rack1A.pdf")
	if got := p.path.Value(); !strings.HasSuffix(got, "storage_slot_cards_rack1A.pdf") {
		t.Errorf("default path = %q", got)
	}
	if !strings.HasPrefix(p.path.Value(), "~") {
		t.Errorf("default should live under the home directory, got %q", p.path.Value())
	}
}

// TestSlotCardPrompt_DoubleEnterCannotQueueTwice — a second enter while the
// render is in flight must not spend another round trip.
func TestSlotCardPrompt_DoubleEnterCannotQueueTwice(t *testing.T) {
	p := newSlotCardPrompt()
	p.open("rack 1", omsapi.SlotCardsForRack(1, "", false), "cards.pdf")
	fire, _, _ := p.handleKey(namedKey("enter"))
	if !fire {
		t.Fatalf("enter should fire the render")
	}
	fire, cancel, _ := p.handleKey(namedKey("enter"))
	if fire || cancel {
		t.Errorf("a second enter while working must be inert")
	}
}

// TestSlotCardErrorText prefers the backend's own sentence for its hand-rolled
// 404s — the operator should read "No storage slots match rack 9", not JSON.
func TestSlotCardErrorText(t *testing.T) {
	err := &omsapi.APIError{
		Status:  404,
		Message: `{"detail":"No storage slots match rack 9.","code":"no_slots_matched"}`,
	}
	if got := slotCardErrorText(err); got != "No storage slots match rack 9." {
		t.Errorf("slotCardErrorText = %q", got)
	}
	plain := &omsapi.APIError{Status: 500, Message: "boom"}
	if got := slotCardErrorText(plain); got != plain.Error() {
		t.Errorf("a non-slot error should fall back to err.Error(), got %q", got)
	}
	if got := slotCardErrorText(nil); got != "" {
		t.Errorf("nil error should be empty, got %q", got)
	}
}
