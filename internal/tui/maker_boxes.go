package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// MakerBoxesScreen surfaces the per-member bin assignments + the
// scanner-driven (bin, user) lookup. Default view is the full
// directory; `s` opens a two-field scan form for the canonical
// shop-floor "I have this bin, who owns it / is their slot current"
// workflow; `p` opens the pre-conversion scan that queues a member
// (badge or username) for later bin allocation.
//
// Hotkey lives at capital `B`; lowercase `b` is taken by the
// ForgeKey device detail screen's blink action.
type MakerBoxesScreen struct {
	deps    Deps
	rows    []omsapi.MakerBox
	cursor  int
	loading bool
	loadErr string

	scanning   bool
	binInput   textinput.Model
	userInput  textinput.Model
	focusUser  bool
	scanResult *omsapi.MakerBoxScanResult
	scanErr    string

	// pre-conversion (queue a member for bin allocation)
	preConverting bool
	preInput      textinput.Model
	preResult     *omsapi.MakerBox
	preErr        string
}

type makerBoxesLoadedMsg struct {
	rows []omsapi.MakerBox
	err  error
}

type makerBoxScanMsg struct {
	result *omsapi.MakerBoxScanResult
	err    error
}

type makerBoxPreConvertMsg struct {
	result *omsapi.MakerBox
	err    error
}

func NewMakerBoxesScreen(deps Deps) *MakerBoxesScreen {
	return &MakerBoxesScreen{deps: deps, loading: true}
}

func (s *MakerBoxesScreen) Title() string { return "Maker boxes" }

func (s *MakerBoxesScreen) WantsRawInput() bool { return s.scanning || s.preConverting }

func (s *MakerBoxesScreen) Init() tea.Cmd { return s.load() }

func (s *MakerBoxesScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListMakerBoxes(ctx, url.Values{"ordering": []string{"bin_id"}})
		if err != nil {
			return makerBoxesLoadedMsg{err: err}
		}
		return makerBoxesLoadedMsg{rows: page.Results}
	}
}

func (s *MakerBoxesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case makerBoxesLoadedMsg:
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
	case makerBoxScanMsg:
		s.scanning = false
		s.scanResult = m.result
		if m.err != nil {
			s.scanErr = m.err.Error()
			return s, Status("scan failed: "+s.scanErr, StatusError)
		}
		s.scanErr = ""
		level := StatusInfo
		switch m.result.Status {
		case "valid":
			level = StatusOK
		case "grace":
			level = StatusWarn
		case "expired", "unknown":
			level = StatusError
		}
		return s, Status(fmt.Sprintf("scan: %s · %s · %s", m.result.Status, m.result.BinID, m.result.Username), level)
	case makerBoxPreConvertMsg:
		s.preConverting = false
		s.preResult = m.result
		if m.err != nil {
			s.preErr = m.err.Error()
			return s, Status("pre-convert failed: "+s.preErr, StatusError)
		}
		s.preErr = ""
		// Refresh the directory so the queue change shows immediately.
		s.loading = true
		return s, tea.Batch(
			s.load(),
			Status(fmt.Sprintf("queued: %s for bin allocation", m.result.AssignedUsername), StatusOK),
		)
	case tea.KeyMsg:
		if s.preConverting {
			switch m.Type {
			case tea.KeyEsc:
				s.preConverting = false
				return s, nil
			case tea.KeyEnter:
				return s.runPreConvert()
			}
			var cmd tea.Cmd
			s.preInput, cmd = s.preInput.Update(msg)
			return s, cmd
		}
		if s.scanning {
			switch m.Type {
			case tea.KeyEsc:
				s.scanning = false
				return s, nil
			case tea.KeyTab, tea.KeyShiftTab:
				s.focusUser = !s.focusUser
				if s.focusUser {
					s.binInput.Blur()
					s.userInput.Focus()
				} else {
					s.userInput.Blur()
					s.binInput.Focus()
				}
				return s, nil
			case tea.KeyEnter:
				return s.runScan()
			}
			var cmd tea.Cmd
			if s.focusUser {
				s.userInput, cmd = s.userInput.Update(msg)
			} else {
				s.binInput, cmd = s.binInput.Update(msg)
			}
			return s, cmd
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
		case "s":
			bin := textinput.New()
			bin.Prompt = ""
			bin.Placeholder = "bin id"
			bin.CharLimit = 20
			bin.Focus()
			user := textinput.New()
			user.Prompt = ""
			user.Placeholder = "username"
			user.CharLimit = 64
			s.binInput = bin
			s.userInput = user
			s.scanning = true
			s.focusUser = false
			s.scanErr = ""
			return s, textinput.Blink
		case "p":
			pre := textinput.New()
			pre.Prompt = ""
			pre.Placeholder = "badge or username"
			pre.CharLimit = 64
			pre.Focus()
			s.preInput = pre
			s.preConverting = true
			s.preErr = ""
			s.preResult = nil
			return s, textinput.Blink
		}
	}
	return s, nil
}

