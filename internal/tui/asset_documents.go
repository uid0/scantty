// AssetDocumentsScreen — the document library that lives WITH a machine.
//
// TUI counterpart to the web's asset document section: manuals, CAD sources,
// wiring diagrams, cut-sheets, and the cut-ready DXF/SVG/G-code/STL files an
// operator wants at the bench. Uploads read a file off the local filesystem via
// a path input — the workstation-friendly analogue of the browser's file picker
// — and POST it multipart, which is the shape po_attachments.go already uses and
// this file deliberately follows rather than inventing a second one.
//
// SUPERSEDE AND DELETE ARE BOTH "REPLACE THIS DOCUMENT" AND ONLY ONE OF THEM
// DESTROYS ANYTHING, which is why exactly one of them is confirmed.
// Superseding keeps both rows: the old version stays retrievable, marked not
// current, with the new one pointing back at it — the server does the version
// bump and the flag, and nothing here re-derives either. Deleting destroys the
// row and its file, and any document that superseded it keeps its own row and
// silently loses the link (`on_delete=SET_NULL`), so the chain is left with a
// gap nothing records. That is unrecoverable from this program and from the web,
// so Ctrl-X opens a confirm and Enter is not bound to the destroy.
//
// SUPERSEDED VERSIONS ARE SHOWN AND MARKED, not filtered out. The web keeps them
// behind a toggle; a terminal at a machine has one job here, which is to stop
// somebody following a stale manual, and a row that says "v1 · superseded"
// states that where a filtered-out row states nothing at all.
//
// Keys, following the sibling attachment grid exactly:
//
//	Enter      upload a document to this asset
//	Ctrl-E     supersede the highlighted one — upload its replacement
//	Ctrl-X     delete the highlighted one (confirm)
//	r          refresh · Esc back · UP/DN move · PgUp/PgDn page
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

type assetDocPhase int

const (
	assetDocPhaseList assetDocPhase = iota
	// One phase drives both writes: the FORM is the same (a path, a title, a
	// category, a description) and only where it posts differs. What must stay
	// apart is what the operator sees — the heading, the pinned header and the
	// bar all name which of the two is being done, because a supersede that
	// reads as an upload would leave the operator thinking they had added a
	// document when they had replaced one.
	assetDocPhaseUpload
	assetDocPhaseCount
)

const (
	assetDocFieldPath = iota
	assetDocFieldTitle
	assetDocFieldCategory
	assetDocFieldDesc
	assetDocFieldCount
)

type AssetDocumentsScreen struct {
	deps      Deps
	assetID   string
	assetName string
	docs      []omsapi.AssetDocument
	loading   bool
	loadErr   string
	loadSeq   int
	jdeScreen

	phase  assetDocPhase
	cursor int
	errMsg string

	inputs      []textinput.Model
	focus       int
	categoryIdx int
	uploading   bool
	// supersedeOf is the document this upload REPLACES, empty for a plain
	// upload. It is captured when the form opens and spent when it posts, so a
	// reload landing under the form cannot re-point it at another row.
	supersedeOf    string
	supersedeTitle string

	confirmingDelete bool
	deleting         bool
	deleteScroll     int
	deleteNote       string
}

type assetDocsLoadedMsg struct {
	docs []omsapi.AssetDocument
	err  error
	seq  int
}

type assetDocUploadedMsg struct {
	doc       *omsapi.AssetDocument
	supersede bool
	err       error
}

type assetDocDeletedMsg struct{ err error }

func NewAssetDocumentsScreen(deps Deps, assetID, assetName string) *AssetDocumentsScreen {
	s := &AssetDocumentsScreen{deps: deps, assetID: assetID, assetName: assetName, loading: true}

	// No placeholders: in a fixed-width columnar field a placeholder fills the
	// input area and hides the underscores that say the field is empty, so what
	// the field wants rides beside it as a Hint.
	s.inputs = make([]textinput.Model, assetDocFieldCount)
	for i, limit := range map[int]int{
		assetDocFieldPath: 500, assetDocFieldTitle: 255, assetDocFieldDesc: 500,
	} {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = limit
		s.inputs[i] = in
	}
	// The category row is a choice, not a box, but the slice is indexed by row
	// so it keeps a zero-value entry there.
	s.inputs[assetDocFieldCategory] = textinput.New()
	return s
}

