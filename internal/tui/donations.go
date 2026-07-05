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

func (s *DonationsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
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
		return StyleMuted.Render("Loading donations…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No donations.")
	}
	var b strings.Builder
	for i, d := range s.rows {
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
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · r refresh · esc back"))
	return b.String()
}
