// MaintenanceItemDetailScreen — one PM item, its task steps + materials, and
// every per-item action the web exposes: mark-complete, clone-to-asset,
// generate-work-order, check-material-stock, plus edit and delete.
//
// The view is a scrollable body (j/k). Action keys open small modal sub-phases;
// while a modal is up the screen opts into raw input (WantsRawInput) so its
// keys aren't eaten by the root's global hotkeys, exactly like the inventory
// detail's delete confirm.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type mDetailPhase int

const (
	mDetailPhaseView mDetailPhase = iota
	mDetailPhaseConfirmDelete
	mDetailPhaseComplete
	mDetailPhaseGenerate
	mDetailPhaseClone
)

type MaintenanceItemDetailScreen struct {
	deps           Deps
	itemID         string
	item           *omsapi.MaintenanceItem
	loading        bool
	loadErr        string
	scroller       *TextScroller
	terminalHeight int
	terminalWidth  int

	actionMsg string
	actionLvl StatusLevel

	phase mDetailPhase
	busy  bool // an action request is in-flight

	// Complete modal.
	cTime   textinput.Model
	cCost   textinput.Model
	cNotes  textinput.Model
	cCursor int

	// Generate-work-order modal.
	gDue    textinput.Model
	gNotes  textinput.Model
	gCursor int

	// Clone asset picker.
	assets       []omsapi.Asset
	assetsLoaded bool
	pickCursor   int
	pickSearch   textinput.Model
	pickTyping   bool
	pickOptions  []assetPickOption
}

type mDetailLoadedMsg struct {
	item *omsapi.MaintenanceItem
	err  error
}

type mDetailCompletedMsg struct{ err error }
type mDetailDeletedMsg struct{ err error }
type mDetailGeneratedMsg struct {
	woID string
	err  error
}
type mDetailClonedMsg struct {
	newID string
	err   error
}
type mDetailStockMsg struct {
	resp *omsapi.MaterialStockResponse
	err  error
}
type mDetailAssetsMsg struct {
	assets []omsapi.Asset
	err    error
}

func NewMaintenanceItemDetailScreen(deps Deps, id string) *MaintenanceItemDetailScreen {
	s := &MaintenanceItemDetailScreen{
		deps:     deps,
		itemID:   id,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
	for _, ti := range []*textinput.Model{&s.cTime, &s.cCost, &s.cNotes, &s.gDue, &s.gNotes, &s.pickSearch} {
		*ti = textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
	}
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60
	return s
}

func (s *MaintenanceItemDetailScreen) Title() string {
	if s.item != nil && s.item.Title != "" {
		return fmt.Sprintf("PM: %s", s.item.Title)
	}
	return "PM item"
}

// WantsRawInput claims keys only while a modal is open, so its y/n/text/esc
// land here instead of the root's global hotkeys.
func (s *MaintenanceItemDetailScreen) WantsRawInput() bool { return s.phase != mDetailPhaseView }

func (s *MaintenanceItemDetailScreen) Init() tea.Cmd { return s.load() }

func (s *MaintenanceItemDetailScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *MaintenanceItemDetailScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		item, err := deps.OMS.GetMaintenanceItem(ctx, id)
		return mDetailLoadedMsg{item: item, err: err}
	}
}

