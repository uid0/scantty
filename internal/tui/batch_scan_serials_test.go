package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestBatchScan_RawInput: the screen swallows every key (scanner-gun capture),
// exactly like the Scan screen.
func TestBatchScan_RawInput(t *testing.T) {
	s := NewBatchScanSerialsScreen(Deps{}, "item-uuid", "Cylinder")
	if !s.WantsRawInput() {
		t.Error("batch scan must claim raw input for scanner-gun capture")
	}
}

// TestBatchScan_SetupToScan: enter with a blank (optional) expiration advances
// from the lot/expiration setup step into scanning; tab moves focus.
func TestBatchScan_SetupToScan(t *testing.T) {
	s := NewBatchScanSerialsScreen(Deps{}, "item-uuid", "Cylinder")
	if s.phase != batchPhaseSetup {
		t.Fatalf("initial phase = %v, want setup", s.phase)
	}
	s.lotIn.SetValue("batch-3")
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab}); s.setupFocus != bsfExp {
		t.Errorf("after tab setupFocus = %d, want expiration", s.setupFocus)
	}
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyEnter}); s.phase != batchPhaseScan {
		t.Fatalf("enter should start scanning, phase = %v", s.phase)
	}
	if s.lot != "batch-3" {
		t.Errorf("lot not captured: %q", s.lot)
	}
	if !s.exp.IsZero() {
		t.Errorf("blank expiration should be zero, got %q", s.exp.String())
	}
}

// TestBatchScan_SetupRejectsBadExpiration: a malformed expiration keeps the
// setup step open with an error rather than entering scan mode.
func TestBatchScan_SetupRejectsBadExpiration(t *testing.T) {
	s := NewBatchScanSerialsScreen(Deps{}, "item-uuid", "Cylinder")
	s.expIn.SetValue("13/40/2026")
	if _, _ = s.Update(tea.KeyMsg{Type: tea.KeyEnter}); s.phase != batchPhaseSetup {
		t.Errorf("bad expiration should keep setup open, phase = %v", s.phase)
	}
	if s.setupErr == "" {
		t.Error("expected a setup error for the malformed expiration")
	}
}

// TestBatchScan_ScanCounts: the scan-result handler tallies new vs duplicate vs
// failed and prepends the entry with its created flag + id.
func TestBatchScan_ScanCounts(t *testing.T) {
	s := NewBatchScanSerialsScreen(Deps{}, "item-uuid", "Cylinder")
	s.phase = batchPhaseScan

	s.Update(batchScanDoneMsg{serial: "SN-1", res: &omsapi.ScanReceiveResult{
		SerializedComponent: omsapi.SerializedComponent{ID: "c1"}, Created: true,
	}})
	s.Update(batchScanDoneMsg{serial: "SN-1", res: &omsapi.ScanReceiveResult{
		SerializedComponent: omsapi.SerializedComponent{ID: "c1"}, Created: false,
	}})
	s.Update(batchScanDoneMsg{serial: "SN-2", err: errStub("boom")})

	if s.newCount != 1 || s.dupCount != 1 || s.failCount != 1 {
		t.Errorf("counts = new %d dup %d fail %d, want 1/1/1", s.newCount, s.dupCount, s.failCount)
	}
	// Entries are most-recent-first; the failed scan is not recorded as an entry.
	if len(s.entries) != 2 {
		t.Fatalf("entries = %d, want 2 (created + duplicate)", len(s.entries))
	}
	if s.entries[0].created {
		t.Error("most-recent entry was the duplicate re-scan, should have created=false")
	}
	if !s.entries[1].created || s.entries[1].id != "c1" {
		t.Errorf("first scan should be created with id c1: %+v", s.entries[1])
	}
}

// TestBatchScan_Undo: undoLast selects the most-recent created (non-duplicate,
// non-undone) unit; the undo-done handler marks it undone and decrements the new
// count. With nothing undoable it is a no-op warning.
func TestBatchScan_Undo(t *testing.T) {
	s := NewBatchScanSerialsScreen(Deps{}, "item-uuid", "Cylinder")
	s.phase = batchPhaseScan

	// Nothing scanned yet → nothing to undo, no work kicked off.
	if _, _ = s.undoLast(); s.undoing {
		t.Error("undo with no entries should not start work")
	}

	// A duplicate (created=false) in front of a real creation: undo must skip
	// the duplicate and target the created unit.
	s.entries = []batchScanEntry{
		{serial: "SN-2", created: false, id: "c2"},
		{serial: "SN-1", created: true, id: "c1"},
	}
	s.newCount = 1
	if _, _ = s.undoLast(); !s.undoing {
		t.Error("undo should start work when a created unit exists")
	}

	// The done handler marks the chosen entry undone and drops the new count.
	s.Update(batchUndoDoneMsg{idx: 1})
	if !s.entries[1].undone {
		t.Error("undone entry should be flagged")
	}
	if s.newCount != 0 {
		t.Errorf("newCount = %d, want 0 after undo", s.newCount)
	}
}

// TestParseOptionalDateOnly covers the shared YYYY-MM-DD parser: blank → zero,
// valid → set, malformed → error.
func TestParseOptionalDateOnly(t *testing.T) {
	if d, err := parseOptionalDateOnly("  "); err != nil || !d.IsZero() {
		t.Errorf("blank should be zero+nil: d=%q err=%v", d.String(), err)
	}
	if d, err := parseOptionalDateOnly("2026-09-09"); err != nil || d.String() != "2026-09-09" {
		t.Errorf("valid date: d=%q err=%v", d.String(), err)
	}
	if _, err := parseOptionalDateOnly("2026/09/09"); err == nil {
		t.Error("slashed date should error")
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
