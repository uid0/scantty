// PurchaseOrderAttachmentsScreen — manage the files attached to a PO.
//
// TUI counterpart to the Attachments section of the web PurchaseOrderPage
// (frontend/src/pages/PurchaseOrderPage.tsx): list the attachments, upload a
// new one (file + optional description), and delete one. Uploads read a file
// off the local filesystem via a path input — the workstation-friendly analogue
// of the browser's file picker — and POST it multipart through
// UploadPurchaseOrderAttachment. Deletion is staff-gated server-side.
//
// Columnar (sc-h412, purchasing view slice): the list is a JD Edwards detail
// grid, the upload form is a columnar sheet, and all three phases render
// through jde_form.go under a persistent action bar. po_edit.go is the pilot
// this follows, and its key scheme is the one used here:
//
//	Up/Down       move between attachments / between fields
//	PgUp/PgDn     page
//	Enter         the phase's own action — upload from the list, upload from
//	              the form, delete at the confirm
//	Ctrl-X        delete the highlighted attachment (Ctrl-E is "open what this
//	              row IS" everywhere else, and a file the terminal cannot open
//	              is not that)
//	Esc           back / cancel
//	r             refresh, the same letter the PO detail sheet refreshes with
//
// Phases:
//
//	poAttachPhaseList    — the attachment grid.
//	poAttachPhaseUpload  — file-path + description inputs; enter uploads.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type poAttachPhase int

const (
	poAttachPhaseList poAttachPhase = iota
	poAttachPhaseUpload
)

const (
	poAttachFieldPath = iota
	poAttachFieldDesc
	poAttachFieldCount
)

type PurchaseOrderAttachmentsScreen struct {
	deps        Deps
	poID        string
	poNumber    string
	attachments []omsapi.PurchaseOrderAttachment
	loading     bool
	loadErr     string
	// jdeScreen carries the pane geometry and the frames (jde_form.go).
	jdeScreen

	phase  poAttachPhase
	cursor int
	errMsg string

	// Upload form.
	uploadInputs []textinput.Model
	uploadFocus  int
	uploading    bool

	// Delete confirm.
	confirmingDelete bool
	deleting         bool
	// Where the delete confirm's caveat is scrolled to, and what the last key
	// pressed on that frame did.
	//
	// The frame owns no navigable row: there is nothing to type into and nothing
	// to choose, so jdeLines.block() answers (0,0) whatever cursor it is handed
	// and a cursor-anchored window is PINNED at the top. At 80x14 the pane read
	// `↑ 1 more above`, the File row, `↓ 3 more below` — an irreversible delete
	// confirmed on a frame whose warning ("cannot be undone", "staff only") was
	// off the pane in both directions with no key able to fetch it. An OFFSET is
	// the layer's instrument for a body nothing navigates (frameScrolled), and
	// the note is what stops a declined key redrawing the pane byte for byte.
	deleteScroll int
	deleteNote   string
}

type poAttachLoadedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

type poAttachUploadedMsg struct {
	att *omsapi.PurchaseOrderAttachment
	err error
}

type poAttachDeletedMsg struct {
	err error
}

// NewPurchaseOrderAttachmentsScreen seeds from the PO already loaded on the
// detail screen and reloads by id after each change so the list stays fresh.
func NewPurchaseOrderAttachmentsScreen(deps Deps, po *omsapi.PurchaseOrder) *PurchaseOrderAttachmentsScreen {
	s := &PurchaseOrderAttachmentsScreen{deps: deps}
	if po != nil {
		s.poID = fmt.Sprintf("%v", po.ID)
		s.poNumber = po.Number
		s.attachments = po.Attachments
	}

	// No placeholders: in a fixed-width columnar field a placeholder fills the
	// input area and hides the underscores that say the field is empty, so what
	// the field wants rides beside it as a Hint (the pilot's rule — see
	// po_edit.go's poMetaHints).
	s.uploadInputs = make([]textinput.Model, poAttachFieldCount)
	path := textinput.New()
	path.Prompt = ""
	path.CharLimit = 500
	s.uploadInputs[poAttachFieldPath] = path
	desc := textinput.New()
	desc.Prompt = ""
	desc.CharLimit = 300
	s.uploadInputs[poAttachFieldDesc] = desc
	return s
}