func (s *MaintenanceItemDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		return s, nil

	case mDetailLoadedMsg:
		s.loading = false
		s.busy = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.item = m.item
		}
		s.scroller.Set(s.renderBody())
		return s, nil

	case mDetailCompletedMsg:
		s.busy = false
		if m.err != nil {
			return s, Status("complete failed: "+m.err.Error(), StatusError)
		}
		s.phase = mDetailPhaseView
		s.setAction("marked complete", StatusOK)
		s.loading = true
		return s, tea.Batch(s.load(), Status("marked complete", StatusOK))

	case mDetailDeletedMsg:
		s.busy = false
		if m.err != nil {
			s.phase = mDetailPhaseView
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("PM item deleted", StatusOK),
			SwitchTo(WSMaintenance, NewMaintenanceItemsScreen(s.deps)),
		)

	case mDetailGeneratedMsg:
		s.busy = false
		if m.err != nil {
			return s, Status("generate WO failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("work order created", StatusOK),
			SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, m.woID)),
		)

	case mDetailClonedMsg:
		s.busy = false
		if m.err != nil {
			return s, Status("clone failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("PM item cloned", StatusOK),
			SwitchTo(WSMaintenance, NewMaintenanceItemDetailScreen(s.deps, m.newID)),
		)

	case mDetailStockMsg:
		s.busy = false
		if m.err != nil {
			return s, Status("stock check failed: "+m.err.Error(), StatusError)
		}
		s.setAction(stockSummary(m.resp), stockLevel(m.resp))
		return s, nil

	case mDetailAssetsMsg:
		if m.err != nil {
			s.phase = mDetailPhaseView
			return s, Status("load assets failed: "+m.err.Error(), StatusError)
		}
		s.assets = m.assets
		s.assetsLoaded = true
		s.applyCloneFilter()
		return s, nil

	case tea.KeyMsg:
		switch s.phase {
		case mDetailPhaseConfirmDelete:
			return s.updateConfirmDelete(m)
		case mDetailPhaseComplete:
			return s.updateComplete(m)
		case mDetailPhaseGenerate:
			return s.updateGenerate(m)
		case mDetailPhaseClone:
			return s.updateClone(m)
		default:
			return s.updateView(m)
		}
	}

	// Cursor blink for whichever modal input is focused.
	return s, s.forwardBlink(msg)
}

func (s *MaintenanceItemDetailScreen) forwardBlink(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch s.phase {
	case mDetailPhaseComplete:
		switch s.cCursor {
		case 0:
			s.cTime, cmd = s.cTime.Update(msg)
		case 1:
			s.cCost, cmd = s.cCost.Update(msg)
		case 2:
			s.cNotes, cmd = s.cNotes.Update(msg)
		}
	case mDetailPhaseGenerate:
		switch s.gCursor {
		case 0:
			s.gDue, cmd = s.gDue.Update(msg)
		case 1:
			s.gNotes, cmd = s.gNotes.Update(msg)
		}
	case mDetailPhaseClone:
		if s.pickTyping {
			s.pickSearch, cmd = s.pickSearch.Update(msg)
		}
	}
	return cmd
}

func (s *MaintenanceItemDetailScreen) setAction(msg string, lvl StatusLevel) {
	s.actionMsg = msg
	s.actionLvl = lvl
}

// ---------------------------------------------------------------------------
// View phase
// ---------------------------------------------------------------------------

