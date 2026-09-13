package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// donationAmount picks the best dollar figure to show for a donation row:
// the computed net value when present, otherwise the estimated value. Both are
// DRF DecimalFields (JSON strings), so this returns the raw "12.34" string.
func donationAmount(d omsapi.Donation) string {
	if !d.NetValue.Empty() {
		return d.NetValue.String()
	}
	if !d.EstimatedValue.Empty() {
		return d.EstimatedValue.String()
	}
	return ""
}

type DonationsScreen struct {
	deps    Deps
	rows    []omsapi.Donation
	cursor  int
	loading bool
	loadErr string

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

type donationsLoadedMsg struct {
	rows []omsapi.Donation
	err  error
}

func NewDonationsScreen(deps Deps) *DonationsScreen {
	return &DonationsScreen{deps: deps, loading: true}
}

func (s *DonationsScreen) Title() string { return "Donations" }

func (s *DonationsScreen) Init() tea.Cmd { return s.load() }

func (s *DonationsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListDonations(ctx, nil)
		if err != nil {
			return donationsLoadedMsg{err: err}
		}
		return donationsLoadedMsg{rows: page.Results}
	}
}

// bar names every key that acts on a list of `rows` donations, as a record the
// honesty sweep can press (prose_bar.go).
//
// It used to be the literal "j/k move · r refresh · esc back", which left the
// arrows moving the cursor unnamed, and it was written under every row with no
// window — so a list longer than the pane took the footer off the bottom.
func (s *DonationsScreen) bar(rows int) proseBar {
	return append(proseNavStep(listNavMoves(rows)), proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, in every state: a load in flight or
// failed draws loadBar's. The EMPTY list is a
// state with a bar: it used to draw the fact alone while `r` reloaded it and
// `esc` left, and naming nothing there is the omission half of the rule.
func (s *DonationsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return s.bar(len(s.rows))
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision): the reload and the way back, and nothing else, since the
// movement keys only walk rows the frame does not draw.
func (s *DonationsScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

func (s *DonationsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *DonationsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case donationsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
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

func (s *DonationsScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading donations…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No donations.") + "\n\n" + s.proseBar().render(s.paneCells())
	}
	rows := make([]string, len(s.rows))
	for i, d := range s.rows {
		var b strings.Builder
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		title := caret + d.DonorName
		if d.DonorName == "" {
			title = caret + StyleMuted.Render("(anonymous)")
		}
		if amount := donationAmount(d); amount != "" {
			title += "  $" + amount
		}
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		b.WriteString(title + "\n")
		var meta []string
		if d.DonationNumber != "" {
			meta = append(meta, d.DonationNumber)
		}
		if d.Status != "" {
			meta = append(meta, d.Status)
		}
		if !d.DateReceived.IsZero() {
			meta = append(meta, d.DateReceived.Format("2006-01-02"))
		}
		if d.TaxReceiptNumber != "" {
			meta = append(meta, "receipt "+d.TaxReceiptNumber)
		}
		if len(meta) > 0 {
			b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
		}
		rows[i] = strings.TrimSuffix(b.String(), "\n")
	}
	return proseFlatListFrame("", rows, s.cursor, &s.windowStart, s.terminalHeight,
		s.paneCells(), s.bar(proseFlatCeilingRows), s.proseBar())
}
