package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// SerializedComponentsScreen lists the serial-numbered units (SerializedComponent)
// for one filter scope — either all units of an inventory item ("instances")
// or all units installed in an asset ("components") — and drives their
// lifecycle actions inline. The highlighted unit's server-computed
// available_actions decide which of receive/install/remove/consume/retire/
// dispose are legal; illegal keys flash a warning rather than round-tripping.
// `h`/`enter` opens that unit's usage history.
type SerializedComponentsScreen struct {
	deps   Deps
	title  string
	scope  string // "item" or "asset" — drives the empty-state copy + hint
	itemID string // owning item (item scope only) — the create-form's `item`
	// stock is the item's serialized unit split (available / on-hand / installed)
	// shown in the header; nil in the asset scope, on an older backend, or when
	// the launcher didn't have it, in which case the header line is omitted.
	stock  *omsapi.SerializedStock
	filter url.Values
	backTo Workspace

	rows           []omsapi.SerializedComponent
	cursor         int
	windowStart    int
	loading        bool
	loadErr        string
	terminalHeight int
	terminalWidth  int

	// Inline action-input form (install → asset UUID, dispose → reason).
	form    serialFormKind
	input   textinput.Model
	pending bool
	result  string
	level   StatusLevel

	// Inline add-unit form (item scope): serial_number + lot.
	createInputs []textinput.Model
	createFocus  int

	// History overlay for the highlighted unit.
	showHistory     bool
	historyFor      string
	historyLoading  bool
	historyErr      string
	historyScroller *TextScroller
}

type serialFormKind int

const (
	serialFormNone serialFormKind = iota
	serialFormInstall
	serialFormDispose
	serialFormCreate
)

// Create-form field indices (item scope only): serial_number is required, lot
// and expiration date are optional — the full writable field set of the web
// "Add unit" form.
const (
	scfSerial = iota
	scfLot
	scfExpiration
	scfCount
)

type serialComponentsLoadedMsg struct {
	rows []omsapi.SerializedComponent
	err  error
}

type serialActionDoneMsg struct {
	action string
	res    *omsapi.SerializedComponentActionResult
	err    error
}

type serialHistoryLoadedMsg struct {
	forID  string
	events []omsapi.ComponentUsageEvent
	err    error
}

type serialCreateDoneMsg struct {
	unit *omsapi.SerializedComponent
	err  error
}

// NewItemInstancesScreen lists every serial-numbered unit of one inventory
// item (all statuses) so an operator can install / consume / retire / dispose
// individual units and read their provenance + history. stock is the item's
// available/on-hand/installed split for the header (op-0cd2); pass nil when the
// launcher doesn't have it (the header line is then omitted).
func NewItemInstancesScreen(deps Deps, itemID, itemName string, stock *omsapi.SerializedStock) *SerializedComponentsScreen {
	title := "Instances"
	if itemName != "" {
		title = "Instances: " + itemName
	}
	return &SerializedComponentsScreen{
		deps:    deps,
		title:   title,
		scope:   "item",
		itemID:  itemID,
		stock:   stock,
		filter:  url.Values{"item": []string{itemID}},
		backTo:  WSInventory,
		loading: true,
	}
}

// NewAssetComponentsScreen lists the serial-numbered units currently installed
// in one asset, for remove / consume / retire / dispose against the physical
// machine in front of the operator.
func NewAssetComponentsScreen(deps Deps, assetID, assetName string) *SerializedComponentsScreen {
	title := "Components"
	if assetName != "" {
		title = "Components: " + assetName
	}
	return &SerializedComponentsScreen{
		deps:    deps,
		title:   title,
		scope:   "asset",
		filter:  url.Values{"installed_in_asset": []string{assetID}},
		backTo:  WSAssets,
		loading: true,
	}
}

func (s *SerializedComponentsScreen) Title() string { return s.title }

// WantsRawInput claims every key while a text form or the history overlay is
// open, so typed serials/UUIDs reach the input and esc closes the sub-view
// (rather than the global esc bouncing to Welcome).
func (s *SerializedComponentsScreen) WantsRawInput() bool {
	return s.form != serialFormNone || s.showHistory
}