func (s *AssetDocumentsScreen) Title() string {
	if s.assetName != "" {
		return fmt.Sprintf("Documents · %s", s.assetName)
	}
	return "Asset documents"
}

// WantsRawInput claims keys during the upload form and the delete confirm so the
// path input and the confirm's own keys land here; the plain list stays non-raw
// so the sidebar's tab and the global back-step keep working while browsing.
func (s *AssetDocumentsScreen) WantsRawInput() bool {
	return s.phase == assetDocPhaseUpload || s.confirmingDelete
}

func (s *AssetDocumentsScreen) Init() tea.Cmd { return s.load() }

func (s *AssetDocumentsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetDocumentsScreen) load() tea.Cmd {
	s.loadSeq++
	seq := s.loadSeq
	deps, id, ctx := s.deps, s.assetID, s.ctx()
	return func() tea.Msg {
		docs, err := deps.OMS.ListAssetDocuments(ctx, id)
		return assetDocsLoadedMsg{docs: docs, err: err, seq: seq}
	}
}

// addressedDoc is the one guarded read every s.docs[s.cursor] goes through: a
// reload can land under an open confirm, and a positional index followed blindly
// would name one document on the prompt and destroy another.
func (s *AssetDocumentsScreen) addressedDoc() (omsapi.AssetDocument, bool) {
	if s.cursor < 0 || s.cursor >= len(s.docs) {
		return omsapi.AssetDocument{}, false
	}
	return s.docs[s.cursor], true
}

func (s *AssetDocumentsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case assetDocsLoadedMsg:
		if m.seq != s.loadSeq {
			return s, nil
		}
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("load documents failed: "+m.err.Error(), StatusError)
		}
		s.loadErr = ""
		prev, had := s.addressedDoc()
		s.docs = m.docs
		s.reseatCursor(prev, had)
		return s, nil

	case assetDocUploadedMsg:
		s.uploading = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("upload failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		s.phase = assetDocPhaseList
		s.blurAll()
		for i := range s.inputs {
			s.inputs[i].SetValue("")
		}
		s.supersedeOf, s.supersedeTitle = "", ""
		word := "document uploaded"
		if m.supersede && m.doc != nil {
			word = fmt.Sprintf("replaced — now v%d", m.doc.Version)
		}
		s.loading = true
		return s, tea.Batch(Status(word, StatusOK), s.load())

	case assetDocDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("document deleted", StatusOK), s.load())

	case tea.KeyMsg:
		switch {
		case s.confirmingDelete:
			return s.updateConfirmDelete(m)
		case s.phase == assetDocPhaseUpload:
			return s.updateUpload(m)
		default:
			return s.updateList(m)
		}
	}

	if s.phase == assetDocPhaseUpload && s.focus != assetDocFieldCategory {
		var cmd tea.Cmd
		s.inputs[s.focus], cmd = s.inputs[s.focus].Update(msg)
		return s, cmd
	}
	return s, nil
}

// reseatCursor follows the operator's place to its own DOCUMENT across a reload,
// by identity rather than by position, and closes a confirm standing on one that
// is gone rather than re-pointing it.
func (s *AssetDocumentsScreen) reseatCursor(prev omsapi.AssetDocument, had bool) {
	if had && prev.ID != "" {
		for i, d := range s.docs {
			if d.ID == prev.ID {
				s.cursor = i
				return
			}
		}
		if s.confirmingDelete {
			s.confirmingDelete = false
			s.deleteNote = assetDocGoneNote
		}
	}
	if s.cursor >= len(s.docs) {
		s.cursor = len(s.docs) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

const assetDocGoneNote = "that document is no longer on this asset — nothing was deleted."

func (s *AssetDocumentsScreen) blurAll() {
	for i := range s.inputs {
		s.inputs[i].Blur()
	}
}

// ---------------------------------------------------------------------------
// The grid
// ---------------------------------------------------------------------------

func (s *AssetDocumentsScreen) listNames(key string) bool {
	for _, it := range s.listBar() {
		if it.Key == key {
			return true
		}
	}
	return false
}

func (s *AssetDocumentsScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, s.assetID))
	case "up":
		s.moveList(-1)
	case "down":
		s.moveList(+1)
	case "pgup":
		if s.listNames("PgUp/PgDn") {
			s.pageList(-1)
		}
	case "pgdown":
		if s.listNames("PgUp/PgDn") {
			s.pageList(+1)
		}
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "enter":
		return s, s.openUpload("", "")
	case "ctrl+e":
		if !s.listNames("Ctrl-E") {
			return s, nil
		}
		doc, ok := s.addressedDoc()
		if !ok {
			return s, nil
		}
		return s, s.openUpload(doc.ID, doc.Title)
	case "ctrl+x":
		if !s.listNames("Ctrl-X") {
			return s, nil
		}
		s.confirmingDelete = true
		s.deleteScroll = 0
		s.deleteNote = ""
		return s, nil
	}
	return s, nil
}