func (s *PurchaseOrderAttachmentsScreen) Title() string {
	if s.poNumber != "" {
		return fmt.Sprintf("Attachments · PO %s", s.poNumber)
	}
	return "Purchase order attachments"
}

// WantsRawInput claims keys during the upload form and the delete confirm so
// the path input and the confirm's enter land here; the plain list stays
// non-raw so the sidebar's tab and the global back-step keep working while
// browsing.
func (s *PurchaseOrderAttachmentsScreen) WantsRawInput() bool {
	return s.phase == poAttachPhaseUpload || s.confirmingDelete
}

func (s *PurchaseOrderAttachmentsScreen) Init() tea.Cmd { return textinput.Blink }

func (s *PurchaseOrderAttachmentsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *PurchaseOrderAttachmentsScreen) load() tea.Cmd {
	deps := s.deps
	id := s.poID
	ctx := s.ctx()
	return func() tea.Msg {
		po, err := deps.OMS.GetPurchaseOrder(ctx, id)
		return poAttachLoadedMsg{po: po, err: err}
	}
}

func (s *PurchaseOrderAttachmentsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case poAttachLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("reload attachments failed: "+m.err.Error(), StatusError)
		}
		if m.po != nil {
			s.attachments = m.po.Attachments
			s.poNumber = m.po.Number
		}
		if s.cursor >= len(s.attachments) {
			s.cursor = len(s.attachments) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		return s, nil

	case poAttachUploadedMsg:
		s.uploading = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("upload failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		s.phase = poAttachPhaseList
		s.uploadInputs[poAttachFieldPath].SetValue("")
		s.uploadInputs[poAttachFieldDesc].SetValue("")
		s.loading = true
		return s, tea.Batch(Status("attachment uploaded", StatusOK), s.load())

	case poAttachDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("attachment deleted", StatusOK), s.load())

	case tea.KeyMsg:
		switch {
		case s.confirmingDelete:
			return s.updateConfirmDelete(m)
		case s.phase == poAttachPhaseUpload:
			return s.updateUpload(m)
		default:
			return s.updateList(m)
		}
	}

	if s.phase == poAttachPhaseUpload {
		var cmd tea.Cmd
		s.uploadInputs[s.uploadFocus], cmd = s.uploadInputs[s.uploadFocus].Update(msg)
		return s, cmd
	}
	return s, nil
}

// listNames reports whether the grid's bar currently names this key.
//
// The handler ASKS THE BAR rather than re-deriving the condition beside it,
// which is what makes "a key the bar does not name does nothing" true by
// construction instead of by two copies of an expression staying in step. They
// had already drifted: the bar names PgUp/PgDn only when the grid is taller than
// the pane, while the handler paged the highlight whatever the bar said.
func (s *PurchaseOrderAttachmentsScreen) listNames(key string) bool {
	for _, it := range s.listBar() {
		if it.Key == key {
			return true
		}
	}
	return false
}

func (s *PurchaseOrderAttachmentsScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, s.poID))
	case "down":
		s.moveList(+1)
	case "up":
		s.moveList(-1)
	case "pgdown":
		if s.listNames("PgUp/PgDn") {
			s.pageList(+1)
		}
	case "pgup":
		if s.listNames("PgUp/PgDn") {
			s.pageList(-1)
		}
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "enter":
		return s, s.openUpload()
	case "ctrl+x":
		if len(s.attachments) > 0 {
			s.confirmingDelete = true
			s.deleteScroll = 0
			s.deleteNote = ""
		}
		return s, nil
	}
	return s, nil
}

// openUpload switches to the upload sheet with the path row focused.
func (s *PurchaseOrderAttachmentsScreen) openUpload() tea.Cmd {
	s.phase = poAttachPhaseUpload
	s.uploadFocus = poAttachFieldPath
	s.errMsg = ""
	s.uploadInputs[poAttachFieldPath].Focus()
	s.uploadInputs[poAttachFieldDesc].Blur()
	return textinput.Blink
}

// moveList and pageList walk the grid's cursor. It is a LIST cursor, so it
// clamps rather than wrapping, and both DECLINE on a pane the frame is not
// drawn into — the highlight they would move is not on screen to be seen, and
// growing the terminal back would find it on a different file.
func (s *PurchaseOrderAttachmentsScreen) moveList(delta int) {
	next, ok := s.pickRow(s.cursor, len(s.attachments), delta, len(s.listHeader()), s.listBar())
	if !ok {
		return
	}
	s.cursor = next
}

