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
// underneath never moves between frames (jdeStatusLine's contract).
func storageSlotStatusLine(saving bool, verb, errMsg, warn, note string) string {
	if line := jdeStatusLine(saving, verb, errMsg); line != "" {
		return line
	}
	if warn != "" {
		return StyleStatusWarn.Render("! " + warn)
	}
	return StyleMuted.Render(note)
}
