// The two halves of the work-order screen that had no terminal route at all
// (gap G14): the per-job TOOL rows, and recording that a lockout/tagout step was
// performed.
//
// They are one file because they are one screen's two missing lists, and they
// are NOT one flow. The tools half is ordinary editing and deliberately copies
// the materials half beside it (wo_detail.go's woModeMaterials block) key for
// key — a / d for add and remove, a single editable field behind its own key,
// j/k and esc — because an operator who has learned one list has learned both.
//
// The LOTO half is a SAFETY RECORD, and three things about it are decisions
// rather than style:
//
//  1. RECORDING IS TWO DELIBERATE KEYSTROKES AND NAVIGATION IS NEVER ONE OF
//     THEM. `enter` on the highlighted step opens a REVIEW frame; `y` on that
//     frame is the write. Nothing else records, and in particular SPACE does
//     not — space toggles the row under the cursor on both neighbouring lists
//     (tasks and materials), so a hand carrying that muscle memory onto this
//     one would record a lockout step it never read. Space is bound here only
//     to SAY SO (woLotoSpaceNote): an unbound key would redraw a byte-identical
//     pane, which reads as a wedged program, and a silent key on this list
//     would be silence about the one thing this screen exists to be careful
//     about.
//     `y` rather than `enter` for the write for the reason po_edit.go's delete
//     confirm uses ctrl+x: `enter` is what OPENED the frame, so binding the
//     write to it would make a reflexive double-tap enough. `y`/`n` is also the
//     idiom this screen's own finalize/cancel confirm already uses, so the
//     answer to a question reads the same everywhere on it.
//
//  2. THE STEP IS SHOWN IN FULL, AND NOTHING ON THE REVIEW FRAME IS CLIPPED.
//     The three descriptive fields are 200, 200 and 300 characters on the wire
//     (WorkOrderLotoCompletion) against the 51 cells an 80-column pane gives,
//     so they are FOLDED (pickerWrap, which never truncates) and the frame
//     SCROLLS. A half-read lockout instruction is the exact failure this screen
//     must not have, so the answer is a layout that fits the text rather than
//     a bound that cuts it: the step's identity is PINNED above the scrolled
//     body — clampToBox drops from the bottom, so a pinned head is the one part
//     a short pane cannot take — and the rest is reachable with j/k.
//
//  3. THE SERVER'S JUDGEMENT IS RELAYED AND NEVER ANTICIPATED. complete_loto
//     has no ordering rule and no already-complete rule (omsapi's
//     CompleteWorkOrderLoto carries the measured contract), so nothing here
//     refuses a step for being out of order or already done. The frame SAYS
//     that steps may be recorded in any order — an operator is entitled to know
//     the terminal is not tracking a sequence — and the two refusals the server
//     can answer with reach the footer in the server's own words.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Add-tool form fields, in render order — the same five the web modal offers.
// The Required slot is a flag toggled with space rather than a typed box, so its
// entry in the inputs slice goes unused, which is the layout the add-material
// form already uses for its stock-item picker.
const (
	woToolName = iota
	woToolQuantity
	woToolLocation
	woToolRequired
	woToolNotes
	woToolFieldMax
)

var woToolFieldLabel = map[int]string{
	woToolName:     "Tool",
	woToolQuantity: "Quantity",
	woToolLocation: "Location for this job",
	woToolRequired: "Required",
	woToolNotes:    "Notes",
}

// woToolIsTextField reports whether a field is typed into. Only Required is not.
func woToolIsTextField(id int) bool { return id != woToolRequired }

// --- Where a tool is, and where a restage writes ---------------------------

// woToolLocationLine is the location line under a tool row.
//
// It reads ResolvedLocation, never LocationHint, because the hint is BLANK
// whenever the linked inventory item's storage location is standing in — so a
// row with a perfectly good location would have shown nothing. Where the two
// differ the source is named, because that is the difference between "somebody
// staged this for this job" and "this is where it normally lives", and the
// second is a place the tool might not actually be.
func woToolLocationLine(row omsapi.WorkOrderToolRow) string {
	switch {
	case row.LocationHint != "":
		return "staged: " + row.LocationHint
	case row.ResolvedLocation != "":
		if row.InventoryItemName != "" {
			return "stored: " + row.ResolvedLocation + " (" + row.InventoryItemName + ")"
		}
		return "stored: " + row.ResolvedLocation
	default:
		return "no location recorded"
	}
}

// --- Tool list -------------------------------------------------------------

// woToolRows is the EDITABLE rows, and it is the one predicate the list, the
// footer entry and every tool arm read. It is deliberately not wo.Tools: that is
// the display projection, which falls back to the PM TEMPLATE's rows on a work
// order that owns none of its own, and offering `d` against a template row the
// work order does not have would be a key acting on something that is not there.
func (s *WorkOrderDetailScreen) woToolRows() []omsapi.WorkOrderToolRow {
	if s.wo == nil {
		return nil
	}
	return s.wo.ToolRows
}

