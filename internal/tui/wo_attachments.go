// WorkOrderAttachmentsScreen — manage the files attached to a work order.
//
// TUI counterpart to the PO attachments screen (internal/tui/po_attachments.go),
// against the work-order attachments endpoint (op-7pjj / OMS #956). It lists a
// work order's attachments, uploads a new one (file + optional description +
// kind), and deletes one. Uploads read a file off the local filesystem via a
// path input — the workstation-friendly analogue of the browser's file picker —
// and POST it multipart through UploadWorkOrderAttachment.
//
// Two things differ from the PO screen, both because a work order's attachments
// are NOT embedded in the work-order payload but live at their own flat endpoint:
// the screen is seeded with only the work-order id and loads the list itself
// (in Init and after every change), and the upload form carries a kind field
// (photo/document/other) the backend records per attachment.
//
// Those are the DATA differences, and they are the only ones left worth
// following the pointer for: the PO screen has since moved to the columnar JD
// Edwards layout and its reduced key scheme (Enter uploads, Ctrl-X deletes,
// Up/Down move), while this screen is still the pre-columnar list described
// below. The two no longer share a layout or a key scheme.
//
// Reads are open to any authenticated user, so a volunteer maker can browse the
// list; create/upload and delete are staff / Logistics / SIG-admin only and the
// server answers 403, which the screen surfaces rather than hiding the keys.
//
// Phases:
//
//	woAttachPhaseList    — the attachment list; j/k move, u upload, x delete
//	                       (with a y/n confirm), r refresh, esc back.
//	woAttachPhaseUpload  — file-path + description + kind inputs; enter uploads.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type woAttachPhase int

const (
	woAttachPhaseList woAttachPhase = iota
	woAttachPhaseUpload
)

const (
	woAttachFieldPath = iota
	woAttachFieldDesc
	woAttachFieldKind
	woAttachFieldCount
)

// woAttachKinds is the set the backend accepts, and the order the upload form's
// hint lists them. The form defaults to "document".
var woAttachKinds = []string{
	omsapi.WorkOrderAttachmentPhoto,
	omsapi.WorkOrderAttachmentDocument,
	omsapi.WorkOrderAttachmentOther,
}

type WorkOrderAttachmentsScreen struct {
	deps           Deps
	woID           string
	attachments    []omsapi.WorkOrderAttachment
	loading        bool
	loadErr        string
	terminalWidth  int
	terminalHeight int
	windowStart    int

	phase  woAttachPhase
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

type woAttachLoadedMsg struct {
	atts []omsapi.WorkOrderAttachment
	err  error
}

type woAttachUploadedMsg struct {
	att *omsapi.WorkOrderAttachment
	err error
}

type woAttachDeletedMsg struct {
	err error
}

// NewWorkOrderAttachmentsScreen is seeded with only the work-order id: unlike a
// PO, whose attachments ride its payload, a work order's attachments are fetched
// from their own endpoint, so the list loads in Init and reloads after each
// change.
func NewWorkOrderAttachmentsScreen(deps Deps, woID string) *WorkOrderAttachmentsScreen {
	s := &WorkOrderAttachmentsScreen{deps: deps, woID: woID, loading: true}

	s.uploadInputs = make([]textinput.Model, woAttachFieldCount)
	path := textinput.New()
	path.Prompt = ""
	path.Placeholder = "/path/to/file.pdf (~ expands to home)"
	path.CharLimit = 512
	s.uploadInputs[woAttachFieldPath] = path
	desc := textinput.New()
	desc.Prompt = ""
	desc.Placeholder = "description (optional)"
	desc.CharLimit = 300
	s.uploadInputs[woAttachFieldDesc] = desc
	kind := textinput.New()
	kind.Prompt = ""
	kind.Placeholder = "photo / document / other"
	kind.CharLimit = 16
	kind.SetValue(omsapi.WorkOrderAttachmentDocument)
	s.uploadInputs[woAttachFieldKind] = kind
	return s
}

func (s *WorkOrderAttachmentsScreen) Title() string {
	return fmt.Sprintf("Attachments · WO #%s", s.woID)
}

// WantsRawInput claims keys during the upload form and the delete confirm so the
// path/description/kind inputs and the y/n land here; the plain list stays
// non-raw so global hotkeys keep working while browsing.
func (s *WorkOrderAttachmentsScreen) WantsRawInput() bool {
	return s.phase == woAttachPhaseUpload || s.confirmingDelete
}

func (s *WorkOrderAttachmentsScreen) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, s.load())
}

