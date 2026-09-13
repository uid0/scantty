// ForgeKey certificates — staff PKI visibility + CA rotation.
//
// TUI counterpart to the web ForgeKeyCertificatesPage ("Facilities · ForgeKey ·
// Certificates (PKI)"). Read-only view of the internal certificate authority
// and every issued device (mTLS) certificate, plus the one write action the web
// exposes: rotate the root CA. Reached from the Facilities menu
// ([[scantty-parity-program]], [[scantty-forgekey-badge-auth]]).
//
// PKI-SENSITIVE — deliberately no key material. The backend serializers carry
// only public certificate metadata (CA fingerprint/validity, device serial /
// fingerprint / subject / validity / status); the stored CA private key and the
// cert PEMs are NOT serializer fields, so, exactly like the web page, nothing
// secret ever reaches this screen and none is rendered.
//
// Device-cert ISSUANCE and REVOCATION are NOT API actions — the web says
// "Issued via enrollment · revoke in admin", so this screen mirrors that: it
// does NOT offer a per-cert revoke (that would be a superset on a security
// surface). The only action is rotate-root-CA, behind a y/n confirm, matching
// the web's confirm modal. There is no PEM download / CSR upload anywhere on
// this surface, so nothing is deferred as web-only.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type ForgeKeyCertificatesScreen struct {
	deps    Deps
	cas     []forgekeyapi.CertificateAuthority
	certs   []forgekeyapi.DeviceCertificate
	loading bool
	loadErr string

	confirmingRotate bool
	rotating         bool

	scroll         int // top row of the device-cert table window
	terminalHeight int
	terminalWidth  int
}

type fkCertsLoadedMsg struct {
	cas   []forgekeyapi.CertificateAuthority
	certs []forgekeyapi.DeviceCertificate
	err   error
}

type fkCaRotatedMsg struct {
	err error
}

func NewForgeKeyCertificatesScreen(deps Deps) *ForgeKeyCertificatesScreen {
	return &ForgeKeyCertificatesScreen{deps: deps, loading: true}
}

func (s *ForgeKeyCertificatesScreen) Title() string { return "Certificates (PKI)" }

// WantsRawInput claims every key while the rotate confirm is up, so y/n/esc land
// here instead of the root's global hotkeys.
func (s *ForgeKeyCertificatesScreen) WantsRawInput() bool { return s.confirmingRotate }

// HandlesKey claims G (scroll-to-bottom), which otherwise opens the global
// Category management surface — mirrors the webhooks list claiming G.
func (s *ForgeKeyCertificatesScreen) HandlesKey(key string) bool { return key == "G" }

func (s *ForgeKeyCertificatesScreen) Init() tea.Cmd { return s.load() }

func (s *ForgeKeyCertificatesScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ForgeKeyCertificatesScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		// Mirror the web's Promise.all: fetch both, surface the first error.
		cas, err := deps.ForgeKey.ListCertificateAuthorities(ctx)
		if err != nil {
			return fkCertsLoadedMsg{err: err}
		}
		certs, err := deps.ForgeKey.ListDeviceCertificates(ctx)
		if err != nil {
			return fkCertsLoadedMsg{err: err}
		}
		return fkCertsLoadedMsg{cas: cas, certs: certs}
	}
}

func (s *ForgeKeyCertificatesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		s.clampScroll()
		return s, nil
	case fkCertsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.cas = m.cas
			s.certs = m.certs
		}
		s.clampScroll()
		return s, nil
	case fkCaRotatedMsg:
		s.rotating = false
		s.confirmingRotate = false
		if m.err != nil {
			return s, Status("CA rotation failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(
			// Mirror the web success toast verbatim.
			Status("Root CA rotated — re-flash / rebuild firmware to trust the new root.", StatusOK),
			s.load(),
		)
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		if s.confirmingRotate {
			return s.updateConfirmRotate(m)
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "R":
			s.confirmingRotate = true
			return s, nil
		case "j", "down":
			s.scroll++
			s.clampScroll()
		case "k", "up":
			s.scroll--
			s.clampScroll()
		case "pgdown":
			s.scroll += s.certWindow()
			s.clampScroll()
		case "pgup":
			s.scroll -= s.certWindow()
			s.clampScroll()
		case "g", "home":
			s.scroll = 0
		case "G", "end":
			s.scroll = len(s.certs)
			s.clampScroll()
		}
	}
	return s, nil
}

