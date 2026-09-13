package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type fkListLoadedMsg struct {
	auths    []forgekeyapi.Authorization
	lockouts []forgekeyapi.Lockout
	err      error
}

type fkActionResultMsg struct {
	action string
	err    error
}

type AuthorizationsScreen struct {
	deps    Deps
	rows    []forgekeyapi.Authorization
	cursor  int
	loading bool
	loadErr string

	confirmingRevoke bool
	revoking         bool

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

func NewAuthorizationsScreen(deps Deps) *AuthorizationsScreen {
	return &AuthorizationsScreen{deps: deps, loading: true}
}

func (s *AuthorizationsScreen) Title() string { return "Authorizations" }

// WantsRawInput claims every key while the revoke confirm is up so y/n/esc land
// here instead of the root's global hotkeys.
func (s *AuthorizationsScreen) WantsRawInput() bool { return s.confirmingRevoke }

// HandlesKey claims `n` (new grant), which otherwise opens the global
// notifications screen. Revoke keys (x/R) and j/k/r are globally free.
func (s *AuthorizationsScreen) HandlesKey(key string) bool { return key == "n" }

func (s *AuthorizationsScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.ForgeKey.ListAuthorizations(ctx, nil)
		return fkListLoadedMsg{auths: rows, err: err}
	}
}

