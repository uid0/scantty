// Shared render/help helpers for the electrical CRUD forms
// (electrical_forms.go, electrical_leaf_forms.go). Factored here so the five
// power-topology forms render selects, toggles and their shared label column
// identically without five copies of the same code.
//
// The five forms now render through the columnar "JD Edwards" layer
// (jde_form.go, sc-h412/sc-dnhx/sc-0zvi); what is left here is what the layer
// deliberately does not know about — the electrical family's own vocabulary.
// The old elecPickView / elecMultiPickView drew the pre-redesign picker
// sub-phase and went with it: jdePickList is the picker now (sc-ye0i).
//
// elecLabelFields and elecClampPick used to live here too. Neither knew
// anything about electricity — sweep D needed both for the storage family, so
// they moved into the layer as jdeLabelFields / jdeClampPick rather than being
// copied a second time (sc-6qsk).
//
// elecVisibleRows and elecFieldHelp are gone as of sc-lmsi (sweep E): both sized
// and captioned the PRE-redesign body, and the last screens still calling them —
// the webhook and site-settings sheets — are on the layer now. A columnar screen
// gets its height from jdeScreen and its captions from the action bar.
package tui

// elecLabelWidth is the ONE label column shared by all five electrical forms,
// so walking panel → breaker → circuit → outlet → disconnect never shifts the
// sheet sideways under the operator. These five screens are reached from one
// another (a panel drills to its breakers, a breaker to its circuits, a circuit
// to its outlets and disconnects), which is exactly when a column that moved
// between sheets would read as the form jumping.
//
// It is computed from the label maps themselves rather than pinned to a
// number, so adding or renaming a field can never silently break the alignment
// — and jdeLabelWidth's own cap still applies, so one verbose label cannot
// shove every input area off a narrow terminal.
var elecLabelWidth = jdeLabelWidth(
	jdeLabelFields(panelFieldLabel),
	jdeLabelFields(breakerFieldLabel),
	jdeLabelFields(circuitFieldLabel),
	jdeLabelFields(outletFieldLabel),
	jdeLabelFields(disconnectFieldLabel),
)

// indexOfField returns the slice index of field id in a visible-field list, or
// -1. Used by the breaker form's rebuildFields to keep the cursor anchored on
// the same field across a conditional show/hide.
func indexOfField(fields []int, id int) int {
	for i, f := range fields {
		if f == id {
			return i
		}
	}
	return -1
}

func elecToggleLabel(on bool) string {
	if on {
		return StyleStatusOK.Render("[x] yes")
	}
	return StyleMuted.Render("[ ] no")
}

func elecSelectLabel(opts []selectOption, idx int) string {
	if idx >= 0 && idx < len(opts) {
		return "‹ " + opts[idx].label + " ›"
	}
	return ""
}