func (s *MaintenanceItemDetailScreen) updateView(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.scroller.Handle(m) {
		return s, nil
	}
	switch m.String() {
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "E":
		if s.item != nil {
			return s, SwitchTo(WSMaintenance, NewMaintenanceItemFormScreen(s.deps, s.itemID))
		}
	case "x":
		if s.item != nil {
			s.phase = mDetailPhaseConfirmDelete
		}
	case "c":
		if s.item != nil {
			s.openComplete()
			return s, textinput.Blink
		}
	case "w":
		if s.item != nil {
			s.openGenerate()
			return s, textinput.Blink
		}
	case "L":
		if s.item != nil {
			return s, s.openClone()
		}
	case "S":
		if s.item != nil && !s.busy {
			s.busy = true
			return s, s.checkStockCmd()
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Delete confirm
// ---------------------------------------------------------------------------

func (s *MaintenanceItemDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.busy {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		s.busy = true
		deps := s.deps
		ctx := s.ctx()
		id := s.itemID
		return s, func() tea.Msg {
			return mDetailDeletedMsg{err: deps.OMS.DeleteMaintenanceItem(ctx, id)}
		}
	case "n", "N", "esc":
		s.phase = mDetailPhaseView
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Complete modal
// ---------------------------------------------------------------------------

func (s *MaintenanceItemDetailScreen) openComplete() {
	s.phase = mDetailPhaseComplete
	s.cCursor = 0
	s.cTime.SetValue("")
	s.cCost.SetValue("")
	s.cNotes.SetValue("")
	s.cTime.Focus()
	s.cCost.Blur()
	s.cNotes.Blur()
}

func (s *MaintenanceItemDetailScreen) updateComplete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.busy {
		return s, nil
	}
	switch m.String() {
	case "esc":
		s.phase = mDetailPhaseView
		return s, nil
	case "tab", "down":
		s.cCursor = (s.cCursor + 1) % 3
		s.syncCompleteFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.cCursor = (s.cCursor + 2) % 3
		s.syncCompleteFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.submitComplete()
	}
	var cmd tea.Cmd
	switch s.cCursor {
	case 0:
		s.cTime, cmd = s.cTime.Update(m)
	case 1:
		s.cCost, cmd = s.cCost.Update(m)
	case 2:
		s.cNotes, cmd = s.cNotes.Update(m)
	}
	return s, cmd
}

func (s *MaintenanceItemDetailScreen) syncCompleteFocus() {
	s.cTime.Blur()
	s.cCost.Blur()
	s.cNotes.Blur()
	switch s.cCursor {
	case 0:
		s.cTime.Focus()
	case 1:
		s.cCost.Focus()
	case 2:
		s.cNotes.Focus()
	}
}

func (s *MaintenanceItemDetailScreen) submitComplete() tea.Cmd {
	mins, err := mfOptPositiveInt(s.cTime.Value(), "time spent")
	if err != nil {
		return Status(err.Error(), StatusError)
	}
	cost, err := mfDecimalOrDefault(s.cCost.Value(), "", "cost incurred")
	if err != nil {
		return Status(err.Error(), StatusError)
	}
	req := omsapi.MaintenanceCompleteRequest{
		TimeSpentMinutes: mins,
		CostIncurred:     cost,
		Notes:            strings.TrimSpace(s.cNotes.Value()),
	}
	s.busy = true
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		_, e := deps.OMS.CompleteMaintenanceItem(ctx, id, req)
		return mDetailCompletedMsg{err: e}
	}
}

// ---------------------------------------------------------------------------
// Generate-work-order modal
// ---------------------------------------------------------------------------

func (s *MaintenanceItemDetailScreen) openGenerate() {
	s.phase = mDetailPhaseGenerate
	s.gCursor = 0
	s.gDue.SetValue("")
	s.gNotes.SetValue("")
	s.gDue.Focus()
	s.gNotes.Blur()
}

func (s *MaintenanceItemDetailScreen) updateGenerate(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.busy {
		return s, nil
	}
	switch m.String() {
	case "esc":
		s.phase = mDetailPhaseView
		return s, nil
	case "tab", "down":
		s.gCursor = (s.gCursor + 1) % 2
		s.syncGenerateFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.gCursor = (s.gCursor + 1) % 2
		s.syncGenerateFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.submitGenerate()
	}
	var cmd tea.Cmd
	if s.gCursor == 0 {
		s.gDue, cmd = s.gDue.Update(m)
	} else {
		s.gNotes, cmd = s.gNotes.Update(m)
	}
	return s, cmd
}

func (s *MaintenanceItemDetailScreen) syncGenerateFocus() {
	s.gDue.Blur()
	s.gNotes.Blur()
	if s.gCursor == 0 {
		s.gDue.Focus()
	} else {
		s.gNotes.Focus()
	}
}

func (s *MaintenanceItemDetailScreen) submitGenerate() tea.Cmd {
	req := omsapi.MaintenanceWorkOrderRequest{
		DueDate: strings.TrimSpace(s.gDue.Value()),
		Notes:   strings.TrimSpace(s.gNotes.Value()),
	}
	s.busy = true
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		wo, e := deps.OMS.GenerateMaintenanceWorkOrder(ctx, id, req)
		woID := ""
		if wo != nil {
			woID = fmt.Sprint(wo.ID)
		}
		return mDetailGeneratedMsg{woID: woID, err: e}
	}
}

// ---------------------------------------------------------------------------
// Clone asset picker
// ---------------------------------------------------------------------------

func (s *MaintenanceItemDetailScreen) openClone() tea.Cmd {
	s.phase = mDetailPhaseClone
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.pickCursor = 0
	if s.assetsLoaded {
		s.applyCloneFilter()
		return nil
	}
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		assets, err := deps.OMS.ListAllAssets(ctx)
		return mDetailAssetsMsg{assets: assets, err: err}
	}
}

func (s *MaintenanceItemDetailScreen) applyCloneFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := make([]assetPickOption, 0, len(s.assets))
	for _, a := range s.assets {
		label := assetPickLabel(a)
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, assetPickOption{id: fmt.Sprint(a.ID), label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *MaintenanceItemDetailScreen) updateClone(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.busy {
		return s, nil
	}
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyCloneFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyCloneFilter()
		return s, cmd
	}
	switch m.String() {
	case "esc":
		s.phase = mDetailPhaseView
	case "j", "down":
		if s.pickCursor < len(s.pickOptions)-1 {
			s.pickCursor++
		}
	case "k", "up":
		if s.pickCursor > 0 {
			s.pickCursor--
		}
	case "/":
		s.pickTyping = true
		s.pickSearch.Focus()
		return s, textinput.Blink
	case "enter":
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
			target := s.pickOptions[s.pickCursor].id
			s.busy = true
			deps := s.deps
			ctx := s.ctx()
			id := s.itemID
			return s, func() tea.Msg {
				cloned, e := deps.OMS.CloneMaintenanceItem(ctx, id, target)
				newID := ""
				if cloned != nil {
					newID = cloned.ID
				}
				return mDetailClonedMsg{newID: newID, err: e}
			}
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Check material stock
// ---------------------------------------------------------------------------

func (s *MaintenanceItemDetailScreen) checkStockCmd() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		resp, err := deps.OMS.CheckMaintenanceMaterialStock(ctx, id)
		return mDetailStockMsg{resp: resp, err: err}
	}
}

func stockSummary(resp *omsapi.MaterialStockResponse) string {
	if resp == nil || len(resp.LowStockAlerts) == 0 {
		return "material stock OK — no linked materials below minimum"
	}
	parts := make([]string, 0, len(resp.LowStockAlerts))
	for _, a := range resp.LowStockAlerts {
		parts = append(parts, fmt.Sprintf("%s %d/%d", a.Name, a.Current, a.Minimum))
	}
	return fmt.Sprintf("%d low: %s", len(resp.LowStockAlerts), strings.Join(parts, ", "))
}

func stockLevel(resp *omsapi.MaterialStockResponse) StatusLevel {
	if resp == nil || len(resp.LowStockAlerts) == 0 {
		return StatusOK
	}
	return StatusWarn
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *MaintenanceItemDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading PM item…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	if s.item == nil {
		return StyleMuted.Render("PM item not found.")
	}

	switch s.phase {
	case mDetailPhaseComplete:
		return s.viewComplete()
	case mDetailPhaseGenerate:
		return s.viewGenerate()
	case mDetailPhaseClone:
		return s.viewClone()
	}

	if s.phase == mDetailPhaseConfirmDelete {
		s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRowsWithAction))
		body := s.scroller.View()
		footer := ""
		name := s.item.Title
		if s.busy {
			footer = StyleMuted.Render("Deleting…")
		} else {
			footer = StyleStatusWarn.Render(fmt.Sprintf("Delete %q? This can't be undone.  y delete · n/esc cancel", name))
		}
		return body + "\n\n" + footer
	}
	s.sizeScroller()
	body := s.scroller.View()
	footer := ""
	if s.actionMsg != "" {
		footer += RenderStatus(s.actionMsg, s.actionLvl) + "\n\n"
	}
	footer += s.bar(s.scroller.HasOverflow()).render(proseBarCells(s.terminalWidth))
	return body + "\n\n" + footer
}

