// Work-order / committee associations on a purchase order (op-shb9).
//
// A purchase order — and each of its lines — can record who it was bought for:
// the WORK ORDER whose job the parts complete, and the COMMITTEE (SIG) it was
// placed on behalf of. Both are attribution only: neither moves stock, changes
// a price, nor bills anyone. That is exactly why they are optional everywhere
// and never gate a purchase.
//
// This file holds what the create, edit and detail screens share: the option
// lists (loaded once per screen), the labels a job and a committee read as, the
// picker rows the editing surfaces build from them, and the columnar row the
// detail sheet draws (poAssocValueField). Keeping them here is what stops the
// surfaces from labelling the same job differently or disagreeing on what "no
// association" means.
package tui

import (
	"context"
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// poAssocOption is one selectable row in an association picker. value is the id
// that goes on the wire — a work order's UUID, or a committee's auth.Group pk
// rendered as text — and the empty string is the explicit "no association" row,
// which is always row 0.
type poAssocOption struct {
	value string
	label string
}

// poWorkOrdersLoadedMsg / poCommitteesLoadedMsg carry the option lists. Each is
// reported separately: a shop can have work orders and no committees (or the
// reverse), and one list failing must not take the other picker down with it.
type poWorkOrdersLoadedMsg struct {
	rows []omsapi.WorkOrder
	err  error
}

type poCommitteesLoadedMsg struct {
	rows []omsapi.SIG
	err  error
}

// poAssocOptions is the option state both PO screens embed. Unlike the supplier
// agreement picker (op-yoos), neither list is scoped to the order's supplier, so
// both load once when the screen opens and never need refetching.
//
// A failed load is remembered rather than swallowed: silence would read as
// "there are no jobs to pick", which may be false, and the operator deserves to
// know the difference before concluding an order can't be tagged.
type poAssocOptions struct {
	workOrders    []omsapi.WorkOrder
	workOrderErr  string
	committees    []omsapi.SIG
	committeeErr  string
	workOrderLoad bool
	committeeLoad bool
}

// load kicks off both option fetches. Run in the background from Init: an
// association is optional on every order, so nothing here may gate the flow.
func (o *poAssocOptions) load(deps Deps) tea.Cmd {
	o.workOrderLoad = true
	o.committeeLoad = true
	o.workOrderErr = ""
	o.committeeErr = ""
	return tea.Batch(loadWorkOrderOptionsCmd(deps), loadCommitteeOptionsCmd(deps))
}

// handle absorbs the two loaded messages, reporting whether msg was one of
// them so the embedding screen's Update can return early.
func (o *poAssocOptions) handle(msg tea.Msg) bool {
	switch m := msg.(type) {
	case poWorkOrdersLoadedMsg:
		o.workOrderLoad = false
		// A partial list still comes back with an error (one status failed):
		// keep both, so the picker offers what loaded and still says it is
		// incomplete rather than quietly presenting itself as the whole set.
		o.workOrders = m.rows
		o.workOrderErr = ""
		if m.err != nil {
			o.workOrderErr = m.err.Error()
		}
		return true
	case poCommitteesLoadedMsg:
		o.committeeLoad = false
		o.committees = m.rows
		o.committeeErr = ""
		if m.err != nil {
			o.committeeErr = m.err.Error()
		}
		return true
	}
	return false
}

// workOrderRows / committeeRows build a picker's rows: the explicit "none" row
// first, then whatever loaded — with the currently-attached target grafted in
// when it isn't in the list (see poWithCurrentOption).
func (o *poAssocOptions) workOrderRows(currentValue, currentLabel string) []poAssocOption {
	rows := make([]poAssocOption, 0, len(o.workOrders)+2)
	rows = append(rows, poAssocOption{value: "", label: "— no work order —"})
	for _, wo := range o.workOrders {
		id := fmt.Sprintf("%v", wo.ID)
		rows = append(rows, poAssocOption{value: id, label: poWorkOrderLabel(wo)})
	}
	return poWithCurrentOption(rows, currentValue, currentLabel)
}

func (o *poAssocOptions) committeeRows(currentValue, currentLabel string) []poAssocOption {
	rows := make([]poAssocOption, 0, len(o.committees)+2)
	rows = append(rows, poAssocOption{value: "", label: "— no committee —"})
	for _, sig := range o.committees {
		rows = append(rows, poAssocOption{value: strconv.Itoa(sig.ID), label: sig.Name})
	}
	return poWithCurrentOption(rows, currentValue, currentLabel)
}

// workOrdersOffered / committeesOffered report whether a picker is worth
// showing at all. Nothing to pick means no row and no key — an empty picker is
// a dead end, and the web renders its selects on the same condition.
//
// A FAILED load still counts as "offered": the row then says the list is
// unavailable, which is a different fact from "there are none" and the only one
// of the two that is safe to let the operator assume. This mirrors the
// agreement picker (op-yoos) exactly.
func (o *poAssocOptions) workOrdersOffered() bool {
	return len(o.workOrders) > 0 || o.workOrderErr != ""
}

func (o *poAssocOptions) committeesOffered() bool {
	return len(o.committees) > 0 || o.committeeErr != ""
}

// poWithCurrentOption keeps the currently-attached target selectable even when
// it isn't in the fetched list — the pickers offer only UNFINISHED jobs and the
// viewer's own committees, so an order tagged with a job that has since been
// completed (or with another shop's committee) would otherwise vanish from its
// own picker and be silently detached by an edit that meant to change something
// else. Grafted directly under the "none" row so it reads as the current value.
// Mirrors the web's withCurrentOption guard.
func poWithCurrentOption(rows []poAssocOption, currentValue, currentLabel string) []poAssocOption {
	if currentValue == "" {
		return rows
	}
	for _, r := range rows {
		if r.value == currentValue {
			return rows
		}
	}
	if currentLabel == "" {
		currentLabel = currentValue
	}
	grafted := make([]poAssocOption, 0, len(rows)+1)
	grafted = append(grafted, rows[0])
	grafted = append(grafted, poAssocOption{value: currentValue, label: currentLabel})
	return append(grafted, rows[1:]...)
}

// poAssocLabelFor looks a committed value's label back out of its row set.
// Returns "" for the "none" row so callers can treat "nothing picked" and
// "picked nothing" the same way.
func poAssocLabelFor(rows []poAssocOption, value string) string {
	if value == "" {
		return ""
	}
	for _, r := range rows {
		if r.value == value {
			return r.label
		}
	}
	return ""
}

// poAssocCursorFor parks a freshly-opened picker on the current value, so enter
// is a no-op confirm and the operator can see what the order carries today
// rather than having to remember. Falls back to the "none" row.
func poAssocCursorFor(rows []poAssocOption, currentValue string) int {
	for i, r := range rows {
		if r.value == currentValue {
			return i
		}
	}
	return 0
}

// poWorkOrderLabel names a work order in a picker: its short id plus whatever
// the backend calls it. display_title is the authoritative name (template
// title, else the reported problem, else the asset) and the only one a
// corrective work order has; the remaining fallbacks cover a payload from a
// backend that predates it.
func poWorkOrderLabel(wo omsapi.WorkOrder) string {
	title := wo.DisplayTitle
	if title == "" {
		title = wo.MaintenanceItemTitle
	}
	if title == "" {
		title = wo.AssetName
	}
	short := wo.ShortID
	switch {
	case short != "" && title != "":
		return short + " — " + title
	case short != "":
		return short
	case title != "":
		return title
	}
	return fmt.Sprintf("%v", wo.ID)
}

// poCommitteeID converts a committee picker's value back into the auth.Group pk
// the payload carries. Returns nil for the "none" row — and for anything
// unparseable, which can only come from a value we did not build, and is safer
// read as "no committee" than as an id the backend would reject.
func poCommitteeID(value string) *int {
	if value == "" {
		return nil
	}
	id, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &id
}

// poCommitteeValue renders an attached committee pk as a picker value.
func poCommitteeValue(id *int) string {
	if id == nil {
		return ""
	}
	return strconv.Itoa(*id)
}

// poCommitteeRefLabel names an attached committee, or "" when none is attached.
func poCommitteeRefLabel(ref *omsapi.OwningGroupRef) string {
	if ref == nil {
		return ""
	}
	return ref.Name
}

func loadWorkOrderOptionsCmd(deps Deps) tea.Cmd {
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListActiveWorkOrders(ctx)
		return poWorkOrdersLoadedMsg{rows: rows, err: err}
	}
}