// woToolsAreTheTemplates reports the legacy state a client has to tell apart
// from "this job needs no tools": the work order owns no rows, but the list it
// DISPLAYS is its PM template's. There is nothing to edit there, and the first
// ad-hoc row added is what makes the work order own its list — at which point
// the template's rows stop being displayed on this job (see openAddTool).
func (s *WorkOrderDetailScreen) woToolsAreTheTemplates() bool {
	return s.wo != nil && len(s.wo.ToolRows) == 0 && len(s.wo.Tools) > 0
}

func (s *WorkOrderDetailScreen) currentToolRow() (omsapi.WorkOrderToolRow, bool) {
	rows := s.woToolRows()
	if s.toolCursor < 0 || s.toolCursor >= len(rows) {
		return omsapi.WorkOrderToolRow{}, false
	}
	return rows[s.toolCursor], true
}

func (s *WorkOrderDetailScreen) handleToolsKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := len(s.woToolRows())
	switch m.String() {
	case "esc", "q":
		s.mode = woModeView
		return s, nil
	case "up", "k":
		if s.toolCursor > 0 {
			s.toolCursor--
		}
		return s, nil
	case "down", "j":
		if s.toolCursor < n-1 {
			s.toolCursor++
		}
		return s, nil
	case "a":
		// The point of the whole half: a CORRECTIVE work order reaches this list
		// empty — it has no PM template to copy rows from — and this is where it
		// gets its first row.
		s.openAddTool()
		return s, textinput.Blink
	case "l":
		if n == 0 {
			return s, nil
		}
		return s.openToolLocation()
	case "d":
		if s.toolPending || n == 0 {
			return s, nil
		}
		return s.removeTool()
	}
	return s, nil
}

// removeTool deletes the highlighted AD-HOC row. The backend's 400 on a
// template-derived row is stated here rather than sent and bounced — the
// operator gets the reason without a round trip — which is exactly what
// removeMaterial does for its own two guards.
//
// This is NOT the LOTO half's "never anticipate the server": is_ad_hoc is a fact
// the wire already carried about THIS row, so declining on it repeats the
// server's own answer rather than inventing one. Nothing here guesses at a state
// the payload does not carry.
func (s *WorkOrderDetailScreen) removeTool() (Screen, tea.Cmd) {
	row, ok := s.currentToolRow()
	if !ok {
		return s, nil
	}
	if !row.IsAdHoc {
		return s, Status("only added tools can be removed — this one is the PM template's", StatusWarn)
	}
	toolID, name, woID := row.IDString(), row.Name, s.woID
	s.toolPending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		return woToolRemovedMsg{name: name, err: deps.OMS.RemoveWorkOrderTool(ctx, woID, toolID)}
	}
}

func (s *WorkOrderDetailScreen) renderToolPicker() string {
	rows := s.woToolRows()
	var b strings.Builder
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Tools for this job (%d)", len(rows))) + "\n\n")
	switch {
	case len(rows) == 0 && s.woToolsAreTheTemplates():
		// Absent and empty are different states, and this is the third one: the
		// job DISPLAYS tools it does not own. Say which list is on screen and
		// what adding a row does to it, because the answer is surprising.
		b.WriteString(s.wrapped(StyleMuted,
			fmt.Sprintf("The %d tool(s) shown on this work order come from its PM template, "+
				"so there is nothing here to restage or remove. "+
				"Press a to give this job its own list.", len(s.wo.Tools))))
	case len(rows) == 0:
		b.WriteString(s.wrapped(StyleMuted,
			"Nothing recorded yet. Press a to add a tool this job turned out to need."))
	}
	for i, row := range rows {
		cursor := "  "
		if i == s.toolCursor {
			cursor = "> "
		}
		// ASSEMBLED FIRST, THEN FOLDED. An MRO tool name runs to the column's
		// 200 characters, so a row built out of a name plus its facts is folded
		// as a WHOLE against what the two-cell lead leaves — the facts then land
		// intact on whichever line they fall on, rather than being appended after
		// a bound and pushed past the pane (the assetScopeRows rule).
		label := row.Name
		if row.Quantity > 1 {
			label += fmt.Sprintf(" ×%d", row.Quantity)
		}
		if row.IsAdHoc {
			label += " (added)"
		}
		if row.IsRequired {
			label += " [REQ]"
		}
		style := lipgloss.NewStyle()
		if i == s.toolCursor {
			style = StyleTitle
		}
		for j, line := range s.wrapIndented(label, "  ") {
			if j == 0 {
				line = cursor + strings.TrimPrefix(line, "  ")
			}
			b.WriteString(style.Render(line) + "\n")
		}
		b.WriteString(s.wrappedIn(StyleMuted, woToolLocationLine(row), "    "))
		if row.Notes != "" {
			b.WriteString(s.wrappedIn(StyleMuted, row.Notes, "    "))
		}
	}
	b.WriteString("\n")
	switch {
	case s.toolPending:
		b.WriteString(StyleMuted.Render("Updating…"))
	case len(rows) == 0:
		// Don't advertise keys that act on a highlighted row when there is none.
		b.WriteString(StyleMuted.Render("a add · esc back"))
	default:
		b.WriteString(pickerHintAt("j/k move · a add · l location · d remove · esc back", s.paneCells()))
	}
	return b.String()
}

