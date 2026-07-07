package tui

import (
	"context"
	"strings"
)

// forgekey_fleet_report.go surfaces the ForgeKey devices/fleet-summary/
// aggregate — the deferred #84 report — as a tab on the shared generic
// ReportTableScreen (report_table.go). It mirrors the web ForgeKey Fleet
// Dashboard (ForgeKeyDashboardPage): headline device/e-paper/firmware counters,
// breakdowns by type / capability / firmware, the prioritised "needs attention"
// feed, and recent command / firmware-update activity.
//
// The web renders this as stat cards + cards + tables; a terminal can't draw
// the cards, so — like the other report pages — each section becomes an aligned
// table. The summary envelope is a single object rather than a row array, so the
// Overview tab reshapes its scalars into Metric/Value rows (the same pattern the
// reorders transparency/logistics tabs use), while the breakdown / feed / recent
// sections render their embedded arrays directly.
//
// Every tab re-fetches the whole envelope on load (matching the reorders
// transparency tabs, which each re-hit their shared endpoint) so each tab stays
// independently loadable and refreshable via 'r'.

// fkFleetWhen trims an isoformat timestamp to minute precision
// ("2026-07-07T03:26:11.5+00:00" → "2026-07-07 03:26"), the granularity that
// matters for connectivity/OTA triage. Empty (a null timestamp — e.g. a
// never-seen device) renders as the table's em-dash; an unexpected bare date
// passes through via dateOnly.
func fkFleetWhen(iso string) string {
	if strings.TrimSpace(iso) == "" {
		return "—"
	}
	if len(iso) >= 16 && iso[10] == 'T' {
		return iso[:10] + " " + iso[11:16]
	}
	return dateOnly(iso)
}

// NewForgeKeyFleetReportScreen mirrors the web ForgeKey Fleet Dashboard as one
// tabbed report: an Overview roll-up, the three device breakdowns, the unified
// needs-attention feed, and recent commands / firmware updates. Backed by the
// single devices/fleet-summary/ aggregate (IsAuthenticated — any signed-in user).
func NewForgeKeyFleetReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "ForgeKey fleet", []reportTab{
		{
			label:   "Overview",
			columns: []reportColumn{{"Metric", alignLeft}, {"Value", alignRight}},
			note:    "Fleet health roll-up · counts are live at the generated time.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				fs, err := deps.ForgeKey.FleetSummary(ctx)
				if err != nil {
					return nil, err
				}
				d := fs.Devices
				return [][]string{
					{"Generated", fkFleetWhen(fs.GeneratedAt)},
					{"Devices total", itoa(d.Total)},
					{"Active", itoa(d.Active)},
					{"Online", itoa(d.Online)},
					{"Offline", itoa(d.Offline)},
					{"Never seen", itoa(d.NeverSeen)},
					{"e-Paper panels", itoa(fs.EPaper.Total)},
					{"e-Paper bound", itoa(fs.EPaper.Bound)},
					{"e-Paper unbound", itoa(fs.EPaper.Unbound)},
					{"e-Paper low battery", itoa(fs.EPaper.LowBattery)},
					{"Firmware updates in flight", itoa(fs.Firmware.UpdatesInFlight)},
					{"Firmware recent failures", itoa(fs.Firmware.RecentFailures)},
				}, nil
			},
		},
		{
			label:   "By type",
			columns: []reportColumn{{"Code", alignLeft}, {"Type", alignLeft}, {"Devices", alignRight}, {"Online", alignRight}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				fs, err := deps.ForgeKey.FleetSummary(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(fs.Devices.ByType))
				for i, t := range fs.Devices.ByType {
					out[i] = []string{orDash(t.Code), orDash(t.Name), itoa(t.Count), itoa(t.Online)}
				}
				return out, nil
			},
		},
		{
			label:   "By capability",
			columns: []reportColumn{{"Capability", alignLeft}, {"Devices", alignRight}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				fs, err := deps.ForgeKey.FleetSummary(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(fs.Devices.ByCapability))
				for i, c := range fs.Devices.ByCapability {
					out[i] = []string{orDash(c.Capability), itoa(c.Count)}
				}
				return out, nil
			},
		},
		{
			label:   "By firmware",
			columns: []reportColumn{{"Version", alignLeft}, {"Devices", alignRight}},
			note:    "\"unknown\" = devices that never reported a firmware version.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				fs, err := deps.ForgeKey.FleetSummary(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(fs.Devices.ByFirmware))
				for i, f := range fs.Devices.ByFirmware {
					out[i] = []string{orDash(f.Version), itoa(f.Count)}
				}
				return out, nil
			},
		},
		{
			label:   "Attention",
			columns: []reportColumn{{"Kind", alignLeft}, {"Device / panel", alignLeft}, {"Detail", alignLeft}, {"When", alignLeft}},
			note:    "Offline: MAC · last seen. Low battery: charge% · last report. OTA failed: version · requested. Ordered by priority.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				fs, err := deps.ForgeKey.FleetSummary(ctx)
				if err != nil {
					return nil, err
				}
				a := fs.Attention
				out := make([][]string, 0, len(a.Offline)+len(a.LowBattery)+len(a.OTAFailed))
				for _, o := range a.Offline {
					out = append(out, []string{"offline", orDash(o.Name), orDash(o.MacAddress), fkFleetWhen(o.LastSeen)})
				}
				for _, p := range a.LowBattery {
					name := p.AssetName
					if strings.TrimSpace(name) == "" {
						name = "unbound panel"
					}
					batt := "—"
					if p.BatteryPercent != nil {
						batt = itoa(*p.BatteryPercent) + "%"
					}
					out = append(out, []string{"low battery", name, batt, fkFleetWhen(p.LastBatteryAt)})
				}
				for _, u := range a.OTAFailed {
					out = append(out, []string{"OTA failed", orDash(u.Name), orDash(u.Version), fkFleetWhen(u.RequestedAt)})
				}
				return out, nil
			},
		},
		{
			label:   "Recent cmds",
			columns: []reportColumn{{"Device", alignLeft}, {"Command", alignLeft}, {"Ack", alignLeft}, {"Sent", alignLeft}, {"By", alignLeft}},
			note:    "Last 10 device commands.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				fs, err := deps.ForgeKey.FleetSummary(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(fs.RecentCommands))
				for i, c := range fs.RecentCommands {
					out[i] = []string{orDash(c.DeviceName), orDash(c.Command), orDash(c.AckStatus), fkFleetWhen(c.SentAt), orDash(c.SentBy)}
				}
				return out, nil
			},
		},
		{
			label:   "Recent updates",
			columns: []reportColumn{{"Device", alignLeft}, {"Version", alignLeft}, {"Status", alignLeft}, {"Requested", alignLeft}, {"By", alignLeft}},
			note:    "Last 10 firmware updates.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				fs, err := deps.ForgeKey.FleetSummary(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(fs.RecentUpdates))
				for i, u := range fs.RecentUpdates {
					out[i] = []string{orDash(u.DeviceName), orDash(u.Version), orDash(u.Status), fkFleetWhen(u.RequestedAt), orDash(u.RequestedBy)}
				}
				return out, nil
			},
		},
	})
}
