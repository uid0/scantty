package tui

import (
	"strings"
	"testing"
)

func TestReportsHub_RendersEntries(t *testing.T) {
	s := NewReportsScreen(Deps{})
	if len(s.items) != 9 {
		t.Fatalf("hub should list 9 reports, got %d", len(s.items))
	}
	out := s.View()
	for _, want := range []string{
		"Analytics Pulse", "Inventory report", "Purchasing report", "Reorders analytics",
		"Asset report", "ForgeKey fleet", "Serialized forecast", "Demand forecast",
		"Reorder alerts",
		"[p]", "[i]", "[c]", "[r]", "[a]", "[d]", "[f]", "[m]", "[n]",
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
	for _, k := range []string{"p", "i", "c", "r", "a", "d", "f", "m", "n"} {
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

// TestReportsHub_ForecastHotkeys: the two demand-forecast entries also sit on
// keys the root uses globally (m = profile, n = notifications). While the hub is
// active its HandlesKey claim must win, opening the forecast screens instead.
func TestReportsHub_ForecastHotkeys(t *testing.T) {
	cases := []struct {
		key       string
		wantTitle string
		wantAlert bool
	}{
		{"m", "Demand forecast", false},
		{"n", "Reorder alerts", true},
	}
	for _, tc := range cases {
		r := newTestRoot(NewReportsScreen(Deps{}))
		next, cmd := r.Update(mtKey(tc.key))
		if _, ok := next.(Root).screen.(*ReportsScreen); !ok {
			t.Fatalf("%q on the hub should not fire the global; screen = %T", tc.key, next.(Root).screen)
		}
		if cmd == nil {
			t.Fatalf("%q should fire a switch cmd", tc.key)
		}
		sm, ok := cmd().(SwitchScreenMsg)
		if !ok {
			t.Fatalf("msg = %T, want SwitchScreenMsg", cmd())
		}
		df, ok := sm.Screen.(*DemandForecastScreen)
		if !ok {
			t.Fatalf("%q should open the demand-forecast screen, got %T", tc.key, sm.Screen)
		}
		if df.alerts != tc.wantAlert || df.Title() != tc.wantTitle {
			t.Errorf("%q opened alerts=%v title=%q, want %v/%q",
				tc.key, df.alerts, df.Title(), tc.wantAlert, tc.wantTitle)
		}
	}
}

// TestRoot_MenuOpensReportsHub locks the nav wiring. The digit 8 that used to
// open this hub went with the letters in phase 3; the sidebar row is the door.
func TestRoot_MenuOpensReportsHub(t *testing.T) {
	r := openFromMenu(t, newTestRoot(NewWelcomeScreen()), WSReports, "")
	if _, ok := r.screen.(*ReportsScreen); !ok {
		t.Fatalf("the Reports workspace row opened %T, want the Reports hub", r.screen)
	}
}
