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

// --- esc back-stack (sc-3oab) -------------------------------------------------

// fakeScreen is a minimal Screen used to exercise the back-stack bookkeeping in
// isolation, without pulling in a real screen's data dependencies. The flags
// opt into the two behaviors that suppress history recording.
type fakeScreen struct {
	name     string
	rawInput bool
	ownsEsc  bool
}

func (f *fakeScreen) Init() tea.Cmd                    { return nil }
func (f *fakeScreen) Update(tea.Msg) (Screen, tea.Cmd) { return f, nil }
func (f *fakeScreen) View() string                     { return f.name }
func (f *fakeScreen) Title() string                    { return f.name }
func (f *fakeScreen) WantsRawInput() bool              { return f.rawInput }
func (f *fakeScreen) HandlesKey(key string) bool       { return f.ownsEsc && key == "esc" }

// esc is a back button: after drilling several workspaces deep, it pops exactly
// one level — restoring the previous screen AND its nav highlight — instead of
// jumping all the way home the way it used to.
func TestEscPopsOneLevelNotHome(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	r = press(t, r, "2") // Inventory list
	r = press(t, r, "4") // Assets list

	r = press(t, r, "esc")
	ls, ok := r.screen.(*ListScreen)
	if !ok {
		t.Fatalf("after esc active screen is %T, want the Inventory *ListScreen (esc jumped past one level)", r.screen)
	}
	if ls.Title() != "Inventory" {
		t.Errorf("after esc list title = %q, want %q (popped the wrong level)", ls.Title(), "Inventory")
	}
	if got := r.nav.Active(); got != WSInventory {
		t.Errorf("after esc nav workspace = %q, want %q (esc did not restore the previous workspace)", got, WSInventory)
	}
}

// Repeated esc unwinds the stack one frame at a time down to the home screen,
// then becomes a harmless no-op — never trapping the user or panicking.
func TestEscUnwindsToHomeThenNoops(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	r = press(t, r, "2") // Inventory
	r = press(t, r, "3") // Purchasing

	r = press(t, r, "esc") // -> Inventory
	if ls, ok := r.screen.(*ListScreen); !ok || ls.Title() != "Inventory" {
		t.Fatalf("first esc active screen is %T (title mismatch), want Inventory *ListScreen", r.screen)
	}
	r = press(t, r, "esc") // -> Welcome
	if _, ok := r.screen.(*WelcomeScreen); !ok {
		t.Fatalf("second esc active screen is %T, want *WelcomeScreen (bottom of stack)", r.screen)
	}
	r = press(t, r, "esc") // no-op at home
	if _, ok := r.screen.(*WelcomeScreen); !ok {
		t.Fatalf("third esc active screen is %T, want *WelcomeScreen (esc at home must be a no-op)", r.screen)
	}
	if len(r.history) != 0 {
		t.Errorf("history depth = %d after unwinding, want 0", len(r.history))
	}
}

// With an empty stack, esc from the home screen changes nothing (no rebuild, no
// trap).
func TestEscOnWelcomeWithEmptyStackIsNoop(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	home := r.screen
	r = press(t, r, "esc")
	if r.screen != home {
		t.Errorf("esc on home with empty stack swapped the screen (%T); want the same instance untouched", r.screen)
	}
}

// With an empty stack but sitting on a non-home top-level screen (e.g. entered
// directly), esc falls back to the home screen rather than doing nothing.
func TestEscFromTopLevelWithEmptyStackFallsHome(t *testing.T) {
	ls := NewListScreen(Deps{}, "Inventory", listScreenSpec{kind: "inventory_items"})
	r := newTestRoot(ls)
	r = press(t, r, "esc")
	if _, ok := r.screen.(*WelcomeScreen); !ok {
		t.Fatalf("esc from a top-level screen with empty stack gave %T, want *WelcomeScreen fallback", r.screen)
	}
}

// A drill-down via SwitchScreenMsg (list -> detail) records the list, so esc
// from the detail returns to it.
func TestDrillDownThenEscReturnsToList(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	r = press(t, r, "4") // Assets list

	next, _ := r.Update(SwitchScreenMsg{Workspace: WSAssets, Screen: NewAssetDetailScreen(Deps{}, "123")})
	r = next.(Root)
	if _, ok := r.screen.(*AssetDetailScreen); !ok {
		t.Fatalf("after drill-down active screen is %T, want *AssetDetailScreen", r.screen)
	}

	r = press(t, r, "esc")
	ls, ok := r.screen.(*ListScreen)
	if !ok {
		t.Fatalf("after esc from detail active screen is %T, want the Assets *ListScreen", r.screen)
	}
	if ls.Title() != "Assets" {
		t.Errorf("after esc list title = %q, want %q", ls.Title(), "Assets")
	}
}

// A form/picker owns its own esc (RawInput) and cancels back to its list via
// SwitchTo. It must never be recorded as a back target, so esc after cancelling
// does not re-open the form the user just dismissed.
func TestFormNotRecordedAsBackTarget(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	r = press(t, r, "N") // create-PO form (RawInput)
	if _, ok := r.screen.(*PurchaseOrderCreateScreen); !ok {
		t.Fatalf("after 'N' active screen is %T, want *PurchaseOrderCreateScreen", r.screen)
	}

	// The form's cancel/save leaves via SwitchTo(WSPurchasing, ...).
	next, _ := r.Update(SwitchScreenMsg{Workspace: WSPurchasing, Screen: nil})
	r = next.(Root)
	if _, ok := r.screen.(*ListScreen); !ok {
		t.Fatalf("after form-cancel active screen is %T, want the Purchasing *ListScreen", r.screen)
	}

	r = press(t, r, "esc")
	if _, ok := r.screen.(*PurchaseOrderCreateScreen); ok {
		t.Fatal("esc after cancelling the form re-opened it; forms must not be back targets")
	}
	if _, ok := r.screen.(*WelcomeScreen); !ok {
		t.Fatalf("after esc active screen is %T, want *WelcomeScreen (the frame recorded before the form)", r.screen)
	}
}

// recordHistory drops transient screens (raw-input forms, and screens that own
// esc via HandlesKey) and keeps ordinary ones.
func TestRecordHistorySkipsTransientScreens(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())

	r.recordHistory(&fakeScreen{name: "form", rawInput: true}, WSInventory)
	if len(r.history) != 0 {
		t.Errorf("a raw-input form was recorded (depth %d); want 0", len(r.history))
	}
	r.recordHistory(&fakeScreen{name: "reptable", ownsEsc: true}, WSReports)
	if len(r.history) != 0 {
		t.Errorf("an esc-owning screen was recorded (depth %d); want 0", len(r.history))
	}
	r.recordHistory(nil, WSInventory)
	if len(r.history) != 0 {
		t.Errorf("a nil screen was recorded (depth %d); want 0", len(r.history))
	}
	r.recordHistory(&fakeScreen{name: "list"}, WSInventory)
	if len(r.history) != 1 {
		t.Fatalf("an ordinary screen was not recorded (depth %d); want 1", len(r.history))
	}
	if r.history[0].ws != WSInventory {
		t.Errorf("recorded workspace = %q, want %q", r.history[0].ws, WSInventory)
	}
}
