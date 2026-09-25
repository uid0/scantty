// Printable storage-slot cards — the shared "render a sheet, put it somewhere
// printable" prompt used by both the slot list (a rack or a hand-picked
// selection) and the slot detail (this one card).
//
// The backend renders Avery 5388 sheets (3 cards per page) and streams the PDF
// back rather than storing it — those sheets are printed once and thrown away,
// so persisting them would only grow MEDIA_ROOT. A terminal can't display a PDF
// and ScanTTY's only printer pipeline is the ESC/POS thermal one (raster PNG on
// a receipt printer, sized for claim tickets, not laser card stock), so the
// honest hand-off is a FILE: write the sheet where the operator asked and name
// the path, which they then send to a real printer (`lp …`) or open. This is
// the inverse of the file-path fields that already exist for uploads
// (site-settings logo, WO manual PDF) and reuses their expandUser().
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// slotCardPrompt is the overlay that collects the destination path (and, for a
// rack print, whether retired slots ride along) before firing the render. It
// owns no API calls — the hosting screen fires the command so its own
// loading/refresh state stays in one place.
type slotCardPrompt struct {
	active  bool
	working bool

	path textinput.Model

	// summary describes what is about to print ("rack 1, level A", "3 selected
	// slots") so the operator can see the SELECTION they are committing, not
	// just the filename.
	summary string
	req     omsapi.SlotCardBatchRequest

	// includeInactive is only meaningful for a rack print — the ids mode prints
	// exactly what was asked for, retired or not — so the row is hidden unless
	// the request carries a rack.
	includeInactive bool

	// cursor: 0 = path, 1 = include-retired (rack mode only).
	cursor int
}

func newSlotCardPrompt() slotCardPrompt {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 512
	return slotCardPrompt{path: ti}
}

// open arms the prompt for one print. defaultName is the filename the backend
// would use, so the suggested path matches what a browser download would have
// been called.
func (p *slotCardPrompt) open(summary string, req omsapi.SlotCardBatchRequest, defaultName string) {
	p.active = true
	p.working = false
	p.summary = summary
	p.req = req
	p.includeInactive = req.IncludeInactive
	p.cursor = 0
	p.path.SetValue(filepath.Join("~", defaultName))
	p.path.CursorEnd()
	p.path.Focus()
}

func (p *slotCardPrompt) close() {
	p.active = false
	p.working = false
	p.path.Blur()
}

func (p *slotCardPrompt) hasToggle() bool { return p.req.Rack != nil }

func (p *slotCardPrompt) rowCount() int {
	if p.hasToggle() {
		return 2
	}
	return 1
}

// request returns the batch request with the live include-retired choice folded
// in. The toggle is dropped for an ids print because the backend ignores it
// there — carrying it would only suggest it did something.
func (p *slotCardPrompt) request() omsapi.SlotCardBatchRequest {
	req := p.req
	req.IncludeInactive = p.hasToggle() && p.includeInactive
	return req
}

// handleKey drives the overlay. It returns fire=true when the operator
// committed, leaving the caller to spend the round trip; the prompt stays up
// (working) so a second enter can't queue a duplicate render.
func (p *slotCardPrompt) handleKey(m tea.KeyMsg) (fire, cancel bool, cmd tea.Cmd) {
	if p.working {
		return false, false, nil
	}
	switch m.String() {
	case "esc":
		return false, true, nil
	case "tab", "down":
		if p.hasToggle() {
			p.cursor = (p.cursor + 1) % p.rowCount()
			p.syncFocus()
		}
		return false, false, textinput.Blink
	case "shift+tab", "up":
		if p.hasToggle() {
			p.cursor = (p.cursor - 1 + p.rowCount()) % p.rowCount()
			p.syncFocus()
		}
		return false, false, textinput.Blink
	case " ":
		if p.cursor == 1 {
			p.includeInactive = !p.includeInactive
			return false, false, nil
		}
	case "enter":
		p.working = true
		return true, false, nil
	}
	if p.cursor == 0 {
		var c tea.Cmd
		p.path, c = p.path.Update(m)
		return false, false, c
	}
	return false, false, nil
}

func (p *slotCardPrompt) syncFocus() {
	if p.cursor == 0 {
		p.path.Focus()
		return
	}
	p.path.Blur()
}

