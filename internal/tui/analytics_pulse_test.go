package tui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func samplePulse() *omsapi.AnalyticsPulse {
	ih, id, since := 100, 90, 97
	cid := 4
	return &omsapi.AnalyticsPulse{
		Summary: omsapi.AnalyticsValueSummary{
			PeriodStart:            "2026-06-01",
			PeriodEnd:              "2026-06-30",
			InternalCompletedCount: 12,
			InternalNetValue:       "2600.00",
			ExternalClosedCount:    3,
			ExternalActualCost:     "1250.50",
			TotalValueToMakerspace: "3850.50",
		},
		WOVolumeTrend: []omsapi.AnalyticsBucket{
			{BucketStart: "2026-06-01", WOCount: 12},
		},
		TopUsers: []omsapi.AnalyticsTopUser{
			{Username: "welder", FullName: "Wanda Welder", WOCount: 5},
		},
		Utilization: []omsapi.AnalyticsUtilizationRow{
			{AssetID: "a-1", AssetName: "Lathe", Category: "Machining", HoursUsed: 40, CompletedWOCount: 2, Status: "operational"},
		},
		CategorySpend: []omsapi.AnalyticsCategorySpend{
			{CategoryID: &cid, CategoryName: "Gases", InternalEstimated: "120.00", ExternalEstimated: "300.00", ExternalActual: "275.25", InternalWOCount: 6, ExternalWOCount: 1},
		},
		MaintenanceForecast: []omsapi.AnalyticsMaintenanceForecast{
			{AssetID: "a-1", AssetName: "Lathe", Category: "Machining", HoursUsed: 40, IntervalHours: &ih, IntervalDays: &id, DaysSinceLastWO: &since, DueReason: "both"},
		},
		MonthlyBudget: "5000.00",
	}
}

func TestAnalyticsPulse_RendersAllSections(t *testing.T) {
	s := NewAnalyticsPulseScreen(Deps{})
	s.pulse = samplePulse()
	body := s.renderPulse()
	for _, want := range []string{
		"Analytics Pulse", "2026-06-01 → 2026-06-30",
		"Value summary", "Total value to makerspace", "$3,850.50",
		"Monthly budget", "$1,250.50 / $5,000.00", // spent / budget
		"WO volume trend", "Top contributors", "Wanda Welder",
		"Equipment utilization", "Machining", "40",
		"Category spend", "Gases", "$275.25",
		"Maintenance forecast", "both",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pulse body missing %q:\n%s", want, body)
		}
	}
	// Budget percentage = 1250.50 / 5000 * 100 = 25.01%.
	if !strings.Contains(body, "25.01%") {
		t.Errorf("pulse body should show budget %%:\n%s", body)
	}
}

func TestAnalyticsPulse_EmptyAggregationsShowNone(t *testing.T) {
	s := NewAnalyticsPulseScreen(Deps{})
	s.pulse = &omsapi.AnalyticsPulse{
		Summary:       omsapi.AnalyticsValueSummary{PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30"},
		MonthlyBudget: "",
	}
	body := s.renderPulse()
	if !strings.Contains(body, "(none)") {
		t.Errorf("empty aggregations should render (none):\n%s", body)
	}
	if !strings.Contains(body, "Not set") {
		t.Errorf("blank monthly_budget should render Not set:\n%s", body)
	}
}

func TestAnalyticsPulse_ForbiddenNotice(t *testing.T) {
	s := NewAnalyticsPulseScreen(Deps{})
	// Simulate the 403 the backend returns for a non-analytics-viewer.
	s.Update(analyticsPulseLoadedMsg{err: &omsapi.APIError{Status: http.StatusForbidden}})
	if !s.forbidden {
		t.Fatalf("403 should set the forbidden flag")
	}
	out := s.View()
	if !strings.Contains(out, "staff-only") {
		t.Errorf("forbidden view should explain the staff gate:\n%s", out)
	}
}

func TestAnalyticsPulse_HandlesKeyAndEsc(t *testing.T) {
	s := NewAnalyticsPulseScreen(Deps{})
	if !s.HandlesKey("G") || !s.HandlesKey("esc") {
		t.Errorf("pulse should claim G and esc")
	}
	if s.HandlesKey("j") {
		t.Errorf("pulse should not claim j")
	}
	_, cmd := s.Update(mtKey("esc"))
	if cmd == nil {
		t.Fatal("esc should fire a cmd")
	}
	sm, ok := cmd().(SwitchScreenMsg)
	if !ok || sm.Workspace != WSReports {
		t.Fatalf("esc should switch to the reports hub, got %T %v", cmd(), sm.Workspace)
	}
	if _, ok := sm.Screen.(*ReportsScreen); !ok {
		t.Errorf("esc should return to the hub, got %T", sm.Screen)
	}
}