func (s *AssetDocumentsScreen) moveList(delta int) {
	next, ok := s.pickRow(s.cursor, len(s.docs), delta, len(s.listHeader()), s.listBar())
	if !ok {
		return
	}
	s.cursor = next
}

func (s *AssetDocumentsScreen) pageList(dir int) {
	next, ok := s.pageRow(s.listLines(), s.cursor, len(s.docs), dir,
		len(s.listHeader()), s.listBar(), s.listBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
}

// openUpload switches to the form with the path row focused. supersedeOf empty
// means a plain upload; otherwise the form is replacing that document, and its
// title and category are PREFILLED from it so a replacement that changes neither
// keeps the identity of the version it replaces.
func (s *AssetDocumentsScreen) openUpload(supersedeOf, supersedeTitle string) tea.Cmd {
	s.phase = assetDocPhaseUpload
	s.focus = assetDocFieldPath
	s.errMsg = ""
	s.supersedeOf, s.supersedeTitle = supersedeOf, supersedeTitle
	if supersedeOf != "" {
		if doc, ok := s.addressedDoc(); ok && doc.ID == supersedeOf {
			s.inputs[assetDocFieldTitle].SetValue(doc.Title)
			s.categoryIdx = assetDocCategoryIndex(doc.Category)
		}
	} else {
		s.inputs[assetDocFieldTitle].SetValue("")
		s.categoryIdx = 0
	}
	s.inputs[assetDocFieldPath].SetValue("")
	s.inputs[assetDocFieldDesc].SetValue("")
	s.blurAll()
	s.inputs[assetDocFieldPath].Focus()
	return textinput.Blink
}

func assetDocCategoryIndex(value string) int {
	for i, c := range omsapi.AssetDocumentCategories() {
		if c.Value == value {
			return i
		}
	}
	return 0
}

// Grid columns. The VERSION is a fact and never gives; the title is the
// identifier that abbreviates around it.
const (
	assetDocNumW = 3
	assetDocVerW = 4
)

var assetDocIndent = strings.Repeat(" ", len(jdeIndent)+assetDocNumW+2)

func (s *AssetDocumentsScreen) titleWidth() int {
	const minW, maxW = 12, 52
	width := 76
	if w := s.bodyWidth(); w > 0 {
		width = w
	}
	switch w := width - (len(jdeIndent) + assetDocNumW + 2 + 2 + assetDocVerW); {
	case w < minW:
		return minW
	case w > maxW:
		return maxW
	default:
		return w
	}
}

func assetDocGridRow(num, title, version string, titleW int) string {
	return jdeIndent + strings.TrimRight(strings.Join([]string{
		padCell(num, assetDocNumW, alignRight),
		padCell(title, titleW, alignLeft),
		padCell(version, assetDocVerW, alignRight),
	}, "  "), " ")
}

func (s *AssetDocumentsScreen) listLines() *jdeLines {
	l := &jdeLines{}
	titleW := s.titleWidth()
	for i, d := range s.docs {
		row := assetDocGridRow(strconv.Itoa(i+1), fitCell(d.Title, titleW),
			"v"+strconv.Itoa(d.Version), titleW)
		if i == s.cursor {
			row = StyleJDEFieldFocused.Render(row)
		}
		l.AddRow(i, row)

		var tokens []jdeToken
		if !d.IsCurrent {
			// THE ONE MARK THAT MATTERS: a superseded row is a manual somebody
			// could otherwise follow.
			tokens = append(tokens, jdeToken{text: "superseded", style: StyleStatusWarn})
		}
		if d.CategoryDisplay != "" {
			tokens = append(tokens, jdeToken{text: d.CategoryDisplay, style: StyleMuted})
		}
		if d.SupersedesTitle != "" {
			tokens = append(tokens, jdeToken{text: "replaces " + d.SupersedesTitle, style: StyleMuted})
		}
		if d.Description != "" {
			tokens = append(tokens, jdeToken{text: d.Description, style: StyleMuted})
		}
		if d.UploadedByName != "" {
			tokens = append(tokens, jdeToken{text: "by " + d.UploadedByName, style: StyleMuted})
		}
		if !d.UploadedAt.IsZero() {
			tokens = append(tokens, jdeToken{text: d.UploadedAt.Local().Format("2006-01-02"), style: StyleMuted})
		}
		for _, line := range jdeWrapTokens(tokens, assetDocIndent, s.bodyWidth()) {
			l.AddRow(i, line)
		}
	}
	return l
}

func (s *AssetDocumentsScreen) listHeader() jdeHeader {
	h := jdeHeader(nil)
	if s.loadErr != "" {
		h = h.add(jdeHeadContext,
			StyleStatusError.Render("Error: ")+fitCellIf(s.loadErr, s.bodyWidth()-poErrPrefixW), "")
	}
	if len(s.docs) == 0 {
		h = h.add(jdeHeadContext, StyleJDEHeading.Render("Documents (0)"))
		if s.loadErr != "" {
			return h.add(jdeHeadEssential, jdeIndent+StyleMuted.Render(
				fitCellIf("Could not read the library — there may still be documents.",
					s.bodyWidth()-len(jdeIndent))))
		}
		return h.add(jdeHeadEssential, jdeIndent+StyleMuted.Render(
			fitCellIf("No documents here. Enter uploads one.", s.bodyWidth()-len(jdeIndent))))
	}
	return h.add(jdeHeadContext, StyleJDEHeading.Render(
		fmt.Sprintf("Documents (%d)", len(s.docs)))).
		add(jdeHeadEssential, StyleMuted.Render(
			assetDocGridRow("#", "Title", "Ver", s.titleWidth())))
}

func (s *AssetDocumentsScreen) listBar() []actionBarItem {
	return s.listBarItems(s.listPages())
}

func (s *AssetDocumentsScreen) listBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Upload"}, {"Esc", "Back"}}
	if jdeRowMoves(len(s.docs)) {
		items = append(items, actionBarItem{"UP/DN", "Move"})
	}
	if len(s.docs) > 0 {
		items = append(items,
			actionBarItem{"Ctrl-E", "Replace"},
			actionBarItem{"Ctrl-X", "Delete"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

func (s *AssetDocumentsScreen) listPages() bool {
	if len(s.docs) == 0 {
		return false
	}
	return s.bodyPagesForBar(s.listLines(), len(s.docs), len(s.listHeader()), s.listBarItems(true))
}

// listStatus reports a DELETE still in flight as readily as a load: Esc leaves
// the confirm while the write is out, so this row is reachable in that state and
// a screen silent about an irreversible write it is running shows the operator
// less than it knows.
func (s *AssetDocumentsScreen) listStatus() string {
	if s.deleting {
		return s.statusRow(true, "Deleting…", "")
	}
	if !s.loading && s.deleteNote != "" {
		return s.statusAnswer(StatusWarn, s.deleteNote)
	}
	return s.statusRow(s.loading, "Reading this asset's documents…", "")
}

func (s *AssetDocumentsScreen) viewList() string {
	return s.frameWrapped(s.listHeader(), s.listLines(), s.cursor, s.listStatus(), s.listBar())
}

// ---------------------------------------------------------------------------
// Upload / supersede
// ---------------------------------------------------------------------------

// assetDocUploadBar is the form's bar, said ONCE: the movement arm asks the
// layer whether the frame is drawn before it moves the caret, and a second
// literal beside the view's would be a bar measured that is not the bar drawn.
//
// The commit LABEL says which of the two writes this is, because the form is
// shared and the bar is what an operator reads before pressing Enter.
func (s *AssetDocumentsScreen) uploadBar() []actionBarItem {
	verb := "Upload"
	if s.supersedeOf != "" {
		verb = "Replace"
	}
	return []actionBarItem{
		{"Enter", verb}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}, {"←→", "Category"},
	}
}

func (s *AssetDocumentsScreen) updateUpload(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = assetDocPhaseList
		s.errMsg = ""
		s.blurAll()
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "up" || m.String() == "shift+tab" {
			delta = -1
		}
		// The header length is the PINNED header this phase really draws, not
		// zero: moveRow asks the layer whether the frame is drawn at all, and
		// with a zero the caret moved at heights where the pane drew nothing but
		// the too-short notice.
		next, ok := s.moveRow(s.focus, assetDocFieldCount, delta,
			len(s.uploadHeader()), s.uploadBar())
		if !ok {
			return s, nil
		}
		s.blurAll()
		s.focus = next
		if s.focus != assetDocFieldCategory {
			s.inputs[s.focus].Focus()
		}
		return s, textinput.Blink
	case "left", "right":
		if s.focus != assetDocFieldCategory {
			break
		}
		cats := omsapi.AssetDocumentCategories()
		delta := +1
		if m.String() == "left" {
			delta = -1
		}
		s.categoryIdx = (s.categoryIdx + delta + len(cats)) % len(cats)
		return s, nil
	case "enter":
		if s.uploading {
			return s, nil
		}
		return s, s.submitUpload()
	}
	if s.focus != assetDocFieldCategory {
		var cmd tea.Cmd
		s.inputs[s.focus], cmd = s.inputs[s.focus].Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *AssetDocumentsScreen) submitUpload() tea.Cmd {
	path := strings.TrimSpace(s.inputs[assetDocFieldPath].Value())
	if path == "" {
		s.errMsg = "nothing uploaded: a file path is required."
		return Status(s.errMsg, StatusError)
	}
	info, err := os.Stat(path)
	if err != nil {
		s.errMsg = "nothing uploaded: cannot read file: " + err.Error()
		return Status(s.errMsg, StatusError)
	}
	if info.IsDir() {
		s.errMsg = "nothing uploaded: that path is a directory, not a file."
		return Status(s.errMsg, StatusError)
	}
	title := strings.TrimSpace(s.inputs[assetDocFieldTitle].Value())
	// A plain upload REQUIRES a title (the server refuses a blank one with a
	// field error); a supersede does not, because an omitted title INHERITS the
	// prior document's. The refusal is made here rather than relayed only
	// because the operator is standing on the frame with the empty box in front
	// of them and can satisfy it without leaving.
	if title == "" && s.supersedeOf == "" {
		s.errMsg = "nothing uploaded: give the document a title."
		return Status(s.errMsg, StatusError)
	}
	desc := strings.TrimSpace(s.inputs[assetDocFieldDesc].Value())
	category := omsapi.AssetDocumentCategories()[s.categoryIdx].Value
	name := filepath.Base(path)

	s.uploading = true
	s.errMsg = ""
	deps, ctx := s.deps, s.ctx()
	assetID, supersedeOf := s.assetID, s.supersedeOf
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return assetDocUploadedMsg{err: err, supersede: supersedeOf != ""}
		}
		defer f.Close()
		if supersedeOf != "" {
			doc, err := deps.OMS.SupersedeAssetDocument(ctx, supersedeOf, title, category, desc, name, f)
			return assetDocUploadedMsg{doc: doc, supersede: true, err: err}
		}
		doc, err := deps.OMS.UploadAssetDocument(ctx, assetID, title, category, desc, name, f)
		return assetDocUploadedMsg{doc: doc, err: err}
	}
}

var assetDocLabels = map[int]string{
	assetDocFieldPath:     "File path",
	assetDocFieldTitle:    "Title",
	assetDocFieldCategory: "Kind",
	assetDocFieldDesc:     "Description",
}

func (s *AssetDocumentsScreen) uploadFields() []jdeField {
	titleHint := "shown in the library"
	if s.supersedeOf != "" {
		titleHint = "blank keeps the one it replaces"
	}
	hints := map[int]string{
		assetDocFieldPath:  "a path on this machine",
		assetDocFieldTitle: titleHint,
		assetDocFieldDesc:  "optional",
	}
	widths := map[int]int{
		assetDocFieldPath: 34, assetDocFieldTitle: 30, assetDocFieldDesc: 30,
	}
	fields := make([]jdeField, assetDocFieldCount)
	for i := 0; i < assetDocFieldCount; i++ {
		if i == assetDocFieldCategory {
			fields[i] = jdeField{
				Label: assetDocLabels[i], Kind: jdeChoice,
				Value:   omsapi.AssetDocumentCategories()[s.categoryIdx].Label,
				Focused: s.focus == i,
			}
			continue
		}
		fields[i] = jdeField{
			Label: assetDocLabels[i], Kind: jdeText, Input: &s.inputs[i],
			Width: widths[i], Hint: hints[i], Focused: s.focus == i,
		}
	}
	return fields
}

// supersedeCaveat says what replacing does to the document it replaces, on the
// frame that does it — and it says what is NOT lost, because that is the half
// that distinguishes this from the delete two keys away.
const supersedeCaveat = "The version this replaces is kept and marked superseded, so it stays " +
	"readable and nobody follows it by accident. The server numbers the new one."

func (s *AssetDocumentsScreen) uploadHeader() jdeHeader {
	title := "Upload a document"
	if s.supersedeOf != "" {
		title = "Replace a document"
	}
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleJDEHeading.Render(title), "")
	if s.supersedeOf != "" {
		h = h.add(jdeHeadEssential, renderJDEField(jdeField{
			Label: "Replaces", Kind: jdeValue,
			Value:   fitCellIf(s.supersedeTitle, jdeStripWidth(s.bodyWidth(), assetLabelW)),
			Focused: true,
		}, assetLabelW, s.bodyWidth()))
		// FITTED, not added as independent rows: jdeFitHeader gives ground from
		// the END within a rank and this caveat is ONE SENTENCE folded, so a trim
		// left a fragment ending on a whole word — which is what a finished
		// sentence looks like. Measured at 80x12, a pane Root really draws.
		width := s.bodyWidth()
		h = h.addFittedBlock(jdeHeadContext, jdeCaveatLines(supersedeCaveat, width),
			func(rows int) []string { return jdeCaveatLinesIn(supersedeCaveat, width, rows) })
	} else {
		h = h.add(jdeHeadEssential, renderJDEField(jdeField{
			Label: "Asset", Kind: jdeValue,
			Value: fitCellIf(s.assetName, jdeStripWidth(s.bodyWidth(), assetLabelW)),
		}, assetLabelW, s.bodyWidth()))
	}
	return h.add(jdeHeadDecorative, "")
}

