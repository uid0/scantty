package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type SIGDetailScreen struct {
	deps    Deps
	sigID   int
	members []omsapi.SIGMember
	loading bool
	loadErr string
}

type sigDetailLoadedMsg struct {
	members []omsapi.SIGMember
	err     error
}

func NewSIGDetailScreen(deps Deps, id string) *SIGDetailScreen {
	sigID, _ := strconv.Atoi(id)
	return &SIGDetailScreen{deps: deps, sigID: sigID, loading: true}
}

func (s *SIGDetailScreen) Title() string { return fmt.Sprintf("SIG #%d", s.sigID) }

func (s *SIGDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.sigID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListSIGMembers(ctx, id, nil)
		if err != nil {
			return sigDetailLoadedMsg{err: err}
		}
		return sigDetailLoadedMsg{members: page.Results}
	}
}

func (s *SIGDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case sigDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.members = m.members
		return s, nil
	case tea.KeyMsg:
		if m.String() == "r" {
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		}
	}
	return s, nil
}

func (s *SIGDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading SIG members…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.members) == 0 {
		return StyleMuted.Render("No members.")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(fmt.Sprintf("%d members", len(s.members))) + "\n\n")
	for _, m := range s.members {
		line := "  · " + m.Username
		if m.Role != "" {
			line += " " + StyleMuted.Render("("+m.Role+")")
		}
		if m.Email != "" {
			line += " " + StyleMuted.Render(m.Email)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("r refresh · esc back"))
	return b.String()
}
