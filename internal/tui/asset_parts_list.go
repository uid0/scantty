// AssetPartsScreen — the create/edit/delete + mark-replaced management list for
// one asset's parts (the web "Part Replacement Tracking" table). Reached by
// pressing S on the asset detail; drills asset → its parts the same way
// electrical_manage.go drills panel → breakers → circuits ([[scantty-parity-program]]).
//
// Keybindings follow the electrical_manage convention: n new, E/enter edit, x
// delete (y/n confirm), R mark-replaced (y/n confirm — a distinct lifecycle
// action that stamps last_replaced_at=now server-side), the whole movement
// vocabulary, r refresh. The bar is a record (proseBar) naming exactly those, in
// every state the screen draws. When the marked part is SERIALIZED
// (part_details.is_serialized), the y/n confirm is followed by a single-field
// prompt for the replacement unit's serial (op-8nxe parity); a blank submit
// records none. n and G collide with global hotkeys (Notifications /
// Categories), so the screen implements LocalKeyScreen to claim them;
// WantsRawInput is asserted while any confirm/prompt is up so y/n and the serial
// keystrokes land here. Delete needs no child-count pre-check — the backend
// SET_NULLs / unlinks the only referencing rows, so it never FK-409s (unlike the
// electrical tiers).
//
// A PART IS SEVERAL LINES — its name, the quantity and replacement facts, and
// its notes — so the window is packed by LINES (proseFlatListFrame) and a PAGE is
// what that window shows (proseLinePage), never a count of rows. Both are
// argued where they are decided.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type partsConfirm int

const (
	partsConfirmNone partsConfirm = iota
	partsConfirmDelete
	partsConfirmReplace
	// partsConfirmReplaceSerial is the second step of mark-replaced for a
	// SERIALIZED part: after the y/n confirm, a single-field prompt captures the
	// replacement unit's serial (op-8nxe). Non-serialized parts skip it.
	partsConfirmReplaceSerial
)

type AssetPartsScreen struct {
	deps      Deps
	assetID   string
	assetName string

	rows           []omsapi.AssetPart
	cursor         int
	windowStart    int
	loading        bool
	loadErr        string
	terminalHeight int
	terminalWidth  int

	confirm partsConfirm
	working bool // a delete/mark-replaced request is in flight

	// serial-entry prompt (mark-replaced of a serialized part)
	serialInput       textinput.Model
	replaceTargetID   string // part id captured when the prompt opens (stale guard)
	replaceTargetName string
}

type assetPartsLoadedMsg struct {
	rows []omsapi.AssetPart
	err  error
}

type assetPartDeletedMsg struct {
	err error
}

type assetPartReplacedMsg struct {
	part *omsapi.AssetPart
	err  error
}

func NewAssetPartsScreen(deps Deps, assetID, assetName string) *AssetPartsScreen {
	serial := textinput.New()
	serial.Prompt = ""
	serial.CharLimit = 100
	serial.Placeholder = "replacement unit serial (blank to skip)"
	s := &AssetPartsScreen{deps: deps, assetID: assetID, assetName: assetName, loading: true, serialInput: serial}
	s.sizeSerialInput()
	return s
}

func (s *AssetPartsScreen) Title() string {
	if s.assetName != "" {
		return "Parts: " + s.assetName
	}
	return "Parts"
}

// WantsRawInput claims every key only while a confirm is up (y/n/esc).
func (s *AssetPartsScreen) WantsRawInput() bool { return s.confirm != partsConfirmNone }

// HandlesKey claims the two action keys that collide with global hotkeys (n new,
// G bottom) so they reach this screen instead of the global nav switch.
func (s *AssetPartsScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *AssetPartsScreen) Init() tea.Cmd { return s.load() }

func (s *AssetPartsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetPartsScreen) load() tea.Cmd {
	deps := s.deps
	assetID := s.assetID
	ctx := s.ctx()
	return func() tea.Msg {
		page, err := deps.OMS.ListAssetParts(ctx, assetID)
		if err != nil {
			return assetPartsLoadedMsg{err: err}
		}
		return assetPartsLoadedMsg{rows: page.Results}
	}
}