func (s *AssetDocumentsScreen) viewUpload() string {
	fields := s.uploadFields()
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	verb := "Uploading…"
	if s.supersedeOf != "" {
		verb = "Uploading the replacement…"
	}
	return s.frameWrapped(s.uploadHeader(), body, s.focus,
		s.statusRow(s.uploading, verb, s.errMsg), s.uploadBar())
}

// ---------------------------------------------------------------------------
// Delete confirm
// ---------------------------------------------------------------------------

func (s *AssetDocumentsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "enter":
		if !s.confirmDeleteDestroys() {
			// A delete is already out. The status row draws "Deleting…" and the
			// bar has dropped the key — the one expression both read — so the
			// frame has answered already. Only the WRITE waits: Esc still
			// leaves and the caveat still scrolls, because reading the warning
			// while the server thinks is exactly what somebody does here.
			return s, nil
		}
		doc, ok := s.addressedDoc()
		if !ok {
			s.confirmingDelete = false
			s.deleteNote = assetDocGoneNote
			return s, nil
		}
		s.deleting = true
		s.deleteNote = ""
		deps, ctx, id := s.deps, s.ctx(), doc.ID
		return s, func() tea.Msg {
			return assetDocDeletedMsg{err: deps.OMS.DeleteAssetDocument(ctx, id)}
		}
	case "esc":
		s.confirmingDelete = false
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.confirmDeleteHeader()), s.confirmDeleteBar()) {
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
		// Every other key ANSWERS: this frame holds no cursor and no caret, so a
		// silent return redraws a pane that is a pure function of unchanged
		// state. The note says what the KEY DID and names none — the bar makes
		// that claim, where no budget can trim it.
		s.deleteNote = m.String() + " does nothing here — this frame only confirms or cancels."
	}
	return s, nil
}

