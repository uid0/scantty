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

	terminalWidth  int
	terminalHeight int
	windowStart    int
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

// bar names every key that acts on a list of `rows` vendors, as a record the
// honesty sweep can press (prose_bar.go).
//
// It used to be the literal "j/k move · r refresh · esc back": the arrows moved
// the cursor unnamed, and it was written under every row with no window — and a
// vendor row is up to three lines, so a directory of a dozen took the footer off
// an 80x24 pane.
func (s *VendorsScreen) bar(rows int) proseBar {
	return append(proseNavStep(listNavMoves(rows)), proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's.
func (s *VendorsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return s.bar(len(s.rows))
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision): the reload and the way back, and nothing else, since the
// movement keys only walk rows the frame does not draw.
func (s *VendorsScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

func (s *VendorsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *VendorsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
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
		return proseLoadingFrame("Loading vendors…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No vendors registered.") + "\n\n" + s.proseBar().render(s.paneCells())
	}
	rows := make([]string, len(s.rows))
	for i, v := range s.rows {
		var b strings.Builder
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
			} else if !v.TDLRLicenseExpiresAt.IsZero() {
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
			} else if !v.COIExpiresAt.IsZero() {
				label += " (exp " + v.COIExpiresAt.Format("2006-01-02") + ")"
			}
			compliance = append(compliance, label)
		}
		if len(compliance) > 0 {
			b.WriteString("    " + strings.Join(compliance, " · ") + "\n")
		}
		rows[i] = strings.TrimSuffix(b.String(), "\n")
	}
	return proseFlatListFrame("", rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.bar(proseFlatCeilingRows), s.proseBar())
}
