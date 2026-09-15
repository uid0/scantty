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
	// NoScreen is set when the scan identified a record ScanTTY has no screen
	// for (noScreenNote), so the history row can say so rather than tick it.
	NoScreen  string
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
		// Where the scan goes is decided BEFORE the entry is recorded, because
		// the history row says whether it opened anything: a row ticked green
		// over a label nothing could open is the same false success as the
		// status line beside it.
		var cmd tea.Cmd
		var label string
		switch {
		case m.urlTarget != nil:
			cmd = s.navigateToURL(m.urlTarget)
			label = fmt.Sprintf("scan url → %s %s", m.urlTarget.Kind, m.urlTarget.ResourceID)
			if cmd == nil {
				entry.NoScreen = noScreenNote(m.urlTarget.Kind)
			}
		case m.err == nil && m.result != nil:
			cmd = s.navigateToResult(m.result)
			label = fmt.Sprintf("scan %s → %s %v", m.code, m.result.Type, m.result.ID)
			if cmd == nil {
				entry.NoScreen = noScreenNote(m.result.Type)
			}
		}
		s.history = append([]scanResult{entry}, s.history...)
		if len(s.history) > 8 {
			s.history = s.history[:8]
		}
		switch {
		case m.err != nil:
			return s, Status(fmt.Sprintf("scan %s: %s", m.code, m.err.Error()), StatusError)
		case entry.NoScreen != "":
			return s, Status(label+": "+entry.NoScreen, StatusWarn)
		case cmd != nil:
			return s, tea.Batch(Status(label, StatusOK), cmd)
		default:
			return s, Status(noMatchNote(m.code), StatusWarn)
		}
	}
	return s, nil
}

// noScreenNote is what a scan says when it identified a record ScanTTY has no
// screen to open, and it is a WARNING on purpose.
//
// Both routes used to end in success regardless: a dispatcher answer the switch
// had no arm for (a location, a fixture, a donation item, a project-storage
// stint) was reported as a green `scan X → location 7` with nothing opened, and
// a URL with no arm said "(no detail screen yet)" even for kinds that HAD one.
// An operator at the bench reads a green line as "done" and waits for a screen
// that is not coming. The arms that have a screen now open it; this names the
// ones that do not, by what the label IS, so the sentence is also a request
// somebody can file.
func noScreenNote(kind string) string {
	switch kind {
	case "fixture":
		return "no ScanTTY screen for fixture labels yet"
	case "donation_item":
		return "no ScanTTY screen for donation items yet"
	case "maker_box":
		return "no ScanTTY screen for maker box labels yet"
	case "unknown":
		return "not an OMS label ScanTTY can read"
	}
	return "no ScanTTY screen for this yet"
}

// noMatchNote is what a code the dispatcher could not place says.
//
// A badge-shaped code gets the extra clause because it is the one miss ScanTTY
// can explain: OMS resolves barcodes, location codes and asset tags, but it
// exposes no member-by-badge READ endpoint at all — its one badge surface is
// keyed by user, not by badge — so a real badge can only ever come back
// unmatched here. An operator holding a card is otherwise left to conclude the
// card is broken.
//
// It is a MAY, not an assertion: eight digits is equally a UPC-E, so the code
// may simply be a barcode nobody has catalogued. The wording says which it is
// uncertain about rather than picking one.
func noMatchNote(code string) string {
	if scanner.MightBeForgeKeyBadge(code) {
		return fmt.Sprintf("scan %s: no match — if this is an access badge, "+
			"ScanTTY cannot look badges up yet", code)
	}
	return fmt.Sprintf("scan %s: no match", code)
}