func (s *ForgeKeyCertificatesScreen) updateConfirmRotate(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.rotating {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		s.rotating = true
		deps := s.deps
		ctx := s.ctx()
		return s, func() tea.Msg {
			_, err := deps.ForgeKey.RotateCA(ctx)
			return fkCaRotatedMsg{err: err}
		}
	case "n", "N", "esc":
		s.confirmingRotate = false
	}
	return s, nil
}

// activeCA returns the single is_active CA (web: cas.find(c => c.is_active)).
func (s *ForgeKeyCertificatesScreen) activeCA() *forgekeyapi.CertificateAuthority {
	for i := range s.cas {
		if s.cas[i].IsActive {
			return &s.cas[i]
		}
	}
	return nil
}

func (s *ForgeKeyCertificatesScreen) retiredCount() int {
	n := 0
	for _, c := range s.cas {
		if !c.IsActive {
			n++
		}
	}
	return n
}

// caSectionLines is the number of rows the CA card occupies, so the device-cert
// table can be windowed to fit the remaining body budget exactly (no clipping of
// the footer hotkeys).
//
// IT IS A COUNT, SO EVERY LINE IT COUNTS MUST BE ONE LINE. The common name is
// read off the CA record, and nothing between the model and this pane refuses a
// newline in it; drawn as stored, a two-line name made the card a line taller
// than this says, the window one row too generous, and what clampToBox took off
// the bottom was the footer. renderCASection flattens it (fkCertOneLine) for
// that reason.
func (s *ForgeKeyCertificatesScreen) caSectionLines() int {
	lines := 1 // "Root certificate authority" heading
	if s.activeCA() != nil {
		lines += 4 // CN, fingerprint, validity, cert counts
	} else {
		lines += len(pickerWrap(fkNoActiveCA, s.paneCells())) // folded, so counted as drawn
	}
	if s.retiredCount() > 0 {
		lines++
	}
	return lines
}

// certRowLines is the physical-line height of one device-cert row: the web
// stacks the fingerprint under the chip id, so each row renders two lines.
const certRowLines = 2

// certWindow is how many device-cert rows fit under the CA card + section
// header + footer within the body budget. Each row is certRowLines tall.
//
// THE FOOTER IS MEASURED, NOT ASSUMED. The constant this replaced reserved two
// rows for it — a blank and ONE hint line — and the footer that names every key
// this switch binds folds onto more than one at 80 columns, so a window budgeted
// at the old constant assembled a frame taller than the pane and the fold was
// what clampToBox took. It is measured against the CEILING bar (the scroll keys
// on it) for the fixed-point reason proseSizeScroller gives: naming the scroll
// keys can fold the bar onto another row, which shrinks this window, which is
// the input to whether the list scrolls at all.
func (s *ForgeKeyCertificatesScreen) certWindow() int {
	body := screenBodyHeight(s.terminalHeight)
	// Reserved chrome: CA card + blank + cert-section header + the 2 rows for the
	// ↑/↓ overflow hints, then the folded footer with its blank separator.
	chrome := s.caSectionLines() + 4 + s.bar(true).rows(s.paneCells())
	avail := body - chrome
	if avail < certRowLines {
		return 1
	}
	return avail / certRowLines
}