func loadCommitteeOptionsCmd(deps Deps) tea.Cmd {
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListSIGs(ctx, nil)
		if err != nil {
			return poCommitteesLoadedMsg{err: err}
		}
		return poCommitteesLoadedMsg{rows: page.Results}
	}
}

// poAssocValueField is one association as a COLUMNAR row (sc-h412): the reading
// the PO detail sheet hangs off its shared leader column, beside the order's
// identifiers and dates.
//
// It carries the same three states renderAssocValue does — attached, could not
// ask, nothing attached — and for the same reason: a picker that failed to load
// must never read as an order with nothing attached. What it does NOT do is
// pre-style the value. The columnar renderer owns the styling (Dim renders an
// absence muted), because a value arriving with its own colour sequence carries
// its own reset, which would end a focused row's highlight partway across the
// field — the rule po_edit.go's assocRowValue keeps for the very same rows on
// the edit side.
func poAssocValueField(label, attached, loadErr string) jdeField {
	f := jdeField{Label: label, Kind: jdeValue}
	switch {
	case loadErr != "":
		f.Value, f.Dim = "(none) · unavailable — "+loadErr, true
	case attached != "":
		f.Value = attached
	default:
		f.Value, f.Dim = "(none)", true
	}
	return f
}

// renderAssocValue renders one "Label: value" association row for a read-only
// or menu surface. attached is the attached target's label ("" when none);
// loadErr turns the row into an explicit unavailable note, because a picker
// that could not be loaded must never read as an order with nothing attached.
//
// This is the PRE-columnar form, and the only caller left is the create screen
// (po_create.go), which the JD Edwards conversion reaches in the purchasing
// ENTRY slice rather than this one. When it converts, it takes
// poAssocValueField above and this goes with it.
// The VALUE is bounded to whatever the 51-column pane has left after the label,
// because it is OMS-supplied: a work-order title or an unbounded error string
// pushed the row past the cut, and clampToBox takes it silently and mid-word.
// One row, clipped with an ellipsis that says a cut happened — folding would
// spend rows the source chooser does not have at 24, and these rows are the
// first thing it drops when it runs out (sourceAttributionShown).
func renderAssocValue(label, attached, loadErr string) string {
	room := pickerPaneWidth - lipgloss.Width(label) - 2
	if room < 6 {
		room = 6
	}
	switch {
	case loadErr != "":
		return label + ": " + StyleStatusWarn.Render(pickerClip("unavailable — "+loadErr, room))
	case attached != "":
		return label + ": " + StyleStatusOK.Render(pickerClip(attached, room))
	}
	return label + ": " + StyleMuted.Render("(none)")
}