// --- Add a tool ------------------------------------------------------------

func (s *WorkOrderDetailScreen) openAddTool() {
	s.atInputs = make([]textinput.Model, woToolFieldMax)
	for _, f := range []struct {
		id          int
		placeholder string
		limit       int
	}{
		{woToolName, "what you need to grab", 200},
		{woToolQuantity, "1", 8},
		{woToolLocation, "e.g. Bench 2 (blank = wherever it is stored)", 200},
		{woToolNotes, "anything the next tech should know", 500},
	} {
		in := textinput.New()
		in.Prompt = ""
		in.Placeholder = f.placeholder
		in.CharLimit = f.limit
		s.atInputs[f.id] = in
	}
	s.atRequired = true // the serializer's own default
	s.atCursor = 0
	s.atErr = ""
	s.atPending = false
	s.mode = woModeAddTool
	s.syncAddToolFocus()
}

func (s *WorkOrderDetailScreen) syncAddToolFocus() {
	for i := range s.atInputs {
		if i == s.atCursor && woToolIsTextField(i) {
			s.atInputs[i].Focus()
			continue
		}
		s.atInputs[i].Blur()
	}
}

func (s *WorkOrderDetailScreen) handleAddToolKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.mode = woModeTools
		return s, nil
	case "up", "shift+tab":
		s.moveAddToolCursor(-1)
		return s, nil
	case "down", "tab":
		s.moveAddToolCursor(1)
		return s, nil
	case " ":
		// Only on the flag row: everywhere else a space is a character in a name
		// or a note, and swallowing it would eat what the operator typed.
		if s.atCursor == woToolRequired {
			s.atRequired = !s.atRequired
			return s, nil
		}
	case "enter":
		if s.atPending {
			return s, nil
		}
		return s.submitAddTool()
	}
	if !woToolIsTextField(s.atCursor) {
		return s, nil
	}
	var cmd tea.Cmd
	s.atInputs[s.atCursor], cmd = s.atInputs[s.atCursor].Update(m)
	return s, cmd
}

func (s *WorkOrderDetailScreen) moveAddToolCursor(delta int) {
	s.atCursor = (s.atCursor + delta + woToolFieldMax) % woToolFieldMax
	s.syncAddToolFocus()
}

// submitAddTool posts the row. Only the two things the terminal genuinely cannot
// send are refused locally: a blank name (which the serializer rejects, and
// which is the only thing the tech reads off the list) and a quantity that is
// not a whole number (which has no wire representation at all).
//
// A quantity of ZERO is sent rather than corrected. "Zero of a tool" is not a
// tool and the serializer says so with min_value 1 — but that is the server's
// judgement, and quietly turning a typed 0 into 1 would record a tool the
// operator did not ask for.
func (s *WorkOrderDetailScreen) submitAddTool() (Screen, tea.Cmd) {
	name := strings.TrimSpace(s.atInputs[woToolName].Value())
	if name == "" {
		s.atCursor = woToolName
		s.syncAddToolFocus()
		s.atErr = "tool name is required"
		return s, nil
	}
	in := omsapi.WorkOrderAdHocTool{
		Name:         name,
		LocationHint: strings.TrimSpace(s.atInputs[woToolLocation].Value()),
		Notes:        strings.TrimSpace(s.atInputs[woToolNotes].Value()),
	}
	if qty := strings.TrimSpace(s.atInputs[woToolQuantity].Value()); qty != "" {
		n, err := strconv.Atoi(qty)
		if err != nil {
			s.atCursor = woToolQuantity
			s.syncAddToolFocus()
			s.atErr = "quantity must be a whole number (or blank for 1)"
			return s, nil
		}
		in.Quantity = &n
	}
	// Sent only when it differs from the serializer's default, so the shortest
	// body that says what the operator chose is the one that goes.
	if !s.atRequired {
		required := false
		in.IsRequired = &required
	}
	woID := s.woID
	s.atPending = true
	s.atErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		_, err := deps.OMS.AddWorkOrderTool(ctx, woID, in)
		return woToolAddedMsg{name: name, err: err}
	}
}

