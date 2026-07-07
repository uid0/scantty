package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// newTestRoot builds a Root with the given active screen, bypassing NewRoot's
// login/welcome bootstrap so tests can drive key dispatch against a specific
// screen directly.
func newTestRoot(screen Screen) Root {
	return Root{
		deps:     Deps{},
		nav:      NewNav(),
		status:   NewStatusBar(),
		navWidth: 24,
		screen:   screen,
	}
}

// press sends a single-rune (or named) key through Root.Update and returns the
// resulting Root so tests can inspect the active screen.
func press(t *testing.T, r Root, key string) Root {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, _ := r.Update(msg)
	rootAfter, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return rootAfter
}

// A SwitchScreenMsg with a nil Screen — SwitchTo(ws, nil), used by the PO-create
// flow (po_create.go / po_create_pickers.go) to return to the workspace default
// — must resolve to that workspace's default screen instead of nil-deref'ing
// r.screen.Init() and panicking the whole program. Regression for the
// "submit a purchase order" crash at app.go SwitchScreenMsg.
func TestSwitchScreenMsgNilResolvesToWorkspaceDefault(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	next, _ := r.Update(SwitchScreenMsg{Workspace: WSPurchasing, Screen: nil})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	if after.screen == nil {
		t.Fatal("nil-target SwitchScreenMsg left a nil screen (would panic on the next Init/Update)")
	}
	if _, ok := after.screen.(*ListScreen); !ok {
		t.Errorf("nil-target SwitchScreenMsg to WSPurchasing gave %T, want the Purchasing *ListScreen default", after.screen)
	}
}

// A screen that claims a local key must win over the colliding global nav key.

func TestLocationCheckinsNStartsCheckinNotNotifications(t *testing.T) {
	r := newTestRoot(NewLocationCheckinsScreen(Deps{}))
	after := press(t, r, "n")

	sc, ok := after.screen.(*LocationCheckinsScreen)
	if !ok {
		t.Fatalf("after 'n' active screen is %T, want *LocationCheckinsScreen (global n=notifications shadowed the local new-check-in)", after.screen)
	}
	if !sc.logging {
		t.Errorf("expected 'n' to open the new-check-in form (logging=true), got logging=false")
	}
}

func TestChecklistRunNAddsNotesNotNotifications(t *testing.T) {
	s := NewChecklistRunScreen(Deps{}, "cmpl-1")
	s.loading = false
	s.checklist = &omsapi.Checklist{Steps: []omsapi.ChecklistStep{{ID: "step-1"}}}
	s.completion = &omsapi.ChecklistCompletion{}
	r := newTestRoot(s)

	after := press(t, r, "n")
	sc, ok := after.screen.(*ChecklistRunScreen)
	if !ok {
		t.Fatalf("after 'n' active screen is %T, want *ChecklistRunScreen (global n=notifications shadowed the local notes key)", after.screen)
	}
	if !sc.addingNotes {
		t.Errorf("expected 'n' to open the notes input (addingNotes=true), got false")
	}
}

func TestChecklistRunFFinalizesNotFirmwareWhenReady(t *testing.T) {
	s := NewChecklistRunScreen(Deps{}, "cmpl-1")
	s.loading = false
	// Required steps done + not yet completed => canFinalize() is true, so the
	// local 'f' must beat the global f=firmware nav.
	s.completion = &omsapi.ChecklistCompletion{
		RequiredStepsCompleted: 1,
		RequiredStepsTotal:     1,
		Status:                 "in_progress",
	}
	r := newTestRoot(s)

	after := press(t, r, "f")
	sc, ok := after.screen.(*ChecklistRunScreen)
	if !ok {
		t.Fatalf("after 'f' active screen is %T, want *ChecklistRunScreen (global f=firmware shadowed the local finalize)", after.screen)
	}
	if !sc.busy {
		t.Errorf("expected 'f' to kick off finalize (busy=true), got busy=false")
	}
}

// Global nav keys must still work on screens that don't claim them locally.

func TestGlobalNOpensNotificationsFromWelcome(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	after := press(t, r, "n")
	if _, ok := after.screen.(*NotificationsScreen); !ok {
		t.Fatalf("after 'n' on welcome active screen is %T, want *NotificationsScreen (global nav regressed)", after.screen)
	}
}

func TestGlobalFOpensFirmwareFromWelcome(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	after := press(t, r, "f")
	if _, ok := after.screen.(*FirmwareScreen); !ok {
		t.Fatalf("after 'f' on welcome active screen is %T, want *FirmwareScreen (global nav regressed)", after.screen)
	}
}

// Uppercase F is the sibling global that opens the ForgeKey device-type
// management list (distinct from lowercase f = firmware). Guards the app.go
// wiring so it can't silently regress.
func TestGlobalShiftFOpensDeviceTypesFromWelcome(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	after := press(t, r, "F")
	if _, ok := after.screen.(*DeviceTypeListScreen); !ok {
		t.Fatalf("after 'F' on welcome active screen is %T, want *DeviceTypeListScreen", after.screen)
	}
}

func TestChecklistRunFFallsThroughToFirmwareWhenNotReady(t *testing.T) {
	s := NewChecklistRunScreen(Deps{}, "cmpl-1")
	s.loading = false
	// Required steps NOT done => canFinalize() is false, so 'f' is not claimed
	// locally and should fall through to the global firmware nav.
	s.completion = &omsapi.ChecklistCompletion{
		RequiredStepsCompleted: 0,
		RequiredStepsTotal:     1,
		Status:                 "in_progress",
	}
	r := newTestRoot(s)

	after := press(t, r, "f")
	if _, ok := after.screen.(*FirmwareScreen); !ok {
		t.Fatalf("after 'f' with finalize unavailable active screen is %T, want *FirmwareScreen (global fallback)", after.screen)
	}
}

// The migrated 's' claim (was a hard-coded special case) still routes to the
// list screen, while other screens keep the global s=settings nav.

func TestListScreenSCyclesSortNotSettings(t *testing.T) {
	ls := NewListScreen(Deps{}, "Test", listScreenSpec{kind: "inventory_items"})
	before := ls.sort
	r := newTestRoot(ls)

	after := press(t, r, "s")
	sc, ok := after.screen.(*ListScreen)
	if !ok {
		t.Fatalf("after 's' active screen is %T, want *ListScreen (global s=settings shadowed the local sort)", after.screen)
	}
	if sc.sort == before {
		t.Errorf("expected 's' to advance the sort mode, still %v", sc.sort)
	}
}

func TestGlobalSOpensSettingsFromWelcome(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	after := press(t, r, "s")
	if _, ok := after.screen.(*SettingsScreen); !ok {
		t.Fatalf("after 's' on welcome active screen is %T, want *SettingsScreen (global nav regressed)", after.screen)
	}
}
