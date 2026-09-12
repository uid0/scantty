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
	deps           Deps
	sigID          int
	sig            *omsapi.SIG
	members        []omsapi.SIGMember
	loading        bool
	loadErr        string
	scroller       *TextScroller
	terminalHeight int
	terminalWidth  int
}

type sigDetailLoadedMsg struct {
	sig     *omsapi.SIG
	members []omsapi.SIGMember
	err     error
}

func NewSIGDetailScreen(deps Deps, id string) *SIGDetailScreen {
	sigID, _ := strconv.Atoi(id)
	return &SIGDetailScreen{
		deps:     deps,
		sigID:    sigID,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *SIGDetailScreen) Title() string {
	if s.sig != nil {
		return "SIG: " + s.sig.Name
	}
	return fmt.Sprintf("SIG #%d", s.sigID)
}

func (s *SIGDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.sigID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		out := sigDetailLoadedMsg{}
		// Fetch SIG metadata from the list endpoint (no per-id GET available).
		if page, err := deps.OMS.ListSIGs(ctx, nil); err == nil && page != nil {
			for i := range page.Results {
				if page.Results[i].ID == id {
					row := page.Results[i]
					out.sig = &row
					break
				}
			}
		}
		if memberPage, err := deps.OMS.ListSIGMembers(ctx, id, nil); err == nil && memberPage != nil {
			out.members = memberPage.Results
		} else if err != nil {
			out.err = err
		}
		return out
	}
}

func (s *SIGDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		proseSizeScroller(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
		return s, nil
	case sigDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.sig = m.sig
		s.members = m.members
		s.scroller.Set(s.renderBody())
		return s, nil
	case tea.KeyMsg:
		if s.scroller.Handle(m) {
			return s, nil
		}
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
		return StyleMuted.Render("Loading SIG…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	return proseScrollFrame(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
}

// bar names every key that acts on this sheet, as a record the honesty sweep
// can press (prose_bar.go).
//
// It used to be the literal "j/k scroll · pgup/pgdn page · r refresh · esc
// back", which named four of the ten keystrokes TextScroller.Handle binds:
// the arrows, `g`/`G` and home/end all scrolled this sheet and no word on it
// said so.
func (s *SIGDetailScreen) bar(scrolls bool) proseBar {
	return append(proseNavScroll(scrolls), proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this sheet is DRAWING — nil in the states that draw
// something else instead, which is what makes "this state has no bar" and "this
// state's bar is empty" different answers to the sweep.
func (s *SIGDetailScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return nil
	}
	return proseScrollBar(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
}

func (s *SIGDetailScreen) renderBody() string {
	var b strings.Builder
	if s.sig != nil {
		b.WriteString(StyleTitle.Render(s.sig.Name))
		if s.sig.IsUserAdmin {
			b.WriteString("  " + StyleStatusOK.Render("(you admin)"))
		}
		b.WriteString("\n")
		b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %d", s.sig.ID)) + "\n")
		if s.sig.GroupEmail != "" {
			b.WriteString(StyleMuted.Render("Email: ") + s.sig.GroupEmail + "\n")
		}
		b.WriteString("\n")

		b.WriteString(StyleTitle.Render("Counts") + "\n")
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Members: %d", s.sig.MemberCount)) + "\n")
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Assets: %d", s.sig.AssetCount)) + "\n")
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Inventory items: %d", s.sig.InventoryCount)) + "\n")
		b.WriteString("\n")

		if len(s.sig.Admins) > 0 {
			b.WriteString(StyleTitle.Render(fmt.Sprintf("Admins (%d)", len(s.sig.Admins))) + "\n")
			for _, a := range s.sig.Admins {
				line := "  · " + a.Username
				meta := []string{}
				if a.Handle != "" && a.Handle != a.Username {
					meta = append(meta, "@"+a.Handle)
				}
				if a.Email != "" {
					meta = append(meta, a.Email)
				}
				if len(meta) > 0 {
					line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
				}
				b.WriteString(line + "\n")
			}
			b.WriteString("\n")
		}
	}

	if len(s.members) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Members (%d)", len(s.members))) + "\n")
		for _, m := range s.members {
			line := "  · " + m.Username
			if m.IsSIGAdmin {
				line += " " + StyleStatusOK.Render("(admin)")
			}
			meta := []string{}
			if m.Handle != "" && m.Handle != m.Username {
				meta = append(meta, "@"+m.Handle)
			}
			if m.Email != "" {
				meta = append(meta, m.Email)
			}
			if len(meta) > 0 {
				line += " " + StyleMuted.Render(strings.Join(meta, " · "))
			}
			b.WriteString(line + "\n")
		}
	} else if s.sig != nil {
		b.WriteString(StyleMuted.Render("No members.") + "\n")
	}

	return b.String()
}
