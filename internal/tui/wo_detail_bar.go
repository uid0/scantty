package tui

import (
	"fmt"
	"strings"
)

// wo_detail_bar.go — the work-order sheet's action bars, as RECORDS
// (prose_bar.go), one per surface the screen draws.
//
// THE SHEET AND ALL FOURTEEN OF ITS MODES WROTE THEIR KEYS AS LITERALS, and the
// literals had drifted from the switches in every direction this program has a
// name for. What was measured, surface by surface, before the conversion:
//
//   - THE SHEET said `j/k scroll` while TextScroller.Handle also moved the body on
//     the arrows, pgup/pgdn, g/G and home/end; and it named `i`/`b`/`c`/`x` only
//     while the job was open, in progress or blocked, while handleViewKey answers
//     them in EVERY status. OMS gates no work-order status change (a PATCH of
//     `status`, whose perform_update reverses the committee charge on the way
//     back out of completed) and the web's own status selector offers every
//     status from every status, so on a completed job `i` reopens it and `c`/`x`
//     open their confirm — named nowhere.
//   - THE FOUR PICK LISTS (tasks, materials, tools, lockout) said `j/k move` and
//     `esc back` while their switches answered the arrows and `q` as well; and
//     the tasks and materials lists drew no keys at all while a toggle was out
//     ("Updating…") although every movement key, `esc`, `p`, `s`, `a` and `c`
//     went on acting under it.
//   - THE FORMS: the add-material and add-tool forms said `tab/arrows move`,
//     which spells neither shift+tab nor the arrows as keys; the photo form said
//     `tab next field` beside a shift+tab that also moves; the checklist said
//     `tab/↑↓ move` beside a shift+tab that also moves; and every form replaced
//     its keys with a working line while its write was out, while `esc` still
//     left it.
//   - THE STOCK-ITEM PICKER said `j/k move` beside the arrows.
//   - THE LOCKOUT REVIEW FRAME said `j/k scroll` beside the whole scroll
//     vocabulary, and `n/esc cancel` beside `q`, `N` and `Y`.
//
// Per surface, the answer is one of two and never a third. A surface whose
// prompt named exactly what its switch answers keeps that prompt and answers nil
// — the finalize/cancel CONFIRM, whose `y confirm · n/esc cancel` is the y/n
// idiom every confirm in this package spells, is the one. Every other surface is
// a record here, drawn by View as the last thing on its pane and pressed by the
// honesty sweep.
//
// THE KEYS ARE ADDED UNDER THE PREDICATE THEIR ARM READS and nowhere else, the
// shape the item sheet's bar argues for: a segment inserted under its own
// condition cannot outlive the condition, where one stripped after the fact
// already has, once, on this program's other long sheet.

// Segments this screen spells on more than one surface.
var (
	woBarBack      = proseBarItem{Keys: []string{"esc", "q"}, Hint: "esc/q back"}
	woBarEscBack   = proseBarItem{Keys: []string{"esc"}, Hint: "esc back"}
	woBarEscCancel = proseBarItem{Keys: []string{"esc"}, Hint: "esc cancel"}
	woBarSubmit    = proseBarItem{Keys: []string{"enter"}, Hint: "enter submit"}
	woBarSave      = proseBarItem{Keys: []string{"enter"}, Hint: "enter save"}
	woBarAdd       = proseBarItem{Keys: []string{"enter"}, Hint: "enter add"}
	// woBarGone is the way off a form whose row a reload removed: `esc` leaves,
	// and `enter` — the submit — finds the row gone, says so and leaves too.
	woBarGone = proseBarItem{Keys: []string{"enter", "esc"}, Hint: "enter/esc back"}
	// woBarToggle is `space`/`enter` on the two lists whose row is a checkbox.
	woBarToggle = proseBarItem{Keys: []string{" ", "enter"}, Hint: "space/enter toggle"}
)

