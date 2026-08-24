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
		if s.cursor < len(s.attachments)-1 {
			s.cursor++
		}
	case "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "pgdown":
		if s.listNames("PgUp/PgDn") {
			s.cursor = jdePageCursor(s.cursor, len(s.attachments), s.pageStep(), +1)
		}
	case "pgup":
		if s.listNames("PgUp/PgDn") {
			s.cursor = jdePageCursor(s.cursor, len(s.attachments), s.pageStep(), -1)
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

// pageStep is how many rows the grid is currently showing, computed from the
// same lines View draws so a page moves by exactly what the operator can see.
//
// The budget is bodyRowsForBar alone because viewList passes frameWrapped no
// header, so that IS the frame's own avail. It used to subtract one more for a
// header that is not there, and a page then advanced by one navigable row fewer
// than the operator could see — the comment above claiming the opposite.
func (s *PurchaseOrderAttachmentsScreen) pageStep() int {
	body, _ := s.listLines()
	_, rows := body.Window(s.cursor, s.bodyAvailForBar(0, s.listBar()))
	if rows < 1 {
		return 1
	}
	return rows
}

// updateConfirmDelete drives the y/n-free confirm: enter deletes, esc backs
// out. The letters it used to take are gone with the rest of the accelerators —
// a scanner burst is a run of letters, and "y" landing on a delete prompt is
// exactly the accident the reduced scheme exists to rule out.
func (s *PurchaseOrderAttachmentsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "enter":
		if s.cursor < 0 || s.cursor >= len(s.attachments) {
			s.confirmingDelete = false
			return s, nil
		}
		att := s.attachments[s.cursor]
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		id := s.poID
		return s, func() tea.Msg {
			return poAttachDeletedMsg{err: deps.OMS.DeletePurchaseOrderAttachment(ctx, id, att.ID)}
		}
	case "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *PurchaseOrderAttachmentsScreen) updateUpload(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = poAttachPhaseList
		s.errMsg = ""
		return s, nil
	case "tab", "down":
		s.uploadInputs[s.uploadFocus].Blur()
		s.uploadFocus = (s.uploadFocus + 1) % poAttachFieldCount
		s.uploadInputs[s.uploadFocus].Focus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.uploadInputs[s.uploadFocus].Blur()
		s.uploadFocus = (s.uploadFocus - 1 + poAttachFieldCount) % poAttachFieldCount
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
	if s.loadErr != "" {
		l.Add(StyleStatusError.Render("Error: ") + fitCellIf(s.loadErr, s.bodyWidth()-poErrPrefixW))
		l.Add("")
	}
	l.Add(StyleJDEHeading.Render(fmt.Sprintf("Attachments (%d)", len(s.attachments))))
	if len(s.attachments) == 0 {
		l.Add(jdeIndent + StyleMuted.Render("No attachments on this PO. Enter uploads one."))
		return l, 0
	}

	nameW := s.attachNameWidth()
	l.Add(StyleMuted.Render(poAttachGridRow("#", "File", "Uploaded", nameW)))
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
	return s.bodyScrollsForBar(body, 0, s.listBarItems(true))
}

func (s *PurchaseOrderAttachmentsScreen) viewList() string {
	body, _ := s.listLines()
	return s.frameWrapped(nil, body, s.cursor,
		s.statusRow(s.loading, "Loading…", ""), s.listBar())
}

// viewConfirmDelete is its own phase rather than a line appended to the list:
// deleting a file is not undoable, and the frame that asks about it should not
// also be scrolling a grid behind the question.
func (s *PurchaseOrderAttachmentsScreen) viewConfirmDelete() string {
	name := ""
	if s.cursor >= 0 && s.cursor < len(s.attachments) {
		name = s.attachments[s.cursor].FileName
		if name == "" {
			name = s.attachments[s.cursor].File
		}
	}
	body := &jdeLines{}
	body.Add(StyleStatusWarn.Render("Delete attachment"))
	body.Add("")
	body.AddRow(0, renderJDEField(jdeField{
		Label: "File", Kind: jdeValue, Value: fitCellIf(name, jdeStripWidth(s.bodyWidth(), 4)), Focused: true,
	}, 4, s.bodyWidth()))
	body.Add("")
	for _, line := range jdeCaveatLines(
		"Removes the file from this purchase order. This cannot be undone, and the server allows it only for staff.",
		s.bodyWidth()) {
		body.Add(line)
	}
	return s.frameWrapped(nil, body, 0,
		s.statusRow(s.deleting, "Deleting…", ""),
		[]actionBarItem{{"Enter", "Delete"}, {"Esc", "Cancel"}})
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
		s.statusRow(s.uploading, "Uploading…", s.errMsg),
		[]actionBarItem{{"Enter", "Upload"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}})
}
