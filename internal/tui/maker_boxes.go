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
// (badge or username) for later bin allocation; `c` converts the
// pre_conversion row under the cursor (allocates MBX-NNN, reprint
// follows via the existing label download flow).
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

	// convert (finalize a queued row → allocate MBX-NNN).
	// confirmConvertID nil = no prompt; non-nil = awaiting y/n.
	confirmConvertID *int
	converting       bool
	convertResult    *omsapi.MakerBox
	convertErr       string

	// delete (destroy the row under the cursor, y/n confirm).
	confirmingDelete bool
	deleting         bool
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

type makerBoxConvertMsg struct {
	result *omsapi.MakerBox
	err    error
}

type makerBoxDeletedMsg struct {
	err error
}

func NewMakerBoxesScreen(deps Deps) *MakerBoxesScreen {
	return &MakerBoxesScreen{deps: deps, loading: true}
}

func (s *MakerBoxesScreen) Title() string { return "Maker boxes" }

// WantsRawInput routes every key here while a textinput form (scan / pre-convert)
// OR a y/n confirm (delete / convert) is up, so the modal owns keys like n and
// esc instead of leaking them to the global nav (n=notifications, esc=welcome).
func (s *MakerBoxesScreen) WantsRawInput() bool {
	return s.scanning || s.preConverting || s.confirmingDelete || s.confirmConvertID != nil
}

// HandlesKey claims lowercase 's' (start a bin+user scan) and 'n' (new maker
// box) so they beat the global s=settings nav and n=notifications. Once a form
// or confirm is open WantsRawInput routes every key here anyway; this claim
// covers the plain list view.
func (s *MakerBoxesScreen) HandlesKey(key string) bool { return key == "s" || key == "n" }

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
	case makerBoxConvertMsg:
		s.converting = false
		s.convertResult = m.result
		if m.err != nil {
			s.convertErr = m.err.Error()
			return s, Status("convert failed: "+s.convertErr, StatusError)
		}
		s.convertErr = ""
		// Reload so the row's bin_id / status flip becomes visible.
		s.loading = true
		return s, tea.Batch(
			s.load(),
			Status(fmt.Sprintf("converted: %s → %s", m.result.AssignedUsername, m.result.BinID), StatusOK),
		)
	case makerBoxDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("maker box deleted", StatusOK), s.load())
	case tea.KeyMsg:
		// Convert confirmation overlay takes precedence — it consumes
		// y/n/esc and nothing else until resolved.
		if s.confirmConvertID != nil {
			switch m.String() {
			case "y", "Y", "enter":
				id := *s.confirmConvertID
				s.confirmConvertID = nil
				s.converting = true
				s.convertErr = ""
				return s, s.runConvert(id)
			case "n", "N", "esc":
				s.confirmConvertID = nil
				return s, nil
			}
			return s, nil
		}
		// Delete confirmation overlay — destructive, so it takes only an
		// explicit y (no enter) and cancels on n/esc.
		if s.confirmingDelete {
			if s.deleting {
				return s, nil
			}
			switch m.String() {
			case "y", "Y":
				row, ok := s.selectedRow()
				if !ok {
					s.confirmingDelete = false
					return s, nil
				}
				s.deleting = true
				return s, s.runDelete(row.ID)
			case "n", "N", "esc":
				s.confirmingDelete = false
			}
			return s, nil
		}
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
			// A scan IS a WHMCS membership lookup — with billing down it can
			// only fail, so the form does not open (the web disables its Scan
			// button on the same key). Common API being down does not stop a
			// scan: this form takes a typed username, which resolves without
			// the badge directory, so that one warns instead (see View).
			if s.billingDown() {
				return s, Status(makerBoxLookupUnavailable, StatusWarn)
			}
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
			// Pre-conversion runs the identity cascade, which consults WHMCS on
			// BOTH paths (badge → Common API → WHMCS, or username → WHMCS), so
			// a degraded WHMCS breaks every lookup and the form stays shut.
			// Mirrors MakerBoxPreConversionPage's billingDown gate.
			if s.billingDown() {
				return s, Status(makerBoxLookupUnavailable, StatusWarn)
			}
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
		case "c":
			// Convert the row under the cursor. Only valid for
			// pre_conversion rows; we leave already-converted rows
			// alone (backend would 409 anyway, but this is cheaper).
			if s.cursor < 0 || s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			if row.Status != "pre_conversion" {
				return s, Status("convert: cursor is not on a queued row", StatusWarn)
			}
			id := row.ID
			s.confirmConvertID = &id
			return s, nil
		case "n":
			// New maker box. 'n' is claimed via HandlesKey (else global
			// notifications would eat it).
			return s, SwitchTo(WSFacilities, NewMakerBoxFormScreen(s.deps, 0))
		case "E":
			if row, ok := s.selectedRow(); ok {
				return s, SwitchTo(WSFacilities, NewMakerBoxFormScreen(s.deps, row.ID))
			}
		case "x":
			if _, ok := s.selectedRow(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *MakerBoxesScreen) selectedRow() (omsapi.MakerBox, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.MakerBox{}, false
	}
	return s.rows[s.cursor], true
}

func (s *MakerBoxesScreen) runDelete(id int) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		return makerBoxDeletedMsg{err: deps.OMS.DeleteMakerBox(ctx, id)}
	}
}