func (s *WorkOrderDetailScreen) renderAddToolForm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Add tool") + "\n")
	b.WriteString(s.wrapped(StyleMuted,
		"Gear this job turned out to need. Nothing here moves stock — a tool is "+
			"gathered, used and returned."))
	// The one surprising consequence of the write, named where it happens
	// rather than discovered afterwards: build_tools_context serves the work
	// order's OWN rows as soon as it has one, so this row replaces the
	// template's list on this job's display.
	if s.woToolsAreTheTemplates() {
		b.WriteString(s.wrapped(StyleStatusWarn,
			fmt.Sprintf("! This job currently shows its PM template's %d tool(s). Adding a row "+
				"gives the job its own list, and the template's tools stop being shown "+
				"HERE — they are not deleted, and the next job off that template still "+
				"gets them.", len(s.wo.Tools))))
	}
	b.WriteString("\n")
	if s.atPending {
		b.WriteString(StyleMuted.Render("Adding…"))
		return b.String()
	}
	for i := 0; i < woToolFieldMax; i++ {
		cursor := "  "
		label := woToolFieldLabel[i]
		if i == s.atCursor {
			cursor = "> "
			label = StyleTitle.Render(label)
		}
		b.WriteString(cursor + label + "\n")

		if i == woToolRequired {
			box := "[ ]"
			if s.atRequired {
				box = StyleStatusOK.Render("[x]")
			}
			b.WriteString("    " + box + " " + StyleMuted.Render("required tools are flagged on the job") + "\n")
			continue
		}
		b.WriteString("    " + woBoxView(s.atInputs[i], s.paneCells(), "    ") + "\n")
	}
	b.WriteString("\n")
	if s.atErr != "" {
		b.WriteString(s.wrapped(StyleStatusError, "✗ "+s.atErr) + "\n")
	}
	hint := "tab/arrows move · enter add · esc back"
	if s.atCursor == woToolRequired {
		hint = "space toggle · " + hint
	}
	b.WriteString(pickerHintAt(hint, s.paneCells()))
	return b.String()
}

// --- Restage one tool ------------------------------------------------------

// openToolLocation opens the restage box for the highlighted row.
//
// It is offered on EVERY row, template-derived included: setting where a tool is
// staged for THIS job is the whole point of the model, and the write lands only
// on the work order — never back on the PM template the row was copied from.
// locToolID is captured so the write and every redraw follow the ROW rather than
// the cursor position, which a reload can re-point at a different tool.
func (s *WorkOrderDetailScreen) openToolLocation() (Screen, tea.Cmd) {
	row, ok := s.currentToolRow()
	if !ok {
		return s, nil
	}
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "e.g. Bench 2"
	in.CharLimit = 200
	in.SetValue(row.LocationHint)
	in.CursorEnd()
	in.Focus()
	s.locIn = in
	s.locToolID = row.IDString()
	s.locErr = ""
	s.mode = woModeToolLocation
	return s, textinput.Blink
}

// locRow is the row the open restage box belongs to, found BY ID. A reload can
// remove the row under the box — somebody else deleting the tool this screen was
// restaging — and following the cursor position instead would hand the write to
// whatever row now sits at that index.
func (s *WorkOrderDetailScreen) locRow() (omsapi.WorkOrderToolRow, bool) {
	for _, row := range s.woToolRows() {
		if row.IDString() == s.locToolID && s.locToolID != "" {
			return row, true
		}
	}
	return omsapi.WorkOrderToolRow{}, false
}

func (s *WorkOrderDetailScreen) handleToolLocationKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.mode = woModeTools
		return s, nil
	case tea.KeyEnter:
		if s.locPending {
			return s, nil
		}
		return s.submitToolLocation()
	}
	var cmd tea.Cmd
	s.locIn, cmd = s.locIn.Update(m)
	return s, cmd
}

// submitToolLocation writes the hint, blank included: blank CLEARS the per-job
// hint and lets the linked item's storage location stand in again, which is a
// different stored state rather than a no-op, so there is nothing to skip.
func (s *WorkOrderDetailScreen) submitToolLocation() (Screen, tea.Cmd) {
	row, ok := s.locRow()
	if !ok {
		s.mode = woModeTools
		return s, Status("that tool is no longer on this work order", StatusWarn)
	}
	hint := strings.TrimSpace(s.locIn.Value())
	toolID, name, woID := row.IDString(), row.Name, s.woID
	s.locPending = true
	s.locErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		_, err := deps.OMS.UpdateWorkOrderToolLocation(ctx, woID, toolID, hint)
		return woToolRestagedMsg{name: name, cleared: hint == "", err: err}
	}
}

func (s *WorkOrderDetailScreen) renderToolLocationForm() string {
	row, ok := s.locRow()
	if !ok {
		return s.wrapped(StyleMuted, "That tool is no longer on this work order. esc back")
	}
	var b strings.Builder
	// The tool's NAME is 200 characters on the wire, so the identity row folds
	// rather than running off the pane: this is the row that says which tool the
	// box about to be typed into belongs to.
	b.WriteString(StyleTitle.Render("Location for this job") + "\n")
	b.WriteString(s.wrappedIn(StyleMuted, row.Name, "  "))
	b.WriteString(s.wrapped(StyleMuted,
		"Where this tool is staged for THIS job. It never rewrites the PM template, "+
			"so the next job off it keeps the template's location."))
	b.WriteString(s.wrapped(StyleMuted, "Now: "+woToolLocationLine(row)))
	b.WriteString("\n")
	if s.locPending {
		b.WriteString(StyleMuted.Render("Saving…"))
		return b.String()
	}
	b.WriteString("Location:\n  " + woBoxView(s.locIn, s.paneCells(), "  ") + "\n")
	if s.locErr != "" {
		b.WriteString("\n" + s.wrapped(StyleStatusError, "✗ "+s.locErr))
	}
	b.WriteString("\n" + pickerHintAt("enter save · esc cancel", s.paneCells()))
	return b.String()
}