// HandlesKey claims 'a' (add unit) in the item-instances scope so the global
// 'a' (authorizations) doesn't shadow it — the sc-k7p LocalKeyScreen pattern.
// It's only consulted in the list view (a form/history open flips WantsRawInput
// true, which routes every key here first); the asset scope has no create form,
// so 'a' falls through to the global nav there.
func (s *SerializedComponentsScreen) HandlesKey(key string) bool {
	return key == "a" && s.scope == "item"
}

func (s *SerializedComponentsScreen) Init() tea.Cmd { return s.load() }

func (s *SerializedComponentsScreen) load() tea.Cmd {
	deps := s.deps
	filter := s.filter
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListSerializedComponents(ctx, filter)
		return serialComponentsLoadedMsg{rows: rows, err: err}
	}
}

func (s *SerializedComponentsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		if s.historyScroller != nil {
			proseSizeScroller(s.historyScroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.historyBar)
		}
		return s, nil

	case serialComponentsLoadedMsg:
		s.loading = false
		s.rows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		if s.cursor >= len(s.rows) {
			s.cursor = len(s.rows) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
		return s, nil

	case serialActionDoneMsg:
		s.pending = false
		s.form = serialFormNone
		if m.err != nil {
			s.result = m.action + " failed: " + m.err.Error()
			s.level = StatusError
			return s, Status(s.result, StatusError)
		}
		s.result = actionPastTense(m.action)
		if m.res != nil && m.res.SerialNumber != "" {
			s.result += " " + m.res.SerialNumber
		}
		s.level = StatusOK
		// Reload so statuses + available_actions reflect the transition.
		s.loading = len(s.rows) == 0
		return s, tea.Batch(Status(s.result, StatusOK), s.load())

	case serialCreateDoneMsg:
		s.pending = false
		if m.err != nil {
			// Keep the form open so the operator can fix + resubmit.
			s.result = "add failed: " + m.err.Error()
			s.level = StatusError
			return s, Status(s.result, StatusError)
		}
		s.form = serialFormNone
		serial := ""
		if m.unit != nil {
			serial = m.unit.SerialNumber
		}
		s.result = strings.TrimSpace("added " + serial)
		s.level = StatusOK
		// Reload so the new (received) unit appears with its available_actions.
		s.loading = len(s.rows) == 0
		return s, tea.Batch(Status(s.result, StatusOK), s.load())

	case serialHistoryLoadedMsg:
		if m.forID != s.historyFor {
			return s, nil // stale — operator moved on
		}
		s.historyLoading = false
		if m.err != nil {
			s.historyErr = m.err.Error()
			return s, nil
		}
		s.historyErr = ""
		s.historyScroller = NewTextScroller(defaultDetailHeight)
		s.historyScroller.Set(s.renderHistory(m.events))
		return s, nil

	case tea.KeyMsg:
		return s.handleKey(m)
	}
	return s, nil
}

func (s *SerializedComponentsScreen) handleKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	// History overlay: scroll + esc back to the list.
	if s.showHistory {
		switch m.Type {
		case tea.KeyEsc:
			s.showHistory = false
			s.historyScroller = nil
			return s, nil
		}
		if s.historyScroller != nil && s.historyScroller.Handle(m) {
			return s, nil
		}
		if m.String() == "r" {
			return s, s.openHistory()
		}
		return s, nil
	}

	// Two-field add-unit form (serial + lot) — its own field navigation.
	if s.form == serialFormCreate {
		return s.handleCreateKey(m)
	}

	// Text-input form (install asset / dispose reason).
	if s.form != serialFormNone {
		switch m.Type {
		case tea.KeyEsc:
			s.form = serialFormNone
			s.result = ""
			return s, nil
		case tea.KeyEnter:
			if s.pending {
				return s, nil
			}
			return s.submitForm()
		}
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(m)
		return s, cmd
	}

	// List navigation + action triggers.
	switch m.String() {
	case "j", "down":
		if s.cursor < len(s.rows)-1 {
			s.cursor++
			s.scrollIntoView()
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
			s.scrollIntoView()
		}
	case "g", "home":
		s.cursor = 0
		s.scrollIntoView()
	case "G", "end":
		s.cursor = len(s.rows) - 1
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "h", "enter":
		if len(s.rows) > 0 {
			return s, s.openHistory()
		}
	case "a":
		// Add a new serial-numbered unit — item scope only (an asset view has
		// no single-item context to create against, matching the web, where the
		// Add-unit form lives on the item panel, not the asset section).
		if s.scope != "item" {
			return s, nil
		}
		s.openCreateForm()
		return s, textinput.Blink
	case "v":
		return s.triggerAction(omsapi.SerialActionReceive)
	case "i":
		return s.triggerAction(omsapi.SerialActionInstall)
	case "x":
		return s.triggerAction(omsapi.SerialActionRemove)
	case "c":
		return s.triggerAction(omsapi.SerialActionConsume)
	case "t":
		return s.triggerAction(omsapi.SerialActionRetire)
	case "d":
		return s.triggerAction(omsapi.SerialActionDispose)
	}
	return s, nil
}

