package tui

import (
	"context"
	"fmt"
	"strings"

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

func (s *AuthorizationsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
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
		return StyleMuted.Render("Loading authorizations…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.confirmingRevoke {
		return s.viewRevokeConfirm()
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No authorizations.") + "\n\n" + StyleMuted.Render("n grant access · esc back")
	}
	var b strings.Builder
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
		b.WriteString(title + "\n")
		if r.Notes != "" {
			b.WriteString("    " + StyleMuted.Render(r.Notes) + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · n grant · x/R revoke · r refresh · esc back"))
	return b.String()
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

func (s *LockoutsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
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
		return StyleMuted.Render("Loading lockouts…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No lockouts.")
	}
	var b strings.Builder
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
		b.WriteString(title + "\n")
		if r.Reason != "" {
			b.WriteString("    " + StyleMuted.Render(r.Reason) + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · u unlock (hierarchical) · r refresh · esc back"))
	return b.String()
}