func (s *MaintenanceItemDetailScreen) bar(scrolls bool) proseBar {
	bar := proseNavScroll(scrolls)
	return append(bar,
		proseBarItem{Keys: []string{"c"}, Hint: "c complete"},
		proseBarItem{Keys: []string{"w"}, Hint: "w gen-WO"},
		proseBarItem{Keys: []string{"L"}, Hint: "L clone"},
		proseBarItem{Keys: []string{"S"}, Hint: "S check-stock"},
		proseBarItem{Keys: []string{"E"}, Hint: "E edit"},
		proseBarItem{Keys: []string{"x"}, Hint: "x delete"},
		proseBarRefresh,
		proseBarEsc,
	)
}

func (s *MaintenanceItemDetailScreen) sizeScroller() {
	rows := s.bar(true).rows(proseBarCells(s.terminalWidth))
	if s.actionMsg != "" {
		rows += detailFooterRowsWithAction - detailFooterRows
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, rows))
}

func (s *MaintenanceItemDetailScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" || s.item == nil || s.phase != mDetailPhaseView {
		return nil
	}
	s.sizeScroller()
	return s.bar(s.scroller.HasOverflow())
}

func (s *MaintenanceItemDetailScreen) renderBody() string {
	it := s.item
	var b strings.Builder

	b.WriteString(StyleTitle.Render(it.Title))
	if it.IsOverdue {
		badge := "OVERDUE"
		if it.DaysOverdue != nil {
			badge = fmt.Sprintf("OVERDUE %dd", *it.DaysOverdue)
		}
		b.WriteString("  " + StyleStatusError.Render(badge))
	}
	if !it.IsActive {
		b.WriteString("  " + StyleMuted.Render("[inactive]"))
	}
	b.WriteString("\n")

	meta := []string{"ID " + it.ID}
	if it.AssetName != "" {
		asset := "Asset: " + it.AssetName
		if it.AssetTag != "" {
			asset += " (" + it.AssetTag + ")"
		}
		meta = append(meta, asset)
	}
	b.WriteString(StyleMuted.Render(strings.Join(meta, " · ")) + "\n")

	sched := []string{}
	if it.IntervalDays != nil {
		sched = append(sched, fmt.Sprintf("every %dd", *it.IntervalDays))
	} else {
		sched = append(sched, "one-time / as-needed")
	}
	if it.EstimatedTimeMin != nil {
		sched = append(sched, fmt.Sprintf("~%dmin", *it.EstimatedTimeMin))
	}
	if c := it.EstimatedCost.String(); c != "" && c != "0" && c != "0.00" {
		sched = append(sched, "$"+c)
	}
	b.WriteString(StyleMuted.Render(strings.Join(sched, " · ")) + "\n")

	if it.NextDueAt != nil {
		b.WriteString(StyleMuted.Render("Next due: ") + it.NextDueAt.Format("2006-01-02") + "\n")
	}
	if it.LastCompletedAt != nil {
		b.WriteString(StyleMuted.Render("Last completed: ") + it.LastCompletedAt.Format("2006-01-02 15:04") + "\n")
	}
	b.WriteString("\n")

	if it.Description != "" {
		b.WriteString(it.Description + "\n\n")
	}
	if it.Instructions != "" {
		b.WriteString(StyleTitle.Render("Instructions") + "\n")
		b.WriteString(it.Instructions + "\n\n")
	}

	b.WriteString(StyleTitle.Render(fmt.Sprintf("Task steps (%d)", len(it.Tasks))) + "\n")
	if len(it.Tasks) == 0 {
		b.WriteString(StyleMuted.Render("  (none)") + "\n")
	} else {
		for i, t := range it.Tasks {
			title := t.Title
			if !t.IsRequired {
				title += " " + StyleMuted.Render("(optional)")
			}
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, title))
			if t.Description != "" {
				b.WriteString("     " + StyleMuted.Render(t.Description) + "\n")
			}
		}
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render(fmt.Sprintf("Materials (%d)", len(it.Materials))) + "\n")
	if len(it.Materials) == 0 {
		b.WriteString(StyleMuted.Render("  (none)") + "\n")
	} else {
		for _, mm := range it.Materials {
			qty := mm.Quantity.String()
			if mm.Unit != "" {
				qty += " " + mm.Unit
			}
			line := "  · " + mm.Name
			if qty != "" {
				line += " — " + qty
			}
			if c := mm.EstimatedCostPerUnit.String(); c != "" {
				line += " @ $" + c
			}
			if mm.InventoryItemDetail != nil {
				line += " " + StyleMuted.Render(fmt.Sprintf("[stock %d]", mm.InventoryItemDetail.CurrentStock))
			}
			b.WriteString(line + "\n")
			if mm.Notes != "" {
				b.WriteString("     " + StyleMuted.Render(mm.Notes) + "\n")
			}
		}
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Metadata") + "\n")
	if !it.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Created: ") + it.CreatedAt.Format("2006-01-02 15:04") + "\n")
	}
	if !it.UpdatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Updated: ") + it.UpdatedAt.Format("2006-01-02 15:04") + "\n")
	}

	return b.String()
}