// confirmDeleteDestroys is the ONE expression behind "may Enter write": the bar
// reads it to decide whether to name the key, the arm to decide whether to act.
func (s *AssetDocumentsScreen) confirmDeleteDestroys() bool { return !s.deleting }

func (s *AssetDocumentsScreen) confirmDeleteName() string {
	doc, ok := s.addressedDoc()
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s (v%d)", doc.Title, doc.Version)
}

func (s *AssetDocumentsScreen) confirmDeleteHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleStatusWarn.Render("Delete document"), "")
	h = h.add(jdeHeadEssential, renderJDEField(jdeField{
		Label: "Document", Kind: jdeValue,
		Value:   fitCellIf(s.confirmDeleteName(), jdeStripWidth(s.bodyWidth(), assetLabelW)),
		Focused: true,
	}, assetLabelW, s.bodyWidth()))
	return h.add(jdeHeadDecorative, "")
}

// confirmDeleteBody is the caveat, said ONCE so the lines the bar is measured
// against are the lines the frame draws. It owns no navigable row: there is
// nothing here to type into, so the window over it is positioned by an OFFSET
// rather than by a cursor, which is what keeps a warning off a pinned body's
// unreachable tail.
//
// The SECOND sentence is the one a reader will not have thought of, and it is
// only said where it is true: deleting a document another version supersedes
// leaves that version pointing at nothing, because the server's FK is
// SET_NULL. Replacing is named as the instrument that does not do that.
func (s *AssetDocumentsScreen) confirmDeleteBody() *jdeLines {
	body := &jdeLines{}
	add := func(text string) {
		for _, line := range jdeCaveatLines(text, s.bodyWidth()) {
			body.Add(line)
		}
	}
	add("Removes the document and its file from this asset. This cannot be undone here or on the web.")
	if s.deleteBreaksChain() {
		add("A later version replaces this one. Deleting it leaves that version with nothing to " +
			"point back at, and the gap is not recorded anywhere. Ctrl-E on the newest version " +
			"replaces a document without destroying the one it replaces.")
	}
	return body
}

