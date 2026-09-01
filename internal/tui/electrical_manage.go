// Electrical management list screens — breakers under a panel, circuits under a
// breaker. These host the create/edit/delete surface for the two lower tiers of
// the power backbone: the read-only topology tree (electrical.go) can't cleanly
// host row selection, so the panel detail drills into PanelBreakersScreen and
// each breaker drills into BreakerCircuitsScreen, mirroring the app's
// list→detail→edit flow ([[scantty-parity-program]]).
//
// Keybindings follow the CategoryList/LocationList convention: n new, E edit,
// x delete (with a y/n confirm), enter drill, j/k/g/G nav, r refresh. n and G
// collide with global hotkeys, so both screens implement LocalKeyScreen to claim
// them; WantsRawInput is asserted only while the delete confirm is up so y/n
// land here. Delete is guarded by a client-side child-count pre-check so a
// panel-with-breakers / breaker-with-circuits gives a clear "remove them first"
// message instead of a raw backend FK-protection error.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// electricalDeleteBlock returns a clear "can't delete, has children" message
// when childCount > 0, else "". Mirrors the web's FK-protection guard without a
// round-trip (there is no pre-delete impact API for this bead).
func electricalDeleteBlock(entity string, childCount int, childNoun string) string {
	if childCount <= 0 {
		return ""
	}
	noun := childNoun
	if childCount != 1 {
		noun += "s"
	}
	return fmt.Sprintf("cannot delete %s — it still has %d %s; remove them first", entity, childCount, noun)
}

// ===========================================================================
// PanelBreakersScreen — breakers under one panel
// ===========================================================================

type PanelBreakersScreen struct {
	deps      Deps
	panelID   int
	panelName string

	rows           []omsapi.PowerBreakerDetail
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type panelBreakersLoadedMsg struct {
	rows []omsapi.PowerBreakerDetail
	err  error
}

type breakerDeletedMsg struct {
	err error
}

func NewPanelBreakersScreen(deps Deps, panelID int, panelName string) *PanelBreakersScreen {
	return &PanelBreakersScreen{deps: deps, panelID: panelID, panelName: panelName, loading: true, windowSize: 18}
}

func (s *PanelBreakersScreen) Title() string {
	if s.panelName != "" {
		return "Breakers: " + s.panelName
	}
	return fmt.Sprintf("Breakers: panel #%d", s.panelID)
}

// WantsRawInput claims every key only while the delete confirm is up (y/n/esc).
func (s *PanelBreakersScreen) WantsRawInput() bool { return s.confirmingDelete }

// HandlesKey claims the two action keys that collide with global hotkeys (n new,
// G bottom) so they reach this screen instead of the global nav switch.
func (s *PanelBreakersScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *PanelBreakersScreen) Init() tea.Cmd { return s.load() }

func (s *PanelBreakersScreen) load() tea.Cmd {
	deps := s.deps
	panelID := s.panelID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListPowerBreakers(ctx, panelID)
		return panelBreakersLoadedMsg{rows: rows, err: err}
	}
}

func (s *PanelBreakersScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *PanelBreakersScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case panelBreakersLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
			if s.panelName == "" && len(m.rows) > 0 {
				s.panelName = m.rows[0].PanelName
			}
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case breakerDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("breaker deleted", StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
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
		case "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
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
		case "n":
			return s, SwitchTo(WSFacilities, NewPowerBreakerFormScreen(s.deps, 0, s.panelID))
		case "enter", "c":
			// enter / c drill into the selected breaker's circuits (the next
			// tier down); E edits the breaker itself.
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewBreakerCircuitsScreen(s.deps, row.ID, s.panelID, "pos "+row.Position))
			}
		case "E":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewPowerBreakerFormScreen(s.deps, row.ID, s.panelID))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *PanelBreakersScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		if msg := electricalDeleteBlock("breaker", row.CircuitCount, "circuit"); msg != "" {
			s.confirmingDelete = false
			return s, Status(msg, StatusError)
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := row.ID
		return s, func() tea.Msg {
			return breakerDeletedMsg{err: deps.OMS.DeletePowerBreaker(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *PanelBreakersScreen) selected() (omsapi.PowerBreakerDetail, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.PowerBreakerDetail{}, false
	}
	return s.rows[s.cursor], true
}

func (s *PanelBreakersScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *PanelBreakersScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading breakers…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirmingDelete {
		return s.viewConfirm()
	}
	var b strings.Builder
	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No breakers on this panel yet.") + "\n\n")
		b.WriteString(StyleMuted.Render("n new breaker · esc back"))
		return b.String()
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d breakers", len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · n new · E edit · c/enter circuits · x delete · r refresh · esc back"))
	return b.String()
}