// --- Lockout / tagout -----------------------------------------------------

// woLotoRows is the structured checklist, and the ONE predicate the list, the
// footer entry and the `L` arm read — so the key the footer names is the key
// that acts. Rows are cut when the work order is generated and there is no
// endpoint that creates one, so an empty list means this job's asset had no
// recorded energy sources and there is nothing for `L` to open.
func (s *WorkOrderDetailScreen) woLotoRows() []omsapi.WorkOrderLotoCompletion {
	if s.wo == nil {
		return nil
	}
	return s.wo.LotoCompletions
}

func (s *WorkOrderDetailScreen) lotoIsolated() int {
	done := 0
	for _, rec := range s.woLotoRows() {
		if rec.IsCompleted {
			done++
		}
	}
	return done
}

// woLotoSourceLabel is what to CALL one step. The label is the denormalized copy
// frozen at generation, so it survives the energy source being edited or
// deleted; a row whose label was never set falls back to the type code and then
// to its own id, because a step with no name is still a step that has to be
// nameable on a confirm.
func woLotoSourceLabel(rec omsapi.WorkOrderLotoCompletion) string {
	if rec.SourceLabel != "" {
		return rec.SourceLabel
	}
	if rec.SourceType != "" {
		return rec.SourceType
	}
	return "energy source " + rec.IDString()
}

// woLotoStateLine is the record itself: who recorded the isolation and when, or
// that nobody has. "Not recorded" is said outright rather than left blank — a
// blank beside a checkbox reads as an oversight, and on a safety record the
// absence of a mark is the fact.
func woLotoStateLine(rec omsapi.WorkOrderLotoCompletion) string {
	if !rec.IsCompleted {
		return "not recorded"
	}
	parts := []string{"recorded"}
	if rec.CompletedByName != "" {
		parts = append(parts, "by "+rec.CompletedByName)
	}
	if rec.CompletedAt != nil {
		parts = append(parts, rec.CompletedAt.Format("2006-01-02 15:04"))
	}
	return strings.Join(parts, " · ")
}

func (s *WorkOrderDetailScreen) handleLotoKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := len(s.woLotoRows())
	switch m.String() {
	case "esc", "q":
		s.mode = woModeView
		s.lotoNote = ""
		return s, nil
	case "up", "k":
		if s.lotoCursor > 0 {
			s.lotoCursor--
			s.lotoNote = ""
		}
		return s, nil
	case "down", "j":
		if s.lotoCursor < n-1 {
			s.lotoCursor++
			s.lotoNote = ""
		}
		return s, nil
	case "enter":
		if n == 0 {
			return s, nil
		}
		return s.openLotoConfirm()
	case " ":
		// Bound only to decline, and bound AT ALL for two reasons: the two lists
		// next door (tasks, materials) toggle the highlighted row on space, so a
		// hand arriving with that habit must be told this one does not; and an
		// unbound key here would redraw a byte-identical pane, which is the
		// wedged-program report this project keeps filing.
		if n == 0 {
			return s, nil
		}
		s.lotoNote = woLotoSpaceNote
		return s, nil
	}
	return s, nil
}

// woLotoSpaceNote is what space says. It names the key it declined and the key
// that works, because two presses that answered with the same sentence would
// redraw one pane.
const woLotoSpaceNote = "space does not record a lockout step — press enter to read it in full first"

func (s *WorkOrderDetailScreen) currentLoto() (omsapi.WorkOrderLotoCompletion, bool) {
	rows := s.woLotoRows()
	if s.lotoCursor < 0 || s.lotoCursor >= len(rows) {
		return omsapi.WorkOrderLotoCompletion{}, false
	}
	return rows[s.lotoCursor], true
}

