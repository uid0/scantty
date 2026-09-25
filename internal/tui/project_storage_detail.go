package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// ProjectStorageDetailScreen mirrors InventoryDetailScreen: it fetches one
// stint by stint_id, renders its lifecycle fields, and — because a terminal
// can't draw the printed PNG — reproduces the printed label as a faithful
// TEXT block ("LABEL PREVIEW") carrying the same field set the physical tag
// does.
//
// IT IS ALSO THE WARDEN'S ENFORCEMENT SHEET, the terminal half of the web's
// FacilitiesProjectStoragePage: send the violation notice (`n`), move the
// stint to purgatory (`P`), list every stint the member has held (`m`) and
// generate the stored QR image (`q`), beside the re-print (`p`) and removal
// (`x`) it already had. omsapi's project_storage.go carries the measured wire
// contract; what the sheet decides is WHERE each action is offered, and the
// answer is the web's, read off that page rather than invented:
//
//   - the notice is offered on an expiring_soon, expired or purgatory_warned
//     stint, and NOT while OMS reports outbound email degraded — the notice IS
//     the email, so there it could only fail (the web disables the button on
//     the same `email` service key);
//   - purgatory is offered on a purgatory_warned stint whose notice is stamped;
//   - the QR and the member's stints are offered on every stint.
//
// NO KEY IS GATED ON WHO IS SIGNED IN, and that is also the web's call: the
// page draws every button for whoever reaches it, and OMS decides — the notice
// and purgatory are is_staff, the other two admit a "Storage Admin" volunteer.
// Deps.InitialStaff is a STARTUP snapshot that an in-app sign-in never updates,
// so gating on it would hide the warden's own keys from a warden who signed in
// after launch. A refusal is relayed in OMS's own sentence (stintRefusal).
type ProjectStorageDetailScreen struct {
	deps              Deps
	stintID           string
	stint             *omsapi.ProjectStorageStint
	loadErr           string
	loading           bool
	scroller          *TextScroller
	terminalHeight    int
	terminalWidth     int
	confirmingReprint bool
	reprinting        bool

	// mark-removed action state. Because the backend has no per-item model, the
	// "remove" is the whole-stint mark-removed transition (mirrors the web's
	// markRemoved). removeNote captures the optional audit note; the screen goes
	// raw-input while it's up so the typed note + enter/esc land here.
	confirmingRemove bool
	removing         bool
	removeNote       textinput.Model

	// The violation notice: a y/n confirm naming the member and the address the
	// email goes to.
	confirmingNotice bool
	sendingNotice    bool

	// Purgatory: a confirm carrying the location box the web's prompt carries,
	// prefilled with the stored location.
	confirmingPurgatory bool
	purging             bool
	purgatoryLocation   textinput.Model

	// generatingQR is the QR write in flight. It has no confirm — the web's
	// button has none, and regenerating replaces an image nothing prints from.
	generatingQR bool

	// note is the answer to the last write this visit — working, done or the
	// server's refusal — drawn at the TOP of the sheet. It is on the pane and not
	// only in the status bar because StatusBar.Flash expires after four seconds
	// and the operator who looked away is still looking (AGENTS.md).
	note      string
	noteLevel StatusLevel
}

type projectStorageDetailLoadedMsg struct {
	stint *omsapi.ProjectStorageStint
	err   error
}

type projectStorageReprintedMsg struct {
	err error
}

type projectStorageRemovedMsg struct {
	err error
}

type projectStorageNoticeSentMsg struct {
	stint *omsapi.ProjectStorageStint
	err   error
}

type projectStoragePurgatoryMsg struct {
	stint *omsapi.ProjectStorageStint
	err   error
}

type projectStorageQRMsg struct {
	err error
}

// projectStorageNoticeStatuses are the computed statuses the web's page offers
// the notice on (canSendNotice). OMS's send_violation_notice refuses the same
// set's complement with 409 invalid_state_for_notice, so the two agree today;
// the web's list is the one mirrored because it is what decides whether the
// button is drawn.
var projectStorageNoticeStatuses = map[string]bool{
	"expiring_soon":    true,
	"expired":          true,
	"purgatory_warned": true,
}

// WantsRawInput claims every keypress while a confirmation is up, so its keys
// land here instead of the root's global hotkeys. In the normal view the screen
// stays non-raw so workspace switching and the global shortcuts keep working.
// Mirrors InventoryDetailScreen's delete confirm.
func (s *ProjectStorageDetailScreen) WantsRawInput() bool {
	return s.confirming()
}

// confirming reports whether one of the four confirm frames is up.
func (s *ProjectStorageDetailScreen) confirming() bool {
	return s.confirmingReprint || s.confirmingRemove || s.confirmingNotice || s.confirmingPurgatory
}

// writeOut reports whether a write to this stint is still out. While one is,
// no second write is offered: a frame closed with esc does not cancel its
// request, and a second notice or purgatory sent over it would be a duplicate
// the operator never meant.
func (s *ProjectStorageDetailScreen) writeOut() bool {
	return s.reprinting || s.removing || s.sendingNotice || s.purging || s.generatingQR
}

