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

	terminalWidth int
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
	case tea.WindowSizeMsg:
		s.terminalWidth = m.Width
		return s, nil
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

// proseBar is the sign-in form's action bar as a record (prose_bar.go).
//
// THE LITERAL IT REPLACES said `tab move · enter submit · esc to skip`, and
// three of the four keys that move the focus were named nowhere: shift+tab and
// the arrow pair walk the two fields exactly as tab does, and the focus WRAPS, so
// every one of them moves the caret row from either field. proseBarFieldFocus is
// the segment that says so.
//
// ENTER IS WORDED FOR WHAT IT DOES ON THE FIELD IT IS PRESSED IN: on the
// username it moves to the password, and only on the password does it sign in.
// "enter submit" over the username field named a submit the key does not make.
func (s *LoginScreen) proseBar() proseBar {
	enter := proseBarItem{Keys: []string{"enter"}, Hint: "enter next field"}
	if s.focused == len(s.inputs)-1 {
		enter.Hint = "enter sign in"
	}
	return proseBar{
		proseBarFieldFocus,
		enter,
		{Keys: []string{"esc"}, Hint: "esc skip (anonymous mode)"},
	}
}

func (s *LoginScreen) View() string {
	cells := proseBarCells(s.terminalWidth)
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Sign in to OMS") + "\n\n")
	b.WriteString(proseFormLine(StyleMuted.Render("Endpoint: ")+s.deps.OMS.BaseURL(), cells) + "\n\n")

	labels := []string{"Username", "Password"}
	for i, ti := range s.inputs {
		caret := "  "
		if i == s.focused {
			caret = "▸ "
		}
		b.WriteString(caret + StyleMuted.Render(labels[i]) + "\n")
		b.WriteString("    " + woBoxView(ti, cells, "    ") + "\n\n")
	}

	if s.pending {
		b.WriteString(StyleMuted.Render("Authenticating…") + "\n")
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render(proseFormLine(s.errMsg, cells)) + "\n")
	}

	b.WriteString("\n" + s.proseBar().render(cells))
	return b.String()
}