// triggerAction validates the action against the highlighted unit's
// available_actions, then either fires it immediately (receive/remove/consume/
// retire) or opens the input form for the ones that need an argument
// (install → asset, dispose → reason).
func (s *SerializedComponentsScreen) triggerAction(action string) (Screen, tea.Cmd) {
	unit := s.current()
	if unit == nil {
		return s, nil
	}
	if !containsStr(unit.AvailableActions, action) {
		s.result = fmt.Sprintf("cannot %s a unit that is %s", action, statusLabel(unit))
		s.level = StatusWarn
		return s, Status(s.result, StatusWarn)
	}
	switch action {
	case omsapi.SerialActionInstall:
		s.openForm(serialFormInstall, "asset UUID to install into")
		return s, textinput.Blink
	case omsapi.SerialActionDispose:
		s.openForm(serialFormDispose, "disposal reason")
		return s, textinput.Blink
	default:
		s.pending = true
		s.result = ""
		return s, s.fireAction(action, omsapi.SerializedComponentAction{})
	}
}

func (s *SerializedComponentsScreen) openForm(kind serialFormKind, placeholder string) {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.CharLimit = 500
	ti.Focus()
	s.input = ti
	s.form = kind
	s.result = ""
}

func (s *SerializedComponentsScreen) submitForm() (Screen, tea.Cmd) {
	val := strings.TrimSpace(s.input.Value())
	switch s.form {
	case serialFormInstall:
		if val == "" {
			s.result = "asset UUID required"
			s.level = StatusError
			return s, nil
		}
		s.pending = true
		return s, s.fireAction(omsapi.SerialActionInstall, omsapi.SerializedComponentAction{Asset: val})
	case serialFormDispose:
		if val == "" {
			s.result = "disposal reason required"
			s.level = StatusError
			return s, nil
		}
		s.pending = true
		return s, s.fireAction(omsapi.SerialActionDispose, omsapi.SerializedComponentAction{DisposalReason: val})
	}
	return s, nil
}

func (s *SerializedComponentsScreen) fireAction(action string, req omsapi.SerializedComponentAction) tea.Cmd {
	unit := s.current()
	if unit == nil {
		return nil
	}
	id := unit.ID
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		res, err := deps.OMS.SerializedComponentAction(ctx, id, action, req)
		return serialActionDoneMsg{action: action, res: res, err: err}
	}
}

// openCreateForm builds the two-field add-unit form (serial_number + lot),
// mirroring the web SerializedComponentsPanel Add-unit form. `item` is the
// screen's owning item; status starts "received" server-side, so the created
// unit surfaces a `receive` action the operator can then fire.
func (s *SerializedComponentsScreen) openCreateForm() {
	s.createInputs = make([]textinput.Model, scfCount)

	serial := textinput.New()
	serial.Prompt = ""
	serial.Placeholder = "SN-000123"
	serial.CharLimit = 100
	serial.Focus()
	s.createInputs[scfSerial] = serial

	lot := textinput.New()
	lot.Prompt = ""
	lot.Placeholder = "batch / lot (optional)"
	lot.CharLimit = 100
	s.createInputs[scfLot] = lot

	exp := textinput.New()
	exp.Prompt = ""
	exp.Placeholder = "YYYY-MM-DD (optional)"
	exp.CharLimit = 10
	s.createInputs[scfExpiration] = exp

	s.createFocus = scfSerial
	s.form = serialFormCreate
	s.result = ""
}

func (s *SerializedComponentsScreen) syncCreateFocus() {
	for i := range s.createInputs {
		if i == s.createFocus {
			s.createInputs[i].Focus()
		} else {
			s.createInputs[i].Blur()
		}
	}
}