func NewProjectStorageDetailScreen(deps Deps, stintID string) *ProjectStorageDetailScreen {
	note := textinput.New()
	note.Prompt = ""
	note.Placeholder = "optional note"
	note.CharLimit = 200
	location := textinput.New()
	location.Prompt = ""
	location.Placeholder = "blank keeps the stored one"
	location.CharLimit = 200
	return &ProjectStorageDetailScreen{
		deps:              deps,
		stintID:           stintID,
		loading:           true,
		scroller:          NewTextScroller(defaultDetailHeight),
		removeNote:        note,
		purgatoryLocation: location,
	}
}

func (s *ProjectStorageDetailScreen) Title() string {
	if s.stint != nil {
		return "Stint: " + s.stintID
	}
	return "Project Storage"
}

func (s *ProjectStorageDetailScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ProjectStorageDetailScreen) Init() tea.Cmd {
	deps := s.deps
	stintID := s.stintID
	ctx := s.ctx()
	return func() tea.Msg {
		st, err := deps.OMS.GetProjectStorageStint(ctx, stintID)
		return projectStorageDetailLoadedMsg{stint: st, err: err}
	}
}

// setNote records a write's answer on the sheet and puts the sheet back at its
// top, where the answer is drawn — an answer scrolled off the pane is an answer
// nobody reads.
func (s *ProjectStorageDetailScreen) setNote(text string, level StatusLevel) {
	s.note, s.noteLevel = text, level
	s.scroller.Top()
}

// reloadAfter re-fetches the stint after a write landed: a write's reply
// carries the audit events as they were BEFORE the write (omsapi's
// project_storage.go), and the QR URL in a form only the retrieve makes
// absolute, so the sheet draws the retrieve and never the reply.
func (s *ProjectStorageDetailScreen) reloadAfter(status string) tea.Cmd {
	s.loading = true
	s.loadErr = ""
	return tea.Batch(Status(status, StatusOK), s.Init())
}

func (s *ProjectStorageDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		proseSizeScroller(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
		return s, nil
	case projectStorageDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.stint = m.stint
		// A failed load carries no stint and renderBody reads one — see
		// AssetDetailScreen's loaded arm, which panicked the same way.
		if s.stint != nil {
			s.scroller.Set(s.renderBody())
		}
		return s, nil
	case projectStorageReprintedMsg:
		s.reprinting = false
		s.confirmingReprint = false
		if m.err != nil {
			s.setNote("✗ nothing queued: "+stintRefusal(m.err), StatusError)
			return s, Status("reprint failed: "+m.err.Error(), StatusError)
		}
		// Re-fetch so the new reprint audit event shows in the timeline and
		// the operator gets a fresh confirmation the queue re-surfaced it.
		s.setNote("Claim ticket queued for re-print.", StatusOK)
		return s, s.reloadAfter("queued for reprint")
	case projectStorageRemovedMsg:
		s.removing = false
		s.confirmingRemove = false
		s.removeNote.Blur()
		if m.err != nil {
			s.setNote("✗ nothing removed: "+stintRefusal(m.err), StatusError)
			return s, Status("remove failed: "+m.err.Error(), StatusError)
		}
		// Re-fetch so the new "removed" event + status show in the timeline.
		s.setNote("Stint marked removed.", StatusOK)
		return s, s.reloadAfter("stint marked removed")
	case projectStorageNoticeSentMsg:
		s.sendingNotice = false
		s.confirmingNotice = false
		if m.err != nil {
			refusal := stintRefusal(m.err)
			s.setNote("✗ no notice sent: "+refusal, StatusError)
			return s, Status("no notice sent: "+refusal, StatusError)
		}
		to := s.stintID
		if m.stint != nil && m.stint.Email != "" {
			to = m.stint.Email
		}
		s.setNote("Violation notice sent to "+to+".", StatusOK)
		return s, s.reloadAfter("violation notice sent")
	case projectStoragePurgatoryMsg:
		s.purging = false
		s.confirmingPurgatory = false
		s.purgatoryLocation.Blur()
		if m.err != nil {
			refusal := stintRefusal(m.err)
			s.setNote("✗ not moved: "+refusal, StatusError)
			return s, Status("not moved to purgatory: "+refusal, StatusError)
		}
		where := "purgatory"
		if m.stint != nil && m.stint.PurgatoryLocationName != "" {
			where = m.stint.PurgatoryLocationName
		}
		s.setNote("Stint "+s.stintID+" moved to "+where+".", StatusOK)
		return s, s.reloadAfter("moved to purgatory")
	case projectStorageQRMsg:
		s.generatingQR = false
		if m.err != nil {
			refusal := stintRefusal(m.err)
			s.setNote("✗ no QR image generated: "+refusal, StatusError)
			return s, Status("QR generate failed: "+refusal, StatusError)
		}
		s.setNote("QR image generated for "+s.stintID+".", StatusOK)
		return s, s.reloadAfter("QR image generated")
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		// A confirm is a raw-input sub-phase; route keys to it BEFORE the
		// scroller so typed characters (j/k/g/G) land in its box rather than
		// being swallowed as scroll commands.
		switch {
		case s.confirmingReprint:
			return s.updateConfirmReprint(m)
		case s.confirmingRemove:
			return s.updateConfirmRemove(m)
		case s.confirmingNotice:
			return s.updateConfirmNotice(m)
		case s.confirmingPurgatory:
			return s.updateConfirmPurgatory(m)
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		return s.updateActions(m)
	}
	return s, nil
}

// updateActions is the sheet's own keys. Every write key asks the same
// predicate its bar segment asks, and where it is not offered it DECLINES AND
// SAYS WHY on the status row rather than doing nothing silently.
func (s *ProjectStorageDetailScreen) updateActions(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.Init()
	}
	if s.stint == nil {
		return s, nil
	}
	switch m.String() {
	case "m":
		if !s.memberOffered() {
			return s, Status("this stint names no member to look up", StatusWarn)
		}
		return s, SwitchTo(WSFacilities, NewProjectStorageMemberStintsScreen(s.deps, s.stint.Username))
	}
	if s.writeOut() {
		switch m.String() {
		case "x", "p", "n", "P", "q":
			return s, Status("a write to "+s.stintID+" is still out — wait for its answer", StatusWarn)
		}
		return s, nil
	}
	switch m.String() {
	case "x":
		// Mark the WHOLE stint removed — the only "remove" OMS has (no item
		// model). Guard an already-removed stint with a clear message rather
		// than firing a request the backend would 409.
		if projectStorageIsRemoved(s.stint) {
			return s, Status("stint already removed", StatusWarn)
		}
		s.confirmingRemove = true
		s.removeNote.SetValue("")
		s.removeNote.Focus()
		return s, textinput.Blink
	case "p":
		// Re-print the claim ticket by re-surfacing the stint in the
		// Pi-daemon print queue. Guarded by a y/n confirm since it
		// spends label stock on the shop printer.
		s.confirmingReprint = true
		return s, nil
	case "n":
		if reason := s.noticeWithheld(); reason != "" {
			return s, Status("no notice: "+reason, StatusWarn)
		}
		s.confirmingNotice = true
		return s, nil
	case "P":
		if !s.purgatoryOffered() {
			return s, Status("no purgatory: the web offers it only on a warned stint, and "+
				s.stintID+" is "+stintStatusName(s.stint), StatusWarn)
		}
		s.confirmingPurgatory = true
		s.purgatoryLocation.SetValue(s.stint.PurgatoryLocationName)
		s.purgatoryLocation.CursorEnd()
		s.purgatoryLocation.Focus()
		return s, textinput.Blink
	case "q":
		s.generatingQR = true
		s.setNote("Generating a QR image for "+s.stintID+"…", StatusInfo)
		deps, ctx, id := s.deps, s.ctx(), s.stintID
		return s, func() tea.Msg {
			_, err := deps.OMS.GenerateProjectStorageStintQR(ctx, id)
			return projectStorageQRMsg{err: err}
		}
	}
	return s, nil
}