func (s *ForgeKeyCertificatesScreen) clampScroll() {
	maxTop := len(s.certs) - s.certWindow()
	if maxTop < 0 {
		maxTop = 0
	}
	if s.scroll > maxTop {
		s.scroll = maxTop
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
}

// paneCells is the width this sheet folds and budgets against: the pane the
// terminal really gave.
func (s *ForgeKeyCertificatesScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// scrolls reports whether the device-certificate table outruns its window —
// the one question every movement key on this sheet is gated on.
//
// EXACT, NOT APPROXIMATE: clampScroll pins the offset to 0 whenever
// len(certs) <= certWindow, so on a table that fits, every one of j/k, the arrows,
// pgup/pgdn and g/G/home/end redraws the pane byte for byte. The bar names them
// exactly where this is true.
func (s *ForgeKeyCertificatesScreen) scrolls() bool {
	return len(s.certs) > s.certWindow()
}

// bar names every key that acts on this sheet, as a RECORD rather than a literal
// — prose_bar.go carries the conversion.
//
// It used to be
//
//	R rotate root CA · r refresh · j/k scroll · esc back
//
// which named two of the ten movement keystrokes this switch binds — the whole
// vocabulary, pgup/pgdn and g/G/home/end included — so eight of them scrolled
// the certificate table under no word; and it named `j/k scroll` over a table
// that fitted, where they scroll nothing.
//
// THE VERB IS `scroll` because what moves is a WINDOW over the table and not a
// cursor — there is no highlighted row, and an operator reading "move" would
// look for one (proseNavScroll).
func (s *ForgeKeyCertificatesScreen) bar(scrolls bool) proseBar {
	return append(proseNavScroll(scrolls),
		proseBarItem{Keys: []string{"R"}, Hint: "R rotate root CA"},
		proseBarRefresh,
		proseBarEsc,
	)
}

// proseBar is the bar this sheet is DRAWING, and nil in the states that draw
// something else instead — the rotate confirm, which names its own keys and is
// left a literal for the reasons the earlier recipes left their y/n confirms. A
// load in flight or failed draws loadBar's.
func (s *ForgeKeyCertificatesScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.confirmingRotate {
		return nil
	}
	return s.bar(s.scrolls())
}

// loadBar is the sheet's bar while its load is out or has failed — what its key
// switch still answers with nothing drawn (prose_bar.go carries the defect and
// the decision): the reload and the way back. `R` is not named, and it is the
// sharpest gating candidate on the sheet: all it does here is arm the ROOT CA
// ROTATION confirm under a frame that does not draw it.
func (s *ForgeKeyCertificatesScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

func (s *ForgeKeyCertificatesScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading certificates…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if s.confirmingRotate {
		return s.viewConfirmRotate()
	}

	var b strings.Builder
	b.WriteString(s.renderCASection())
	b.WriteString("\n")
	b.WriteString(s.renderCertSection())
	b.WriteString("\n")
	b.WriteString(s.proseBar().render(s.paneCells()))
	return b.String()
}

// renderCASection draws the CA card, every line of it ONE line (caSectionLines
// counts them) and clipped to the pane with the cut marked, because clampToBox
// cuts from the right with nothing saying so and a 64-hex-digit fingerprint is
// wider than an 80-column pane.
func (s *ForgeKeyCertificatesScreen) renderCASection() string {
	cells := s.paneCells()
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Root certificate authority") + "\n")
	if ca := s.activeCA(); ca != nil {
		cn := ca.CommonName
		if cn == "" {
			cn = ca.Name
		}
		b.WriteString(StyleMuted.Render("CN: ") + pickerClip(fkCertOneLine(cn), cells-len("CN: ")) + "\n")
		b.WriteString(StyleMuted.Render(pickerClip(fkCertOneLine(ca.FingerprintSHA256), cells)) + "\n")
		b.WriteString(fmt.Sprintf("Valid %s → %s\n", fkCertDate(ca.NotBefore), fkCertDate(ca.NotAfter)))
		b.WriteString(StyleMuted.Render(fmt.Sprintf("%d active · %d revoked device certs",
			derefCount(ca.ActiveCertCount), derefCount(ca.RevokedCertCount))) + "\n")
	} else {
		// FOLDED rather than clipped: its tail is the command that fixes it.
		b.WriteString(pickerHintAt(fkNoActiveCA, cells) + "\n")
	}
	if n := s.retiredCount(); n > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("%d retired CA(s) in history.", n)) + "\n")
	}
	return b.String()
}

