package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// VendorsScreen lists third-party service vendors. Read-only for v1;
// CRUD (add/edit/deactivate) is a v2 follow-up once we know whether
// staff want to manage vendors from the TUI or stick with the web
// admin for that. The whole point of surfacing them here is to give
// a staff member triaging a maintenance order a quick "who do I
// call" lookup without bouncing to the browser.
//
// Per the OMS compliance model, electrician/plumber/HVAC vendors
// carry TDLR licenses + COI dates that expire; this view flags
// either when stale so a vendor isn't picked for a regulated job
// after their paperwork lapses.
type VendorsScreen struct {
	deps    Deps
	rows    []omsapi.Vendor
	cursor  int
	loading bool
	loadErr string
}

type vendorsLoadedMsg struct {
	rows []omsapi.Vendor
	err  error
}

func NewVendorsScreen(deps Deps) *VendorsScreen {
	return &VendorsScreen{deps: deps, loading: true}
}

func (s *VendorsScreen) Title() string { return "Vendors" }

func (s *VendorsScreen) Init() tea.Cmd { return s.load() }

func (s *VendorsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListVendors(ctx, nil)
		if err != nil {
			return vendorsLoadedMsg{err: err}
		}
		return vendorsLoadedMsg{rows: page.Results}
	}
}

func (s *VendorsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case vendorsLoadedMsg:
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
			return s, s.load()
		}
	}
	return s, nil
}

func (s *VendorsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading vendors…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No vendors registered.") + "\n\n" + StyleMuted.Render("r refresh · esc back")
	}
	var b strings.Builder
	for i, v := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		kind := v.VendorKindDisplay
		if kind == "" {
			kind = v.VendorKind
		}
		header := fmt.Sprintf("%s%s  [%s]", caret, v.Name, kind)
		if !v.IsActive {
			header += "  " + StyleMuted.Render("inactive")
		}
		if i == s.cursor {
			header = StyleSidebarItemActive.Render(header)
		}
		b.WriteString(header + "\n")

		contact := []string{}
		if v.ContactName != "" {
			contact = append(contact, v.ContactName)
		}
		if v.Phone != "" {
			contact = append(contact, v.Phone)
		}
		if v.Email != "" {
			contact = append(contact, v.Email)
		}
		if len(contact) > 0 {
			b.WriteString("    " + StyleMuted.Render(strings.Join(contact, " · ")) + "\n")
		}

		compliance := []string{}
		if v.TDLRLicenseNumber != "" {
			label := "TDLR " + v.TDLRLicenseNumber
			if v.TDLRIsExpired {
				label = StyleStatusError.Render(label + " EXPIRED")
			} else if v.TDLRLicenseExpiresAt != nil {
				label += " (exp " + v.TDLRLicenseExpiresAt.Format("2006-01-02") + ")"
			}
			compliance = append(compliance, label)
		}
		if v.COIProvider != "" || v.COIPolicyNumber != "" {
			label := "COI"
			if v.COIProvider != "" {
				label += " " + v.COIProvider
			}
			if v.COIIsExpired {
				label = StyleStatusError.Render(label + " EXPIRED")
			} else if v.COIExpiresAt != nil {
				label += " (exp " + v.COIExpiresAt.Format("2006-01-02") + ")"
			}
			compliance = append(compliance, label)
		}
		if len(compliance) > 0 {
			b.WriteString("    " + strings.Join(compliance, " · ") + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · r refresh · esc back"))
	return b.String()
}