// emailDown reports whether OMS positively reports outbound email degraded.
// Unknown is healthy: nothing is taken away on ignorance (service_status.go).
func (s *ProjectStorageDetailScreen) emailDown() bool {
	return s.deps.Health.IsDegraded(omsapi.ServiceKeyEmail)
}

// noticeWithheld is why the notice is not offered on this stint right now, or
// "" where it is. The bar and the key both read it, so the legend and the arm
// cannot disagree.
func (s *ProjectStorageDetailScreen) noticeWithheld() string {
	switch {
	case s.stint == nil:
		return "no stint loaded"
	case s.writeOut():
		return "a write is still out"
	case !projectStorageNoticeStatuses[s.stint.Status]:
		return "the web sends it only to an expiring, expired or already-warned stint, and " +
			s.stintID + " is " + stintStatusName(s.stint)
	case s.emailDown():
		return "OMS reports email delivery down, and the notice is an email"
	}
	return ""
}

func (s *ProjectStorageDetailScreen) noticeOffered() bool { return s.noticeWithheld() == "" }

// purgatoryOffered is the web's canMoveToPurgatory: a warned stint whose notice
// is stamped. OMS itself checks only the stamp, so this is stricter than the
// server on a stint already in purgatory or removed — which is the web's
// choice, and the one mirrored.
func (s *ProjectStorageDetailScreen) purgatoryOffered() bool {
	return s.stint != nil && !s.writeOut() &&
		s.stint.Status == "purgatory_warned" && s.stint.NoticeSentAt != nil
}

func (s *ProjectStorageDetailScreen) memberOffered() bool {
	return s.stint != nil && strings.TrimSpace(s.stint.Username) != ""
}

// stintStatusName is a status as a reader says it, keeping "the server named
// none" distinct from a real one.
func stintStatusName(st *omsapi.ProjectStorageStint) string {
	if st == nil || st.Status == "" {
		return "in no state the server named"
	}
	return strings.ReplaceAll(st.Status, "_", " ")
}

// stintRefusal is the one sentence an operator reads for a failed stint write:
// OMS's own words wherever the body carries them. These endpoints answer in
// three shapes — the standardized envelope for a permission refusal, a
// hand-built {"detail", "code"} for the state rules and {"error": "<prose>"}
// for the QR rate limit — and interlockRefusal already recognises all three
// with the recognisers that exist for them; anything else (a gateway's HTML)
// keeps the shape it arrived in.
func stintRefusal(err error) string {
	return interlockRefusal(err)
}