// bar names every key that acts on a list of `rows` authorizations, as a record
// the honesty sweep can press (prose_bar.go).
//
// It used to be two literals: "j/k move · n grant · x/R revoke · r refresh · esc
// back" under every row with no window, so a grid longer than the pane took the
// footer off the bottom and the arrows moved the cursor unnamed; and "n grant
// access · esc back" on the empty list, where `r` reloaded under no word at all.
// `x`/`R` need a row to act on and are not offered without one. `n` is offered
// either way: to a non-staff operator it answers why it will not open the grant
// form, which is a key that says something rather than one that does nothing.
func (s *AuthorizationsScreen) bar(rows int) proseBar {
	out := append(proseNavStep(listNavMoves(rows)), proseBarItem{Keys: []string{"n"}, Hint: "n grant"})
	if rows > 0 {
		out = append(out, proseBarItem{Keys: []string{"x", "R"}, Hint: "x/R revoke"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, and nil in the state that draws
// something else instead: the revoke confirm, whose own prompt names its keys. A
// load in flight or failed draws loadBar's.
func (s *AuthorizationsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.confirmingRevoke {
		return nil
	}
	return s.bar(len(s.rows))
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `n` opens the grant form whatever the list holds (or says it
// needs staff). `x`/`R` are not named: on a row a refresh kept, all they do is
// arm a confirm the frame does not draw.
func (s *AuthorizationsScreen) loadBar() proseBar {
	return proseBar{
		{Keys: []string{"n"}, Hint: "n grant"},
		proseBarReloadFor(s.loadErr != ""),
		proseBarEsc,
	}
}

func (s *AuthorizationsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *AuthorizationsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case fkListLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.auths
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case fkActionResultMsg:
		s.revoking = false
		s.confirmingRevoke = false
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
		}
		return s, tea.Batch(Status(m.action, StatusOK), s.Init())
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		if s.confirmingRevoke {
			return s.updateRevokeConfirm(m)
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
			return s, s.Init()
		case "n":
			// Grant mirrors the web asset-access card's staff-gated "Grant access"
			// button (isStaff). The backend accepts any authed user, but ScanTTY
			// mirrors the web's staff-only affordance.
			if !s.deps.InitialStaff {
				return s, Status("granting access requires staff", StatusWarn)
			}
			return s, SwitchTo(WSAuthorizations, NewAuthorizationGrantScreen(s.deps))
		case "x", "R":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			if !s.rows[s.cursor].IsActive {
				return s, Status("already revoked", StatusWarn)
			}
			s.confirmingRevoke = true
		}
	}
	return s, nil
}

func (s *AuthorizationsScreen) updateRevokeConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.revoking {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.cursor >= len(s.rows) {
			s.confirmingRevoke = false
			return s, nil
		}
		row := s.rows[s.cursor]
		s.revoking = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := fmt.Sprint(row.ID)
		return s, func() tea.Msg {
			// No notes: the web card's Revoke button posts none either.
			err := deps.ForgeKey.RevokeAuthorization(ctx, id, "")
			return fkActionResultMsg{action: fmt.Sprintf("revoked %s", id), err: err}
		}
	case "n", "N", "esc":
		s.confirmingRevoke = false
	}
	return s, nil
}

func (s *AuthorizationsScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading authorizations…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if s.confirmingRevoke {
		return s.viewRevokeConfirm()
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No authorizations.") + "\n\n" + s.proseBar().render(s.paneCells())
	}
	rows := make([]string, len(s.rows))
	for i, r := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		state := StyleStatusOK.Render("active")
		if !r.IsActive {
			state = StyleMuted.Render("revoked")
		}
		title := fmt.Sprintf("%suser %v → asset %v  [%s]", caret, r.User, r.Asset, state)
		if r.UserName != "" || r.AssetName != "" {
			title = fmt.Sprintf("%s%s → %s  [%s]", caret, r.UserName, r.AssetName, state)
		}
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		if r.Notes != "" {
			title += "\n    " + StyleMuted.Render(r.Notes)
		}
		rows[i] = title
	}
	return proseFlatListFrame("", rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.bar(proseFlatCeilingRows), s.proseBar())
}

func (s *AuthorizationsScreen) viewRevokeConfirm() string {
	if s.revoking {
		return StyleMuted.Render("Revoking…")
	}
	who := ""
	if s.cursor < len(s.rows) {
		r := s.rows[s.cursor]
		who = fmt.Sprintf("%v", r.User)
		if r.UserName != "" {
			who = r.UserName
		}
		asset := fmt.Sprintf("%v", r.Asset)
		if r.AssetName != "" {
			asset = r.AssetName
		}
		who = fmt.Sprintf("%s's access to %s", who, asset)
	}
	return StyleStatusWarn.Render(fmt.Sprintf(
		"Revoke %s?  y revoke · n/esc cancel", who))
}

type LockoutsScreen struct {
	deps    Deps
	rows    []forgekeyapi.Lockout
	cursor  int
	loading bool
	loadErr string

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

func NewLockoutsScreen(deps Deps) *LockoutsScreen {
	return &LockoutsScreen{deps: deps, loading: true}
}

func (s *LockoutsScreen) Title() string { return "Lockouts" }

func (s *LockoutsScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.ForgeKey.ListLockouts(ctx, nil)
		return fkListLoadedMsg{lockouts: rows, err: err}
	}
}

// bar names every key that acts on a list of `rows` lockouts, as a record the
// honesty sweep can press (prose_bar.go).
//
// It used to be the literal "j/k move · u unlock (hierarchical) · r refresh · esc
// back" under every row with no window, so a list longer than the pane took the
// footer off the bottom and the arrows moved the cursor unnamed — and the empty
// list drew the fact alone, with `r` and `esc` working under no word. `u` needs
// a row to act on and is not offered without one.
func (s *LockoutsScreen) bar(rows int) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if rows > 0 {
		out = append(out, proseBarItem{Keys: []string{"u"}, Hint: "u unlock (hierarchical)"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's.
func (s *LockoutsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return s.bar(len(s.rows))
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `u` still acts on the row a refresh kept under the cursor,
// which the frame no longer draws: named because it acts, and a candidate for
// gating.
func (s *LockoutsScreen) loadBar() proseBar {
	var out proseBar
	if len(s.rows) > 0 {
		out = append(out, proseBarItem{Keys: []string{"u"}, Hint: "u unlock (hierarchical)"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *LockoutsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *LockoutsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case fkListLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.lockouts
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case fkActionResultMsg:
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
		}
		return s, tea.Batch(Status(m.action, StatusOK), s.Init())
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
			return s, s.Init()
		case "u":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			if !row.IsActive {
				return s, Status("already unlocked", StatusWarn)
			}
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			id := fmt.Sprint(row.ID)
			return s, func() tea.Msg {
				err := deps.ForgeKey.Unlock(ctx, id)
				return fkActionResultMsg{action: fmt.Sprintf("unlock %s (level %s)", id, row.LockoutLevel), err: err}
			}
		}
	}
	return s, nil
}

func (s *LockoutsScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading lockouts…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No lockouts.") + "\n\n" + s.proseBar().render(s.paneCells())
	}
	rows := make([]string, len(s.rows))
	for i, r := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		state := StyleStatusError.Render("active")
		if !r.IsActive {
			state = StyleMuted.Render("unlocked")
		}
		title := fmt.Sprintf("%sasset %v  [%s · %s]", caret, r.Asset, r.LockoutLevel, state)
		if r.AssetName != "" {
			title = fmt.Sprintf("%s%s  [%s · %s]", caret, r.AssetName, r.LockoutLevel, state)
		}
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		if r.Reason != "" {
			title += "\n    " + StyleMuted.Render(r.Reason)
		}
		rows[i] = title
	}
	return proseFlatListFrame("", rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.bar(proseFlatCeilingRows), s.proseBar())
}