func (s *WorkOrderDetailScreen) renderLotoList() string {
	rows := s.woLotoRows()
	var b strings.Builder
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Lockout / Tagout (%d/%d isolated)",
		s.lotoIsolated(), len(rows))) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(s.wrapped(StyleMuted,
			"No energy sources are recorded on this job's asset, so there is no "+
				"structured lockout checklist to mark. Rows are created when the work "+
				"order is generated — they cannot be added here."))
	}
	for i, rec := range rows {
		cursor := "  "
		if i == s.lotoCursor {
			cursor = "> "
		}
		box := "[ ]"
		if rec.IsCompleted {
			box = StyleStatusOK.Render("[x]")
		}
		// The label is folded, never clipped: it is 200 characters on the wire
		// and it is what names the hazard.
		// The row's own lead ("> [x] ") is six cells, so the label is folded
		// against what is LEFT of the pane and every continuation sits under it.
		label := s.wrapIndented(woLotoSourceLabel(rec), strings.Repeat(" ", 6))
		head := cursor + box + " " + strings.TrimLeft(label[0], " ")
		if i == s.lotoCursor {
			head = StyleTitle.Render(head)
		}
		b.WriteString(head + "\n")
		for _, cont := range label[1:] {
			b.WriteString(cont + "\n")
		}
		state := woLotoStateLine(rec)
		style := StyleStatusWarn
		if rec.IsCompleted {
			style = StyleStatusOK
		}
		b.WriteString(s.wrappedIn(style, state, strings.Repeat(" ", 6)))
	}
	b.WriteString("\n")
	if s.lotoNote != "" {
		b.WriteString(s.wrapped(StyleStatusWarn, "! "+s.lotoNote))
	}
	if len(rows) == 0 {
		b.WriteString(StyleMuted.Render("esc back"))
		return b.String()
	}
	b.WriteString(pickerHintAt("j/k move · enter review the step · esc back", s.paneCells()))
	return b.String()
}

// --- The review frame, and the one key that records ------------------------

// openLotoConfirm opens the review frame for the highlighted step.
//
// It captures BOTH the row's id and the state it was opened against. The id is
// what every redraw and the write itself look the row up by — a positional
// cursor can be re-pointed at a different step by a reload, and a confirm
// naming one lockout step while `y` records another is the shape of defect this
// project has already shipped once on a purchase-order line.
//
// lotoWas is the other half: the write sends the OPPOSITE of the state the
// operator read. If a reload finds the row already flipped — somebody ticked it
// on the web while this frame was up — the frame CLOSES rather than silently
// re-wording itself, because the meaning of `y` must not change under a hand
// that is already reaching for it (see reseatLotoConfirm).
func (s *WorkOrderDetailScreen) openLotoConfirm() (Screen, tea.Cmd) {
	rec, ok := s.currentLoto()
	if !ok {
		return s, nil
	}
	s.lotoID = rec.IDString()
	s.lotoWas = rec.IsCompleted
	s.lotoErr = ""
	s.lotoNote = ""
	if s.lotoScroller == nil {
		s.lotoScroller = NewTextScroller(defaultDetailHeight)
	}
	s.lotoScroller.Top()
	s.mode = woModeLotoConfirm
	return s, nil
}

// lotoRow is the record the open review frame is about, found BY ID.
func (s *WorkOrderDetailScreen) lotoRow() (omsapi.WorkOrderLotoCompletion, bool) {
	for _, rec := range s.woLotoRows() {
		if s.lotoID != "" && rec.IDString() == s.lotoID {
			return rec, true
		}
	}
	return omsapi.WorkOrderLotoCompletion{}, false
}

// reseatLotoConfirm is what a reload landing under an open review frame does.
//
// Two states close it, and each is a case where `y` would no longer mean what
// the frame said it meant: the row is GONE from the payload, or its recorded
// state has CHANGED since the frame opened. Both leave the operator on the list
// with the reason on the status row, so the next press is made against what is
// actually there rather than against what was.
func (s *WorkOrderDetailScreen) reseatLotoConfirm() string {
	if s.mode != woModeLotoConfirm {
		return ""
	}
	rec, ok := s.lotoRow()
	if !ok {
		s.mode = woModeLoto
		return "that lockout step is no longer on this work order"
	}
	if rec.IsCompleted != s.lotoWas {
		s.mode = woModeLoto
		s.lotoCursor = s.lotoIndexOf(s.lotoID)
		return fmt.Sprintf("%q changed while you were reading it — %s. Nothing was recorded.",
			woLotoSourceLabel(rec), woLotoStateLine(rec))
	}
	return ""
}

func (s *WorkOrderDetailScreen) lotoIndexOf(id string) int {
	for i, rec := range s.woLotoRows() {
		if rec.IDString() == id {
			return i
		}
	}
	return 0
}

func (s *WorkOrderDetailScreen) handleLotoConfirmKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.lotoScroller != nil && s.lotoScroller.Handle(m) {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.lotoPending {
			return s, nil
		}
		return s.submitLoto()
	case "n", "N", "esc", "q":
		s.mode = woModeLoto
		s.lotoErr = ""
		return s, nil
	}
	// Every other key declines and says so. A frame that binds a handful of keys
	// and answers the rest with nil redraws itself byte for byte, and a
	// reflexive second press of the `enter` that opened this frame is exactly
	// the press that lands here.
	s.lotoErr = fmt.Sprintf("%s does not record anything — press y to record, n to leave it", m.String())
	return s, nil
}