func (s *AssetPartsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		s.sizeSerialInput()
		return s, nil
	case assetPartsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case assetPartDeletedMsg:
		s.working = false
		s.confirm = partsConfirmNone
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("part deleted", StatusOK), s.load())
	case assetPartReplacedMsg:
		s.working = false
		s.confirm = partsConfirmNone
		if m.err != nil {
			return s, Status("mark-replaced failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("marked replaced", StatusOK), s.load())
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		if s.confirm != partsConfirmNone {
			return s.updateConfirm(m)
		}
		// The movement arms only move the cursor: the window is refitted to it
		// where the frame is drawn (proseFlatListFrame), against the rows as they
		// really draw.
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "pgdown":
			s.page(true)
		case "pgup":
			s.page(false)
		case "g", "home":
			s.cursor = 0
		case "G", "end":
			s.cursor = len(s.rows) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "n":
			return s, SwitchTo(WSAssets, NewAssetPartFormScreen(s.deps, s.assetID, s.assetName, ""))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSAssets, NewAssetPartFormScreen(s.deps, s.assetID, s.assetName, row.IDString()))
			}
		case "R":
			if _, ok := s.selected(); ok {
				s.confirm = partsConfirmReplace
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirm = partsConfirmDelete
			}
		}
	}
	return s, nil
}

// page moves the cursor a screenful with pgdn (`down`) or pgup, and starts the
// window where the page lands.
//
// IT USED TO BE `cursor += windowSize`, with windowSize the body budget counted
// in ROWS — a screenful only while every row is one line, and no part on this
// list ever is: the name, the quantity and replacement facts, and the notes are
// two to four lines before a stored newline adds any. Measured at 80x24 with the
// row-counted window, one pgdn took the cursor from the first part to the
// fifteenth while the pane held six, so the operator's place went off the pane
// and every part between was skipped. proseLinePage carries the decision: a page
// is what the packed window shows, so pgdn lands on the first part the window did
// not hold whole and pgup on the last one above it. It asks the SAME budget and
// the same rendered rows the frame draws with, so the page and the window cannot
// disagree about what a screenful is.
func (s *AssetPartsScreen) page(down bool) {
	s.cursor, s.windowStart = proseLinePage(proseRowHeights(s.listRows()), s.cursor, s.windowStart, s.listBudget(), down)
}

// listBudget is how many body lines the list window gets — proseFlatListBudget
// under the head the frame draws, against the bar's ceiling — or zero on an
// unsized screen, where the frame draws every row.
func (s *AssetPartsScreen) listBudget() int {
	if s.terminalHeight <= 0 {
		return 0
	}
	return proseFlatListBudget(s.listHead(), s.terminalHeight, s.paneCells(), s.listBar(true))
}

func (s *AssetPartsScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.working {
		return s, nil
	}
	if s.confirm == partsConfirmReplaceSerial {
		return s.updateReplaceSerial(m)
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirm = partsConfirmNone
			return s, nil
		}
		deps := s.deps
		ctx := s.ctx()
		id := row.IDString()
		switch s.confirm {
		case partsConfirmDelete:
			s.working = true
			return s, func() tea.Msg {
				return assetPartDeletedMsg{err: deps.OMS.DeleteAssetPart(ctx, id)}
			}
		case partsConfirmReplace:
			// A serialized part captures the replacement unit's serial first
			// (op-8nxe); a non-serialized part fires immediately with "" so its
			// one-click behavior is unchanged.
			if row.PartDetails.IsSerialized {
				s.openReplaceSerial(row)
				return s, textinput.Blink
			}
			s.working = true
			return s, func() tea.Msg {
				p, err := deps.OMS.MarkAssetPartReplaced(ctx, id, "")
				return assetPartReplacedMsg{part: p, err: err}
			}
		}
	case "n", "N", "esc":
		s.confirm = partsConfirmNone
	}
	return s, nil
}

