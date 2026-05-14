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
}

func NewAuthorizationsScreen(deps Deps) *AuthorizationsScreen {
	return &AuthorizationsScreen{deps: deps, loading: true}
}

func (s *AuthorizationsScreen) Title() string { return "Authorizations" }

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
		case "x":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			if !row.IsActive {
				return s, Status("already revoked", StatusWarn)
			}
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			id := row.ID
			return s, func() tea.Msg {
				err := deps.ForgeKey.RevokeAuthorization(ctx, id)
				return fkActionResultMsg{action: fmt.Sprintf("revoked #%d", id), err: err}
			}
		}
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
	if len(s.rows) == 0 {
		return StyleMuted.Render("No authorizations.")
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
		title := fmt.Sprintf("%suser %d → asset %d  [%s]", caret, r.User, r.Asset, state)
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
	b.WriteString("\n" + StyleMuted.Render("j/k move · x revoke · r refresh · esc back"))
	return b.String()
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
			id := row.ID
			return s, func() tea.Msg {
				err := deps.ForgeKey.Unlock(ctx, id)
				return fkActionResultMsg{action: fmt.Sprintf("unlock #%d (level %s)", id, row.LockoutLevel), err: err}
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
		title := fmt.Sprintf("%sasset %d  [%s · %s]", caret, r.Asset, r.LockoutLevel, state)
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