func (s *PurchaseOrderAttachmentsScreen) pageList(dir int) {
	body, _ := s.listLines()
	next, ok := s.pageRow(body, s.cursor, len(s.attachments), dir, len(s.listHeader()),
		s.listBar(), s.listBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
}

// updateConfirmDelete drives the y/n-free confirm: enter deletes, esc backs
// out. The letters it used to take are gone with the rest of the accelerators —
// a scanner burst is a run of letters, and "y" landing on a delete prompt is
// exactly the accident the reduced scheme exists to rule out.
func (s *PurchaseOrderAttachmentsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "enter":
		if s.deleting {
			// A delete is already out. The status row draws "Deleting…" and the
			// bar has dropped the key (confirmDeleteDestroys, the one
			// expression both read), so the frame has answered already.
			//
			// This arm used to be a blanket `if s.deleting { return s, nil }`
			// over the WHOLE handler, which made every key on the frame inert
			// while the bar went on naming Enter, Esc and — once the scroll keys
			// arrived — three movement tokens too: rule 2 in the naming
			// direction, the mirror of the defect the line-delete confirm had.
			// Only the WRITE waits; Esc still leaves and the caveat still
			// scrolls, which is what the sibling confirm does.
			return s, nil
		}
		if s.cursor < 0 || s.cursor >= len(s.attachments) {
			s.confirmingDelete = false
			return s, nil
		}
		att := s.attachments[s.cursor]
		s.deleting = true
		// The last decline is retired with the press that answers: a stale note
		// leading "Deleting…" would be the answer to a keypress the operator has
		// already moved past.
		s.deleteNote = ""
		deps := s.deps
		ctx := s.ctx()
		id := s.poID
		return s, func() tea.Msg {
			return poAttachDeletedMsg{err: deps.OMS.DeletePurchaseOrderAttachment(ctx, id, att.ID)}
		}
	case "esc":
		s.confirmingDelete = false
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.confirmDeleteHeader()), s.confirmDeleteBar()) {
			// Refused pane: the caveat is not drawn, so scrolling it would move
			// the operator's place invisibly and the terminal grown back would
			// open a destroy confirm somewhere they never scrolled to. An arm
			// whose whole product is a POSITION says nothing rather than
			// declining out loud — see the layer's Movement block.
			return s, nil
		}
		if !s.confirmDeleteScrolls() {
			s.deleteNote = m.String() + " moves nothing — the whole warning is on the pane."
			return s, nil
		}
		s.deleteScroll = jdeScrollStep(m.String(), s.deleteScroll,
			s.confirmDeleteBody().Len(),
			s.scrollRows(len(s.confirmDeleteHeader()), s.confirmDeleteBar()))
	default:
		// Every other key ANSWERS. This frame binds a handful of keys and holds
		// no cursor and no caret, so a silent return redraws a pane that is a
		// pure function of unchanged state — byte for byte identical, which
		// reads as a wedged program. The note says what the KEY DID and names
		// none: the bar makes that claim, where no budget can trim it.
		s.deleteNote = m.String() + " does nothing here — this frame only confirms or cancels."
	}
	return s, nil
}

// poAttachUploadBar is the upload form's bar, said ONCE: the movement arm needs
// it to ask the layer whether the frame is drawn before it moves the caret, and
// a second literal beside the view's would be a bar measured that is not the
// bar drawn.
var poAttachUploadBar = []actionBarItem{{"Enter", "Upload"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}

func (s *PurchaseOrderAttachmentsScreen) updateUpload(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = poAttachPhaseList
		s.errMsg = ""
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		next, ok := s.moveRow(s.uploadFocus, poAttachFieldCount, delta, 0, poAttachUploadBar)
		if !ok {
			return s, nil
		}
		s.uploadInputs[s.uploadFocus].Blur()
		s.uploadFocus = next
		s.uploadInputs[s.uploadFocus].Focus()
		return s, textinput.Blink
	case "enter":
		if s.uploading {
			return s, nil
		}
		return s, s.submitUpload()
	}
	var cmd tea.Cmd
	s.uploadInputs[s.uploadFocus], cmd = s.uploadInputs[s.uploadFocus].Update(m)
	return s, cmd
}