// loadFrameDrawn reports whether View is drawing a load frame — a first load, a
// refresh, a failure, or a work order that came back as nothing — rather than
// the sheet or one of its modes.
//
// A REFRESH DRAWS THE LOAD FRAME NOW, which it did not: `r` used to leave the
// sheet on the pane with nothing saying a reload was out, and the keys under it
// went on acting against a work order about to be replaced. Every other sheet
// this record serves draws its load frame for a refresh, and the load-state
// sweep holds the bar there (prose_bar_load_states_test.go).
//
// It is the ONE predicate View, proseBar, the key gate and WantsRawInput read. A
// reload that fails under an open MODE — every write on this screen fires one —
// leaves the mode set and the work order gone, so a mode handler reached from
// that frame would index a nil work order; the gate routes the frame's named
// keys to the sheet's own handler instead, and WantsRawInput lets `esc` reach
// Root's back-step, which is what the frame's bar says it does.
func (s *WorkOrderDetailScreen) loadFrameDrawn() bool {
	return s.loading || s.loadErr != "" || s.wo == nil
}

// proseBar is the bar this screen is DRAWING, for whichever surface is up — nil
// on the confirm, which names its own keys.
func (s *WorkOrderDetailScreen) proseBar() proseBar {
	if s.loadFrameDrawn() {
		return s.loadBar()
	}
	switch s.mode {
	case woModeTasks:
		return s.tasksBar(len(s.wo.TaskCompletions), s.actionPending, s.timerPending)
	case woModeMaterials:
		return s.materialsBar(len(s.wo.MaterialUsage), s.actionPending)
	case woModeAddMaterial:
		if s.amPicking {
			return s.itemPickBar(len(s.amPickRows))
		}
		return s.addMaterialBar()
	case woModeMaterialCost:
		if _, ok := s.currentMaterial(); !ok {
			return proseBar{woBarGone}
		}
		return proseBarIdle(!s.costPending, woBarSave, woBarEscCancel)
	case woModePhoto:
		return proseBarIdle(!s.photoPending, woBarSubmit, woBarEscCancel,
			proseBarItem{Keys: []string{"tab", "shift+tab"}, Hint: "tab/shift+tab field"})
	case woModePdf:
		return proseBarIdle(!s.pdfPending, woBarSubmit, woBarEscCancel)
	case woModeChecklist:
		var out proseBar
		if s.checklistFocus < 3 {
			out = append(out, proseBarItem{Keys: []string{" "}, Hint: "space toggle"})
		}
		out = append(out, proseBarFieldFocus)
		return append(out, proseBarIdle(!s.checklistPending, woBarSubmit, woBarEscCancel)...)
	case woModeConfirm:
		return nil
	case woModeNotes:
		return proseBarIdle(!s.notesPending, woBarSave, woBarEscCancel)
	case woModeTools:
		return s.toolsBar(len(s.woToolRows()), s.toolPending)
	case woModeAddTool:
		var out proseBar
		if s.atCursor == woToolRequired {
			out = append(out, proseBarItem{Keys: []string{" "}, Hint: "space toggle"})
		}
		out = append(out, proseBarFieldFocus)
		return append(out, proseBarIdle(!s.atPending, woBarAdd, woBarEscBack)...)
	case woModeToolLocation:
		if _, ok := s.locRow(); !ok {
			return proseBar{woBarGone}
		}
		return proseBarIdle(!s.locPending, woBarSave, woBarEscCancel)
	case woModeLoto:
		return s.lotoListBar(len(s.woLotoRows()))
	case woModeLotoConfirm:
		if _, ok := s.lotoRow(); !ok {
			return s.lotoConfirmBar(false)
		}
		s.layoutLotoConfirm()
		return s.lotoConfirmBar(s.lotoScroller.HasOverflow())
	}
	return proseScrollBarUnder(s.scroller, s.terminalHeight, s.paneCells(), s.sheetChrome(), s.sheetBar)
}

// proseBarIdle is `submit` and `cancel` on a form, with the submit taken off
// while the form's write is out — its arm answers nothing then, and the working
// line above the bar says why — and any segments that act either way (a focus
// key) ahead of them.
func proseBarIdle(idle bool, submit, cancel proseBarItem, always ...proseBarItem) proseBar {
	out := append(proseBar{}, always...)
	if idle {
		out = append(out, submit)
	}
	return append(out, cancel)
}