// openReplaceSerial switches the replace confirm into the single-field
// serial-entry prompt, capturing the target part's id so the async result maps
// to the right row even if the list reloads underneath.
func (s *AssetPartsScreen) openReplaceSerial(row omsapi.AssetPart) {
	s.confirm = partsConfirmReplaceSerial
	s.replaceTargetID = row.IDString()
	s.replaceTargetName = assetPartName(row)
	s.serialInput.SetValue("")
	s.serialInput.Focus()
}

// updateReplaceSerial drives the replacement-serial prompt. enter submits (a
// blank value is allowed → records no serial, mirroring the web so an operator
// without the serial isn't blocked); esc cancels the whole mark-replaced.
func (s *AssetPartsScreen) updateReplaceSerial(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.serialInput.Blur()
		s.confirm = partsConfirmNone
		return s, nil
	case "enter":
		serial := strings.TrimSpace(s.serialInput.Value())
		deps := s.deps
		ctx := s.ctx()
		id := s.replaceTargetID
		s.working = true
		s.serialInput.Blur()
		return s, func() tea.Msg {
			p, err := deps.OMS.MarkAssetPartReplaced(ctx, id, serial)
			return assetPartReplacedMsg{part: p, err: err}
		}
	}
	var cmd tea.Cmd
	s.serialInput, cmd = s.serialInput.Update(m)
	return s, cmd
}

// sizeSerialInput gives the serial box the pane's width, less the caret's cell.
// A textinput with no Width grows past its row and clampToBox takes the caret
// with it (AGENTS.md), and the box has a row of its own under its label.
func (s *AssetPartsScreen) sizeSerialInput() {
	s.serialInput.Width = s.paneCells() - 1
}

func (s *AssetPartsScreen) selected() (omsapi.AssetPart, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.AssetPart{}, false
	}
	return s.rows[s.cursor], true
}

// assetPartName is what a part is called on this screen: its item's name, the
// item id where OMS sent no name, and the link's own id where it sent neither.
func assetPartName(p omsapi.AssetPart) string {
	switch {
	case p.PartName != "":
		return p.PartName
	case p.Part != "":
		return p.Part
	}
	return "part #" + p.IDString()
}

// ---------------------------------------------------------------------------
// Bar
// ---------------------------------------------------------------------------