func (s *MakerBoxesScreen) runPreConvert() (Screen, tea.Cmd) {
	query := strings.TrimSpace(s.preInput.Value())
	if query == "" {
		s.preErr = "badge or username required"
		return s, nil
	}
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.preErr = ""
	return s, func() tea.Msg {
		res, err := deps.OMS.PreConvertMakerBox(ctx, query, "")
		return makerBoxPreConvertMsg{result: res, err: err}
	}
}

func (s *MakerBoxesScreen) runScan() (Screen, tea.Cmd) {
	bin := strings.TrimSpace(s.binInput.Value())
	user := strings.TrimSpace(s.userInput.Value())
	if bin == "" || user == "" {
		s.scanErr = "bin id and username required"
		return s, nil
	}
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.scanErr = ""
	return s, func() tea.Msg {
		res, err := deps.OMS.ScanMakerBox(ctx, bin, user)
		return makerBoxScanMsg{result: res, err: err}
	}
}

func (s *MakerBoxesScreen) View() string {
	if s.preConverting {
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Pre-conversion: queue a member") + "\n\n")
		b.WriteString(StyleMuted.Render("Badge or username: ") + s.preInput.View() + "\n")
		if s.preErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.preErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("enter queue · esc cancel"))
		return b.String()
	}
	if s.scanning {
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Scan maker box") + "\n\n")
		b.WriteString(StyleMuted.Render("Bin id:   ") + s.binInput.View() + "\n")
		b.WriteString(StyleMuted.Render("Username: ") + s.userInput.View() + "\n")
		if s.scanErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.scanErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("tab next field · enter scan · esc cancel"))
		return b.String()
	}
	if s.loading {
		return StyleMuted.Render("Loading maker boxes…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · s scan · p pre-convert · esc back")
	}

	var b strings.Builder
	if s.preResult != nil {
		r := s.preResult
		who := r.DisplayName
		if who == "" {
			who = r.AssignedUsername
		}
		b.WriteString(StyleTitle.Render("Last pre-conversion") + "  " + StyleStatusOK.Render("queued") + "\n")
		src := r.IdentitySource
		if src == "" {
			src = "—"
		}
		b.WriteString("  " + StyleMuted.Render("user: ") + r.AssignedUsername + "  " + StyleMuted.Render("name: ") + who + "  " + StyleMuted.Render("· source: ") + src + "\n\n")
	}
	if s.scanResult != nil {
		r := s.scanResult
		statusStyled := r.Status
		switch r.Status {
		case "valid":
			statusStyled = StyleStatusOK.Render(r.Status)
		case "grace":
			statusStyled = StyleStatusWarn.Render(r.Status)
		case "expired", "unknown":
			statusStyled = StyleStatusError.Render(r.Status)
		}
		b.WriteString(StyleTitle.Render("Last scan") + "  " + statusStyled + "\n")
		b.WriteString("  " + StyleMuted.Render("bin: ") + r.BinID + "  " + StyleMuted.Render("user: ") + r.Username)
		if r.DaysRemaining != nil {
			b.WriteString("  " + StyleMuted.Render(fmt.Sprintf("· %d days remaining", *r.DaysRemaining)))
		}
		b.WriteString("\n\n")
	}

	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No maker boxes assigned.") + "\n")
	} else {
		for i, r := range s.rows {
			caret := "  "
			if i == s.cursor {
				caret = "▸ "
			}
			status := r.Status
			switch r.Status {
			case "valid":
				status = StyleStatusOK.Render(r.Status)
			case "grace":
				status = StyleStatusWarn.Render(r.Status)
			case "expired", "unknown":
				status = StyleStatusError.Render(r.Status)
			case "pre_conversion":
				status = StyleStatusWarn.Render("queued")
			}
			who := r.DisplayName
			if who == "" {
				who = r.AssignedUsername
			}
			binDisplay := r.BinID
			if binDisplay == "" {
				binDisplay = "—"
			}
			line := fmt.Sprintf("%s%s  [%s]  %s", caret, binDisplay, status, who)
			if i == s.cursor {
				line = StyleSidebarItemActive.Render(line)
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if r.ExpiresAt != nil {
				meta = append(meta, "expires "+r.ExpiresAt.Format("2006-01-02"))
			}
			if r.LastVerifiedAt != nil {
				meta = append(meta, "verified "+r.LastVerifiedAt.Format("2006-01-02"))
			}
			if r.PaidAt != nil {
				meta = append(meta, "paid "+r.PaidAt.Format("2006-01-02"))
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · s scan bin+user · p pre-convert · r refresh · esc back"))
	return b.String()
}
