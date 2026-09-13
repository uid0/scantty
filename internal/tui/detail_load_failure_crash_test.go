package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// detail_load_failure_crash_test.go — a FAILED LOAD on four detail sheets used
// to take the whole program down.
//
// Each sheet's loaded arm drew its scrolled body unconditionally, and the body
// reads the record the load was for — the asset, the panel topology, the PM
// item, the project stint. A failed load delivers no record, so Update
// dereferenced nil on the event loop; bubbletea caught the panic, restored the
// terminal and Run returned ErrProgramPanic, which cmd/scantty returns: ScanTTY
// exited on an ordinary 502. The error frame — and the bar it draws, which is
// what prose_bar_load_states_test.go sweeps — was never reached.
//
// Each case drives the failure through Update AND View, the way Root does, on
// the path an operator reaches it by: OPENING the sheet, and pressing `r` on an
// open one where a refresh also drops the record. The PM item keeps the item it
// had when a refresh fails, so its refresh never crashed and is not a case.
//
// WATCHED FAILING: with each sheet's nil guard removed, every case below panics
// with a nil-pointer dereference inside renderBody.

var detailLoadFailure = errors.New("oms: http 502: bad gateway")

type detailLoadFailureCase struct {
	name string
	// open is a freshly constructed sheet — the state its load starts from.
	open func() Screen
	// loaded is the sheet after a successful load, for the refresh path; nil
	// where a failed refresh never dropped the record.
	loaded func() Screen
	// failed is the message the sheet's own load command returns on an error.
	failed tea.Msg
}

func detailLoadFailureCases() []detailLoadFailureCase {
	return []detailLoadFailureCase{
		{
			name:   "asset detail",
			open:   func() Screen { return NewAssetDetailScreen(Deps{}, "a-1") },
			loaded: func() Screen { return proseBarAsset(3) },
			failed: assetDetailLoadedMsg{err: detailLoadFailure},
		},
		{
			name: "electrical panel detail",
			open: func() Screen { return NewElectricalPanelDetailScreen(Deps{}, 1) },
			loaded: func() Screen {
				next, _ := NewElectricalPanelDetailScreen(Deps{}, 1).Update(
					electricalPanelDetailLoadedMsg{topology: proseBarPanelTopology()})
				return next
			},
			failed: electricalPanelDetailLoadedMsg{err: detailLoadFailure},
		},
		{
			name:   "maintenance item detail",
			open:   func() Screen { return NewMaintenanceItemDetailScreen(Deps{}, "pm-1") },
			failed: mDetailLoadedMsg{err: detailLoadFailure},
		},
		{
			name:   "project storage detail",
			open:   func() Screen { return NewProjectStorageDetailScreen(Deps{}, "PS-AB23CDFG") },
			loaded: func() Screen { return detailLoadFailureFixture("project storage detail") },
			failed: projectStorageDetailLoadedMsg{err: detailLoadFailure},
		},
	}
}

// detailLoadFailureFixture is the loaded fixture proseBarFixtures builds under
// this name — the same loaded sheet the load-state sweep refreshes from.
func detailLoadFailureFixture(name string) Screen {
	for _, f := range proseBarFixtures() {
		if f.name == name {
			return f.build()
		}
	}
	panic("no prose bar fixture named " + name)
}

func TestDetailLoadFailure_AFailedLoadDrawsTheErrorAndDoesNotCrash(t *testing.T) {
	for _, c := range detailLoadFailureCases() {
		t.Run(c.name+"/open", func(t *testing.T) {
			detailLoadFailureDrive(t, c.open(), nil, c.failed)
		})
		if c.loaded == nil {
			continue
		}
		t.Run(c.name+"/refresh", func(t *testing.T) {
			detailLoadFailureDrive(t, c.loaded(), []string{"r"}, c.failed)
		})
	}
}

// detailLoadFailureDrive sizes the sheet, presses the keys, delivers the failed
// load through Update, renders it through View, and requires the frame to say
// what failed.
func detailLoadFailureDrive(t *testing.T, s Screen, keys []string, failed tea.Msg) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a failed load panicked — in the program this exits ScanTTY: %v", r)
		}
	}()
	s, _ = s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, k := range keys {
		s, _ = s.Update(listRuneKey(k))
	}
	s, _ = s.Update(failed)
	if pane := stripANSI(s.View()); !strings.Contains(pane, detailLoadFailure.Error()) {
		t.Errorf("the failed load is not on the frame:\n%s", pane)
	}
}