func (s *MaintenanceItemDetailScreen) viewComplete() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Mark complete") + "  " + StyleMuted.Render("tab/↑↓ move · enter log completion · esc cancel") + "\n\n")
	b.WriteString(StyleMuted.Render("All fields optional. Logs a completion and resets the schedule.") + "\n\n")
	rows := []struct{ label, value string }{
		{"Time spent (min)", s.cTime.View()},
		{"Cost incurred ($)", s.cCost.View()},
		{"Notes", s.cNotes.View()},
	}
	for i, r := range rows {
		caret := "  "
		if i == s.cCursor {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(r.label+": ") + r.value + "\n")
	}
	if s.busy {
		b.WriteString("\n" + StyleMuted.Render("Saving…"))
	}
	return b.String()
}

func (s *MaintenanceItemDetailScreen) viewGenerate() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Generate work order") + "  " + StyleMuted.Render("tab/↑↓ move · enter create · esc cancel") + "\n\n")
	b.WriteString(StyleMuted.Render("Both optional. Due date defaults to the next-due date; format YYYY-MM-DD.") + "\n\n")
	rows := []struct{ label, value string }{
		{"Due date", s.gDue.View()},
		{"Notes", s.gNotes.View()},
	}
	for i, r := range rows {
		caret := "  "
		if i == s.gCursor {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(r.label+": ") + r.value + "\n")
	}
	if s.busy {
		b.WriteString("\n" + StyleMuted.Render("Creating…"))
	}
	return b.String()
}

func (s *MaintenanceItemDetailScreen) viewClone() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Clone to asset") + "  " + StyleMuted.Render("j/k move · / filter · enter clone · esc cancel") + "\n\n")
	if !s.assetsLoaded {
		b.WriteString(StyleMuted.Render("Loading assets…"))
		return b.String()
	}
	if s.busy {
		b.WriteString(StyleMuted.Render("Cloning…"))
		return b.String()
	}
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n\n")
	}
	if len(s.pickOptions) == 0 {
		b.WriteString(StyleMuted.Render("(no matching assets)"))
		return b.String()
	}
	const window = 12
	start, end := fieldWindow(s.pickCursor, len(s.pickOptions), window)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		line := caret + s.pickOptions[i].label
		if i == s.pickCursor {
			line = StyleSidebarItemActive.Render(caret + s.pickOptions[i].label)
		}
		b.WriteString(line + "\n")
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}