// view draws the prompt at a pane `cells` wide.
//
// BOUNDED TO THE PANE, because the host budgets its body around what this draws
// (StorageSlotsScreen.foot): the summary is clipped with the cut marked, the path
// box is sized so the caret stays on the pane however long the path is typed,
// and the two help lines fold rather than running past the edge — the Avery line
// is 68 cells against the 51 an 80-column pane gives. The prompt's KEYS are
// untouched: it is a recorded exception to the prose-bar conversion
// (proseBarUnconverted), and its own words still name them.
func (p *slotCardPrompt) view(cells int) string {
	const title, label = "Print slot cards", "Save PDF to: "
	var b strings.Builder
	summary := pickerClip(jdeStatusOneLine(p.summary), cells-lipgloss.Width(title)-2)
	b.WriteString(StyleTitle.Render(title) + "  " + StyleMuted.Render(summary) + "\n")
	caret := func(i int) string {
		if i == p.cursor {
			return "▸ "
		}
		return "  "
	}
	b.WriteString(caret(0) + StyleMuted.Render(label) + woBoxView(p.path, cells, strings.Repeat(" ", 2+lipgloss.Width(label))) + "\n")
	if p.hasToggle() {
		b.WriteString(caret(1) + StyleMuted.Render("Include retired slots: ") + elecToggleLabel(p.includeInactive) + "\n")
	}
	if p.working {
		b.WriteString(StyleMuted.Render("Rendering sheet…"))
		return b.String()
	}
	help := "enter render · esc cancel"
	if p.hasToggle() {
		help = "tab move · space toggle · " + help
	}
	b.WriteString(pickerHintAt(help, cells) + "\n")
	b.WriteString(pickerHintAt("3 cards per Avery 5388 sheet · print the saved file (e.g. lp <path>)", cells))
	return b.String()
}

// saveSlotCardPDF writes a rendered sheet to the operator's path and returns
// where it actually landed, plus whether an existing file was replaced. A path
// that names a DIRECTORY (or ends in a separator) gets the server's own
// filename appended, so "~/" is a valid answer and the file is still named the
// way a browser download would have been.
//
// An existing file IS overwritten — re-printing a rack to the same name is the
// common case and a confirm on every save would be noise — but the caller is
// told so it can say "replaced" rather than "saved", which is the difference
// between a reprint and a file the operator didn't mean to lose.
func saveSlotCardPDF(path string, pdf *omsapi.SlotCardPDF, fallbackName string) (string, bool, error) {
	if pdf == nil {
		return "", false, fmt.Errorf("no PDF was returned")
	}
	name := pdf.Filename
	if strings.TrimSpace(name) == "" {
		name = fallbackName
	}

	target := strings.TrimSpace(path)
	if target == "" {
		return "", false, fmt.Errorf("a destination path is required")
	}
	dirTarget := strings.HasSuffix(target, string(os.PathSeparator))
	target = expandUser(target)
	if !dirTarget {
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			dirTarget = true
		}
	}
	if dirTarget {
		target = filepath.Join(target, name)
	}

	_, statErr := os.Stat(target)
	replaced := statErr == nil

	if err := os.WriteFile(target, pdf.Data, 0o644); err != nil {
		return "", false, err
	}
	return target, replaced, nil
}

// slotCardSavedSummary is the status line after a successful write — the path
// plus the size, because "did it actually render?" is the question a zero-byte
// file would otherwise answer wrong.
func slotCardSavedSummary(path string, n int, replaced bool) string {
	verb := "saved"
	if replaced {
		verb = "replaced"
	}
	return fmt.Sprintf("%s %s → %s", verb, humanBytes(n), path)
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// slotCardErrorText prefers the backend's own sentence for the hand-rolled
// 404s the cards action returns (unknown slot ids, "no storage slots match rack
// 9"), falling back to the raw error. Without this the operator would be shown
// a JSON blob — those responses are plain Response(...) returns, so the
// standardized exception envelope parseError understands never wraps them.
func slotCardErrorText(err error) string {
	if err == nil {
		return ""
	}
	if detail, _ := omsapi.StorageSlotErrorDetail(err); detail != "" {
		return detail
	}
	return err.Error()
}