func (s *PanelBreakersScreen) viewConfirm() string {
	row, ok := s.selected()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	warn := ""
	if row.CircuitCount > 0 {
		warn = StyleStatusWarn.Render(fmt.Sprintf("  (has %d circuit(s) — delete will be blocked)", row.CircuitCount))
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Delete breaker pos %s? This can't be undone.  y delete · n/esc cancel", row.Position)) + warn
}

func (s *PanelBreakersScreen) renderRow(i int) string {
	br := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	head := fmt.Sprintf("[%s] %dA · %s · %dp", br.Position, br.Amperage, br.Phase, br.PoleCount)
	if br.Label != "" {
		head += " — " + br.Label
	}
	line := marker + head
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	badges := []string{}
	if br.Status != "" && br.Status != "active" {
		badges = append(badges, br.Status)
	}
	if br.IsCritical {
		badges = append(badges, "critical")
	}
	if br.ReviewStatus != "" && br.ReviewStatus != "ok" {
		badges = append(badges, br.ReviewStatus)
	}
	if br.NeedsReview {
		badges = append(badges, "needs review")
	}
	badges = append(badges, fmt.Sprintf("%d circuits", br.CircuitCount))
	line += " " + StyleMuted.Render("("+strings.Join(badges, " · ")+")")
	return line
}

// ===========================================================================
// BreakerCircuitsScreen — circuits under one breaker
// ===========================================================================

type BreakerCircuitsScreen struct {
	deps         Deps
	breakerID    int
	panelID      int
	breakerLabel string

	rows           []omsapi.PowerCircuitDetail
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type breakerCircuitsLoadedMsg struct {
	rows []omsapi.PowerCircuitDetail
	err  error
}

type circuitDeletedMsg struct {
	err error
}

func NewBreakerCircuitsScreen(deps Deps, breakerID, panelID int, breakerLabel string) *BreakerCircuitsScreen {
	return &BreakerCircuitsScreen{deps: deps, breakerID: breakerID, panelID: panelID, breakerLabel: breakerLabel, loading: true, windowSize: 18}
}

func (s *BreakerCircuitsScreen) Title() string {
	if s.breakerLabel != "" {
		return "Circuits: " + s.breakerLabel
	}
	return fmt.Sprintf("Circuits: breaker #%d", s.breakerID)
}

func (s *BreakerCircuitsScreen) WantsRawInput() bool { return s.confirmingDelete }

// HandlesKey claims n/G (collide with global new/bottom) plus the o/d drill keys
// for a circuit's outlets/disconnects. o collides with the global operational-
// modes hotkey, so it MUST be claimed here to reach the screen; d is claimed too
// for symmetry and to future-proof against a new global d.
func (s *BreakerCircuitsScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G" || key == "o" || key == "d"
}

func (s *BreakerCircuitsScreen) Init() tea.Cmd { return s.load() }

func (s *BreakerCircuitsScreen) load() tea.Cmd {
	deps := s.deps
	breakerID := s.breakerID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListPowerCircuits(ctx, breakerID)
		return breakerCircuitsLoadedMsg{rows: rows, err: err}
	}
}

func (s *BreakerCircuitsScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *BreakerCircuitsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case breakerCircuitsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
			if s.breakerLabel == "" && len(m.rows) > 0 {
				s.breakerLabel = m.rows[0].BreakerLabel
			}
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case circuitDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("circuit deleted", StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
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
		case "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
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
		case "n":
			return s, SwitchTo(WSFacilities, NewPowerCircuitFormScreen(s.deps, 0, s.breakerID, s.panelID))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewPowerCircuitFormScreen(s.deps, row.ID, s.breakerID, s.panelID))
			}
		case "o":
			// Drill into the selected circuit's outlets (the leaf receptacle tier).
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewCircuitOutletsScreen(s.deps, row.ID, s.panelID, s.circuitRowLabel(row)))
			}
		case "d":
			// Drill into the selected circuit's disconnects.
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewCircuitDisconnectsScreen(s.deps, row.ID, s.panelID, s.circuitRowLabel(row)))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

