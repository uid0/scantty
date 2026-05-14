package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type ProfileScreen struct {
	deps    Deps
	profile *omsapi.Profile
	loading bool
	loadErr string
}

type profileLoadedMsg struct {
	profile *omsapi.Profile
	err     error
}

func NewProfileScreen(deps Deps) *ProfileScreen {
	return &ProfileScreen{deps: deps, loading: true}
}

func (s *ProfileScreen) Title() string {
	if s.profile != nil {
		return fmt.Sprintf("Profile: %s", s.profile.Username)
	}
	return "Profile"
}

func (s *ProfileScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		p, err := deps.OMS.GetProfile(ctx)
		return profileLoadedMsg{profile: p, err: err}
	}
}

func (s *ProfileScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case profileLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.profile = m.profile
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

func (s *ProfileScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading profile…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.profile == nil {
		return StyleMuted.Render("No profile.")
	}
	p := s.profile
	var b strings.Builder
	b.WriteString(StyleTitle.Render(p.Username))
	if p.IsSuperuser {
		b.WriteString("  " + StyleStatusOK.Render("(superuser)"))
	} else if p.IsStaff {
		b.WriteString("  " + StyleStatusOK.Render("(staff)"))
	}
	b.WriteString("\n")
	if p.Email != "" {
		b.WriteString(StyleMuted.Render(p.Email) + "\n")
	}
	full := strings.TrimSpace(p.FirstName + " " + p.LastName)
	if full != "" {
		b.WriteString(full + "\n")
	}
	b.WriteString("\n")
	if len(p.Groups) > 0 {
		b.WriteString(StyleMuted.Render("Groups: ") + strings.Join(p.Groups, ", ") + "\n\n")
	}
	if len(p.BadgeIDs) > 0 {
		b.WriteString(StyleMuted.Render("Badges: ") + strings.Join(p.BadgeIDs, ", ") + "\n\n")
	}
	if len(p.Certifications) > 0 {
		b.WriteString(StyleTitle.Render("Certifications") + "\n")
		for _, c := range p.Certifications {
			line := "  · " + c.Name
			if c.AssetName != "" {
				line += " " + StyleMuted.Render("("+c.AssetName+")")
			}
			if !c.Active() {
				line = StyleMuted.Render(line + " — revoked")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(StyleMuted.Render("r refresh · esc back"))
	return b.String()
}
