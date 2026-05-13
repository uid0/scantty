package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
	"github.com/uid0/scantty/internal/scanner"
)

type ScanScreen struct {
	deps    Deps
	input   string
	history []scanResult
	pending bool
}

type scanResult struct {
	Code      string
	Kind      scanner.Kind
	Lookup    *omsapi.LookupResult
	Err       string
	Timestamp time.Time
}

type scanLookupMsg struct {
	code   string
	result *omsapi.LookupResult
	kind   scanner.Kind
	err    error
}

func NewScanScreen(deps Deps) *ScanScreen { return &ScanScreen{deps: deps} }

func (s *ScanScreen) Title() string { return "Scan" }

func (s *ScanScreen) Init() tea.Cmd { return nil }

func (s *ScanScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyEnter:
			if strings.TrimSpace(s.input) == "" {
				return s, nil
			}
			code := strings.TrimSpace(s.input)
			s.input = ""
			return s, s.lookup(code)
		case tea.KeyBackspace:
			if len(s.input) > 0 {
				s.input = s.input[:len(s.input)-1]
			}
			return s, nil
		case tea.KeyRunes, tea.KeySpace:
			s.input += string(m.Runes)
			return s, nil
		}
	case scanLookupMsg:
		s.pending = false
		entry := scanResult{
			Code:      m.code,
			Kind:      m.kind,
			Lookup:    m.result,
			Timestamp: time.Now(),
		}
		if m.err != nil {
			entry.Err = m.err.Error()
		}
		s.history = append([]scanResult{entry}, s.history...)
		if len(s.history) > 8 {
			s.history = s.history[:8]
		}
		switch {
		case m.err != nil:
			return s, Status(fmt.Sprintf("scan %s: %s", m.code, m.err.Error()), StatusError)
		case m.result == nil:
			return s, Status(fmt.Sprintf("scan %s: no match", m.code), StatusWarn)
		default:
			cmd := s.navigateToResult(m.result)
			if cmd == nil {
				return s, Status(fmt.Sprintf("scan %s → %s #%d", m.code, m.result.Type, m.result.ID), StatusOK)
			}
			return s, tea.Batch(
				Status(fmt.Sprintf("scan %s → %s #%d", m.code, m.result.Type, m.result.ID), StatusOK),
				cmd,
			)
		}
	}
	return s, nil
}

func (s *ScanScreen) navigateToResult(r *omsapi.LookupResult) tea.Cmd {
	switch r.Type {
	case "item":
		return SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, fmt.Sprintf("%d", r.ID)))
	}
	return nil
}

func (s *ScanScreen) lookup(code string) tea.Cmd {
	s.pending = true
	kind := scanner.Classify(code)
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if kind == scanner.KindForgeKeyBadge {
			return scanLookupMsg{
				code: code,
				kind: kind,
				err:  fmt.Errorf("badge lookup not yet wired — needs OMS member-by-badge endpoint"),
			}
		}
		if deps.OMS == nil {
			return scanLookupMsg{code: code, kind: kind, err: fmt.Errorf("OMS client not configured")}
		}
		result, err := deps.OMS.LookupCode(ctx, code)
		return scanLookupMsg{code: code, kind: kind, result: result, err: err}
	}
}

func (s *ScanScreen) View() string {
	var b strings.Builder

	prompt := StyleTitle.Render("▶ ") + s.input
	cursor := "_"
	if s.pending {
		cursor = StyleMuted.Render("…")
	}
	b.WriteString(prompt + cursor + "\n\n")
	b.WriteString(StyleMuted.Render("Type or scan a code, press Enter. 6-char OMS or 8-20 char hex badge.") + "\n\n")

	if len(s.history) == 0 {
		b.WriteString(StyleMuted.Render("No scans yet.") + "\n")
		return b.String()
	}

	b.WriteString(StyleTitle.Render("Recent scans") + "\n\n")
	for _, r := range s.history {
		header := fmt.Sprintf("%s  %s", r.Timestamp.Format("15:04:05"), r.Code)
		switch {
		case r.Err != "":
			b.WriteString(StyleStatusError.Render("✗ "+header) + "\n")
			b.WriteString("    " + StyleStatusError.Render(r.Err) + "\n")
		case r.Lookup != nil:
			b.WriteString(StyleStatusOK.Render("✓ "+header) + "\n")
			b.WriteString(fmt.Sprintf("    %s #%d %s\n", r.Lookup.Type, r.Lookup.ID, r.Lookup.Name))
		default:
			b.WriteString(StyleStatusWarn.Render("· "+header+" (no match)") + "\n")
		}
	}
	return b.String()
}