func (s *SerializedComponentsScreen) handleCreateKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.form = serialFormNone
		s.result = ""
		return s, nil
	case "tab", "down":
		s.createFocus = (s.createFocus + 1) % len(s.createInputs)
		s.syncCreateFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.createFocus = (s.createFocus - 1 + len(s.createInputs)) % len(s.createInputs)
		s.syncCreateFocus()
		return s, textinput.Blink
	case "enter":
		if s.pending {
			return s, nil
		}
		return s.submitCreate()
	}
	var cmd tea.Cmd
	s.createInputs[s.createFocus], cmd = s.createInputs[s.createFocus].Update(m)
	return s, cmd
}

// buildCreatePayload validates + assembles the create body. Only serial_number
// is operator-entered-required; item comes from the screen context and lot is
// optional (omitempty on the wire).
func (s *SerializedComponentsScreen) buildCreatePayload() (omsapi.SerializedComponentCreate, error) {
	var w omsapi.SerializedComponentCreate
	if s.itemID == "" {
		return w, errors.New("no item context to add a unit to")
	}
	serial := strings.TrimSpace(s.createInputs[scfSerial].Value())
	if serial == "" {
		return w, errors.New("serial number is required")
	}
	exp, err := parseOptionalDateOnly(s.createInputs[scfExpiration].Value())
	if err != nil {
		return w, err
	}
	w = omsapi.SerializedComponentCreate{
		Item:           s.itemID,
		SerialNumber:   serial,
		Lot:            strings.TrimSpace(s.createInputs[scfLot].Value()),
		ExpirationDate: exp,
	}
	return w, nil
}

// parseOptionalDateOnly parses a YYYY-MM-DD field that may be blank. A blank
// value yields the zero DateOnly (serializes to null → no expiry); a malformed
// one is an error the form surfaces. Shared by the add-unit form and the
// batch-scan screen so both accept dates identically to po_detail's ship date.
func parseOptionalDateOnly(raw string) (omsapi.DateOnly, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return omsapi.DateOnly{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return omsapi.DateOnly{}, errors.New("expiration date must be YYYY-MM-DD")
	}
	return omsapi.DateOnly{Time: t}, nil
}

func (s *SerializedComponentsScreen) submitCreate() (Screen, tea.Cmd) {
	payload, err := s.buildCreatePayload()
	if err != nil {
		s.result = err.Error()
		s.level = StatusError
		return s, nil
	}
	s.pending = true
	s.result = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		unit, err := deps.OMS.CreateSerializedComponent(ctx, payload)
		return serialCreateDoneMsg{unit: unit, err: err}
	}
}

func (s *SerializedComponentsScreen) openHistory() tea.Cmd {
	unit := s.current()
	if unit == nil {
		return nil
	}
	s.showHistory = true
	s.historyFor = unit.ID
	s.historyLoading = true
	s.historyErr = ""
	s.historyScroller = nil
	deps := s.deps
	id := unit.ID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	q := url.Values{"component": []string{id}}
	return func() tea.Msg {
		events, err := deps.OMS.ListComponentUsageEvents(ctx, q)
		return serialHistoryLoadedMsg{forID: id, events: events, err: err}
	}
}

func (s *SerializedComponentsScreen) current() *omsapi.SerializedComponent {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return nil
	}
	return &s.rows[s.cursor]
}

// stockSplitLine renders the serialized unit split header (available / on-hand /
// installed) from the item-detail serializer's serialized_stock (op-0cd2), or ""
// when it wasn't supplied (asset scope, older backend). Installed is shown only
// when non-zero so a never-installed item stays uncluttered.
func (s *SerializedComponentsScreen) stockSplitLine() string {
	if s.stock == nil {
		return ""
	}
	parts := []string{
		fmt.Sprintf("available %d", s.stock.Available),
		fmt.Sprintf("on-hand %d", s.stock.OnHand),
	}
	if s.stock.Installed > 0 {
		parts = append(parts, fmt.Sprintf("installed %d", s.stock.Installed))
	}
	return StyleMuted.Render(strings.Join(parts, " · "))
}