// submitLoto is the write, and the only one. It sends the OPPOSITE of the state
// the operator read on the frame (lotoWas), not the opposite of whatever the
// payload says now — reseatLotoConfirm has already closed the frame if those two
// ever came apart.
//
// No note rides along: the completion's own `notes` field exists on the wire but
// the web's checkbox does not offer it either, and the backend writes it only
// when the key is present, so sending nothing leaves whatever is recorded alone.
func (s *WorkOrderDetailScreen) submitLoto() (Screen, tea.Cmd) {
	rec, ok := s.lotoRow()
	if !ok {
		s.mode = woModeLoto
		return s, Status("that lockout step is no longer on this work order", StatusWarn)
	}
	next := !s.lotoWas
	lotoID, label, woID := rec.IDString(), woLotoSourceLabel(rec), s.woID
	s.lotoPending = true
	s.lotoErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		_, err := deps.OMS.CompleteWorkOrderLoto(ctx, woID, lotoID, next, "")
		return woLotoRecordedMsg{label: label, recorded: next, err: err}
	}
}

// woLotoAnyOrder is the one thing the frame says about SEQUENCE, and it says it
// because the server has no ordering rule at all: complete_loto is a plain
// toggle over one row's flag. An operator is entitled to know the terminal is
// not tracking a sequence on their behalf — and this project must not invent one
// here, so the sentence reports the absence rather than implying a rule.
const woLotoAnyOrder = "Steps may be recorded in any order: nothing here or on the server enforces a sequence."

func (s *WorkOrderDetailScreen) renderLotoConfirm() string {
	rec, ok := s.lotoRow()
	if !ok {
		return s.wrapped(StyleMuted, "That lockout step is no longer on this work order. esc back")
	}
	recording := !s.lotoWas

	// HEAD — pinned, because clampToBox drops from the BOTTOM: whatever is up
	// here is the part a short pane cannot take, and what must survive is the
	// DIRECTION the write goes and WHICH step it is about.
	var head strings.Builder
	title := "Clear lockout record"
	if recording {
		title = "Record lockout step"
	}
	head.WriteString(StyleTitle.Render(title) + "\n")
	for _, line := range s.wrapLines(woLotoSourceLabel(rec)) {
		head.WriteString(line + "\n")
	}
	head.WriteString("\n")

	// BODY — the step in full, folded so nothing is cut, and scrolled so nothing
	// is lost on a pane too short to hold it.
	//
	// The step's own text LEADS. It is what a tech standing at the machine came
	// for, and the sentences about what the keypress means follow it — the legend
	// below carries the short form of those ("y clear the record"), and the legend
	// is pinned where no budget can trim it.
	var body strings.Builder
	writeBlock := func(label, value string) {
		if value == "" {
			return
		}
		body.WriteString(StyleMuted.Render(label) + "\n")
		body.WriteString(s.wrappedIn(lipgloss.NewStyle(), value, "  "))
	}
	writeBlock("Isolate at", rec.IsolationPoint)
	writeBlock("Lockout devices", rec.RequiredDevices)
	writeBlock("Source type", rec.SourceType)
	writeBlock("Recorded note", rec.Notes)
	body.WriteString(StyleMuted.Render("Record") + "\n")
	recStyle := StyleStatusWarn
	if rec.IsCompleted {
		recStyle = StyleStatusOK
	}
	body.WriteString(s.wrappedIn(recStyle, woLotoStateLine(rec), "  "))
	body.WriteString("\n")
	if recording {
		body.WriteString(s.wrapped(StyleStatusWarn,
			"Pressing y says you have isolated this energy source on this job. It stamps "+
				"your name and the time onto the work order's safety record."))
	} else {
		body.WriteString(s.wrapped(StyleStatusWarn,
			"Pressing y takes the record back: the name and time recorded against this "+
				"step are cleared, and the step reads as not isolated again."))
	}
	body.WriteString(s.wrapped(StyleMuted, woLotoAnyOrder))

	// FOOT — the write key, the way out, and (only where the body really moves)
	// the scroll keys.
	//
	// THE BUDGET IS MEASURED AGAINST THE TALLEST FOOT, always including the
	// scroll keys, even on the frames that will not draw them. Naming them costs
	// cells, cells can fold the hint onto another row, another row costs the body
	// a line, and a shorter body can change whether it overflows at all — so a
	// budget measured against the foot actually drawn could oscillate between
	// frames. The tallest is the fixed point (AGENTS.md's actionBarRowsFor rule).
	keys := "y record · n/esc cancel"
	if !recording {
		keys = "y clear the record · n/esc cancel"
	}
	footAt := func(hint string) string {
		var foot strings.Builder
		foot.WriteString("\n")
		if s.lotoErr != "" {
			foot.WriteString(s.wrapped(StyleStatusWarn, "! "+s.lotoErr))
		}
		if s.lotoPending {
			foot.WriteString(StyleMuted.Render("Recording…"))
			return foot.String()
		}
		foot.WriteString(pickerHintAt(hint, s.paneCells()))
		return foot.String()
	}
	rows := screenBodyRows(s.terminalHeight) -
		strings.Count(head.String(), "\n") -
		strings.Count(footAt(keys+" · j/k scroll"), "\n")
	if rows < 1 {
		rows = 1
	}
	if s.lotoScroller == nil {
		s.lotoScroller = NewTextScroller(rows)
	}
	// Set BEFORE the foot is worded: HasOverflow answers off the content, so a
	// foot built first would name the scroll keys off the PREVIOUS frame's
	// content — which on the opening frame is no content at all, so the keys
	// worked and the bar did not name them.
	s.lotoScroller.Set(strings.TrimRight(body.String(), "\n"))
	s.lotoScroller.SetViewHeight(rows)
	if s.lotoScroller.HasOverflow() {
		keys += " · j/k scroll"
	}
	return head.String() + s.lotoScroller.View() + footAt(keys)
}