func (s *WorkOrderAttachmentsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *WorkOrderAttachmentsScreen) load() tea.Cmd {
	deps := s.deps
	id := s.woID
	ctx := s.ctx()
	return func() tea.Msg {
		atts, err := deps.OMS.ListWorkOrderAttachments(ctx, id)
		return woAttachLoadedMsg{atts: atts, err: err}
	}
}

func (s *WorkOrderAttachmentsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil

	case woAttachLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("load attachments failed: "+m.err.Error(), StatusError)
		}
		s.loadErr = ""
		s.attachments = m.atts
		if s.cursor >= len(s.attachments) {
			s.cursor = len(s.attachments) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		return s, nil

	case woAttachUploadedMsg:
		s.uploading = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("upload failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		s.phase = woAttachPhaseList
		s.uploadInputs[woAttachFieldPath].SetValue("")
		s.uploadInputs[woAttachFieldDesc].SetValue("")
		s.uploadInputs[woAttachFieldKind].SetValue(omsapi.WorkOrderAttachmentDocument)
		s.loading = true
		return s, tea.Batch(Status("attachment uploaded", StatusOK), s.load())

	case woAttachDeletedMsg:
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
		case s.phase == woAttachPhaseUpload:
			return s.updateUpload(m)
		default:
			return s.updateList(m)
		}
	}

	if s.phase == woAttachPhaseUpload {
		var cmd tea.Cmd
		s.uploadInputs[s.uploadFocus], cmd = s.uploadInputs[s.uploadFocus].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *WorkOrderAttachmentsScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
		return s, nil
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, s.woID))
	case "j", "down":
		if s.cursor < len(s.attachments)-1 {
			s.cursor++
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "u":
		s.phase = woAttachPhaseUpload
		s.uploadFocus = woAttachFieldPath
		s.errMsg = ""
		s.uploadInputs[woAttachFieldPath].Focus()
		s.uploadInputs[woAttachFieldDesc].Blur()
		s.uploadInputs[woAttachFieldKind].Blur()
		return s, textinput.Blink
	case "x":
		if len(s.attachments) > 0 {
			s.confirmingDelete = true
		}
		return s, nil
	}
	return s, nil
}