func (s *PurchaseOrderAttachmentsScreen) submitUpload() tea.Cmd {
	path := strings.TrimSpace(s.uploadInputs[poAttachFieldPath].Value())
	if path == "" {
		s.errMsg = "a file path is required"
		return Status(s.errMsg, StatusError)
	}
	info, err := os.Stat(path)
	if err != nil {
		s.errMsg = "cannot read file: " + err.Error()
		return Status(s.errMsg, StatusError)
	}
	if info.IsDir() {
		s.errMsg = "path is a directory, not a file"
		return Status(s.errMsg, StatusError)
	}
	desc := strings.TrimSpace(s.uploadInputs[poAttachFieldDesc].Value())
	name := filepath.Base(path)

	s.uploading = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.poID
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return poAttachUploadedMsg{err: err}
		}
		defer f.Close()
		att, err := deps.OMS.UploadPurchaseOrderAttachment(ctx, id, name, f, desc)
		return poAttachUploadedMsg{att: att, err: err}
	}
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *PurchaseOrderAttachmentsScreen) View() string {
	if s.phase == poAttachPhaseUpload {
		return s.viewUpload()
	}
	if s.confirmingDelete {
		return s.viewConfirmDelete()
	}
	return s.viewList()
}

// Detail-grid column widths. The file column takes whatever the pane has left,
// which at 80 columns is what keeps the upload date on the row rather than off
// the right edge.
const (
	poAttachNumW  = 3
	poAttachDateW = 10
)

// poAttachIndent puts an attachment's continuation line under the file column.
var poAttachIndent = strings.Repeat(" ", len(jdeIndent)+poAttachNumW+2)

// attachNameWidth sizes the file column from the pane, with the same floor and
// ceiling reasoning as the PO line grid: a narrow terminal shortens the name
// rather than collapsing the column, and a wide one does not strand the dates
// out at the far right of an otherwise empty row.
func (s *PurchaseOrderAttachmentsScreen) attachNameWidth() int {
	const minW, maxW = 12, 48
	width := 76
	if w := s.bodyWidth(); w > 0 {
		width = w
	}
	switch w := width - (len(jdeIndent) + poAttachNumW + 2 + 2 + poAttachDateW); {
	case w < minW:
		return minW
	case w > maxW:
		return maxW
	default:
		return w
	}
}

// poAttachGridRow lays one attachment row out in its columns.
func poAttachGridRow(num, name, uploaded string, nameW int) string {
	return jdeIndent + strings.TrimRight(strings.Join([]string{
		padCell(num, poAttachNumW, alignRight),
		padCell(name, nameW, alignLeft),
		padCell(uploaded, poAttachDateW, alignLeft),
	}, "  "), " ")
}

// listLines builds the grid, tagging each line with the attachment it belongs
// to so the window keeps a whole entry — name row plus its readings — on
// screen rather than the first line of it.
func (s *PurchaseOrderAttachmentsScreen) listLines() (*jdeLines, int) {
	l := &jdeLines{}
	if len(s.attachments) == 0 {
		return l, 0
	}
	nameW := s.attachNameWidth()
	for i, att := range s.attachments {
		name := att.FileName
		if name == "" {
			name = att.File
		}
		uploaded := ""
		if !att.UploadedAt.IsZero() {
			uploaded = att.UploadedAt.Format("2006-01-02")
		}
		row := poAttachGridRow(strconv.Itoa(i+1), fitCell(name, nameW), uploaded, nameW)
		if i == s.cursor {
			row = StyleJDEFieldFocused.Render(row)
		}
		l.AddRow(i, row)

		// The description and the uploader ride under the row as wrapped
		// readings: they are what the grid has no column for, and at 80 columns
		// a description in a column of its own would have nowhere to go.
		var tokens []jdeToken
		if att.Description != "" {
			tokens = append(tokens, jdeToken{text: att.Description, style: StyleMuted})
		}
		if att.UploadedByName != "" {
			tokens = append(tokens, jdeToken{text: "by " + att.UploadedByName, style: StyleMuted})
		}
		for _, line := range jdeWrapTokens(tokens, poAttachIndent, s.bodyWidth()) {
			l.AddRow(i, line)
		}
	}
	return l, len(s.attachments)
}