// updateConfirmReprint handles the y/n prompt shown before re-queuing a
// stint's claim ticket. The screen is in raw-input mode here
// (WantsRawInput), so esc/n reach us instead of the root's global handlers.
func (s *ProjectStorageDetailScreen) updateConfirmReprint(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.reprinting {
		// Only the way out acts while the write is out: esc closes the frame,
		// and the answer still lands on the sheet when it comes back.
		if m.String() == "esc" {
			s.confirmingReprint = false
		}
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.stint == nil {
			s.confirmingReprint = false
			return s, nil
		}
		s.reprinting = true
		deps, ctx, stintID := s.deps, s.ctx(), s.stintID
		return s, func() tea.Msg {
			return projectStorageReprintedMsg{err: deps.OMS.ReprintProjectStorageStint(ctx, stintID, "reprint requested via ScanTTY")}
		}
	case "n", "N", "esc":
		s.confirmingReprint = false
	}
	return s, nil
}

// updateConfirmRemove handles the mark-removed note prompt. The screen is raw
// (WantsRawInput) here, so every key reaches us: esc cancels, enter confirms the
// removal with whatever optional note has been typed, and everything else edits
// the note. Mirrors the web markRemoved's note prompt — the note is surfaced in
// the OMS audit log.
func (s *ProjectStorageDetailScreen) updateConfirmRemove(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.removing {
		if m.String() == "esc" {
			s.confirmingRemove = false
			s.removeNote.Blur()
		}
		return s, nil
	}
	switch m.String() {
	case "esc":
		s.confirmingRemove = false
		s.removeNote.Blur()
		return s, nil
	case "enter":
		if s.stint == nil {
			s.confirmingRemove = false
			return s, nil
		}
		s.removing = true
		s.removeNote.Blur()
		deps, ctx, stintID := s.deps, s.ctx(), s.stintID
		note := strings.TrimSpace(s.removeNote.Value())
		return s, func() tea.Msg {
			return projectStorageRemovedMsg{err: deps.OMS.MarkProjectStorageStintRemoved(ctx, stintID, note)}
		}
	}
	var cmd tea.Cmd
	s.removeNote, cmd = s.removeNote.Update(m)
	return s, cmd
}

// updateConfirmNotice is the notice's y/n frame. `y` is the write and `n`,
// which OPENED the frame, cancels it — so a reflexive double-tap of the opening
// key backs out rather than emailing a member.
func (s *ProjectStorageDetailScreen) updateConfirmNotice(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.sendingNotice {
		if m.String() == "esc" {
			s.confirmingNotice = false
		}
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		// Re-asked at the write: the email breaker can trip, or a reload can
		// move the stint, while the frame is up.
		if reason := s.noticeWithheld(); reason != "" {
			s.confirmingNotice = false
			s.setNote("✗ no notice sent: "+reason, StatusWarn)
			return s, nil
		}
		s.sendingNotice = true
		deps, ctx, stintID := s.deps, s.ctx(), s.stintID
		return s, func() tea.Msg {
			st, err := deps.OMS.SendProjectStorageViolationNotice(ctx, stintID)
			return projectStorageNoticeSentMsg{stint: st, err: err}
		}
	case "n", "N", "esc":
		s.confirmingNotice = false
	}
	return s, nil
}

// updateConfirmPurgatory is the purgatory frame: a location box, enter to move,
// esc to cancel, and every other key edits the box.
func (s *ProjectStorageDetailScreen) updateConfirmPurgatory(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.purging {
		if m.String() == "esc" {
			s.confirmingPurgatory = false
		}
		return s, nil
	}
	switch m.String() {
	case "esc":
		s.confirmingPurgatory = false
		s.purgatoryLocation.Blur()
		return s, nil
	case "enter":
		if !s.purgatoryOffered() {
			s.confirmingPurgatory = false
			s.purgatoryLocation.Blur()
			s.setNote("✗ not moved: "+s.stintID+" is "+stintStatusName(s.stint)+
				" now, and purgatory is offered only on a warned stint", StatusWarn)
			return s, nil
		}
		s.purging = true
		s.purgatoryLocation.Blur()
		deps, ctx, stintID := s.deps, s.ctx(), s.stintID
		location := strings.TrimSpace(s.purgatoryLocation.Value())
		return s, func() tea.Msg {
			st, err := deps.OMS.MoveProjectStorageStintToPurgatory(ctx, stintID, location)
			return projectStoragePurgatoryMsg{stint: st, err: err}
		}
	}
	var cmd tea.Cmd
	s.purgatoryLocation, cmd = s.purgatoryLocation.Update(m)
	return s, cmd
}

