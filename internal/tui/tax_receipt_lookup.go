package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TaxReceiptLookupScreen mirrors the web Tax Receipt Lookup page
// (frontend/src/pages/TaxReceiptLookupPage.tsx): an operator types a receipt
// serial number, and the screen shows that donation tax receipt's data. It
// backs onto the same public lookup endpoint the web uses
// (donationsAPI.lookupTaxReceipt → GET /donations/tax-receipts/lookup/).
//
// The web page also offers "Download Receipt (Clean / Copy)" buttons that stream
// a generated PDF. A PDF blob has no meaningful terminal surface, so — like the
// program's other file-download features — that stays web-only; the TUI instead
// surfaces the receipt DATA and says so.
type TaxReceiptLookupScreen struct {
	deps     Deps
	input    textinput.Model
	receipt  *omsapi.TaxReceipt
	pending  bool
	notFound bool
	lastErr  string
	// pendingSerial is the serial of the in-flight (most-recently submitted)
	// lookup. Only a response tagged with this serial is the one we're waiting
	// on, so responses from superseded queries — or ones that land after the
	// operator left and reopened the screen (a fresh instance has an empty
	// pendingSerial) — are dropped without touching state. Gating on the
	// submitted serial rather than the live input box means a response the
	// operator asked for still lands even if they've started typing the next one.
	pendingSerial string
}

// taxReceiptLookupMsg carries a lookup's result tagged with the serial it was
// issued for, so the Update handler can tell whether it's the response to the
// current in-flight request.
type taxReceiptLookupMsg struct {
	serial  string
	receipt *omsapi.TaxReceipt
	err     error
}

func NewTaxReceiptLookupScreen(deps Deps) *TaxReceiptLookupScreen {
	ti := textinput.New()
	ti.Prompt = "▸ "
	ti.Placeholder = "receipt serial number…"
	ti.CharLimit = 100
	ti.Focus()
	return &TaxReceiptLookupScreen{deps: deps, input: ti}
}

func (s *TaxReceiptLookupScreen) Title() string { return "Tax Receipt Lookup" }

// WantsRawInput keeps every keystroke flowing to the serial-number input so the
// global hotkey layer never steals a character; esc/enter are handled here.
func (s *TaxReceiptLookupScreen) WantsRawInput() bool { return true }

func (s *TaxReceiptLookupScreen) Init() tea.Cmd { return textinput.Blink }

func (s *TaxReceiptLookupScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case taxReceiptLookupMsg:
		if m.serial != s.pendingSerial {
			return s, nil // not the response to the current request — drop it
		}
		s.pending = false
		if m.err != nil {
			var apiErr *omsapi.APIError
			if errors.As(m.err, &apiErr) && apiErr.IsNotFound() {
				s.notFound = true
				s.receipt = nil
				s.lastErr = ""
			} else {
				s.lastErr = m.err.Error()
				s.receipt = nil
				s.notFound = false
			}
			return s, nil
		}
		s.receipt = m.receipt
		s.notFound = false
		s.lastErr = ""
		return s, nil
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyEnter:
			return s.submit()
		case tea.KeyEsc:
			// Tax receipt lookup lives under Settings in the web app, so esc
			// returns to the Settings screen it was launched from.
			return s, SwitchTo(WSSettings, NewSettingsScreen(s.deps))
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

// submit fires a lookup for the current serial number. A blank input is a no-op
// with a gentle hint (mirroring the web's "Please enter a serial number").
func (s *TaxReceiptLookupScreen) submit() (Screen, tea.Cmd) {
	serial := strings.TrimSpace(s.input.Value())
	if serial == "" {
		s.lastErr = "enter a serial number"
		s.receipt = nil
		s.notFound = false
		return s, nil
	}
	if s.deps.OMS == nil {
		return s, nil
	}
	s.pending = true
	s.pendingSerial = serial
	s.receipt = nil
	s.notFound = false
	s.lastErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		rec, err := deps.OMS.LookupTaxReceipt(ctx, serial)
		return taxReceiptLookupMsg{serial: serial, receipt: rec, err: err}
	}
}

func (s *TaxReceiptLookupScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Look up a donation tax receipt by its serial number.") + "\n\n")
	b.WriteString(s.input.View() + "\n\n")

	if s.pending {
		b.WriteString(StyleMuted.Render("Looking up…") + "\n")
	}
	if s.lastErr != "" {
		b.WriteString(StyleStatusError.Render("Error: ") + s.lastErr + "\n")
	}
	if s.notFound {
		b.WriteString(StyleStatusWarn.Render("Tax receipt not found.") + "\n")
	}
	if s.receipt != nil {
		b.WriteString(renderTaxReceipt(s.receipt))
	}

	b.WriteString("\n" + StyleMuted.Render("enter look up · esc back"))
	return b.String()
}

// renderTaxReceipt lays out the receipt data the web page shows (serial, donor,
// donation number, issued date), plus the two other operator-meaningful read
// fields (donor email, issuing clerk) and the reprint flag when set. The
// serializer's remaining fields are deliberately omitted: id and the donation
// FK are raw UUIDs redundant with donation_number, numeric issued_by is
// redundant with issued_by_username, created_at/updated_at are record-audit
// timestamps the web page doesn't surface, and pdf_file is a server path covered
// by the web-only PDF caveat below.
func renderTaxReceipt(r *omsapi.TaxReceipt) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Receipt") + "\n")
	rows := [][2]string{
		{"Serial number", r.SerialNumber},
		{"Donor", r.DonorName},
		{"Donor email", r.DonorEmail},
		{"Donation number", r.DonationNumber},
		{"Issued date", r.IssuedDate.String()},
		{"Issued by", r.IssuedByUsername},
	}
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", StyleMuted.Render(row[0]+":"), row[1]))
	}
	if r.IsCopy {
		b.WriteString(fmt.Sprintf("  %s reprint (watermarked copy)\n", StyleMuted.Render("Copy:")))
	}
	b.WriteString("\n" + StyleMuted.Render("PDF download (clean / copy) is available in the web app only.") + "\n")
	return b.String()
}