func (s *SerializedComponentsScreen) visibleCount() int {
	header := 1
	if s.stockSplitLine() != "" {
		header++ // the available/on-hand line sits under the "N unit(s)" line
	}
	const indicators = 2
	footer := s.listBar(true).rows(proseBarCells(s.terminalWidth))
	if s.pending || s.result != "" {
		footer++
	}
	if unit := s.current(); unit == nil || len(unit.AvailableActions) == 0 {
		footer++
	}
	avail := screenBodyHeight(s.terminalHeight) - header - footer - indicators
	if avail < 2 {
		avail = 2
	}
	rowsFit := avail / 2 // each unit renders a title + a meta line
	if rowsFit < 1 {
		rowsFit = 1
	}
	return rowsFit
}

func (s *SerializedComponentsScreen) scrollIntoView() {
	win := s.visibleCount()
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+win {
		s.windowStart = s.cursor - win + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= win {
		s.windowStart = 0
	}
}

func (s *SerializedComponentsScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading units…", proseBarCells(s.terminalWidth), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, proseBarCells(s.terminalWidth), s.proseBar())
	}

	if s.showHistory {
		return s.viewHistory()
	}
	if s.form != serialFormNone {
		return s.viewForm()
	}

	var b strings.Builder
	if len(s.rows) == 0 {
		if s.scope == "asset" {
			b.WriteString(StyleMuted.Render("No serialized components installed in this asset."))
		} else {
			b.WriteString(StyleMuted.Render("No serial-numbered units for this item yet."))
		}
		b.WriteString("\n\n" + s.listBar(false).render(proseBarCells(s.terminalWidth)))
		return b.String()
	}

	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d unit(s)", len(s.rows))) + "\n")
	if line := s.stockSplitLine(); line != "" {
		b.WriteString(line + "\n")
	}

	win := s.visibleCount()
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + win
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		s.renderRow(&b, i)
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}

	b.WriteString("\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Working…") + "\n")
	} else if s.result != "" {
		b.WriteString(RenderStatus(s.result, s.level) + "\n")
	}
	if unit := s.current(); unit == nil || len(unit.AvailableActions) == 0 {
		b.WriteString(StyleMuted.Render("No actions available for this unit.") + "\n")
	}
	b.WriteString(s.listBar(len(s.rows) > 1).render(proseBarCells(s.terminalWidth)))
	return b.String()
}

func (s *SerializedComponentsScreen) renderRow(b *strings.Builder, i int) {
	u := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	serial := u.SerialNumber
	if serial == "" {
		serial = "(no serial)"
	}
	line := marker + serial + "  " + renderStatusBadge(u.Status, statusLabel(&u))
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(marker+serial) + "  " + renderStatusBadge(u.Status, statusLabel(&u))
	}
	b.WriteString(line + "\n")

	meta := []string{}
	if u.Lot != "" {
		meta = append(meta, "lot "+u.Lot)
	}
	if !u.ExpirationDate.IsZero() {
		meta = append(meta, "exp "+u.ExpirationDate.String())
	}
	if u.TrackingMode != "" {
		meta = append(meta, u.TrackingMode)
	}
	if u.InstalledInAssetName != "" {
		meta = append(meta, "in "+u.InstalledInAssetName)
	} else if u.InstalledInAsset != "" {
		meta = append(meta, "in asset "+shortID(u.InstalledInAsset))
	}
	if u.Status == omsapi.SerialStatusDisposed && u.DisposalReason != "" {
		meta = append(meta, "reason: "+u.DisposalReason)
	}
	if len(meta) > 0 {
		b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
	} else {
		b.WriteString("    " + StyleMuted.Render("no metadata") + "\n")
	}
}

func (s *SerializedComponentsScreen) listBar(moves bool) proseBar {
	bar := proseNavList(moves, false)
	if s.scope == "item" {
		bar = append(proseBar{{Keys: []string{"a"}, Hint: "a add"}}, bar...)
	}
	unit := s.current()
	if unit != nil {
		bar = append(bar, proseBarItem{Keys: []string{"h", "enter"}, Hint: "h/enter history"})
	}
	bar = append(bar, proseBarRefresh, proseBarEsc)
	if unit == nil || len(unit.AvailableActions) == 0 {
		return bar
	}
	labels := map[string]proseBarItem{
		omsapi.SerialActionReceive: {Keys: []string{"v"}, Hint: "v receive"},
		omsapi.SerialActionInstall: {Keys: []string{"i"}, Hint: "i install"},
		omsapi.SerialActionRemove:  {Keys: []string{"x"}, Hint: "x remove"},
		omsapi.SerialActionConsume: {Keys: []string{"c"}, Hint: "c consume"},
		omsapi.SerialActionRetire:  {Keys: []string{"t"}, Hint: "t retire"},
		omsapi.SerialActionDispose: {Keys: []string{"d"}, Hint: "d dispose"},
	}
	// Preserve a stable, lifecycle order rather than map iteration order.
	order := []string{
		omsapi.SerialActionReceive, omsapi.SerialActionInstall, omsapi.SerialActionRemove,
		omsapi.SerialActionConsume, omsapi.SerialActionRetire, omsapi.SerialActionDispose,
	}
	for _, a := range order {
		if containsStr(unit.AvailableActions, a) {
			bar = append(bar, labels[a])
		}
	}
	return bar
}