// paneCells is the width this screen folds and clips against.
func (s *AssetPartsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// listBar names every key that acts on the parts list, as a record the honesty
// sweep can press (prose_bar.go).
//
// It used to be
//
//	j/k move · n new · E/enter edit · R mark replaced · x delete · r refresh · esc back
//
// 83 cells against the 51 an 80-column pane gives, unfolded, so clampToBox took
// `r refresh · esc back` off it — and it named two of the ten movement
// keystrokes the switch binds, while the arrows, the pager and g/G/home/end all
// moved the cursor unnamed. The movement half is proseNavCursor: every one of
// those keystrokes moves exactly when there is a second part to move to, the
// pager included, because proseLinePage clamps to the end part rather than
// stopping short of it.
func (s *AssetPartsScreen) listBar(moves bool) proseBar {
	return append(proseNavCursor(moves),
		proseBarItem{Keys: []string{"n"}, Hint: "n new"},
		proseBarItem{Keys: []string{"E", "enter"}, Hint: "E/enter edit"},
		proseBarItem{Keys: []string{"R"}, Hint: "R mark replaced"},
		proseBarItem{Keys: []string{"x"}, Hint: "x delete"},
		proseBarRefresh,
		proseBarEsc,
	)
}

// emptyBar is the bar over an asset with no parts: nothing to move through or to
// act on, so only the way to add the first part, the reload and the way back.
// The literal it replaced named `n` and `esc` and left `r` off while `r`
// reloaded the list.
func (s *AssetPartsScreen) emptyBar() proseBar {
	return proseBar{{Keys: []string{"n"}, Hint: "n new"}, proseBarRefresh, proseBarEsc}
}

// confirmBar is the bar under a y/n confirm. Both cases of each letter answer it
// (updateConfirm), so the bar spells both: the literal said `y` and `n/esc` and
// `Y` and `N` acted unnamed.
//
// THE CONFIRM IS NOT LEFT A LITERAL HERE, where the earlier recipes left theirs
// nil because a one-line prompt named its own two keys. This one did not stay one
// line: the mark-replaced literal is 76 cells before the part's name, so at 80
// columns the keys that answer it were exactly what clampToBox took.
func (s *AssetPartsScreen) confirmBar() proseBar {
	verb := "y/Y confirm"
	if s.confirm == partsConfirmDelete {
		verb = "y/Y delete"
	}
	return proseBar{
		{Keys: []string{"y", "Y"}, Hint: verb},
		{Keys: []string{"n", "N", "esc"}, Hint: "n/N/esc cancel"},
	}
}

// serialBar is the bar under the replacement-serial prompt, whose box takes
// every other key.
func (s *AssetPartsScreen) serialBar() proseBar {
	return proseBar{
		{Keys: []string{"enter"}, Hint: "enter record (blank records none)"},
		{Keys: []string{"esc"}, Hint: "esc cancel"},
	}
}

// loadBar is this list's bar while its load is out or has failed — what its key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `n` opens the form whatever the list holds; `E`/`enter` still
// open the part a refresh kept under the cursor, which the frame no longer draws
// — named because they act, and candidates for gating. `R` and `x` are not named:
// all they do here is arm a confirm the frame does not draw, so they are ignored.
func (s *AssetPartsScreen) loadBar() proseBar {
	out := proseBar{{Keys: []string{"n"}, Hint: "n new"}}
	if _, ok := s.selected(); ok {
		out = append(out, proseBarItem{Keys: []string{"E", "enter"}, Hint: "E/enter edit"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

// proseBar is the bar this screen is DRAWING, for whichever surface is up: the
// load bar, a confirm's, the serial prompt's, the empty list's or the list's. A
// write in flight answers nil — its frame is a working line and every key is held
// until the write answers (updateConfirm), so there is nothing for a bar to name.
func (s *AssetPartsScreen) proseBar() proseBar {
	switch {
	case s.loading || s.loadErr != "":
		return s.loadBar()
	case s.confirm != partsConfirmNone && s.working:
		return nil
	case s.confirm == partsConfirmReplaceSerial:
		return s.serialBar()
	case s.confirm != partsConfirmNone:
		return s.confirmBar()
	case len(s.rows) == 0:
		return s.emptyBar()
	}
	return s.listBar(listNavMoves(len(s.rows)))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View draws the parts as a line-packed window (proseFlatListFrame) under a
// count, with the bar under it.
//
// IT USED TO DRAW `windowSize` PARTS, a budget of screenBodyHeight less a chrome
// constant of four counted in rows, and every part is two lines or more — so at
// 80x40 the frame already ran past the pane with the cursor at the top, and what
// clampToBox took was the footer. A part whose notes outrun the pane is clipped
// by the frame with the cut named, and the bar stays.
func (s *AssetPartsScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Loading parts…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	if s.confirm != partsConfirmNone {
		return s.viewConfirm()
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No parts on this asset yet.") + "\n\n" + s.proseBar().render(cells)
	}
	return proseFlatListFrame(s.listHead(), s.listRows(), s.cursor, &s.windowStart,
		s.terminalHeight, cells, s.listBar(true), s.proseBar())
}

// listHead is the count line the list opens with, ending in its newline as
// proseFlatListFrame counts it.
func (s *AssetPartsScreen) listHead() string {
	return StyleMuted.Render(fmt.Sprintf("%d parts", len(s.rows))) + "\n"
}

// listRows is every part as it will be drawn.
func (s *AssetPartsScreen) listRows() []string {
	rows := make([]string, len(s.rows))
	for i := range s.rows {
		rows[i] = s.renderRow(i)
	}
	return rows
}

// viewConfirm is a confirm's whole pane: the question, folded to the pane with
// the part's name flattened and clipped so the question is a bounded height, and
// the bar that answers it.
func (s *AssetPartsScreen) viewConfirm() string {
	cells := s.paneCells()
	if s.working {
		working := "Marking replaced…"
		if s.confirm == partsConfirmDelete {
			working = "Deleting…"
		}
		return StyleMuted.Render(working)
	}
	var question string
	switch s.confirm {
	case partsConfirmDelete, partsConfirmReplace:
		row, ok := s.selected()
		if !ok {
			return ""
		}
		name := proseFormLine(assetPartName(row), cells)
		question = fmt.Sprintf("Mark %s replaced now (resets its replacement clock)?", name)
		if s.confirm == partsConfirmDelete {
			question = fmt.Sprintf("Delete %s? This can't be undone.", name)
		}
		question = StyleStatusWarn.Render(strings.Join(pickerWrap(question, cells), "\n"))
	case partsConfirmReplaceSerial:
		var b strings.Builder
		b.WriteString(StyleTitle.Render(proseFormLine("Mark "+s.replaceTargetName+" replaced", cells)) + "\n")
		b.WriteString(pickerHintAt("This part is serialized — record the replacement unit's serial.", cells) + "\n\n")
		b.WriteString(StyleTitle.Render("Replacement serial number:") + "\n")
		b.WriteString(s.serialInput.View())
		question = b.String()
	}
	return question + "\n\n" + s.proseBar().render(cells)
}

// renderRow is one part: its name, the quantity and replacement facts, and its
// notes, each line bounded to the pane with any cut marked.
//
// THE NAME AND THE NOTES ARE OMS VALUES and are drawn as stored, newlines
// included, one clipped line each (proseClipEachLine, for the reason
// proseCursorWindow gives about not flattening what the record holds); the notes'
// continuation lines are indented with the first, where they used to start at the
// left edge and read as a row of their own. The facts line gives ground a token at
// a time (listFitFacts), so a count or a date is whole or absent. The NEEDS
// REPLACEMENT flag is reserved before the name is clipped: it is the one word on
// the row an operator acts on.
func (s *AssetPartsScreen) renderRow(i int) string {
	const indent = "    "
	p := s.rows[i]
	cells := s.paneCells()
	pad := StyleSidebarItemActive.GetHorizontalPadding()
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	flag := ""
	if p.NeedsReplacement {
		flag = " " + StyleStatusWarn.Render("NEEDS REPLACEMENT")
	}
	line := proseClipEachLine(marker+assetPartName(p), cells-pad-lipgloss.Width(flag))
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	if p.PartSKU != "" {
		last := line[strings.LastIndex(line, "\n")+1:]
		if room := cells - lipgloss.Width(flag) - lipgloss.Width(last) - 1; room > 0 {
			line += " " + StyleMuted.Render(pickerClip("("+p.PartSKU+")", room))
		}
	}
	line += flag

	meta := []string{}
	if p.QuantityNeeded > 0 {
		meta = append(meta, fmt.Sprintf("qty %d", p.QuantityNeeded))
	}
	if p.IsRequired {
		meta = append(meta, "required")
	}
	if p.MaintenanceIntervalDays != nil {
		meta = append(meta, fmt.Sprintf("every %dd", *p.MaintenanceIntervalDays))
	}
	if p.LastReplacedAt != nil {
		entry := "replaced " + p.LastReplacedAt.Format("2006-01-02")
		if p.DaysSinceReplacement != nil {
			entry += fmt.Sprintf(" (%dd ago)", *p.DaysSinceReplacement)
		}
		meta = append(meta, entry)
	} else {
		meta = append(meta, "never replaced")
	}
	if p.ReplacementSerialNumber != "" {
		meta = append(meta, "s/n "+p.ReplacementSerialNumber)
	}
	out := line
	if len(meta) > 0 {
		out += "\n" + indent + StyleMuted.Render(listFitFacts(strings.Join(meta, " · "), cells-len(indent), "…"))
	}
	if p.Notes != "" {
		for _, note := range strings.Split(proseClipEachLine(p.Notes, cells-len(indent)), "\n") {
			out += "\n" + indent + StyleMuted.Render(note)
		}
	}
	return out
}