// listHeader is the grid's chrome, PINNED above the rows: the count, the failed
// load, the empty state, and the column header the rows are drawn under.
//
// They used to lead the BODY, and a body line that belongs to no navigable row
// is a line no key can reach: jdeLines.Window anchors on the cursor's block and
// a columnar cursor cannot go above its first row, so with a SINGLE attachment
// on the order — one navigable row, so the bar rightly drops UP/DN — the pane at
// 80x12 read `↑ 2 more above`, the file's name, `↓ 2 more below`, and no key on
// the frame could fetch any of it. Pinned, they are trimmed by jdeFitHeader,
// which gives ground by RANK and claims nothing about what it dropped.
//
// The COLUMN HEADER is essential and the count is context: an operator reading a
// grid needs to know which column is which, and the count is restated by the
// row numbers themselves. The empty state takes the essential row when there are
// no rows at all, because on that frame it is the only thing on the pane that
// says what to do next.
func (s *PurchaseOrderAttachmentsScreen) listHeader() jdeHeader {
	h := jdeHeader(nil)
	if s.loadErr != "" {
		h = h.add(jdeHeadContext,
			StyleStatusError.Render("Error: ")+fitCellIf(s.loadErr, s.bodyWidth()-poErrPrefixW), "")
	}
	if len(s.attachments) == 0 {
		return h.add(jdeHeadContext, StyleJDEHeading.Render("Attachments (0)")).
			add(jdeHeadEssential, jdeIndent+StyleMuted.Render(
				"No attachments on this PO. Enter uploads one."))
	}
	return h.add(jdeHeadContext, StyleJDEHeading.Render(
		fmt.Sprintf("Attachments (%d)", len(s.attachments)))).
		add(jdeHeadEssential, StyleMuted.Render(
			poAttachGridRow("#", "File", "Uploaded", s.attachNameWidth())))
}

// listBar names the keys that work on the grid — and only those: Ctrl-X is
// dropped when there is nothing to delete, and the movement keys when there is
// nothing to move between.
func (s *PurchaseOrderAttachmentsScreen) listBar() []actionBarItem {
	return s.listBarItems(s.listPages())
}