// circuitRowLabel is the terse breadcrumb label for a circuit row, reused for the
// outlets/disconnects drill titles.
func (s *BreakerCircuitsScreen) circuitRowLabel(row omsapi.PowerCircuitDetail) string {
	if row.Label != "" {
		return row.Label
	}
	return fmt.Sprintf("circuit #%d", row.ID)
}

func (s *BreakerCircuitsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		if msg := electricalDeleteBlock("circuit", row.OutletCount, "outlet"); msg != "" {
			s.confirmingDelete = false
			return s, Status(msg, StatusError)
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := row.ID
		return s, func() tea.Msg {
			return circuitDeletedMsg{err: deps.OMS.DeletePowerCircuit(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *BreakerCircuitsScreen) selected() (omsapi.PowerCircuitDetail, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.PowerCircuitDetail{}, false
	}
	return s.rows[s.cursor], true
}

func (s *BreakerCircuitsScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *BreakerCircuitsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading circuits…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirmingDelete {
		return s.viewConfirm()
	}
	var b strings.Builder
	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No circuits on this breaker yet.") + "\n\n")
		b.WriteString(StyleMuted.Render("n new circuit · esc back"))
		return b.String()
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d circuits", len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k · n new · E edit · o outlets · d disconnects · x del · r · esc"))
	return b.String()
}

func (s *BreakerCircuitsScreen) viewConfirm() string {
	row, ok := s.selected()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	name := row.Label
	if name == "" {
		name = fmt.Sprintf("#%d", row.ID)
	}
	warn := ""
	if row.OutletCount > 0 {
		warn = StyleStatusWarn.Render(fmt.Sprintf("  (has %d outlet(s) — delete will be blocked)", row.OutletCount))
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Delete circuit %s? This can't be undone.  y delete · n/esc cancel", name)) + warn
}

func (s *BreakerCircuitsScreen) renderRow(i int) string {
	ck := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	title := ck.Label
	if title == "" {
		title = fmt.Sprintf("circuit #%d", ck.ID)
	}
	line := marker + title
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	meta := []string{}
	if ck.MaxLoadAmps != nil {
		meta = append(meta, fmt.Sprintf("max %dA", *ck.MaxLoadAmps))
	}
	if ck.ConductorSize != "" {
		meta = append(meta, ck.ConductorSize)
	}
	if ck.ConductorLengthFt != nil {
		meta = append(meta, fmt.Sprintf("%dft", *ck.ConductorLengthFt))
	}
	if ck.OutletCount > 0 {
		meta = append(meta, fmt.Sprintf("%d outlets", ck.OutletCount))
	}
	if ck.NeedsReview {
		meta = append(meta, "needs review")
	}
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	return line
}

// ===========================================================================
// CircuitOutletsScreen — outlets under one circuit (the leaf receptacle tier)
// ===========================================================================

type CircuitOutletsScreen struct {
	deps         Deps
	circuitID    int
	panelID      int
	circuitLabel string

	rows           []omsapi.PowerOutletDetail
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type circuitOutletsLoadedMsg struct {
	rows []omsapi.PowerOutletDetail
	err  error
}

type outletDeletedMsg struct {
	err error
}

func NewCircuitOutletsScreen(deps Deps, circuitID, panelID int, circuitLabel string) *CircuitOutletsScreen {
	return &CircuitOutletsScreen{deps: deps, circuitID: circuitID, panelID: panelID, circuitLabel: circuitLabel, loading: true, windowSize: 18}
}

func (s *CircuitOutletsScreen) Title() string {
	if s.circuitLabel != "" {
		return "Outlets: " + s.circuitLabel
	}
	return fmt.Sprintf("Outlets: circuit #%d", s.circuitID)
}

func (s *CircuitOutletsScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *CircuitOutletsScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *CircuitOutletsScreen) Init() tea.Cmd { return s.load() }

func (s *CircuitOutletsScreen) load() tea.Cmd {
	deps := s.deps
	circuitID := s.circuitID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListPowerOutlets(ctx, circuitID)
		return circuitOutletsLoadedMsg{rows: rows, err: err}
	}
}

func (s *CircuitOutletsScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *CircuitOutletsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case circuitOutletsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
			if s.circuitLabel == "" && len(m.rows) > 0 {
				s.circuitLabel = m.rows[0].CircuitLabel
			}
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case outletDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("outlet deleted", StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
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
		case "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
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
		case "n":
			return s, SwitchTo(WSFacilities, NewPowerOutletFormScreen(s.deps, 0, s.circuitID, s.panelID))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewPowerOutletFormScreen(s.deps, row.ID, s.circuitID, s.panelID))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *CircuitOutletsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		// An outlet is a leaf (nothing FK-references it under PROTECT), so there
		// is no child pre-check — the delete always proceeds.
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := row.ID
		return s, func() tea.Msg {
			return outletDeletedMsg{err: deps.OMS.DeletePowerOutlet(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *CircuitOutletsScreen) selected() (omsapi.PowerOutletDetail, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.PowerOutletDetail{}, false
	}
	return s.rows[s.cursor], true
}

func (s *CircuitOutletsScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *CircuitOutletsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading outlets…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirmingDelete {
		return s.viewConfirm()
	}
	var b strings.Builder
	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No outlets on this circuit yet.") + "\n\n")
		b.WriteString(StyleMuted.Render("n new outlet · esc back"))
		return b.String()
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d outlets", len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · n new · E/enter edit · x delete · r refresh · esc back"))
	return b.String()
}

func (s *CircuitOutletsScreen) viewConfirm() string {
	row, ok := s.selected()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	name := row.Label
	if name == "" {
		name = fmt.Sprintf("#%d", row.ID)
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Delete outlet %s? This can't be undone.  y delete · n/esc cancel", name))
}

func (s *CircuitOutletsScreen) renderRow(i int) string {
	o := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	title := o.Label
	if title == "" {
		title = fmt.Sprintf("outlet #%d", o.ID)
	}
	line := marker + title
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	meta := []string{}
	if o.OutletType != "" {
		meta = append(meta, o.OutletType)
	}
	if o.LocationName != "" {
		meta = append(meta, "@ "+o.LocationName)
	}
	if o.DisconnectLabel != "" {
		meta = append(meta, "disc: "+o.DisconnectLabel)
	}
	if o.Status != "" && o.Status != "active" {
		meta = append(meta, o.Status)
	}
	if o.NeedsReview {
		meta = append(meta, "needs review")
	}
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	return line
}

// ===========================================================================
// CircuitDisconnectsScreen — disconnects under one circuit
// ===========================================================================

type CircuitDisconnectsScreen struct {
	deps         Deps
	circuitID    int
	panelID      int
	circuitLabel string

	rows           []omsapi.DisconnectDetail
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type circuitDisconnectsLoadedMsg struct {
	rows []omsapi.DisconnectDetail
	err  error
}

type disconnectDeletedMsg struct {
	err error
}

func NewCircuitDisconnectsScreen(deps Deps, circuitID, panelID int, circuitLabel string) *CircuitDisconnectsScreen {
	return &CircuitDisconnectsScreen{deps: deps, circuitID: circuitID, panelID: panelID, circuitLabel: circuitLabel, loading: true, windowSize: 18}
}

func (s *CircuitDisconnectsScreen) Title() string {
	if s.circuitLabel != "" {
		return "Disconnects: " + s.circuitLabel
	}
	return fmt.Sprintf("Disconnects: circuit #%d", s.circuitID)
}

func (s *CircuitDisconnectsScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *CircuitDisconnectsScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *CircuitDisconnectsScreen) Init() tea.Cmd { return s.load() }

func (s *CircuitDisconnectsScreen) load() tea.Cmd {
	deps := s.deps
	circuitID := s.circuitID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListDisconnects(ctx, circuitID)
		return circuitDisconnectsLoadedMsg{rows: rows, err: err}
	}
}

func (s *CircuitDisconnectsScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *CircuitDisconnectsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case circuitDisconnectsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
			if s.circuitLabel == "" && len(m.rows) > 0 {
				s.circuitLabel = m.rows[0].CircuitLabel
			}
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case disconnectDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("disconnect deleted", StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
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
		case "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
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
		case "n":
			return s, SwitchTo(WSFacilities, NewDisconnectFormScreen(s.deps, 0, s.circuitID, s.panelID))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewDisconnectFormScreen(s.deps, row.ID, s.circuitID, s.panelID))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *CircuitDisconnectsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		// Both reverse FKs to a Disconnect (PowerOutlet.disconnect, Asset.disconnect)
		// are SET_NULL, so the delete is never FK-blocked — it just unlinks them.
		// No child pre-check; any backend 4xx is surfaced by the deleted handler.
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := row.ID
		return s, func() tea.Msg {
			return disconnectDeletedMsg{err: deps.OMS.DeleteDisconnect(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *CircuitDisconnectsScreen) selected() (omsapi.DisconnectDetail, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.DisconnectDetail{}, false
	}
	return s.rows[s.cursor], true
}

func (s *CircuitDisconnectsScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *CircuitDisconnectsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading disconnects…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirmingDelete {
		return s.viewConfirm()
	}
	var b strings.Builder
	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No disconnects on this circuit yet.") + "\n\n")
		b.WriteString(StyleMuted.Render("n new disconnect · esc back"))
		return b.String()
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d disconnects", len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · n new · E/enter edit · x delete · r refresh · esc back"))
	return b.String()
}

func (s *CircuitDisconnectsScreen) viewConfirm() string {
	row, ok := s.selected()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	name := row.Label
	if name == "" {
		name = fmt.Sprintf("#%d", row.ID)
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Delete disconnect %s? Any outlets/assets referencing it are unlinked. This can't be undone.  y delete · n/esc cancel", name))
}

func (s *CircuitDisconnectsScreen) renderRow(i int) string {
	d := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	title := d.Label
	if title == "" {
		title = fmt.Sprintf("disconnect #%d", d.ID)
	}
	line := marker + title
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	meta := []string{}
	if d.DisconnectType != "" {
		meta = append(meta, d.DisconnectType)
	}
	if d.Amperage != nil {
		meta = append(meta, fmt.Sprintf("%dA", *d.Amperage))
	}
	if d.FuseSize != "" {
		meta = append(meta, "fuse "+d.FuseSize)
	}
	if d.IsLockable {
		meta = append(meta, "lockable")
	}
	if n := len(d.RequiredLOTODevices); n > 0 {
		meta = append(meta, fmt.Sprintf("%d LOTO", n))
	}
	if d.LocationName != "" {
		meta = append(meta, "@ "+d.LocationName)
	}
	if d.NeedsReview {
		meta = append(meta, "needs review")
	}
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	return line
}