func (s *SerializedComponentsScreen) viewForm() string {
	if s.form == serialFormCreate {
		return s.viewCreateForm()
	}
	unit := s.current()
	serial := ""
	if unit != nil {
		serial = unit.SerialNumber
	}
	var b strings.Builder
	switch s.form {
	case serialFormInstall:
		b.WriteString(StyleTitle.Render("Install "+serial) + "\n")
		b.WriteString(StyleMuted.Render("Install this unit into an asset. Enter the asset's UUID") + "\n")
		b.WriteString(StyleMuted.Render("(shown as \"ID\" on the asset detail screen).") + "\n\n")
	case serialFormDispose:
		b.WriteString(StyleTitle.Render("Dispose "+serial) + "\n")
		b.WriteString(StyleMuted.Render("Record why this unit is being disposed.") + "\n\n")
	}
	b.WriteString(s.input.View() + "\n")
	if s.pending {
		b.WriteString("\n" + StyleMuted.Render("Submitting…"))
	} else {
		if s.result != "" {
			b.WriteString("\n" + RenderStatus(s.result, s.level))
		}
		b.WriteString("\n" + StyleMuted.Render("enter submit · esc cancel"))
	}
	return b.String()
}

func (s *SerializedComponentsScreen) viewCreateForm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Add serialized unit") + "\n")
	b.WriteString(StyleMuted.Render("Record a new serial-numbered unit for this item.") + "\n\n")
	b.WriteString(s.renderCreateField(scfSerial, "Serial number") + "\n")
	b.WriteString(s.renderCreateField(scfLot, "Lot (optional)") + "\n")
	b.WriteString(s.renderCreateField(scfExpiration, "Expiration (optional)") + "\n")
	b.WriteString("\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…"))
	} else {
		if s.result != "" {
			b.WriteString(RenderStatus(s.result, s.level) + "\n")
		}
		b.WriteString(StyleMuted.Render("tab/↑↓ move · enter add · esc cancel"))
	}
	return b.String()
}

func (s *SerializedComponentsScreen) renderCreateField(idx int, label string) string {
	caret := "  "
	if idx == s.createFocus {
		caret = "▸ "
	}
	return caret + StyleTitle.Render(label+": ") + s.createInputs[idx].View()
}

func (s *SerializedComponentsScreen) viewHistory() string {
	unit := s.current()
	serial := ""
	if unit != nil {
		serial = unit.SerialNumber
	}
	if s.historyLoading {
		return StyleTitle.Render("History "+serial) + "\n\n" + StyleMuted.Render("Loading…")
	}
	if s.historyErr != "" {
		return StyleTitle.Render("History "+serial) + "\n\n" +
			StyleStatusError.Render("Error: ") + s.historyErr + "\n\n" +
			StyleMuted.Render("r retry · esc back")
	}
	body := ""
	if s.historyScroller != nil {
		return proseScrollFrame(s.historyScroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.historyBar)
	}
	return body
}

func (s *SerializedComponentsScreen) historyBar(scrolls bool) proseBar {
	bar := proseNavScroll(scrolls)
	return append(bar, proseBarRefresh, proseBarEsc)
}

func (s *SerializedComponentsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.form != serialFormNone {
		return nil
	}
	if s.showHistory {
		if s.historyLoading || s.historyErr != "" || s.historyScroller == nil {
			return nil
		}
		return proseScrollBar(s.historyScroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.historyBar)
	}
	return s.listBar(len(s.rows) > 1)
}

