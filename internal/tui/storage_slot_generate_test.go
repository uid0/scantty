package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func genKey(t *testing.T, s *StorageSlotGenerateScreen, key string) *StorageSlotGenerateScreen {
	t.Helper()
	next, _ := s.Update(namedKey(key))
	out, ok := next.(*StorageSlotGenerateScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *StorageSlotGenerateScreen", next)
	}
	return out
}

func readyGenScreen(rack int) *StorageSlotGenerateScreen {
	s := NewStorageSlotGenerateScreen(Deps{}, rack)
	s.terminalHeight = 40
	next, _ := s.Update(storageGenLoadedMsg{sigs: []omsapi.SIG{{ID: 3, Name: "Woodshop"}}})
	return next.(*StorageSlotGenerateScreen)
}

// TestStorageSlotGenerate_SeedsRackFromScope — "generate" is almost always
// aimed at the rack the operator is already looking at.
func TestStorageSlotGenerate_SeedsRackFromScope(t *testing.T) {
	if got := readyGenScreen(3).rackInput.Value(); got != "3" {
		t.Errorf("rack = %q, want the scoped rack pre-filled", got)
	}
	if got := readyGenScreen(0).rackInput.Value(); got != "" {
		t.Errorf("rack = %q, want empty when there is no scope", got)
	}
}

// TestStorageSlotGenerate_LevelRowValidation restates the serializer's own
// rules locally so a bad row is a message instead of a 400 that loses the whole
// spec the operator just typed.
func TestStorageSlotGenerate_LevelRowValidation(t *testing.T) {
	s := readyGenScreen(1)
	s.openLevelRow(-1)

	s.rowPositions.SetValue("12")
	s.rowLevel.SetValue("")
	s.commitLevelRow()
	if s.rowErr == "" || len(s.levels) != 0 {
		t.Errorf("a blank level should be rejected, err=%q levels=%v", s.rowErr, s.levels)
	}

	s.rowLevel.SetValue("A")
	s.rowPositions.SetValue("0")
	s.commitLevelRow()
	if s.rowErr == "" || len(s.levels) != 0 {
		t.Errorf("0 positions should be rejected")
	}
	s.rowPositions.SetValue("101")
	s.commitLevelRow()
	if s.rowErr == "" || len(s.levels) != 0 {
		t.Errorf("101 positions exceeds the serializer's cap and should be rejected")
	}

	s.rowPositions.SetValue("12")
	s.commitLevelRow()
	if s.rowErr != "" || len(s.levels) != 1 {
		t.Fatalf("a valid row should commit, err=%q levels=%v", s.rowErr, s.levels)
	}
	if s.levels[0].level != "A" || s.levels[0].positions != 12 {
		t.Errorf("row = %+v", s.levels[0])
	}

	// Each letter may appear at most once per request.
	s.openLevelRow(-1)
	s.rowLevel.SetValue("a")
	s.rowPositions.SetValue("6")
	s.commitLevelRow()
	if s.rowErr == "" || len(s.levels) != 1 {
		t.Errorf("a duplicate level should be rejected (case-insensitively), err=%q", s.rowErr)
	}

	// Editing the SAME row keeps its letter — the duplicate check must not
	// collide with the row being edited.
	s.openLevelRow(0)
	s.rowLevel.SetValue("A")
	s.rowPositions.SetValue("14")
	s.commitLevelRow()
	if s.rowErr != "" {
		t.Errorf("re-saving a row with its own letter should be allowed, err=%q", s.rowErr)
	}
	if s.levels[0].positions != 14 {
		t.Errorf("edit did not apply: %+v", s.levels[0])
	}
}

// TestStorageSlotGenerate_BuildPayload pins the bulk request shape.
func TestStorageSlotGenerate_BuildPayload(t *testing.T) {
	s := readyGenScreen(1)
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("a rack with no levels should be rejected — it would create nothing")
	}

	s.levels = []storageGenLevelRow{
		{level: "A", positions: 12},
		{level: "Y", positions: 10, palletJack: true},
	}
	group := 3
	s.owningGroupID = &group
	s.notesInput.SetValue("  north aisle  ")

	req, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if req.Rack != 1 || len(req.Levels) != 2 {
		t.Fatalf("request = %+v", req)
	}
	if req.Levels[1].Level != "Y" || req.Levels[1].Positions != 10 || !req.Levels[1].RequiresPalletJack {
		t.Errorf("level spec wrong: %+v", req.Levels[1])
	}
	if req.OwningGroup == nil || *req.OwningGroup != 3 {
		t.Errorf("owning group not carried: %v", req.OwningGroup)
	}
	if req.Notes != "north aisle" {
		t.Errorf("notes = %q, want trimmed", req.Notes)
	}

	s.rackInput.SetValue("")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("a missing rack should be rejected")
	}
}