// deleteBreaksChain reports whether any OTHER document on this asset supersedes
// the one about to be destroyed. It is answered from the rows already loaded —
// the whole library is walked into memory — rather than guessed from the row's
// own fields, which say what it replaces and never what replaces it.
func (s *AssetDocumentsScreen) deleteBreaksChain() bool {
	doc, ok := s.addressedDoc()
	if !ok {
		return false
	}
	for _, other := range s.docs {
		if other.ID != doc.ID && other.Supersedes != nil && *other.Supersedes == doc.ID {
			return true
		}
	}
	return false
}

// confirmDeleteScrolls is measured against the bar that will really be DRAWN
// with the scroll keys added, because THAT is the fixed point: while the write
// is out the drawn bar has lost Enter and can be a row shorter, so a body
// measured against an unconditionally taller bar could answer "it scrolls" for a
// body that fits — the arm would bump the offset, the clamp would put it back,
// and the pane would return byte-identical with no note.
func (s *AssetDocumentsScreen) confirmDeleteScrolls() bool {
	return s.bodyScrollsForBar(s.confirmDeleteBody(), len(s.confirmDeleteHeader()),
		s.confirmDeleteBarItems(s.confirmDeleteDestroys(), true))
}

func (s *AssetDocumentsScreen) confirmDeleteBar() []actionBarItem {
	return s.confirmDeleteBarItems(s.confirmDeleteDestroys(), s.confirmDeleteScrolls())
}

func (s *AssetDocumentsScreen) confirmDeleteBarItems(destroy, scroll bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Delete"}, {"Esc", "Cancel"}}
	if !destroy {
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

func (s *AssetDocumentsScreen) confirmDeleteStatus() string {
	if !s.deleting {
		return s.statusAnswer(StatusInfo, s.deleteNote)
	}
	verb := "Deleting…"
	switch room := s.bodyWidth(); {
	case s.deleteNote == "":
	case room > 0:
		verb = poLeadOnto(s.deleteNote, verb, room)
	default:
		verb = s.deleteNote + poLeadJoint + verb
	}
	return s.statusRow(true, verb, "")
}

func (s *AssetDocumentsScreen) viewConfirmDelete() string {
	frame, offset := s.frameScrolled(s.confirmDeleteHeader(), s.confirmDeleteBody(),
		s.deleteScroll, s.confirmDeleteStatus(), s.confirmDeleteBar())
	s.deleteScroll = offset
	return frame
}

func (s *AssetDocumentsScreen) View() string {
	if s.phase == assetDocPhaseUpload {
		return s.viewUpload()
	}
	if s.confirmingDelete {
		return s.viewConfirmDelete()
	}
	return s.viewList()
}
