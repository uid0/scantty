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
	URLTarget *scanner.URLTarget
	Err       string
	Timestamp time.Time
}

type scanLookupMsg struct {
	code      string
	result    *omsapi.LookupResult
	urlTarget *scanner.URLTarget
	kind      scanner.Kind
	err       error
}

func NewScanScreen(deps Deps) *ScanScreen { return &ScanScreen{deps: deps} }

func (s *ScanScreen) Title() string { return "Scan" }

// WantsRawInput keeps every keypress in the scan buffer instead of routing it
// through the root. Phase 3 removed the letter and digit accelerators a code's
// own characters used to trip, but the claim still earns its keep: a scanner
// gun can be configured to emit TAB as a field separator, and tab is the root's
// key for the sidebar menu — so a scan would jump the operator into the menu
// mid-code. `esc` is the way out of here (once to clear a partial buffer, again
// to leave), exactly as it is on a form.
func (s *ScanScreen) WantsRawInput() bool { return true }

func (s *ScanScreen) Init() tea.Cmd { return nil }

func (s *ScanScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyEsc:
			// First esc clears a partial buffer; second drops back to
			// the welcome screen.
			if s.input != "" {
				s.input = ""
				return s, nil
			}
			return s, SwitchTo(WSScan, NewWelcomeScreen())
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
			URLTarget: m.urlTarget,
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
		case m.urlTarget != nil:
			cmd := s.navigateToURL(m.urlTarget)
			label := fmt.Sprintf("scan url → %s %s", m.urlTarget.Kind, m.urlTarget.ResourceID)
			if cmd == nil {
				return s, Status(label+" (no detail screen yet)", StatusWarn)
			}
			return s, tea.Batch(Status(label, StatusOK), cmd)
		case m.result == nil:
			return s, Status(fmt.Sprintf("scan %s: no match", m.code), StatusWarn)
		default:
			cmd := s.navigateToResult(m.result)
			label := fmt.Sprintf("scan %s → %s %v", m.code, m.result.Type, m.result.ID)
			if cmd == nil {
				return s, Status(label, StatusOK)
			}
			return s, tea.Batch(Status(label, StatusOK), cmd)
		}
	}
	return s, nil
}

func (s *ScanScreen) navigateToResult(r *omsapi.LookupResult) tea.Cmd {
	id := fmt.Sprint(r.ID)
	switch r.Type {
	case "item":
		return SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, id))
	case "asset":
		return SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, id))
	case "work_order":
		return SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, id))
	}
	return nil
}

func (s *ScanScreen) navigateToURL(target *scanner.URLTarget) tea.Cmd {
	switch target.Kind {
	case "item", "code":
		return SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, target.ResourceID))
	case "asset":
		return SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, target.ResourceID))
	case "work_order":
		return SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, target.ResourceID))
	case "purchase_order":
		return SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, target.ResourceID))
	case "forgekey_device":
		return SwitchTo(WSForgeKey, NewForgeKeyDeviceDetailScreen(s.deps, target.ResourceID))
	case "location":
		return SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, target.ResourceID))
	case "supplier":
		return SwitchTo(WSInventory, NewSupplierDetailScreen(s.deps, target.ResourceID))
	case "sig":
		return SwitchTo(WSSIGs, NewSIGDetailScreen(s.deps, target.ResourceID))
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
		if kind == scanner.KindOMSURL {
			target, err := scanner.ParseOMSURL(code)
			if err != nil {
				return scanLookupMsg{code: code, kind: kind, err: err}
			}
			return scanLookupMsg{code: code, kind: kind, urlTarget: target}
		}
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
			b.WriteString(fmt.Sprintf("    %s #%v %s\n", r.Lookup.Type, r.Lookup.ID, r.Lookup.Name))
		default:
			b.WriteString(StyleStatusWarn.Render("· "+header+" (no match)") + "\n")
		}
	}
	return b.String()
}