func (s *ProjectStorageDetailScreen) View() string {
	cells := proseBarCells(s.terminalWidth)
	if s.loading {
		return proseLoadingFrame("Loading stint…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	if s.stint == nil {
		return StyleMuted.Render("Stint not found.")
	}
	if s.confirming() {
		return s.confirmFrame(cells)
	}
	return proseScrollFrame(s.refreshedScroller(), s.terminalHeight, cells, s.bar)
}

// refreshedScroller re-sets the body before it is measured or drawn. The body
// folds the note against the LIVE pane and carries the email-degraded notice,
// and both change after the load that first built it; Set keeps the offset.
func (s *ProjectStorageDetailScreen) refreshedScroller() *TextScroller {
	if s.stint != nil {
		s.scroller.Set(s.renderBody())
	}
	return s.scroller
}

// bar names every key that acts on this sheet, as a record the honesty sweep
// can press (prose_bar.go).
//
// The literal this replaced was "j/k scroll · p re-print · x remove · r refresh
// · esc back" — "j/k scroll" ALONE, while the arrows, pgup/pgdn, `g`/`G` and
// home/end all scrolled it. Eight of the vocabulary's ten keystrokes worked and
// nothing said so, which is the plainest form of the omission this conversion
// closes.
//
// The enforcement segments are CONDITIONAL and each asks the predicate its arm
// asks (noticeOffered, purgatoryOffered, memberOffered, writeOut), so the legend
// changes shape with the stint's status, with email health and with a write in
// flight — the states proseBarFixtures sweeps it in.
func (s *ProjectStorageDetailScreen) bar(scrolls bool) proseBar {
	out := proseNavScroll(scrolls)
	if s.noticeOffered() {
		out = append(out, proseBarItem{Keys: []string{"n"}, Hint: "n send notice"})
	}
	if s.purgatoryOffered() {
		out = append(out, proseBarItem{Keys: []string{"P"}, Hint: "P purgatory"})
	}
	if s.memberOffered() {
		out = append(out, proseBarItem{Keys: []string{"m"}, Hint: "m member's stints"})
	}
	if s.stint != nil && !s.writeOut() {
		qr := "q generate QR"
		if s.stint.QRCodeURL != "" {
			qr = "q regenerate QR"
		}
		out = append(out,
			proseBarItem{Keys: []string{"q"}, Hint: qr},
			proseBarItem{Keys: []string{"p"}, Hint: "p re-print"},
			proseBarItem{Keys: []string{"x"}, Hint: "x remove"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this sheet is DRAWING: a confirm frame's own while one is
// up, loadBar's while a load is in flight or failed, and the sheet's otherwise.
func (s *ProjectStorageDetailScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.stint == nil {
		return nil
	}
	if s.confirming() {
		return s.confirmBar()
	}
	return proseScrollBar(s.refreshedScroller(), s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
}

// loadBar is the sheet's bar while its load is out or has failed — what its key
// switch still answers with nothing drawn (prose_bar.go carries the defect and
// the decision): the reload and the way back. The write keys are not named: on a
// stint a refresh kept, each arms a confirm this frame does not draw.
func (s *ProjectStorageDetailScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

// confirmBar is the bar of whichever confirm frame is up. While its write is
// out, the one key that acts is the way out.
func (s *ProjectStorageDetailScreen) confirmBar() proseBar {
	escClose := proseBarItem{Keys: []string{"esc"}, Hint: "esc close"}
	switch {
	case s.confirmingReprint:
		if s.reprinting {
			return proseBar{escClose}
		}
		return proseBar{
			{Keys: []string{"y", "Y"}, Hint: "y/Y print"},
			{Keys: []string{"n", "N", "esc"}, Hint: "n/N/esc cancel"},
		}
	case s.confirmingNotice:
		if s.sendingNotice {
			return proseBar{escClose}
		}
		return proseBar{
			{Keys: []string{"y", "Y"}, Hint: "y/Y send"},
			{Keys: []string{"n", "N", "esc"}, Hint: "n/N/esc cancel"},
		}
	case s.confirmingRemove:
		if s.removing {
			return proseBar{escClose}
		}
		return proseBar{
			{Keys: []string{"enter"}, Hint: "enter remove"},
			{Keys: []string{"esc"}, Hint: "esc cancel"},
		}
	case s.confirmingPurgatory:
		if s.purging {
			return proseBar{escClose}
		}
		return proseBar{
			{Keys: []string{"enter"}, Hint: "enter move"},
			{Keys: []string{"esc"}, Hint: "esc cancel"},
		}
	}
	return nil
}

// stintConfirm is one confirm frame's content, before it is fitted to the pane.
type stintConfirm struct {
	// headline says what the key on the bar is about to do, to which stint. It
	// never gives.
	headline string
	// facts name who and what the write touches — the member first — one row
	// each, clipped and marked. They give from the END.
	facts []string
	// boxLabel and box are the typed row, when the write takes one. It never
	// gives: it is what enter sends.
	boxLabel string
	box      *textinput.Model
	// caveat is what the write does that the headline does not say. It is folded
	// whole or not drawn, and it is the first thing to give.
	caveat string
	// working replaces the caveat while the write is out, and never gives.
	working string
}

// confirmFrame draws the confirm that is up IN PLACE of the sheet, so the frame
// is only as tall as what it says.
//
// THE GIVE-ORDER IS WRITTEN DOWN, because clampToBox drops from the BOTTOM and
// the bar is what sits there: the bar and the headline never give, nor the
// typed box or the working line; the caveat goes first and goes whole; then the
// facts give from the end. The confirms this replaced drew the whole scrolled
// sheet ABOVE their prompt, so the prompt — with its keys — was exactly what a
// short pane took.
func (s *ProjectStorageDetailScreen) confirmFrame(cells int) string {
	c := s.confirmContent()
	bar := s.confirmBar()
	avail := -1 // unsized: draw everything and let clampToBox decide
	if s.terminalHeight > 0 {
		avail = screenBodyRows(s.terminalHeight) - bar.rows(cells)
	}

	fixed := 1 // the headline
	var boxLine string
	if c.box != nil {
		boxLine = StyleMuted.Render(c.boxLabel) + woBoxView(*c.box, cells, c.boxLabel)
		fixed += 2 // a blank, then the box
	}
	var tail []string
	if c.working != "" {
		tail = []string{StyleMuted.Render(pickerClip(c.working, cells))}
	} else if c.caveat != "" {
		tail = pickerWrap(c.caveat, cells)
	}
	facts := c.facts
	if avail >= 0 {
		room := avail - fixed
		if c.working != "" {
			room -= 1 + len(tail)
		} else if len(tail) > 0 && room-(1+len(tail)) >= 1+len(facts) {
			room -= 1 + len(tail)
		} else {
			tail = nil
		}
		if len(facts) > 0 {
			if room-1 < len(facts) {
				n := room - 1
				if n < 0 {
					n = 0
				}
				facts = facts[:n]
			}
		}
	}

	var b strings.Builder
	b.WriteString(StyleStatusWarn.Render(proseFormLine(c.headline, cells)) + "\n")
	if len(facts) > 0 {
		b.WriteString("\n")
		for _, f := range facts {
			b.WriteString(proseFormLine(f, cells) + "\n")
		}
	}
	if boxLine != "" {
		b.WriteString("\n" + boxLine + "\n")
	}
	if len(tail) > 0 {
		if c.working != "" {
			b.WriteString("\n" + strings.Join(tail, "\n") + "\n")
		} else {
			b.WriteString("\n" + StyleMuted.Render(strings.Join(tail, "\n")) + "\n")
		}
	}
	return b.String() + "\n" + bar.render(cells)
}

// confirmContent is what the frame that is up says.
func (s *ProjectStorageDetailScreen) confirmContent() stintConfirm {
	st := s.stint
	member := "Member: " + projectStorageOwner(st)
	if st.Username != "" && st.Username != projectStorageOwner(st) {
		member += " (@" + st.Username + ")"
	}
	project := "Project: " + projectTitleOrPersonal(st)
	switch {
	case s.confirmingNotice:
		to := "Email to: " + st.Email
		if strings.TrimSpace(st.Email) == "" {
			to = "Email to: none on file"
		}
		c := stintConfirm{
			headline: "Send the violation notice for " + st.StintID + "?",
			facts:    []string{member, to, project, "Status: " + stintStatusName(st)},
			caveat: "OMS emails the member and dates the purgatory deadline from " +
				"the moment it goes out.",
		}
		if st.NoticeSentAt != nil {
			c.caveat = "A notice already went out on " + st.NoticeSentAt.Format("2006-01-02") +
				"; sending another restarts the purgatory clock from now."
		}
		if strings.TrimSpace(st.Email) == "" {
			c.caveat = "No email is on file for this member, so OMS will refuse to send " +
				"it — reach them another way."
		}
		if s.sendingNotice {
			c.working = "Sending the notice to " + firstNonEmpty(st.Email, st.Username) + "…"
		}
		return c
	case s.confirmingPurgatory:
		facts := []string{member, project}
		if st.NoticeSentAt != nil {
			facts = append(facts, "Notice sent: "+st.NoticeSentAt.Format("2006-01-02"))
		}
		if st.SlotCode != "" {
			facts = append(facts, "Vacates slot: "+st.SlotCode)
		}
		c := stintConfirm{
			headline: "Move " + st.StintID + " to purgatory?",
			facts:    facts,
			boxLabel: "Purgatory location: ",
			box:      &s.purgatoryLocation,
			caveat:   "A blank location keeps the stored one.",
		}
		if st.SlotCode != "" {
			c.caveat += " Slot " + st.SlotCode + " is free for the next member at once; the stint keeps it as history."
		}
		if st.PurgatoryAt != nil && time.Now().Before(*st.PurgatoryAt) {
			c.caveat = "The grace period runs to " + st.PurgatoryAt.Format("2006-01-02") +
				" and OMS does not wait for it. " + c.caveat
		}
		if s.purging {
			c.working = "Moving " + st.StintID + " to purgatory…"
		}
		return c
	case s.confirmingRemove:
		c := stintConfirm{
			headline: "Mark " + st.StintID + " removed?",
			facts:    []string{member, project},
			boxLabel: "Note: ",
			box:      &s.removeNote,
			caveat:   "Ends the stint and frees its AprilTag. It can't be undone.",
		}
		if s.removing {
			c.working = "Marking " + st.StintID + " removed…"
		}
		return c
	case s.confirmingReprint:
		c := stintConfirm{
			headline: "Re-print the claim ticket for " + st.StintID + "?",
			facts:    []string{member, project},
			caveat:   "Spends label stock: the print daemon prints it on its next poll.",
		}
		if s.reprinting {
			c.working = "Queuing the re-print…"
		}
		return c
	}
	return stintConfirm{}
}

func (s *ProjectStorageDetailScreen) renderBody() string {
	st := s.stint
	cells := proseBarCells(s.terminalWidth)
	var b strings.Builder

	if s.note != "" {
		style := StyleMuted
		switch s.noteLevel {
		case StatusError:
			style = StyleStatusError
		case StatusWarn:
			style = StyleStatusWarn
		case StatusOK:
			style = StyleStatusOK
		}
		// A refusal can be a gateway's whole HTML page (parseError hands the raw
		// body over), so the note is bounded, folded and its cut marked by the
		// one copy of that shape rather than wrapped whole.
		b.WriteString(style.Render(strings.Join(failDetailLines(jdeStatusOneLine(s.note), cells, stintNoteRows), "\n")) + "\n\n")
	}
	if s.emailDown() {
		b.WriteString(StyleStatusWarn.Render(strings.Join(pickerWrap("⚠ "+projectStorageEmailDown, cells), "\n")) + "\n\n")
	}

	b.WriteString(StyleTitle.Render(projectStorageOwner(st)))
	if st.Status != "" {
		b.WriteString("  " + StyleMuted.Render("["+st.Status+"]"))
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("Stint " + st.StintID))
	if st.Username != "" {
		b.WriteString(StyleMuted.Render(" · @" + st.Username))
	}
	if st.Email != "" {
		b.WriteString(StyleMuted.Render(" · " + st.Email))
	}
	b.WriteString("\n\n")

	b.WriteString(StyleTitle.Render("Project") + "\n")
	b.WriteString(projectTitleOrPersonal(st) + "\n\n")

	b.WriteString(StyleTitle.Render("Timeline") + "\n")
	if !st.StartedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Started: ") + st.StartedAt.Format("2006-01-02") + "\n")
	}
	switch {
	case st.ExpiresAt != nil && !st.ExpiresAt.IsZero():
		b.WriteString(StyleMuted.Render("Expires: ") + st.ExpiresAt.Format("2006-01-02") +
			StyleMuted.Render(fmt.Sprintf("  (%s)", expiryWeekDay(st))) + "\n")
	case st.ExpiryWeek > 0 || st.ExpiryDayOfYear > 0:
		b.WriteString(StyleMuted.Render("Expiry: ") + expiryWeekDay(st) + "\n")
	}
	if st.NoticeSentAt != nil && !st.NoticeSentAt.IsZero() {
		b.WriteString(StyleMuted.Render("Notice sent: ") + st.NoticeSentAt.Format("2006-01-02") + "\n")
	}
	// The deadline the notice set, until the stint has actually been moved —
	// the web's "→ Purgatory at" row, drawn on the same condition.
	if st.PurgatoryAt != nil && st.Status == "purgatory_warned" {
		b.WriteString(StyleMuted.Render("Purgatory due: ") + st.PurgatoryAt.Format("2006-01-02") + "\n")
	}
	if st.MovedToPurgatoryAt != nil && !st.MovedToPurgatoryAt.IsZero() {
		b.WriteString(StyleMuted.Render("Moved to purgatory: ") + st.MovedToPurgatoryAt.Format("2006-01-02") + "\n")
	}
	if st.RemovedAt != nil && !st.RemovedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Removed: ") + st.RemovedAt.Format("2006-01-02") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Location") + "\n")
	// The slot is authoritative when the stint claimed one — a surveyed
	// physical place with a printed code beats a free-text description — so it
	// leads, and the ad-hoc name is only shown when it says something the slot
	// doesn't. location_display is the backend's own spelling of that
	// precedence; the rows below expand it rather than restate it.
	if st.SlotCode != "" {
		b.WriteString(StyleMuted.Render("Slot: ") + st.SlotCode + "\n")
	}
	if st.StorageLocationName != "" {
		b.WriteString(StyleMuted.Render("Storage: ") + st.StorageLocationName + "\n")
	} else if st.SlotCode == "" {
		b.WriteString(StyleMuted.Render("Storage: ") + "—\n")
	}
	if st.PurgatoryLocationName != "" {
		b.WriteString(StyleMuted.Render("Purgatory: ") + st.PurgatoryLocationName + "\n")
	}
	b.WriteString("\n")

	if strings.TrimSpace(st.Notes) != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(st.Notes + "\n\n")
	}

	if len(st.Events) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Events (%d)", len(st.Events))) + "\n")
		for _, ev := range st.Events {
			line := "  · "
			if !ev.CreatedAt.IsZero() {
				line += ev.CreatedAt.Format("2006-01-02") + " "
			}
			line += ev.Action
			b.WriteString(line + "\n")
			meta := []string{}
			if ev.ActorUsername != "" {
				meta = append(meta, "by @"+ev.ActorUsername)
			}
			if ev.Notes != "" {
				meta = append(meta, ev.Notes)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// The stored QR image, which the web previews and a terminal cannot draw:
	// where it is served, off the retrieve (the only endpoint that sends it
	// absolute), or that none has been generated.
	// A URL is one unspaced token longer than an 80-column pane, so it is FOLDED
	// rather than handed to clampToBox, which would take its tail — the file
	// name — with no mark.
	b.WriteString(StyleTitle.Render("QR image") + "\n")
	if st.QRCodeURL != "" {
		b.WriteString(strings.Join(pickerWrap(st.QRCodeURL, cells), "\n") + "\n\n")
	} else {
		b.WriteString(StyleMuted.Render("none generated yet") + "\n\n")
	}

	b.WriteString(s.renderLabelPreview())
	return b.String()
}

// stintNoteRows is the most rows a write's answer takes at the top of the sheet:
// a sentence and the head of anything longer, with the cut said.
const stintNoteRows = 4

// projectStorageEmailDown is the web's notice-email-unavailable sentence.
const projectStorageEmailDown = "Email delivery is temporarily unavailable — the violation notice can't be sent yet."

// renderLabelPreview reproduces the printed claim tag as text. It is NOT an
// attempt to draw the PNG — it carries the same field set the physical label
// prints (stint id, owner, project, expiry week/day, storage + purgatory
// locations, status) so an operator can verify a tag against the record from
// a terminal.
func (s *ProjectStorageDetailScreen) renderLabelPreview() string {
	st := s.stint
	lines := []string{"STINT    " + st.StintID}
	// The printed ticket puts "Slot <code>" immediately under the stint id and
	// omits the line entirely for ad-hoc storage (backend label_service.
	// ticket_lines), so the preview places it the same way — a tag on a shelf
	// today still describes exactly what the renderer produces.
	if st.SlotCode != "" {
		lines = append(lines, "SLOT     "+st.SlotCode)
	}
	lines = append(lines,
		"OWNER    "+projectStorageOwner(st),
		"PROJECT  "+projectTitleOrPersonal(st),
		"EXPIRY   "+expiryWeekDay(st),
	)
	if storage := st.StorageLocationName; storage != "" {
		lines = append(lines, "STORAGE  "+storage)
	} else if st.SlotCode == "" {
		lines = append(lines, "STORAGE  —")
	}
	if st.PurgatoryLocationName != "" {
		lines = append(lines, "PURGTRY  "+st.PurgatoryLocationName)
	}
	lines = append(lines, "STATUS   "+st.Status)

	var b strings.Builder
	b.WriteString(StyleTitle.Render("LABEL PREVIEW") + "\n")
	b.WriteString(StyleMuted.Render("text of what the printed label carries — not the image itself") + "\n")
	b.WriteString(boxText(lines))
	return b.String()
}

// projectStorageIsRemoved reports whether a stint has already been marked
// removed — either the computed status says so or removed_at is stamped. Used to
// pre-guard the mark-removed action so an operator gets a clear "already
// removed" message instead of the backend's 409.
func projectStorageIsRemoved(st *omsapi.ProjectStorageStint) bool {
	if st == nil {
		return false
	}
	if st.Status == "removed" {
		return true
	}
	return st.RemovedAt != nil && !st.RemovedAt.IsZero()
}

// projectStorageOwner is the human name for a stint, preferring the
// backend-computed display_name and falling back to first/last then the
// bare username so the header/label never renders blank.
func projectStorageOwner(st *omsapi.ProjectStorageStint) string {
	if strings.TrimSpace(st.DisplayName) != "" {
		return st.DisplayName
	}
	if name := strings.TrimSpace(st.FirstName + " " + st.LastName); name != "" {
		return name
	}
	return st.Username
}

// projectTitleOrPersonal renders the project title, or "(personal)" for a
// blank title — matching the web overview's treatment of personal storage.
func projectTitleOrPersonal(st *omsapi.ProjectStorageStint) string {
	if strings.TrimSpace(st.ProjectTitle) != "" {
		return st.ProjectTitle
	}
	return "(personal)"
}

// expiryWeekDay formats the computed expiry as the label's "Wk NN / Day DDD"
// (week zero-padded to 2, day-of-year to 3).
func expiryWeekDay(st *omsapi.ProjectStorageStint) string {
	return fmt.Sprintf("Wk %02d / Day %03d", st.ExpiryWeek, st.ExpiryDayOfYear)
}

// boxText frames PLAIN (unstyled) text lines in a light box so the label
// preview reads like a physical tag. Lines must be unstyled — the frame
// width is measured with lipgloss.Width, so embedded ANSI would misalign
// the right border.
func boxText(lines []string) string {
	width := 0
	for _, l := range lines {
		if w := lipgloss.Width(l); w > width {
			width = w
		}
	}
	inner := width + 2 // one space of padding on each side
	var b strings.Builder
	b.WriteString("┌" + strings.Repeat("─", inner) + "┐\n")
	for _, l := range lines {
		pad := width - lipgloss.Width(l)
		b.WriteString("│ " + l + strings.Repeat(" ", pad) + " │\n")
	}
	b.WriteString("└" + strings.Repeat("─", inner) + "┘")
	return b.String()
}