func (s *MakerBoxesScreen) runConvert(id int) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		res, err := deps.OMS.ConvertMakerBox(ctx, id)
		return makerBoxConvertMsg{result: res, err: err}
	}
}

// billingDown reports whether the WHMCS breaker is open — i.e. whether a
// membership lookup can resolve at all. Nothing is gated on an unknown status.
func (s *MakerBoxesScreen) billingDown() bool {
	return s.deps.Health.IsDegraded(omsapi.ServiceKeyWHMCS)
}

func (s *MakerBoxesScreen) runPreConvert() (Screen, tea.Cmd) {
	query := strings.TrimSpace(s.preInput.Value())
	if query == "" {
		s.preErr = "badge or username required"
		return s, nil
	}
	// Re-check at submit: the breaker can trip while the form is open.
	if s.billingDown() {
		s.preErr = makerBoxLookupUnavailable
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
	// Re-check at submit: the breaker can trip while the form is open.
	if s.billingDown() {
		s.scanErr = makerBoxLookupUnavailable
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
	if s.confirmConvertID != nil {
		var who string
		for _, r := range s.rows {
			if r.ID == *s.confirmConvertID {
				who = r.AssignedUsername
				if r.DisplayName != "" {
					who = r.DisplayName + " (" + r.AssignedUsername + ")"
				}
				break
			}
		}
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Convert?") + "\n\n")
		b.WriteString("Allocate the next MBX-NNN for " + who + " and reprint the label?\n\n")
		b.WriteString(StyleMuted.Render("y confirm · n / esc cancel"))
		return b.String()
	}
	if s.confirmingDelete {
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Delete maker box?") + "\n\n")
		if s.deleting {
			b.WriteString(StyleMuted.Render("Deleting…"))
			return b.String()
		}
		binDisplay := "(unallocated)"
		who := ""
		if row, ok := s.selectedRow(); ok {
			if row.BinID != "" {
				binDisplay = row.BinID
			}
			who = row.DisplayName
			if who == "" {
				who = row.AssignedUsername
			}
		}
		line := binDisplay
		if who != "" {
			line += " (" + who + ")"
		}
		b.WriteString("Delete " + line + "? This can't be undone.\n\n")
		b.WriteString(StyleStatusWarn.Render("y delete · n / esc cancel"))
		return b.String()
	}
	if s.preConverting {
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Pre-conversion: queue a member") + "\n\n")
		b.WriteString(StyleMuted.Render("Badge or username: ") + s.preInput.View() + "\n")
		if s.preErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.preErr) + "\n")
		}
		// A degraded member directory only breaks BADGE resolution — a typed
		// username still resolves — so this warns without closing the form,
		// exactly as the web's common_api notice does.
		if notice := serviceUnavailableNotice(s.deps.Health, omsapi.ServiceKeyCommonAPI, badgeLookupUnavailable); notice != "" {
			b.WriteString("\n" + notice + "\n")
		}
		if notice := serviceUnavailableNotice(s.deps.Health, omsapi.ServiceKeyWHMCS, makerBoxLookupUnavailable); notice != "" {
			b.WriteString("\n" + notice + "\n")
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
		if notice := serviceUnavailableNotice(s.deps.Health, omsapi.ServiceKeyWHMCS, makerBoxLookupUnavailable); notice != "" {
			b.WriteString("\n" + notice + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("tab next field · enter scan · esc cancel"))
		return b.String()
	}
	if s.loading {
		return StyleMuted.Render("Loading maker boxes…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · E edit · x delete · esc back")
	}

	var b strings.Builder
	if s.convertResult != nil {
		r := s.convertResult
		who := r.DisplayName
		if who == "" {
			who = r.AssignedUsername
		}
		b.WriteString(StyleTitle.Render("Last conversion") + "  " + StyleStatusOK.Render("allocated") + "\n")
		b.WriteString("  " + StyleMuted.Render("bin: ") + r.BinID + "  " + StyleMuted.Render("· user: ") + who + "\n\n")
	}
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
	// Inline gate, right above the keys it takes away. Convert is NOT gated:
	// it allocates the bin from the expiry the pre-conversion already stored
	// and calls no lookup, so it works through a billing outage.
	if notice := serviceUnavailableLine(s.deps.Health, omsapi.ServiceKeyWHMCS, makerBoxLookupUnavailable); notice != "" {
		b.WriteString("\n" + notice)
	}
	if notice := serviceUnavailableLine(s.deps.Health, omsapi.ServiceKeyCommonAPI, badgeLookupUnavailable); notice != "" {
		b.WriteString(notice)
	}
	// Two footer lines keep each within a narrow content pane (no width overflow).
	b.WriteString("\n" + StyleMuted.Render("j/k move · n new · E edit · x delete · r refresh"))
	if s.billingDown() {
		// The scan and pre-convert keys come off the hint line while they
		// cannot resolve anybody — the same move the web makes by disabling
		// those two buttons.
		b.WriteString("\n" + StyleMuted.Render("c convert (queued) · esc back"))
	} else {
		b.WriteString("\n" + StyleMuted.Render("s scan · p pre-convert · c convert (queued) · esc back"))
	}
	return b.String()
}
