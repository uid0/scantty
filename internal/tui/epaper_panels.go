package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

// EPaperPanelsScreen is the staff-side e-paper panel fleet view. Mirrors
// ForgeKeyEPaperPanelsPage on the web: per-panel asset binding, battery
// (tri-state: percent, "no sensor" badge, or "—"), firmware (reported +
// pending target), and a retire/reactivate toggle. Binding to an asset
// from inside the TUI needs an asset picker — out of scope for v1, link
// users to the QR / web flow instead.
type EPaperPanelsScreen struct {
	deps    Deps
	rows    []forgekeyapi.EPaperDisplay
	cursor  int
	loading bool
	loadErr string
}

type epaperPanelsLoadedMsg struct {
	rows []forgekeyapi.EPaperDisplay
	err  error
}

func NewEPaperPanelsScreen(deps Deps) *EPaperPanelsScreen {
	return &EPaperPanelsScreen{deps: deps, loading: true}
}

func (s *EPaperPanelsScreen) Title() string { return "ePaper panels" }

func (s *EPaperPanelsScreen) Init() tea.Cmd { return s.load() }

func (s *EPaperPanelsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.ForgeKey.ListEPaperDisplays(ctx)
		return epaperPanelsLoadedMsg{rows: rows, err: err}
	}
}

func (s *EPaperPanelsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case epaperPanelsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case fkActionResultMsg:
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
		}
		// Reload after a successful retire/reactivate so battery / last_seen
		// also refresh — the backend returns the row but a full reload also
		// pulls any other rows that changed concurrently.
		return s, tea.Batch(Status(m.action, StatusOK), s.load())
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading = true
			return s, s.load()
		case "t":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			id := row.ID
			next := !row.IsActive
			verb := "retired"
			if next {
				verb = "reactivated"
			}
			label := row.ID
			if row.AssetName != nil && *row.AssetName != "" {
				label = *row.AssetName
			}
			return s, func() tea.Msg {
				_, err := deps.ForgeKey.SetEPaperActive(ctx, id, next)
				return fkActionResultMsg{action: fmt.Sprintf("%s %s", verb, label), err: err}
			}
		}
	}
	return s, nil
}

func (s *EPaperPanelsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading e-paper panels…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No e-paper panels registered yet.") + "\n\n" + StyleMuted.Render("r refresh · esc back")
	}
	var b strings.Builder
	for i, r := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		asset := "unbound"
		if r.AssetName != nil && *r.AssetName != "" {
			asset = *r.AssetName
			if r.AssetTag != nil && *r.AssetTag != "" {
				asset += " " + StyleMuted.Render("("+*r.AssetTag+")")
			}
		} else {
			asset = StyleMuted.Render(asset)
		}

		var status string
		if r.IsActive {
			status = StyleStatusOK.Render("active")
		} else {
			status = StyleMuted.Render("retired")
		}

		line := caret + asset + "  [" + status + "]"
		if i == s.cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")

		// Detail line — battery (tri-state), firmware, last health.
		b.WriteString("    " + StyleMuted.Render("battery: ") + renderEPaperBattery(r))
		b.WriteString("  " + StyleMuted.Render("firmware: ") + renderEPaperFirmware(r))
		b.WriteString("\n")
		b.WriteString("    " + StyleMuted.Render("last health: ") + renderRelative(r.LastHealthAt))
		if r.LastImageAt != nil {
			b.WriteString("  " + StyleMuted.Render("last image: ") + renderRelative(r.LastImageAt))
		}
		if r.DeviceMACAddress != nil && *r.DeviceMACAddress != "" {
			b.WriteString("  " + StyleMuted.Render("MAC: ") + *r.DeviceMACAddress)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · t retire/reactivate · r refresh · esc back"))
	return b.String()
}

func renderEPaperBattery(r forgekeyapi.EPaperDisplay) string {
	if r.BatteryPercent != nil {
		pct := fmt.Sprintf("%d%%", *r.BatteryPercent)
		if r.IsLowBattery {
			return StyleStatusWarn.Render(pct)
		}
		return pct
	}
	if r.BatteryAvailable != nil && !*r.BatteryAvailable {
		reason := r.BatteryUnavailableReason
		if reason == "" {
			reason = "no reason reported"
		}
		return StyleMuted.Render("no sensor (" + reason + ")")
	}
	return StyleMuted.Render("—")
}

func renderEPaperFirmware(r forgekeyapi.EPaperDisplay) string {
	if r.FirmwareVersion == "" {
		return StyleMuted.Render("—")
	}
	if r.TargetFirmwareVersionString != nil && *r.TargetFirmwareVersionString != "" &&
		*r.TargetFirmwareVersionString != r.FirmwareVersion {
		return r.FirmwareVersion + StyleStatusWarn.Render(" → "+*r.TargetFirmwareVersionString)
	}
	return r.FirmwareVersion
}

// renderRelative formats a *time.Time as a friendly "Xs/m/h/d ago" or
// "never" when nil. Mirrors the formatRelative helper on the web page.
func renderRelative(t *time.Time) string {
	if t == nil || t.IsZero() {
		return StyleMuted.Render("never")
	}
	secs := int(time.Since(*t).Seconds())
	if secs < 0 {
		secs = 0
	}
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds ago", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm ago", secs/60)
	case secs < 86400:
		return fmt.Sprintf("%dh ago", secs/3600)
	default:
		return fmt.Sprintf("%dd ago", secs/86400)
	}
}