func (s *ForgeKeyCertificatesScreen) renderCertSection() string {
	var b strings.Builder
	// Clipped PLAIN and styled after, so a cut can never fall between a style's
	// opening sequence and its reset.
	const heading = "Device certificates"
	b.WriteString(StyleTitle.Render(heading) + "  " +
		StyleMuted.Render(pickerClip("· issued via enrollment · revoke in admin", s.paneCells()-len(heading)-2)) + "\n")
	if len(s.certs) == 0 {
		b.WriteString(StyleMuted.Render("No device certificates issued yet.") + "\n")
		return b.String()
	}

	window := s.certWindow()
	start := s.scroll
	if start > len(s.certs) {
		start = len(s.certs)
	}
	end := start + window
	if end > len(s.certs) {
		end = len(s.certs)
	}
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(s.renderCertRow(s.certs[i]) + "\n")
	}
	if end < len(s.certs) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.certs)-end)) + "\n")
	}
	return b.String()
}

// renderCertRow shows the same fields the web table shows — device chip id,
// fingerprint, serial, expiry (not_after), and the colour-coded status — but
// stacked over two lines (primary + dimmed secondary) the way the web stacks
// the fingerprint under the chip id in the Device cell. The status badge rides
// line 1 next to the chip so it's never the column a narrow terminal clips; the
// fingerprint is last on line 2 so only its (least-important) tail can clip.
// Subject / issued_by / not_before are intentionally NOT shown — the web page
// doesn't show them either (mirror the surface, no superset).
func (s *ForgeKeyCertificatesScreen) renderCertRow(c forgekeyapi.DeviceCertificate) string {
	chip := c.DeviceChipID
	if chip == "" {
		chip = "—"
	}
	// chip ids are CharField(max_length=64); cap the display so a long one
	// can't push the status badge off the row.
	chip = truncateOneLine(chip, 28)
	fp := truncateOneLine(c.FingerprintSHA256, 40)
	// The status is flattened too: the window counts every row as certRowLines,
	// so a row that drew a third line would take the footer's last fold with it.
	line1 := fmt.Sprintf("  %-28s  %s", chip, certStatusBadge(fkCertOneLine(c.Status)))
	secondary := fmt.Sprintf("#%s · exp %s · %s", truncateOneLine(c.Serial, 24), fkCertDate(c.NotAfter), fp)
	// Clipped with the cut marked: the serial, the expiry and the fingerprint come
	// to about 90 cells against the 45 an 80-column pane leaves this indent, and
	// clampToBox would take the fingerprint's tail with nothing saying so.
	line2 := "      " + StyleMuted.Render(pickerClip(secondary, s.paneCells()-6))
	return line1 + "\n" + line2
}

func (s *ForgeKeyCertificatesScreen) viewConfirmRotate() string {
	if s.rotating {
		return StyleMuted.Render("Rotating root CA…")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Rotate the root CA?") + "\n\n")
	b.WriteString("This mints a brand-new self-signed root CA and retires the current one.\n")
	b.WriteString(StyleMuted.Render("Devices won't trust the new root until they're re-flashed (or rebuilt via the") + "\n")
	b.WriteString(StyleMuted.Render("firmware pipeline with the new CA embedded). This can't be undone.") + "\n\n")
	b.WriteString(StyleStatusWarn.Render("y rotate root CA · n/esc cancel"))
	return b.String()
}

// certStatusBadge colours the lifecycle label the same way the web
// CERT_STATUS_COLORS map does: active=green, expired=yellow, revoked=red.
// fkNoActiveCA is what the CA card says when there is no active CA, and what it
// tells staff to run.
const fkNoActiveCA = "No active CA. Bootstrap one with `manage.py forgekey_ca init`."

// fkCertOneLine flattens a value the CA card counts as ONE line — see
// caSectionLines for what a stored newline cost.
func fkCertOneLine(v string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(v)
}

func certStatusBadge(status string) string {
	switch status {
	case "active":
		return StyleStatusOK.Render(status)
	case "expired":
		return StyleStatusWarn.Render(status)
	case "revoked":
		return StyleStatusError.Render(status)
	default:
		return StyleMuted.Render(status)
	}
}

// fkCertDate renders a validity date the way the web's fmtDate does: a plain
// date, or an em-dash for the zero value (the web's null case).
func fkCertDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02")
}

func derefCount(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