// sheetChrome is the rows the sheet spends between its body and its bar: the
// answer to the last key and the blank under it, where there is one.
func (s *WorkOrderDetailScreen) sheetChrome() int {
	if s.actionMsg != "" {
		return 2
	}
	return 0
}

// sheetBar names every key that acts on the read-only sheet.
//
// THE STATUS KEYS ARE NAMED IN EVERY STATUS because handleViewKey answers them in
// every status — see this file's header for what OMS and the web do with a
// status change out of a finished job. Gating the arms to the statuses the old
// literal named would change what the screen DOES, and is recorded as a
// candidate rather than made here.
//
// `s` follows toggleWOTimer: it declines with a toast on a status whose clock is
// closed (a decline that says why, and correctly unnamed), and does nothing at
// all while a toggle is out. `t`, `L` and `R` follow their own arms' predicates
// for the reason each arm's comment gives — a key that opened a list of nothing
// would be the bar naming a key that cannot act.
func (s *WorkOrderDetailScreen) sheetBar(scrolls bool) proseBar {
	out := proseNavScroll(scrolls)
	out = append(out,
		proseBarItem{Keys: []string{"i"}, Hint: "i in-progress"},
		proseBarItem{Keys: []string{"b"}, Hint: "b block"},
		proseBarItem{Keys: []string{"c"}, Hint: "c complete"},
		proseBarItem{Keys: []string{"x"}, Hint: "x cancel"})
	if item, ok := s.timerItem(); ok {
		out = append(out, item)
	}
	if len(s.wo.TaskCompletions) > 0 {
		out = append(out, proseBarItem{Keys: []string{"t"}, Hint: "t tasks"})
	}
	// Always offered: an empty list is the corrective case, where adding the
	// first line is exactly what the operator came here to do. The same is true
	// of the per-job tool rows beside them.
	out = append(out,
		proseBarItem{Keys: []string{"M"}, Hint: "M materials"},
		proseBarItem{Keys: []string{"T"}, Hint: "T tools"})
	// The count rides along because "1/3 isolated" is the fact a tech at the
	// machine came for.
	if loto := s.woLotoRows(); len(loto) > 0 {
		out = append(out, proseBarItem{Keys: []string{"L"},
			Hint: fmt.Sprintf("L lockout (%d/%d)", s.lotoIsolated(), len(loto))})
	}
	if len(s.woPendingReview()) > 0 {
		out = append(out, proseBarItem{Keys: []string{"R"}, Hint: "R review scan"})
	}
	return append(out,
		proseBarItem{Keys: []string{"A"}, Hint: "A attachments"},
		proseBarItem{Keys: []string{"p"}, Hint: "p photo"},
		proseBarItem{Keys: []string{"U"}, Hint: "U upload-pdf"},
		proseBarItem{Keys: []string{"v"}, Hint: "v validate"},
		proseBarItem{Keys: []string{"E"}, Hint: "E notes"},
		proseBarRefresh, proseBarEsc)
}

// timerItem is `s` on the sheet, worded for the action it will perform so an
// operator can tell a running clock from a stopped one — or nothing, where the
// key cannot start or pause anything.
func (s *WorkOrderDetailScreen) timerItem() (proseBarItem, bool) {
	if s.wo == nil || s.timerPending || !woTimerAllowed(s.wo.Status) {
		return proseBarItem{}, false
	}
	if s.wo.IsTiming {
		return proseBarItem{Keys: []string{"s"}, Hint: "s pause"}, true
	}
	return proseBarItem{Keys: []string{"s"}, Hint: "s start"}, true
}

