// Shared pieces of the STORAGE family of columnar forms — the slot sheet, the
// rack generator, the staff assignment and the project-storage intake
// (sc-6qsk, sweep D of the sc-h412 redesign).
//
// Everything generic lives in jde_form.go; what is here is what the layer
// deliberately does not know about — this family's own vocabulary.
package tui

// storageLabelWidth is the ONE label column shared by all four storage forms,
// so walking the racking never shifts the sheet sideways under the operator.
// These screens are reached from one another: the slots list opens the slot
// sheet and the rack generator, the slot detail opens the assign form AND the
// stint occupying the slot, and the stint intake claims a slot back. That is
// exactly when a column computed per-sheet would read as the form jumping.
//
// It is computed from the label maps themselves rather than pinned to a number,
// so adding or renaming a field can never silently break the alignment — and
// jdeLabelWidth's own cap still applies, so one verbose label cannot shove
// every input area off a narrow terminal.
//
// The location and maker-box sheets are NOT in it: neither is reached from the
// racking (Locations hangs off inventory, Maker boxes off the bin list), and
// folding maker box's "Conversion completed at" in would widen every storage
// row by six columns to line up with a sheet the operator never sees beside it.
var storageLabelWidth = jdeLabelWidth(
	jdeLabelFields(storageSlotFieldLabel),
	jdeLabelFields(storageGenFieldLabel),
	jdeLabelFields(storageAssignFieldLabel),
	jdeLabelFields(projectStorageFieldLabel),
)

// storageSlotStatusLine is the row above the bar on the storage sheets: what is
// in flight or what went wrong, and otherwise the standing note that field
// carries. A screen renders it on every frame, blank included, so the bar
// underneath never moves between frames (jdeScreen.statusRow's contract).
//
// It takes the pane rather than a bare string because all four of these rows are
// the SAME row, and the row cannot fold: the warning and the note are bounded by
// jdeScreen.fitStatus exactly as the verb and the error are, so clampToBox
// cannot cut a styled line and take its closing SGR reset with it.
//
// Each branch hands fitStatus the mark it is really going to draw — the warning
// its "! ", the note nothing at all — so the bound reserves what is spent and no
// more. Reserving the mark's two columns on the note as well cut it at 49 on an
// 80-column pane with 51 to give, which is the rule this row exists for pointed
// the wrong way.
func storageSlotStatusLine(g jdeScreen, saving bool, verb, errMsg, warn, note string) string {
	if line := g.statusRow(saving, verb, errMsg); line != "" {
		return line
	}
	if warn != "" {
		return StyleStatusWarn.Render(g.fitStatus(jdeStatusWarnMark, warn))
	}
	return StyleMuted.Render(g.fitStatus("", note))
}
