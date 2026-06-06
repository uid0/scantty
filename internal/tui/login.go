package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/cache"
	"github.com/uid0/scantty/internal/omsapi"
)

type LoginScreen struct {
	deps    Deps
	inputs  []textinput.Model
	focused int
	pending bool
	errMsg  string
}

type loginDoneMsg struct {
	resp *omsapi.LoginResponse
	err  error
}

type sessionRestoredMsg struct {
	session *cache.Session
}

func NewLoginScreen(deps Deps) *LoginScreen {
	s := &LoginScreen{deps: deps}
	user := textinput.New()
	user.Prompt = ""
	user.Placeholder = "username"
	user.CharLimit = 100
	user.Focus()

	pass := textinput.New()
	pass.Prompt = ""
	pass.Placeholder = "password"
	pass.CharLimit = 200
	pass.EchoMode = textinput.EchoPassword
	pass.EchoCharacter = '•'

	s.inputs = []textinput.Model{user, pass}
	return s
}

func (s *LoginScreen) Title() string { return "Sign in" }

func (s *LoginScreen) WantsRawInput() bool { return true }

func (s *LoginScreen) Init() tea.Cmd { return textinput.Blink }

func (s *LoginScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case loginDoneMsg:
		s.pending = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("login failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		return s, tea.Batch(
			Status(fmt.Sprintf("signed in as %s", m.resp.Username), StatusOK),
			persistLogin(s.deps, m.resp),
			SwitchTo(WSScan, NewScanScreen(s.deps)),
		)
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyTab, tea.KeyDown, tea.KeyShiftTab, tea.KeyUp:
			s.focusNext(m.Type == tea.KeyShiftTab || m.Type == tea.KeyUp)
			return s, nil
		case tea.KeyEnter:
			if s.focused == 0 {
				s.focusNext(false)
				return s, nil
			}
			return s.submit()
		case tea.KeyEsc:
			return s, SwitchTo(WSScan, NewWelcomeScreen())
		}
	}
	var cmd tea.Cmd
	s.inputs[s.focused], cmd = s.inputs[s.focused].Update(msg)
	return s, cmd
}

func (s *LoginScreen) focusNext(reverse bool) {
	s.inputs[s.focused].Blur()
	if reverse {
		s.focused--
		if s.focused < 0 {
			s.focused = len(s.inputs) - 1
		}
	} else {
		s.focused = (s.focused + 1) % len(s.inputs)
	}
	s.inputs[s.focused].Focus()
}

func (s *LoginScreen) submit() (Screen, tea.Cmd) {
	username := strings.TrimSpace(s.inputs[0].Value())
	password := s.inputs[1].Value()
	if username == "" || password == "" {
		s.errMsg = "username and password are required"
		return s, nil
	}
	s.pending = true
	s.errMsg = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		resp, err := deps.OMS.Login(ctx, username, password)
		return loginDoneMsg{resp: resp, err: err}
	}
}

func persistLogin(deps Deps, resp *omsapi.LoginResponse) tea.Cmd {
	return func() tea.Msg {
		if deps.Cache == nil {
			return nil
		}
		_ = deps.Cache.SaveSession(cache.Session{
			BaseURL:      deps.OMS.BaseURL(),
			Username:     resp.Username,
			AccessToken:  resp.Access,
			RefreshToken: resp.Refresh,
			IsStaff:      resp.IsStaff,
			IsSuperuser:  resp.IsSuperuser,
		})
		return nil
	}
}

func (s *LoginScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Sign in to OMS") + "\n\n")
	b.WriteString(StyleMuted.Render("Endpoint: ") + s.deps.OMS.BaseURL() + "\n\n")

	labels := []string{"Username", "Password"}
	for i, ti := range s.inputs {
		caret := "  "
		if i == s.focused {
			caret = "▸ "
		}
		b.WriteString(caret + StyleMuted.Render(labels[i]) + "\n")
		b.WriteString("    " + ti.View() + "\n\n")
	}

	if s.pending {
		b.WriteString(StyleMuted.Render("Authenticating…") + "\n")
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render(s.errMsg) + "\n")
	}

	b.WriteString("\n" + StyleMuted.Render("tab move · enter submit · esc to skip (anonymous mode)"))
	return b.String()
}