// loadBar is the screen's bar while a load is out, has failed, or brought back
// no work order — what its key switch still answers with no sheet drawn
// (prose_bar.go carries the defect and the decision).
//
// `i` and `b` are named in every one of those frames because handleViewKey
// fires them against the work order's ID whether or not a work order has been
// drawn, and `A` because the attachments list is reached by that ID alone:
// named because they act, and candidates for gating. A refresh keeps the work
// order, so `s` still toggles its clock and `R` still opens a parked sheet's
// review. Every other key opens a mode this frame does not draw, or walks a body
// it does not draw, and is ignored.
func (s *WorkOrderDetailScreen) loadBar() proseBar {
	out := proseBar{
		{Keys: []string{"i"}, Hint: "i in-progress"},
		{Keys: []string{"b"}, Hint: "b block"},
	}
	if s.wo != nil {
		if item, ok := s.timerItem(); ok {
			out = append(out, item)
		}
		if len(s.woPendingReview()) > 0 {
			out = append(out, proseBarItem{Keys: []string{"R"}, Hint: "R review scan"})
		}
	}
	return append(out,
		proseBarItem{Keys: []string{"A"}, Hint: "A attachments"},
		proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

// tasksBar is the task picker's bar for a list of `n` steps. A toggle out takes
// the toggle off and a clock toggle out takes `s` off — each arm answers nothing
// then — while movement, `p` and the way back go on acting under the working
// line that says why.
func (s *WorkOrderDetailScreen) tasksBar(n int, actionPending, timerPending bool) proseBar {
	out := proseNavStep(n > 1)
	if n > 0 && !actionPending {
		out = append(out, woBarToggle)
	}
	if n > 0 && !timerPending && woTimerAllowed(s.wo.Status) {
		out = append(out, proseBarItem{Keys: []string{"s"}, Hint: "s timer"})
	}
	if n > 0 {
		out = append(out, proseBarItem{Keys: []string{"p"}, Hint: "p evidence photo"})
	}
	return append(out, woBarBack)
}

// materialsBar is the material picker's bar for a list of `n` lines. `a` acts
// on an empty list — it is how a corrective job gets its first line — and every
// other row key needs a row. `c` and `d` stay named on a line whose stock is
// applied or which the PM template owns: their arms decline there and say why.
func (s *WorkOrderDetailScreen) materialsBar(n int, actionPending bool) proseBar {
	out := proseNavStep(n > 1)
	if n > 0 && !actionPending {
		out = append(out, woBarToggle)
	}
	out = append(out, proseBarItem{Keys: []string{"a"}, Hint: "a add"})
	if n > 0 {
		out = append(out, proseBarItem{Keys: []string{"c"}, Hint: "c cost"})
	}
	if n > 0 && !actionPending {
		out = append(out, proseBarItem{Keys: []string{"d"}, Hint: "d remove"})
	}
	return append(out, woBarBack)
}

// addMaterialBar is the add-material form's bar. The focus WRAPS
// (moveAddMaterialCursor), so all four focus keystrokes act from every field.
func (s *WorkOrderDetailScreen) addMaterialBar() proseBar {
	var out proseBar
	if s.amCursor == woMatItem {
		out = append(out, proseBarItem{Keys: []string{" "}, Hint: "space pick stock item"})
	}
	out = append(out, proseBarFieldFocus)
	return append(out, proseBarIdle(!s.amPending, woBarAdd, woBarEscBack)...)
}

// itemPickBar is the stock-item picker's bar for `n` rows — the "(none)" row
// included — or, while its filter box is focused, the one key pair that closes
// the box: every other key goes into the filter.
func (s *WorkOrderDetailScreen) itemPickBar(n int) proseBar {
	if s.amPickTyping {
		return proseBar{{Keys: []string{"enter", "esc"}, Hint: "enter/esc stop filtering"}}
	}
	return append(proseNavStep(n > 1),
		proseBarItem{Keys: []string{"/"}, Hint: "/ filter"},
		proseBarItem{Keys: []string{"enter"}, Hint: "enter select"},
		woBarEscBack)
}

// toolsBar is the tool list's bar for `n` rows, beside the materials list's and
// keyed the same way.
func (s *WorkOrderDetailScreen) toolsBar(n int, toolPending bool) proseBar {
	out := append(proseNavStep(n > 1), proseBarItem{Keys: []string{"a"}, Hint: "a add"})
	if n > 0 {
		out = append(out, proseBarItem{Keys: []string{"l"}, Hint: "l location"})
	}
	if n > 0 && !toolPending {
		out = append(out, proseBarItem{Keys: []string{"d"}, Hint: "d remove"})
	}
	return append(out, woBarBack)
}

// lotoListBar is the lockout list's bar for `n` steps. `space` is NOT named: it
// is bound only to decline and say so (woLotoSpaceNote), which is a key that
// has not acted.
func (s *WorkOrderDetailScreen) lotoListBar(n int) proseBar {
	out := proseNavStep(n > 1)
	if n > 0 {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter review the step"})
	}
	return append(out, woBarBack)
}

// lotoConfirmBar is the lockout review frame's bar.
//
// THE WRITE AND THE WAY OUT ARE SPELLED IN BOTH CASES because the switch answers
// both, and on this frame an unnamed key is not merely untidy: `Y` records a
// lockout step. Every key it does not name declines and says so (lotoErr), so
// the frame answers every press. The scroll keys come from proseNavScroll, asked
// of the scroller renderLotoConfirm sizes, and the write comes off while one is
// out — `y` answers nothing then.
func (s *WorkOrderDetailScreen) lotoConfirmBar(scrolls bool) proseBar {
	if _, ok := s.lotoRow(); !ok {
		return proseBar{{Keys: []string{"y", "Y", "n", "N", "esc", "q"}, Hint: "y/Y/n/N/esc/q back"}}
	}
	// The write and the way out LEAD, as they did on the literal: they are what
	// this frame is for, and leading they also fold the bar onto one row fewer
	// at 80 columns than trailing the scroll segments did — a row the body, which
	// is the step's own text, gets back.
	var out proseBar
	if !s.lotoPending {
		hint := "y/Y record"
		if s.lotoWas {
			hint = "y/Y clear the record"
		}
		out = append(out, proseBarItem{Keys: []string{"y", "Y"}, Hint: hint})
	}
	out = append(out, proseBarItem{Keys: []string{"n", "N", "esc", "q"}, Hint: "n/N/esc/q cancel"})
	return append(out, proseNavScroll(scrolls)...)
}

// woListFrame is one of the four pick lists' panes (and the stock-item
// picker's): `head` — whole lines, each ending in a newline — a line-packed
// WINDOW of `rows` around the cursor, and a foot of `above` — the working line,
// the note, the totals — over the bar (proseFlatListFrameFoot).
//
// THOSE LISTS DREW EVERY ROW AND THEN THEIR KEYS, with no window. A task row
// carries its clock, its reference photo and every evidence photo filed against
// it; a material row its quantities and its cost; the stock picker the shop's
// whole catalogue. A list longer than the pane pushed the keys off the bottom
// whatever they said, which is the prose-footer defect with the footer not even
// a literal the operator could have read.
//
// The foot is budgeted against the CEILING bar — the list's bar with a second
// row and nothing out — for the reason proseListWindow gives, and a row taller
// than the window is clipped to it with the lines left out named.
func (s *WorkOrderDetailScreen) woListFrame(head string, rows []string, cursor int, start *int, above []string, ceiling, drawn proseBar) string {
	cells := s.paneCells()
	footRows := ceiling.rows(cells)
	foot := drawn.render(cells)
	if len(above) > 0 {
		lines := strings.Join(above, "\n")
		footRows += strings.Count(lines, "\n") + 2
		foot = lines + "\n\n" + foot
	}
	return proseFlatListFrameFoot(head, rows, cursor, start, s.terminalHeight, footRows, foot)
}

// woFormFrame is a form's pane: the form, then the working line where its write
// is out, then the bar. The form is drawn WHILE the write is out — it used to be
// replaced by the working line, and the keys that still act under it (`esc`,
// the focus keys, typing) went on changing a form nobody could see.
func (s *WorkOrderDetailScreen) woFormFrame(form string, pending bool, working string) string {
	out := strings.TrimRight(form, "\n")
	if pending {
		out += "\n\n" + StyleMuted.Render(working)
	}
	return out + "\n\n" + s.proseBar().render(s.paneCells())
}

// woErrLine is a form's refusal on ONE row, with the cut marked: these errors
// carry an OMS body, which omsapi.parseError fills with the entire raw payload
// whenever the envelope carries no code, and a gateway page written out whole
// pushed the form's own bar off the pane (proseFormLine).
func (s *WorkOrderDetailScreen) woErrLine(msg string) string {
	return StyleStatusError.Render(proseFormLine("✗ "+msg, s.paneCells()))
}