// listBarItems builds the grid's bar for a given paging state. listBar and
// listPages both go through it so the bar that is MEASURED is the bar that is
// drawn: the height used to be taken from a partial list — missing both the
// PgUp/PgDn entry it was about to add and the r=Refresh appended on return — so
// it measured a bar one row shorter than the frame draws and budgeted the body
// one row too tall.
func (s *PurchaseOrderAttachmentsScreen) listBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Upload"}, {"Esc", "Back"}}
	if s.listMoves() {
		items = append(items, actionBarItem{"UP/DN", "Move"})
	}
	if len(s.attachments) > 0 {
		items = append(items, actionBarItem{"Ctrl-X", "Delete"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

// listMoves reports whether Up/Down have anywhere to go — the condition
// updateList's own arms are gated on (`cursor < len-1` and `cursor > 0`), which
// no cursor can satisfy with a single row.
//
// Delete deliberately does NOT share it: Ctrl-X works on one file, so it keeps
// the `> 0` condition its handler reads. The two conditions look alike and are
// not the same one, which is how the bar came to advertise a movement pair that
// could not move on the repo's own canonical single-attachment fixture.
func (s *PurchaseOrderAttachmentsScreen) listMoves() bool {
	return len(s.attachments) > 1
}

// listPages reports whether the grid is taller than the pane leaves it, and is
// the ONE condition both listBar and (through listNames) updateList read.
//
// The decision and the bar height are mutually dependent, so it is settled
// against the bar WITH the paging entry on it — the tallest bar and therefore
// the smallest body budget. That is conservative and cannot oscillate: a grid
// that overflows the smallest budget also overflows the larger one left when the
// entry is dropped.
func (s *PurchaseOrderAttachmentsScreen) listPages() bool {
	if len(s.attachments) == 0 {
		return false
	}
	body, _ := s.listLines()
	return s.bodyPagesForBar(body, len(s.attachments), len(s.listHeader()), s.listBarItems(true))
}

func (s *PurchaseOrderAttachmentsScreen) viewList() string {
	body, _ := s.listLines()
	return s.frameWrapped(s.listHeader(), body, s.cursor, s.listStatus(), s.listBar())
}

// listStatus is the grid's status row, and it reports a DELETE still in flight
// as readily as a load.
//
// Esc leaves the confirm while the write is out — deliberately, because a frame
// with no way off it while a slow gateway thinks is the worse defect, and it is
// what the sibling line-delete confirm does. That made this row reachable in a
// state it could not describe: the grid drew "Loading…" or nothing at all, the
// file being destroyed was still listed as though nothing were happening, and
// ctrl+x on any row reopened a confirm the reply then closed out from under the
// operator. Nothing is written to the wrong target — the request closed over
// its attachment before esc — but a screen silent about an irreversible write
// it is running is a screen showing the operator less than it knows.
//
// The DELETE wins over the load when both are out. They do not overlap on the
// ordinary path (poAttachDeletedMsg clears deleting in the same breath it sets
// loading), but `r` on the grid mid-delete puts both up, and of the two facts
// the one an operator would act on is the one that cannot be undone.
func (s *PurchaseOrderAttachmentsScreen) listStatus() string {
	if s.deleting {
		return s.statusRow(true, "Deleting…", "")
	}
	return s.statusRow(s.loading, "Loading…", "")
}

// viewConfirmDelete is its own phase rather than a line appended to the list:
// deleting a file is not undoable, and the frame that asks about it should not
// also be scrolling a grid behind the question.
func (s *PurchaseOrderAttachmentsScreen) viewConfirmDelete() string {
	frame, offset := s.frameScrolled(
		s.confirmDeleteHeader(), s.confirmDeleteBody(), s.deleteScroll,
		s.confirmDeleteStatus(), s.confirmDeleteBar())
	// Stored back so the offset this screen holds is the one that was DRAWN:
	// `end` asks for the whole body and the frame clamps it against the pane it
	// has, which is what makes "↓ 0 more below" impossible.
	s.deleteScroll = offset
	return frame
}

// confirmDeleteStatus is the confirm's one status row: what is in flight, and
// what the last keypress DID.
//
// deleteNote used to be handed to statusRow's THIRD argument, which is the
// FAILURE slot — jdeStatusErrMark behind StyleStatusError. So "j does nothing
// here — this frame only confirms or cancels", a perfectly benign answer to an
// inert key, was drawn to the operator as a red ✗ on a destructive confirm:
// "this key is inert here" and "something went wrong" told apart by nothing,
// on the frame where the difference decides whether they press Enter.
// statusAnswer is the surface built for it — the screen's answer to the last
// keypress, at its own level, bounded by the same fitStatus — and StatusInfo is
// the muted, unmarked one.
//
// The ANSWER LEADS the working line rather than replacing it, through the same
// poLeadOnto the kit picker's status row uses. statusRow's saving branch wins
// outright, so a note handed to it while a delete is out would be drawn by
// nothing at all — and every key this frame declines is still pressable while
// the write is in flight, which is exactly when a swallowed answer reads as a
// wedged program.
func (s *PurchaseOrderAttachmentsScreen) confirmDeleteStatus() string {
	if !s.deleting {
		return s.statusAnswer(StatusInfo, s.deleteNote)
	}
	verb := "Deleting…"
	switch room := s.bodyWidth(); {
	case s.deleteNote == "":
	case room > 0:
		verb = poLeadOnto(s.deleteNote, verb, room)
	default:
		// An unsized pane means "do not truncate" everywhere in this layer, and
		// fitStatus leaves the row alone there too — but poLeadOnto would read a
		// room of 0 as no room at all and drop the subject entirely.
		verb = s.deleteNote + poLeadJoint + verb
	}
	return s.statusRow(true, verb, "")
}

// confirmDeleteName is the file about to be destroyed, empty where the cursor
// addresses none.
func (s *PurchaseOrderAttachmentsScreen) confirmDeleteName() string {
	if s.cursor < 0 || s.cursor >= len(s.attachments) {
		return ""
	}
	if name := s.attachments[s.cursor].FileName; name != "" {
		return name
	}
	return s.attachments[s.cursor].File
}

// confirmDeleteHeader is what the frame may not be drawn without: the heading
// and the FILE it names.
//
// The file row is ESSENTIAL and pinned, for the reason the line-delete confirm's
// headline is: jdeFitHeader gives ground by RANK and keeps the essential row
// last, so wherever this frame is drawn at all it names what Enter will destroy.
// It used to be a body row with the heading above it and the caveat below, and
// on a short pane the layer window put the heading out of reach on one side and
// the whole warning out of reach on the other.
func (s *PurchaseOrderAttachmentsScreen) confirmDeleteHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleStatusWarn.Render("Delete attachment"), "")
	h = h.add(jdeHeadEssential, renderJDEField(jdeField{
		Label: "File", Kind: jdeValue,
		Value:   fitCellIf(s.confirmDeleteName(), jdeStripWidth(s.bodyWidth(), 4)),
		Focused: true,
	}, 4, s.bodyWidth()))
	return h.add(jdeHeadDecorative, "")
}

// confirmDeleteBody is the caveat, said ONCE so the lines the bar is measured
// against are the lines the frame draws. It owns no navigable row on purpose:
// there is nothing here to type into, and the window over it is positioned by an
// OFFSET rather than by a cursor.
func (s *PurchaseOrderAttachmentsScreen) confirmDeleteBody() *jdeLines {
	body := &jdeLines{}
	for _, line := range jdeCaveatLines(
		"Removes the file from this purchase order. This cannot be undone, and the server allows it only for staff.",
		s.bodyWidth()) {
		body.Add(line)
	}
	return body
}

// confirmDeleteScrolls is the confirm's half of the bar-honesty rule: the scroll
// keys are named when, and only when, the caveat outruns the window.
//
// Measured against the bar that will really be DRAWN with the scroll keys added,
// because THAT is the fixed point — not an unconditional
// confirmDeleteBarItems(true, true). While the write is out the drawn bar has
// lost Enter and can be a row shorter, so a body measured against the taller bar
// can answer "it scrolls" for a body that fits: the arm would bump the offset,
// ClampScroll would put it straight back, and the pane would come back
// byte-identical with no note.
func (s *PurchaseOrderAttachmentsScreen) confirmDeleteScrolls() bool {
	return s.bodyScrollsForBar(s.confirmDeleteBody(),
		len(s.confirmDeleteHeader()), s.confirmDeleteBarItems(s.confirmDeleteDestroys(), true))
}

// confirmDeleteDestroys is the ONE expression behind "may Enter write". The bar
// reads it to decide whether to name the key and the arm reads it to decide
// whether to act — two copies of that condition is how the sibling line-delete
// confirm's bar and arm came apart.
func (s *PurchaseOrderAttachmentsScreen) confirmDeleteDestroys() bool {
	return !s.deleting
}

func (s *PurchaseOrderAttachmentsScreen) confirmDeleteBar() []actionBarItem {
	return s.confirmDeleteBarItems(s.confirmDeleteDestroys(), s.confirmDeleteScrolls())
}

// confirmDeleteBarItems is confirmDeleteBar for a given destroy and scroll
// state, so the bar that is MEASURED against the pane is the bar that is DRAWN
// on it.
func (s *PurchaseOrderAttachmentsScreen) confirmDeleteBarItems(destroy, scroll bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Delete"}, {"Esc", "Cancel"}}
	if !destroy {
		// Esc still LEAVES while the write is out — a frame with no way off it
		// is the worse defect — so the bar keeps one key and drops the one that
		// would not act.
		items = []actionBarItem{{"Esc", "Back"}}
	}
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return items
}

var poAttachLabels = map[int]string{
	poAttachFieldPath: "File path",
	poAttachFieldDesc: "Description",
}

var poAttachHints = map[int]string{
	poAttachFieldPath: "a path on this machine",
	poAttachFieldDesc: "optional",
}

func (s *PurchaseOrderAttachmentsScreen) viewUpload() string {
	fields := make([]jdeField, poAttachFieldCount)
	for i := 0; i < poAttachFieldCount; i++ {
		fields[i] = jdeField{
			Label:   poAttachLabels[i],
			Kind:    jdeText,
			Input:   &s.uploadInputs[i],
			Width:   34,
			Hint:    poAttachHints[i],
			Focused: s.uploadFocus == i,
		}
	}
	labelW := jdeLabelWidth(fields)
	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render("Upload attachment"))
	body.Add("")
	body.AddFittedFields(fields, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(nil, body, s.uploadFocus,
		s.statusRow(s.uploading, "Uploading…", s.errMsg), poAttachUploadBar)
}