// loadBar is the unit list's bar while its load is out or has failed — what its
// key switch still answers with no rows drawn (prose_bar.go carries the defect
// and the decision). On the unit a refresh kept under the cursor, which the
// frame no longer draws, `h`/`enter` still loads its history and `v`, `x`, `c`
// and `t` still FIRE their lifecycle writes — named because they act, and
// candidates for gating. `i`, `d` and `a` are not named: each opens a form this
// frame does not draw.
func (s *SerializedComponentsScreen) loadBar() proseBar {
	var out proseBar
	if unit := s.current(); unit != nil {
		out = append(out, proseBarItem{Keys: []string{"h", "enter"}, Hint: "h/enter history"})
		writes := []struct {
			action string
			item   proseBarItem
		}{
			{omsapi.SerialActionReceive, proseBarItem{Keys: []string{"v"}, Hint: "v receive"}},
			{omsapi.SerialActionRemove, proseBarItem{Keys: []string{"x"}, Hint: "x remove"}},
			{omsapi.SerialActionConsume, proseBarItem{Keys: []string{"c"}, Hint: "c consume"}},
			{omsapi.SerialActionRetire, proseBarItem{Keys: []string{"t"}, Hint: "t retire"}},
		}
		for _, w := range writes {
			if containsStr(unit.AvailableActions, w.action) {
				out = append(out, w.item)
			}
		}
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *SerializedComponentsScreen) renderHistory(events []omsapi.ComponentUsageEvent) string {
	unit := s.current()
	var b strings.Builder
	title := "Usage history"
	if unit != nil {
		title += " — " + unit.SerialNumber
		if unit.StatusDisplay != "" {
			title += " (" + unit.StatusDisplay + ")"
		}
	}
	b.WriteString(StyleTitle.Render(title) + "\n\n")
	if len(events) == 0 {
		b.WriteString(StyleMuted.Render("No recorded events yet."))
		return b.String()
	}
	for _, e := range events {
		when := ""
		if e.At != nil && !e.At.IsZero() {
			when = e.At.Format("2006-01-02 15:04")
		} else if !e.CreatedAt.IsZero() {
			when = e.CreatedAt.Format("2006-01-02 15:04")
		}
		action := e.ActionDisplay
		if action == "" {
			action = e.Action
		}
		line := fmt.Sprintf("%s  %s", when, action)
		b.WriteString(strings.TrimSpace(line) + "\n")
		detail := []string{}
		if e.AssetName != "" {
			detail = append(detail, "asset: "+e.AssetName)
		}
		if e.ActorUsername != "" {
			detail = append(detail, "by "+e.ActorUsername)
		}
		if e.Notes != "" {
			detail = append(detail, "\""+e.Notes+"\"")
		}
		if len(detail) > 0 {
			b.WriteString("    " + StyleMuted.Render(strings.Join(detail, " · ")) + "\n")
		}
	}
	return b.String()
}

// --- small helpers -------------------------------------------------------

func containsStr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// statusLabel returns the human display status, falling back to the raw
// status code when the server didn't send a display string.
func statusLabel(u *omsapi.SerializedComponent) string {
	if u.StatusDisplay != "" {
		return u.StatusDisplay
	}
	return u.Status
}

// renderStatusBadge colors a unit's status by lifecycle stage: green for the
// "healthy in service" states, amber for transitional ones, muted for the
// terminal states.
func renderStatusBadge(status, label string) string {
	badge := "[" + label + "]"
	switch status {
	case omsapi.SerialStatusInStock, omsapi.SerialStatusInstalled:
		return StyleStatusOK.Render(badge)
	case omsapi.SerialStatusReceived, omsapi.SerialStatusRemoved:
		return StyleStatusWarn.Render(badge)
	default: // consumed, retired, disposed
		return StyleMuted.Render(badge)
	}
}

func actionPastTense(action string) string {
	switch action {
	case omsapi.SerialActionReceive:
		return "received"
	case omsapi.SerialActionInstall:
		return "installed"
	case omsapi.SerialActionRemove:
		return "removed"
	case omsapi.SerialActionConsume:
		return "consumed"
	case omsapi.SerialActionRetire:
		return "retired"
	case omsapi.SerialActionDispose:
		return "disposed"
	}
	return action + " done"
}

// shortID trims a UUID to its first segment for compact display when no
// human-readable name is available.
func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