func (s *ScanScreen) navigateToResult(r *omsapi.LookupResult) tea.Cmd {
	id := fmt.Sprint(r.ID)
	switch r.Type {
	// The dispatcher's own spelling is "inventory_item" — every arm of
	// scanner/resolvers.py that lands on an InventoryItem writes that
	// target_type, and NONE writes "item" — so reading only "item" here meant
	// every dispatcher-resolved item scan stopped at a status line naming the
	// record it had just identified instead of opening it. "item" stays as the
	// defensive alias, the same pair the search palette carries for the same
	// reason (search.go).
	//
	// A KIT arrives here like any other item: no resolver filters on is_kit, and
	// the detail this opens already draws the bill of materials
	// (inventory_detail_kit.go). It is not the ONLY way a kit is scanned into —
	// a shelf label is an OMS URL, which scanner.ParseOMSURL resolves locally
	// and navigateToURL opens without a round trip — but it is the one that was
	// broken.
	case "item", "inventory_item":
		return SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, id))
	case "asset":
		return SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, id))
	case "work_order":
		return SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, id))
	// A location is what a location access code resolves to, and it is where a
	// room count starts, so a scan that named one and opened nothing left the
	// operator to go and find it by hand.
	case "location":
		return SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, id))
	// The dispatcher's target_id for a stint is its `stint_id` (PS-…), not a
	// pk, and that is what the detail screen fetches by.
	case "project_storage_stint":
		return SwitchTo(WSFacilities, NewProjectStorageDetailScreen(s.deps, id))
	}
	// "fixture" and "donation_item" also come back from the dispatcher and have
	// no screen yet; Update says so through noScreenNote.
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
	case "third_party_work_order":
		return SwitchTo(WSMaintenance, NewVendorWorkOrderDetailScreen(s.deps, target.ResourceID))
	case "project_storage_stint":
		return SwitchTo(WSFacilities, NewProjectStorageDetailScreen(s.deps, target.ResourceID))
	}
	// "fixture", "donation_item", "maker_box" and "unknown" have no screen;
	// Update says so through noScreenNote.
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
		// Nothing is claimed for the badge path before the dispatcher has been
		// asked, and that is the whole of sc-classify-hex. A badge shares its
		// shape with a UPC-E, ScanTTY has no badge resolver to send one to, and
		// the branch that used to intercept here answered every 8-to-20-character
		// hex string — which is every UPC-A, UPC-E, EAN-13 and ITF-14 there is —
		// with "badge lookup not yet wired". A barcode therefore never reached
		// /api/scanner/dispatch/ at all. scanner.Kind's doc carries the evidence.
		//
		// The badge is still recognised, one step later: noMatchNote reads the
		// shape only once a lookup has come back with nothing, where it explains
		// a miss instead of causing one.
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
	// Two rows, each inside the 51 cells an 80-column pane gives. This screen
	// writes its own body rather than riding jde_form.go's folder, so the bound
	// is kept by hand here; the single line this replaced was 68 cells and lost
	// its tail to clampToBox, which is where it named what the screen accepts.
	b.WriteString(StyleMuted.Render("Type or scan a code, press Enter.") + "\n")
	b.WriteString(StyleMuted.Render("Barcode, asset tag, location code or OMS URL.") + "\n\n")

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
		case r.NoScreen != "":
			b.WriteString(StyleStatusWarn.Render("· "+header) + "\n")
			b.WriteString("    " + StyleStatusWarn.Render(r.NoScreen) + "\n")
		case r.Lookup != nil:
			b.WriteString(StyleStatusOK.Render("✓ "+header) + "\n")
			b.WriteString(fmt.Sprintf("    %s #%v %s\n", r.Lookup.Type, r.Lookup.ID, r.Lookup.Name))
		case r.URLTarget != nil:
			// A label that opened its screen was drawn as "(no match)" here,
			// because only a dispatcher answer counted as a match.
			b.WriteString(StyleStatusOK.Render("✓ "+header) + "\n")
			b.WriteString(fmt.Sprintf("    %s #%s\n", r.URLTarget.Kind, r.URLTarget.ResourceID))
		default:
			b.WriteString(StyleStatusWarn.Render("· "+header+" (no match)") + "\n")
		}
	}
	return b.String()
}
