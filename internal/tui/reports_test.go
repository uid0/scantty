package tui

import (
	"strings"
	"testing"
)

func TestReportsHub_RendersEntries(t *testing.T) {
	s := NewReportsScreen(Deps{})
	if len(s.items) != 7 {
		t.Fatalf("hub should list 7 reports, got %d", len(s.items))
	}
	out := s.View()
	for _, want := range []string{
		"Analytics Pulse", "Inventory report", "Purchasing report", "Reorders analytics",
		"Asset report", "ForgeKey fleet", "Serialized forecast",
		"[p]", "[i]", "[c]", "[r]", "[a]", "[d]", "[f]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hub view missing %q:\n%s", want, out)
		}
	}
}

func TestReportsHub_EnterOpensPulse(t *testing.T) {
	s := NewReportsScreen(Deps{})
	_, cmd := s.Update(mtKey("enter")) // cursor at 0 = Analytics Pulse
	if cmd == nil {
		t.Fatal("enter should open the selected report")
	}
	sm, ok := cmd().(SwitchScreenMsg)
	if !ok || sm.Workspace != WSReports {
		t.Fatalf("enter msg = %T, want SwitchScreenMsg(reports)", cmd())
	}
	if _, ok := sm.Screen.(*AnalyticsPulseScreen); !ok {
		t.Errorf("first entry should open the pulse, got %T", sm.Screen)
	}
}

func TestReportsHub_HotkeyOpensInventoryReport(t *testing.T) {
	s := NewReportsScreen(Deps{})
	_, cmd := s.Update(mtKey("i"))
	if cmd == nil {
		t.Fatal("i should open the inventory report")
	}
	sm := cmd().(SwitchScreenMsg)
	rt, ok := sm.Screen.(*ReportTableScreen)
	if !ok {
		t.Fatalf("i should open a ReportTableScreen, got %T", sm.Screen)
	}
	if rt.Title() != "Inventory report" {
		t.Errorf("title = %q, want Inventory report", rt.Title())
	}
}

func TestReportsHub_HandlesKey(t *testing.T) {
	s := NewReportsScreen(Deps{})
	for _, k := range []string{"p", "i", "c", "r", "a", "d", "f"} {
		if !s.HandlesKey(k) {
			t.Errorf("hub should claim entry hotkey %q", k)
		}
	}
	for _, k := range []string{"j", "esc", "g", "z"} {
		if s.HandlesKey(k) {
			t.Errorf("hub should not claim %q", k)
		}
	}
}

// TestReportsHub_AKeyOpensAssetReportNotAuthorizations is the key regression:
// 'a' is the global authorizations hotkey, but while the Reports hub is active
// its HandlesKey claim routes 'a' to the hub so it opens the Asset report.
func TestReportsHub_AKeyOpensAssetReportNotAuthorizations(t *testing.T) {
	r := newTestRoot(NewReportsScreen(Deps{}))
	next, cmd := r.Update(mtKey("a"))
	rootAfter := next.(Root)
	// The hub claimed 'a', so the root screen is still the hub (the switch
	// happens via the returned cmd), NOT the AuthorizationsScreen.
	if _, ok := rootAfter.screen.(*ReportsScreen); !ok {
		t.Fatalf("'a' on the hub should not open authorizations; screen = %T", rootAfter.screen)
	}
	if cmd == nil {
		t.Fatal("'a' should fire a switch cmd")
	}
	sm, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("msg = %T, want SwitchScreenMsg", cmd())
	}
	rt, ok := sm.Screen.(*ReportTableScreen)
	if !ok || rt.Title() != "Asset report" {
		t.Errorf("'a' should open the Asset report, got %T", sm.Screen)
	}
}

// TestRoot_Digit8OpensReportsHub locks the nav wiring: pressing 8 opens the hub.
func TestRoot_Digit8OpensReportsHub(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	r = press(t, r, "8")
	if _, ok := r.screen.(*ReportsScreen); !ok {
		t.Fatalf("digit 8 should open the Reports hub, got %T", r.screen)
	}
}