func (s *WorkOrderAttachmentsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.cursor < 0 || s.cursor >= len(s.attachments) {
			s.confirmingDelete = false
			return s, nil
		}
		att := s.attachments[s.cursor]
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		return s, func() tea.Msg {
			return woAttachDeletedMsg{err: deps.OMS.DeleteWorkOrderAttachment(ctx, att.ID)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *WorkOrderAttachmentsScreen) updateUpload(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = woAttachPhaseList
		s.errMsg = ""
		return s, nil
	case "tab", "down":
		s.uploadInputs[s.uploadFocus].Blur()
		s.uploadFocus = (s.uploadFocus + 1) % woAttachFieldCount
		s.uploadInputs[s.uploadFocus].Focus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.uploadInputs[s.uploadFocus].Blur()
		s.uploadFocus = (s.uploadFocus - 1 + woAttachFieldCount) % woAttachFieldCount
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

func (s *WorkOrderAttachmentsScreen) submitUpload() tea.Cmd {
	path := expandUser(strings.TrimSpace(s.uploadInputs[woAttachFieldPath].Value()))
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
	// Kind is constrained to the backend's set: normalise case, and reject an
	// unknown value here rather than sending it into a 400. Blank is allowed —
	// the client omits it and the server applies its own default.
	kind := strings.ToLower(strings.TrimSpace(s.uploadInputs[woAttachFieldKind].Value()))
	if kind != "" && !woAttachKindValid(kind) {
		s.errMsg = "kind must be one of photo / document / other (or blank)"
		return Status(s.errMsg, StatusError)
	}
	desc := strings.TrimSpace(s.uploadInputs[woAttachFieldDesc].Value())
	name := filepath.Base(path)

	s.uploading = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.woID
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return woAttachUploadedMsg{err: err}
		}
		defer f.Close()
		att, err := deps.OMS.UploadWorkOrderAttachment(ctx, id, name, f, desc, kind)
		return woAttachUploadedMsg{att: att, err: err}
	}
}

func woAttachKindValid(kind string) bool {
	for _, k := range woAttachKinds {
		if kind == k {
			return true
		}
	}
	return false
}

// woAttachmentName is what the list shows for a row. The serializer has no
// file_name field, so the display name is the base of the stored file path (or
// of the URL as a fallback) — never a raw storage key.
func woAttachmentName(att omsapi.WorkOrderAttachment) string {
	if att.File != "" {
		return filepath.Base(att.File)
	}
	if att.AttachmentURL != "" {
		return filepath.Base(att.AttachmentURL)
	}
	return "attachment"
}

// ---------------------------------------------------------------------------
// Bar
// ---------------------------------------------------------------------------

// woAttachListBar names every key that acts on a list of `rows` attachments, as
// a record the honesty sweep can press (prose_bar.go).
//
// It used to be one literal under every row with no window — "j/k move · u
// upload · x delete · r refresh · esc back" — so a work order with more
// attachments than the pane has rows took the footer off the bottom, the arrows
// moved the cursor unnamed, and `x` was named over an empty list where it does
// nothing. `u` is offered either way: the form it opens is how the first
// attachment gets there.
func woAttachListBar(rows int) proseBar {
	out := append(proseNavStep(listNavMoves(rows)), proseBarItem{Keys: []string{"u"}, Hint: "u upload"})
	if rows > 0 {
		out = append(out, proseBarItem{Keys: []string{"x"}, Hint: "x delete"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING: the upload form's in that phase,
// nil under the delete confirm (whose own prompt names its keys, the precedent
// every converted list keeps), loadBar's while a load is out or has failed, and
// the list's otherwise.
func (s *WorkOrderAttachmentsScreen) proseBar() proseBar {
	switch {
	case s.phase == woAttachPhaseUpload:
		return s.uploadBar()
	case s.confirmingDelete:
		return nil
	case s.loading || s.loadErr != "":
		return s.loadBar()
	}
	return woAttachListBar(len(s.attachments))
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `u` opens the upload form, which is drawn in place of the load
// frame. j/k move a cursor the frame does not draw and `x` arms a confirm it does
// not draw either, so they are not named and are ignored.
//
// THIS SCREEN USED TO DRAW ITS LOAD ERROR ABOVE THE ROWS a failed refresh kept,
// with the whole list still answering keys under it — the one screen of its kind
// that did. It draws the shared failure frame now, and the rows come back with
// the next load that works.
func (s *WorkOrderAttachmentsScreen) loadBar() proseBar {
	return proseBar{
		{Keys: []string{"u"}, Hint: "u upload"},
		proseBarReloadFor(s.loadErr != ""),
		proseBarEsc,
	}
}

// uploadBar is the upload form's bar. Its focus WRAPS over three fields on tab,
// shift+tab and both arrows, so all four are named (proseBarFieldFocus) — the
// literal said `tab move`, and shift+tab and the arrows moved the caret unnamed.
// `enter` comes off while an upload is out, where the arm returns without acting.
func (s *WorkOrderAttachmentsScreen) uploadBar() proseBar {
	out := proseBar{proseBarFieldFocus}
	if !s.uploading {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter upload"})
	}
	return append(out, proseBarItem{Keys: []string{"esc"}, Hint: "esc cancel"})
}

func (s *WorkOrderAttachmentsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *WorkOrderAttachmentsScreen) View() string {
	if s.phase == woAttachPhaseUpload {
		return s.viewUpload()
	}
	return s.viewList()
}

// viewList draws the attachments as a line-packed window
// (proseFlatListFrameFoot) with the bar — or the delete confirm, which is drawn
// under the rows in the bar's place — as the foot the window is budgeted around.
//
// THE CONFIRM WAS WRITTEN UNDER EVERY ROW, so on a work order with more
// attachments than the pane has rows `x` asked a question nobody could read and
// `y` answered it. It is the foot now, and the attachment's name in it is clipped
// so the keys that answer it stay on the pane.
func (s *WorkOrderAttachmentsScreen) viewList() string {
	cells := s.paneCells()
	confirming := s.confirmingDelete && s.cursor >= 0 && s.cursor < len(s.attachments)
	if !confirming {
		if s.loading {
			return proseLoadingFrame("Loading attachments…", cells, s.proseBar())
		}
		if s.loadErr != "" {
			return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
		}
		if len(s.attachments) == 0 {
			return StyleMuted.Render("No attachments on this work order.") + "\n\n" + s.proseBar().render(cells)
		}
	}
	rows := make([]string, len(s.attachments))
	for i, att := range s.attachments {
		rows[i] = s.attachmentRow(i, att, cells)
	}
	footRows := woAttachListBar(proseFlatCeilingRows).rows(cells)
	foot := s.proseBar().render(cells)
	if confirming {
		name := woAttachmentName(s.attachments[s.cursor])
		if s.deleting {
			foot = StyleMuted.Render("Deleting…")
		} else {
			foot = StyleStatusWarn.Render(pickerClip(fmt.Sprintf("Delete %q?", name), cells)) + "\n" +
				StyleStatusWarn.Render("y delete · n/esc cancel")
		}
		footRows = 1 + strings.Count(foot, "\n") + 1
	}
	return proseFlatListFrameFoot("", rows, s.cursor, &s.windowStart, s.terminalHeight, footRows, foot)
}

// attachmentRow is one attachment and its kind and date. The name and the
// description are OMS values, clipped to the pane line by line with the cut
// marked; the highlight's padding is reserved on every row.
func (s *WorkOrderAttachmentsScreen) attachmentRow(i int, att omsapi.WorkOrderAttachment, cells int) string {
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	line := caret + woAttachmentName(att)
	if att.Description != "" {
		line += " — " + att.Description
	}
	line = proseClipEachLine(line, cells-StyleSidebarItemActive.GetHorizontalPadding())
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	meta := []string{}
	if att.Kind != "" {
		meta = append(meta, att.Kind)
	}
	if !att.UploadedAt.IsZero() {
		meta = append(meta, att.UploadedAt.Format("2006-01-02"))
	}
	if len(meta) > 0 {
		line += "\n    " + StyleMuted.Render(pickerClip(strings.Join(meta, " · "), cells-4))
	}
	return line
}

// viewUpload draws the upload form. It has no window — three fields and a fixed
// head — so its height is bounded by bounding the lines that carry a value the
// screen does not control: the typed boxes (woBoxView) and a failure's OMS body
// (proseFormLine), each held to one row of the pane. The working line and the
// failure sit ABOVE the bar now, which is the last thing on every converted pane.
func (s *WorkOrderAttachmentsScreen) viewUpload() string {
	cells := s.paneCells()
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Upload attachment") + "\n\n")
	labels := []string{"File path", "Description", "Kind"}
	for i := 0; i < woAttachFieldCount; i++ {
		caret := "  "
		if i == s.uploadFocus {
			caret = "▸ "
		}
		prefix := caret + StyleTitle.Render(labels[i]+": ")
		b.WriteString(prefix + woBoxView(s.uploadInputs[i], cells, strings.Repeat(" ", lipgloss.Width(prefix))) + "\n")
	}
	if s.uploading {
		b.WriteString("\n" + StyleMuted.Render("Uploading…") + "\n")
	} else if s.errMsg != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+proseFormLine(s.errMsg, cells-2)) + "\n")
	}
	b.WriteString("\n" + s.proseBar().render(cells))
	return b.String()
}