// TestStorageSlotGenerate_PreviewSizesTheRun — an operator should see how many
// slots (and which codes) a run would create before committing to it.
func TestStorageSlotGenerate_PreviewSizesTheRun(t *testing.T) {
	s := readyGenScreen(1)
	if !strings.Contains(s.previewLine(), "no levels yet") {
		t.Errorf("an empty spec should say so, got %q", s.previewLine())
	}
	s.levels = []storageGenLevelRow{{level: "A", positions: 12}, {level: "B", positions: 8}}
	line := s.previewLine()
	if !strings.Contains(line, "20 slot") {
		t.Errorf("preview should total the positions, got %q", line)
	}
	if !strings.Contains(line, "1A1") || !strings.Contains(line, "1B8") {
		t.Errorf("preview should show the code range, got %q", line)
	}
}

// TestStorageSlotGenerate_ResultReportsIdempotence — created/skipped/no-tag is
// the whole point of a re-runnable generator, so the report stays on screen
// rather than flashing past in the status bar.
func TestStorageSlotGenerate_ResultReportsIdempotence(t *testing.T) {
	s := readyGenScreen(1)
	next, _ := s.Update(storageGenDoneMsg{result: &omsapi.GenerateRackResult{
		Rack: 1, Created: []string{"1A1", "1A2"}, Skipped: []string{"1B1"},
		CreatedCount: 2, SkippedCount: 1, WithoutTag: []string{"1A2"},
	}})
	s = next.(*StorageSlotGenerateScreen)
	if s.phase != genPhaseResult {
		t.Fatalf("a finished run should show its report")
	}
	out := s.View()
	for _, want := range []string{"Created 2", "Already existed", "1B1", "No AprilTag", "1A2"} {
		if !strings.Contains(out, want) {
			t.Errorf("result view missing %q:\n%s", want, out)
		}
	}

	summary := storageGenSummary(&omsapi.GenerateRackResult{Rack: 1, CreatedCount: 2, SkippedCount: 1})
	if !strings.Contains(summary, "2 created") || !strings.Contains(summary, "1 already existed") {
		t.Errorf("summary should not imply everything was made fresh, got %q", summary)
	}
}

// TestStorageSlotGenerate_CodeListNamesWhatItDropped — a 200-slot rack must not
// bury the summary, but a silent truncation would read as "that's all of them".
func TestStorageSlotGenerate_CodeListNamesWhatItDropped(t *testing.T) {
	codes := make([]string, 40)
	for i := range codes {
		codes[i] = "1A1"
	}
	out := storageGenCodeList(codes)
	if !strings.Contains(out, "and 16 more") {
		t.Errorf("a truncated code list must say how many it left off, got %q", out)
	}
	if got := storageGenCodeList(nil); !strings.Contains(got, "—") {
		t.Errorf("an empty list should render a dash, got %q", got)
	}
}

// TestStorageSlotGenerate_LevelListEditing walks the sub-phase keys.
func TestStorageSlotGenerate_LevelListEditing(t *testing.T) {
	s := readyGenScreen(1)
	s.cursor = 1 // the Levels row
	s = genKey(t, s, " ")
	if s.phase != genPhaseLevels {
		t.Fatalf("space on the Levels row should open the list")
	}
	s = genKey(t, s, "a")
	if s.phase != genPhaseLevelRow {
		t.Fatalf("a should open the row editor")
	}
	s.rowLevel.SetValue("A")
	s.rowPositions.SetValue("4")
	s = genKey(t, s, "enter")
	if s.phase != genPhaseLevels || len(s.levels) != 1 {
		t.Fatalf("enter should commit the row, phase=%v levels=%v", s.phase, s.levels)
	}
	if !strings.Contains(s.View(), "A — 4 position") {
		t.Errorf("the list should show the row:\n%s", s.View())
	}
	s = genKey(t, s, "x")
	if len(s.levels) != 0 {
		t.Errorf("x should remove the row, got %v", s.levels)
	}
	s = genKey(t, s, "esc")
	if s.phase != genPhaseForm {
		t.Errorf("esc should return to the form")
	}
	if !strings.Contains(s.View(), "none — space to add") {
		t.Errorf("the Levels row should say it is empty:\n%s", s.View())
	}
}