// woBoxView renders one textinput bounded to the pane it is drawn into, under
// `indent`.
//
// A box with NO Width is the defect this exists to stop, and it is AGENTS.md's
// own: bubbles' handleOverflow returns early at Width 0, so View() emits the
// WHOLE value — past the column where the row fills the pane, every further
// keystroke redrew the row byte for byte with clampToBox having already taken the
// caret. That is the reported hang, reached by typing. It lands here rather than
// in the layer because this screen is not on the columnar one, whose
// jdeFitInputValue would have sized the row for it — and this function is that
// function's shape, for the same two reasons.
//
// SETTING Width IS NOT ENOUGH BY ITSELF. bubbles computes the scrolling window in
// handleOverflow, which runs on SetValue and on a cursor move and NOT in View —
// so a Width assigned after the value was set leaves the window it was computed
// without, and View emits the whole value regardless. Re-seating the cursor on
// its own position is what re-runs it. The model is taken BY VALUE so the caller's
// box keeps its own state and only the drawn string is bounded.
//
// One cell is held back for the cursor sitting past the end of the value, the
// same reservation listSearchInputWidth makes. Floored so a narrow pane leaves a
// box to type in rather than none.
func woBoxView(ti textinput.Model, paneCells int, indent string) string {
	width := paneCells - lipgloss.Width(indent) - 1 - lipgloss.Width(ti.Prompt)
	if width < 8 {
		width = 8
	}
	ti.Width = width
	pos := ti.Position()
	ti.CursorEnd()
	ti.SetCursor(pos)
	// The PLACEHOLDER is the one string on a text row that is not the value, and
	// bubbles draws it whole for an empty box whatever Width says, so it is
	// bounded here rather than trusted.
	return fitCell(ti.View(), paneCells-lipgloss.Width(indent))
}

// --- Pane-local text -------------------------------------------------------

// paneCells is the columns this screen's body really has. Zero — an unsized
// terminal — reads as the 51 an 80-column terminal gives, which is what every
// FOLD in this package falls back to: folding narrow costs an extra line and
// loses nothing, where folding wide overruns the pane and clampToBox takes the
// tail.
func (s *WorkOrderDetailScreen) paneCells() int {
	if w := screenBodyCells(s.terminalWidth); w > 0 {
		return w
	}
	return pickerPaneWidth
}

// wrapLines folds one value onto as many lines as it needs. pickerWrap never
// truncates — it breaks mid-token where a token has nowhere else to break — which
// is the whole reason the LOTO frames use it: a lockout instruction that does not
// fit is a layout problem, not a value to cut.
func (s *WorkOrderDetailScreen) wrapLines(text string) []string {
	return s.wrapIndented(text, "")
}

// wrapIndented folds one value and puts `indent` in front of every line,
// BUDGETING FOR THE INDENT rather than adding it afterwards.
//
// Adding it afterwards is the defect this function exists to remove, and it was
// measured rather than imagined: the review frame's step text was folded at the
// pane's full 51 cells and then written out under a two-space indent, so every
// continuation line was 53 cells into a 51-cell pane and clampToBox took two
// characters off the end of each one — silently, with no ellipsis, in the middle
// of a lockout instruction. A bound applied to one PART of a row that is
// afterwards added to is not a bound (AGENTS.md).
func (s *WorkOrderDetailScreen) wrapIndented(text, indent string) []string {
	lines := pickerWrap(text, s.paneCells()-lipgloss.Width(indent))
	if len(lines) == 0 {
		return []string{indent}
	}
	for i, line := range lines {
		lines[i] = indent + line
	}
	return lines
}

// wrapped renders one folded, styled paragraph and the newline after it, so a
// caller writing prose onto these panes cannot hand clampToBox an over-wide line.
func (s *WorkOrderDetailScreen) wrapped(style lipgloss.Style, text string) string {
	return s.wrappedIn(style, text, "")
}

func (s *WorkOrderDetailScreen) wrappedIn(style lipgloss.Style, text, indent string) string {
	lines := s.wrapIndented(text, indent)
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n") + "\n"
}
