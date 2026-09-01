package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// EPaperPanelsScreen is the staff-side e-paper panel fleet view. Mirrors
// ForgeKeyEPaperPanelsPage on the web: per-panel asset binding, battery
// (tri-state: percent, "no sensor" badge, or "—"), firmware (reported +
// pending target), a retire/reactivate toggle, and an asset-rebind
// picker.
type EPaperPanelsScreen struct {
	deps    Deps
	rows    []forgekeyapi.EPaperDisplay
	cursor  int
	loading bool
	loadErr string

	// Bind picker — opens over the panel list when `b` is pressed.
	// `bindSeq` is a monotonic request counter so a slow search
	// response from a stale query string is dropped on arrival rather
	// than overwriting the freshest results.
	binding     bool
	bindPanelID string
	bindLabel   string // textual label for the current panel ("Lathe", or display id slice)
	bindInput   textinput.Model
	bindResults []omsapi.Asset
	bindCursor  int
	bindSeq     int
	bindErr     string
}

type epaperPanelsLoadedMsg struct {
	rows []forgekeyapi.EPaperDisplay
	err  error
}

type epaperBindResultsMsg struct {
	seq     int
	results []omsapi.Asset
	err     error
}

type epaperBoundMsg struct {
	label string
	err   error
}

func NewEPaperPanelsScreen(deps Deps) *EPaperPanelsScreen {
	return &EPaperPanelsScreen{deps: deps, loading: true}
}

func (s *EPaperPanelsScreen) Title() string { return "ePaper panels" }

// WantsRawInput keeps the global hotkey router (j/k/B/V/K/P/Q/D/m/…)
// from stealing characters while the operator is typing into the
// asset-picker textinput.
func (s *EPaperPanelsScreen) WantsRawInput() bool { return s.binding }

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
	case epaperBindResultsMsg:
		// Drop stale responses — the user kept typing and a newer
		// search has already been fired.
		if m.seq != s.bindSeq {
			return s, nil
		}
		if m.err != nil {
			s.bindErr = m.err.Error()
			s.bindResults = nil
			return s, nil
		}
		s.bindErr = ""
		s.bindResults = m.results
		if s.bindCursor >= len(s.bindResults) {
			s.bindCursor = 0
		}
		return s, nil
	case epaperBoundMsg:
		s.binding = false
		if m.err != nil {
			return s, Status("bind failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(Status("bound to "+m.label, StatusOK), s.load())
	case tea.KeyMsg:
		if s.binding {
			return s.updateBind(m)
		}
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
		case "b":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			return s.openBindPicker()
		}
	}
	return s, nil
}

// openBindPicker enters bind mode for the row under the cursor, seeded
// with an empty asset search.
func (s *EPaperPanelsScreen) openBindPicker() (Screen, tea.Cmd) {
	row := s.rows[s.cursor]
	s.binding = true
	s.bindPanelID = row.ID
	s.bindCursor = 0
	s.bindResults = nil
	s.bindErr = ""
	// Label the picker with whatever identifies the panel for a human —
	// asset name if bound, else a slice of the display id.
	s.bindLabel = "display " + row.ID[:8]
	if row.AssetName != nil && *row.AssetName != "" {
		s.bindLabel = *row.AssetName
	}
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "search asset name (empty = top 15 by name)"
	in.CharLimit = 80
	in.Focus()
	s.bindInput = in
	return s, tea.Batch(textinput.Blink, s.searchAssets())
}

// updateBind owns the key handling while the asset picker is open.
// Down/Up move the result cursor; Enter binds the selected asset; Esc
// cancels. Every other keystroke is forwarded to the textinput and
// triggers a fresh asset search (cheap — listAssets is paginated and
// the backend search is indexed).
func (s *EPaperPanelsScreen) updateBind(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.binding = false
		return s, nil
	case tea.KeyDown:
		if s.bindCursor < len(s.bindResults)-1 {
			s.bindCursor++
		}
		return s, nil
	case tea.KeyUp:
		if s.bindCursor > 0 {
			s.bindCursor--
		}
		return s, nil
	case tea.KeyEnter:
		if s.bindCursor >= len(s.bindResults) {
			return s, nil
		}
		return s, s.runBind(s.bindResults[s.bindCursor])
	}
	prev := s.bindInput.Value()
	var cmd tea.Cmd
	s.bindInput, cmd = s.bindInput.Update(m)
	if s.bindInput.Value() != prev {
		return s, tea.Batch(cmd, s.searchAssets())
	}
	return s, cmd
}

func (s *EPaperPanelsScreen) searchAssets() tea.Cmd {
	s.bindSeq++
	seq := s.bindSeq
	query := strings.TrimSpace(s.bindInput.Value())
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		q := url.Values{
			"is_active": []string{"true"},
			"page_size": []string{"15"},
			"ordering":  []string{"name"},
		}
		if query != "" {
			q.Set("search", query)
		}
		page, err := deps.OMS.ListAssets(ctx, q)
		if err != nil {
			return epaperBindResultsMsg{seq: seq, err: err}
		}
		return epaperBindResultsMsg{seq: seq, results: page.Results}
	}
}

func (s *EPaperPanelsScreen) runBind(asset omsapi.Asset) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	displayID := s.bindPanelID
	assetID := fmt.Sprint(asset.ID)
	fallbackLabel := asset.Name
	return func() tea.Msg {
		resp, err := deps.ForgeKey.BindEPaperDisplay(ctx, displayID, assetID)
		if err != nil {
			return epaperBoundMsg{label: fallbackLabel, err: err}
		}
		return epaperBoundMsg{label: resp.AssetName}
	}
}

func (s *EPaperPanelsScreen) View() string {
	if s.binding {
		return s.viewBind()
	}
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
	b.WriteString("\n" + StyleMuted.Render("j/k move · b bind to asset · t retire/reactivate · r refresh · esc back"))
	return b.String()
}

func (s *EPaperPanelsScreen) viewBind() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Bind "+s.bindLabel+" to asset") + "\n\n")
	b.WriteString(StyleMuted.Render("Search: ") + s.bindInput.View() + "\n\n")
	if s.bindErr != "" {
		b.WriteString(StyleStatusError.Render("Error: "+s.bindErr) + "\n\n")
	}
	if len(s.bindResults) == 0 {
		b.WriteString(StyleMuted.Render("(no matching active assets — narrow the query or clear it to see the top 15)") + "\n")
	} else {
		for i, a := range s.bindResults {
			caret := "  "
			if i == s.bindCursor {
				caret = "▸ "
			}
			line := caret + a.Name
			if a.AssetTag != "" {
				line += " " + StyleMuted.Render("("+a.AssetTag+")")
			}
			if a.LocationName != "" {
				line += "  " + StyleMuted.Render("· "+a.LocationName)
			}
			if i == s.bindCursor {
				line = StyleSidebarItemActive.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("type to search · ↑/↓ move · enter bind · esc cancel"))
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
