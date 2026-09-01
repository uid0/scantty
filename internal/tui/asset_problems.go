// Asset problems — per-asset list + read-only detail + the three lifecycle
// writes: resolve, promote to an in-house work order, and send to a vendor.
//
// Until sc-dcs3 ScanTTY could only *report* an asset problem (asset detail, p);
// everything after that lived on the web. This screen closes the loop, and it
// is the FIRST interface for the promote actions — the web's assetProblemsAPI
// still only does get + upload-photo, so there is no web page to mirror
// field-for-field. The shape therefore mirrors the sibling
// LocationProblemsScreen (list → filter → detail overlay → resolve overlay),
// with the two promote actions added on top.
//
// Promote-to-in-house needs NO MaintenanceItem picker, unlike the LocationProblem
// promote noted in location_problems.go: a corrective work order for an asset
// problem anchors straight to the problem's asset (maintenance_item=null), so
// there is nothing to pick and w fires in one keystroke. After the promote the
// screen jumps to wo_detail for the new work order, where completing it is what
// resolves the report (the backend resolves promoted problems on WO completion).
//
// Reached from the asset detail screen with P (uppercase, pairing with p =
// report a problem the same way N/n and I/i pair globally). f and G collide with
// global hotkeys, so the screen implements LocalKeyScreen to claim them, and it
// flips to raw input while an overlay is up so esc / typing / tab land here
// instead of the root's global switch.
package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type apFilter int

const (
	apFilterOpen apFilter = iota
	apFilterResolved
	apFilterAll
)

func (f apFilter) label() string {
	switch f {
	case apFilterResolved:
		return "resolved"
	case apFilterAll:
		return "all"
	default:
		return "open"
	}
}

// apVendorStep tracks where the operator is in the send-to-vendor prompt.
// apVendorStepNone is the zero value: no overlay open. Modeled on the
// inventory-detail consume modal's step chain.
type apVendorStep int

const (
	apVendorStepNone apVendorStep = iota
	apVendorStepVendor
	apVendorStepTitle
	apVendorStepWorkType
)

// apWorkTypeOptions is the third-party work-type cycle (codes + labels), in the
// backend's WORK_TYPE_CHOICES order. Index 0 ("standard") is the default.
var apWorkTypeOptions = []selectOption{
	{omsapi.ThirdPartyWorkTypeStandard, "Standard"},
	{omsapi.ThirdPartyWorkTypeMajorRepair, "Major Repair"},
	{omsapi.ThirdPartyWorkTypeBuildout, "Buildout"},
	{omsapi.ThirdPartyWorkTypeBuildingEmergency, "Building Emergency"},
}

type AssetProblemsScreen struct {
	deps      Deps
	assetID   string
	assetName string

	rows           []omsapi.AssetProblem
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int
	filter         apFilter

	// read-only detail overlay
	viewing bool

	// resolve overlay
	resolving         bool
	submittingResolve bool
	resolveTarget     string
	resolveNotes      textinput.Model
	resolveClosed     bool // false → mark resolved, true → mark closed

	// promote-to-in-house-work-order (no overlay — one keystroke, then jump to
	// the new work order). promoting guards against a double fire.
	promoting bool

	// send-to-vendor overlay (vendor pick-list → title → work type)
	vendorStep     apVendorStep
	vendorTarget   string
	vendors        []omsapi.Vendor
	vendorsErr     string
	loadingVendors bool
	vendorIx       int
	vendorTitle    textinput.Model
	workTypeIx     int
	submittingTP   bool
	vendorErr      string
}

type assetProblemsLoadedMsg struct {
	rows []omsapi.AssetProblem
	err  error
}

type assetProblemResolvedMsg struct {
	problem *omsapi.AssetProblem
	err     error
}

// assetProblemPromotedMsg carries the result of either promote. woID is the new
// in-house work order's id when the standard path ran (empty for the vendor
// path, which has no ScanTTY detail screen to jump to); label is what the status
// line names — the work order's short id when the serializer supplied one.
type assetProblemPromotedMsg struct {
	problem *omsapi.AssetProblem
	woID    string
	label   string
	err     error
}

// assetProblemVendorsLoadedMsg carries the vendor pick-list for the
// send-to-vendor overlay.
type assetProblemVendorsLoadedMsg struct {
	vendors []omsapi.Vendor
	err     error
}

// NewAssetProblemsScreen builds the problems list for one asset. Default filter
// is "open" (the actionable set), matching the location-problems sibling.
func NewAssetProblemsScreen(deps Deps, assetID, assetName string) *AssetProblemsScreen {
	notes := textinput.New()
	notes.Prompt = ""
	notes.CharLimit = 1000
	notes.Placeholder = "optional resolution notes"

	title := textinput.New()
	title.Prompt = ""
	title.CharLimit = 200
	title.Placeholder = "scope line for the vendor (required)"

	return &AssetProblemsScreen{
		deps:         deps,
		assetID:      assetID,
		assetName:    strings.TrimSpace(assetName),
		loading:      true,
		windowSize:   18,
		filter:       apFilterOpen,
		resolveNotes: notes,
		vendorTitle:  title,
	}
}

func (s *AssetProblemsScreen) Title() string {
	if s.assetName != "" {
		return "Problems: " + s.assetName
	}
	return "Asset problems"
}

// WantsRawInput claims every key while an overlay is up so esc / typing / tab /
// enter land here instead of the global hotkeys.
func (s *AssetProblemsScreen) WantsRawInput() bool {
	return s.viewing || s.resolving || s.vendorStep != apVendorStepNone
}

// HandlesKey claims the two list keys that collide with global hotkeys (f
// filter, G bottom), exactly as the location-problems sibling does. The three
// action keys were picked from the free letters — R resolve, w work order, t
// vendor — so they shadow nothing and need no claim.
func (s *AssetProblemsScreen) HandlesKey(key string) bool {
	return key == "f" || key == "G"
}

func (s *AssetProblemsScreen) Init() tea.Cmd { return s.load() }

func (s *AssetProblemsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetProblemsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	assetID := s.assetID
	return func() tea.Msg {
		page, err := deps.OMS.ListAssetProblems(ctx, url.Values{"asset": []string{assetID}})
		if err != nil {
			return assetProblemsLoadedMsg{err: err}
		}
		if page == nil {
			return assetProblemsLoadedMsg{}
		}
		return assetProblemsLoadedMsg{rows: page.Results}
	}
}

// visible returns the rows matching the active filter. Navigation + selection
// operate on this filtered view, so the cursor always indexes a shown row.
func (s *AssetProblemsScreen) visible() []omsapi.AssetProblem {
	if s.filter == apFilterAll {
		return s.rows
	}
	out := make([]omsapi.AssetProblem, 0, len(s.rows))
	for _, p := range s.rows {
		switch s.filter {
		case apFilterResolved:
			if p.IsResolved() {
				out = append(out, p)
			}
		default: // open
			if !p.IsResolved() {
				out = append(out, p)
			}
		}
	}
	return out
}

func (s *AssetProblemsScreen) openCount() int {
	n := 0
	for _, p := range s.rows {
		if !p.IsResolved() {
			n++
		}
	}
	return n
}

func (s *AssetProblemsScreen) selected() (omsapi.AssetProblem, bool) {
	vis := s.visible()
	if s.cursor < 0 || s.cursor >= len(vis) {
		return omsapi.AssetProblem{}, false
	}
	return vis[s.cursor], true
}

func (s *AssetProblemsScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *AssetProblemsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case assetProblemsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
		}
		s.clampCursor()
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case assetProblemVendorsLoadedMsg:
		s.loadingVendors = false
		if m.err != nil {
			s.vendorsErr = m.err.Error()
			return s, nil
		}
		s.vendorsErr = ""
		s.vendors = activeVendors(m.vendors)
		if s.vendorIx >= len(s.vendors) {
			s.vendorIx = 0
		}
		return s, nil
	case assetProblemResolvedMsg:
		s.submittingResolve = false
		s.resolving = false
		if m.err != nil {
			return s, Status("resolve failed: "+m.err.Error(), StatusError)
		}
		verb := "resolved"
		if m.problem != nil {
			verb = strings.ToLower(m.problem.StatusLabel())
		}
		s.loading = true
		return s, tea.Batch(Status("problem "+verb, StatusOK), s.load())
	case assetProblemPromotedMsg:
		s.promoting = false
		s.submittingTP = false
		if m.err != nil {
			// Keep the vendor overlay up so the operator can fix the input in
			// place (a missing title / an already-promoted report both 400 here).
			if s.vendorStep != apVendorStepNone {
				s.vendorErr = m.err.Error()
			}
			return s, Status("promote failed: "+m.err.Error(), StatusError)
		}
		s.closeVendor()
		if m.woID != "" {
			// In-house promote: land on the work order, where completing it is
			// what resolves the report.
			return s, tea.Batch(
				Status("work order created"+labelSuffix(m.label), StatusOK),
				SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, m.woID)),
			)
		}
		s.loading = true
		return s, tea.Batch(Status("sent to vendor"+labelSuffix(m.label), StatusOK), s.load())
	case tea.KeyMsg:
		if s.vendorStep != apVendorStepNone {
			return s.updateVendor(m)
		}
		if s.resolving {
			return s.updateResolve(m)
		}
		if s.viewing {
			return s.updateView(m)
		}
		return s.updateList(m)
	}
	return s, nil
}

func (s *AssetProblemsScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	vis := s.visible()
	switch m.String() {
	case "j", "down":
		if s.cursor < len(vis)-1 {
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
		if s.cursor >= len(vis) {
			s.cursor = len(vis) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
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
		s.cursor = len(vis) - 1
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "f":
		s.filter = (s.filter + 1) % 3
		s.cursor = 0
		s.windowStart = 0
		s.scrollIntoView()
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "v", "enter":
		if _, ok := s.selected(); ok {
			s.viewing = true
		}
	case "R":
		if p, ok := s.selected(); ok {
			if p.IsResolved() {
				return s, Status("already "+strings.ToLower(p.StatusLabel()), StatusWarn)
			}
			s.openResolve(p)
			return s, textinput.Blink
		}
	case "w":
		if p, ok := s.selected(); ok {
			return s.promoteStandard(p)
		}
	case "t":
		if p, ok := s.selected(); ok {
			return s.openVendor(p)
		}
	}
	return s, nil
}

func (s *AssetProblemsScreen) updateView(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "enter", "q", "v":
		s.viewing = false
	case "R":
		// Resolve straight from the detail view, mirroring the location sibling.
		if p, ok := s.selected(); ok && !p.IsResolved() {
			s.viewing = false
			s.openResolve(p)
			return s, textinput.Blink
		}
	case "w":
		if p, ok := s.selected(); ok {
			s.viewing = false
			return s.promoteStandard(p)
		}
	case "t":
		if p, ok := s.selected(); ok {
			s.viewing = false
			return s.openVendor(p)
		}
	}
	return s, nil
}

// --- resolve -----------------------------------------------------------------

func (s *AssetProblemsScreen) openResolve(p omsapi.AssetProblem) {
	s.resolving = true
	s.resolveTarget = p.ID
	s.resolveClosed = false
	s.resolveNotes.SetValue("")
	s.resolveNotes.Focus()
}

func (s *AssetProblemsScreen) updateResolve(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.submittingResolve {
		return s, nil
	}
	switch m.String() {
	case "esc":
		s.resolving = false
		s.resolveNotes.Blur()
		return s, nil
	case "tab":
		s.resolveClosed = !s.resolveClosed
		return s, nil
	case "enter":
		return s.submitResolve()
	}
	var cmd tea.Cmd
	s.resolveNotes, cmd = s.resolveNotes.Update(m)
	return s, cmd
}

// resolveStatus maps the overlay's toggle to the status code the resolve action
// accepts. Extracted so the resolved/closed mapping is unit-testable without a
// network round-trip.
func (s *AssetProblemsScreen) resolveStatus() string {
	if s.resolveClosed {
		return omsapi.AssetProblemClosed
	}
	return omsapi.AssetProblemResolved
}

func (s *AssetProblemsScreen) submitResolve() (Screen, tea.Cmd) {
	status := s.resolveStatus()
	notes := strings.TrimSpace(s.resolveNotes.Value())
	s.submittingResolve = true
	deps := s.deps
	ctx := s.ctx()
	id := s.resolveTarget
	return s, func() tea.Msg {
		p, err := deps.OMS.ResolveAssetProblem(ctx, id, status, notes)
		return assetProblemResolvedMsg{problem: p, err: err}
	}
}

// --- promote to an in-house work order ---------------------------------------

// promoteStandard fires the one-keystroke in-house promote. Gated on an
// already-promoted report (the backend 400s) and on a terminal one (there is no
// work left to do), so the operator gets an instant answer instead of an error
// round-trip.
func (s *AssetProblemsScreen) promoteStandard(p omsapi.AssetProblem) (Screen, tea.Cmd) {
	if s.promoting {
		return s, nil
	}
	if p.WorkOrderShortID != "" || (p.WorkOrder != nil && *p.WorkOrder != "") {
		return s, Status("already promoted to "+firstNonEmpty(p.WorkOrderShortID, "a work order"), StatusWarn)
	}
	if p.IsResolved() {
		return s, Status("already "+strings.ToLower(p.StatusLabel()), StatusWarn)
	}
	s.promoting = true
	deps := s.deps
	ctx := s.ctx()
	id := p.ID
	return s, func() tea.Msg {
		out, err := deps.OMS.PromoteAssetProblemStandard(ctx, id)
		msg := assetProblemPromotedMsg{problem: out, err: err}
		if out != nil && out.WorkOrder != nil {
			msg.woID = *out.WorkOrder
			msg.label = out.WorkOrderShortID
		}
		return msg
	}
}

// --- send to a vendor (third-party work order) --------------------------------

// openVendor enters the send-to-vendor prompt at the vendor step and kicks off
// the vendor pick-list load, mirroring the consume modal's load-while-you-type.
func (s *AssetProblemsScreen) openVendor(p omsapi.AssetProblem) (Screen, tea.Cmd) {
	if p.ThirdPartyWorkOrderShortID != "" || (p.ThirdPartyWorkOrder != nil && *p.ThirdPartyWorkOrder != "") {
		return s, Status("already sent to "+firstNonEmpty(p.ThirdPartyWorkOrderShortID, "a vendor"), StatusWarn)
	}
	if p.IsResolved() {
		return s, Status("already "+strings.ToLower(p.StatusLabel()), StatusWarn)
	}
	s.vendorStep = apVendorStepVendor
	s.vendorTarget = p.ID
	s.vendorIx = 0
	s.workTypeIx = 0
	s.vendorErr = ""
	s.vendorsErr = ""
	// Seed the scope line from the report so the operator edits rather than
	// retypes; the backend requires a human-written title.
	s.vendorTitle.SetValue(truncateOneLine(p.Description, 120))
	s.vendorTitle.Blur()
	if len(s.vendors) > 0 {
		return s, nil
	}
	s.loadingVendors = true
	return s, s.loadVendors()
}

func (s *AssetProblemsScreen) loadVendors() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		page, err := deps.OMS.ListVendors(ctx, nil)
		if err != nil {
			return assetProblemVendorsLoadedMsg{err: err}
		}
		if page == nil {
			return assetProblemVendorsLoadedMsg{}
		}
		return assetProblemVendorsLoadedMsg{vendors: page.Results}
	}
}

func (s *AssetProblemsScreen) closeVendor() {
	s.vendorStep = apVendorStepNone
	s.vendorErr = ""
	s.submittingTP = false
	s.vendorTitle.Blur()
}

// updateVendor drives the three-step prompt: vendor → title → work type. esc
// cancels at any step; the screen is in raw-input mode throughout, so these keys
// reach us before the global hotkeys.
func (s *AssetProblemsScreen) updateVendor(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.submittingTP {
		return s, nil
	}
	if m.String() == "esc" {
		s.closeVendor()
		return s, nil
	}
	switch s.vendorStep {
	case apVendorStepVendor:
		switch m.String() {
		case "up", "k":
			if s.vendorIx > 0 {
				s.vendorIx--
			}
		case "down", "j":
			if s.vendorIx < len(s.vendors)-1 {
				s.vendorIx++
			}
		case "enter":
			if s.loadingVendors {
				return s, nil
			}
			if len(s.vendors) == 0 {
				// A load failure is already rendered in the list area; only
				// explain an empty-but-successful list. esc then t retries.
				if s.vendorsErr == "" {
					s.vendorErr = "no active vendors to send to"
				}
				return s, nil
			}
			s.vendorErr = ""
			s.vendorStep = apVendorStepTitle
			s.vendorTitle.Focus()
			return s, textinput.Blink
		}
		return s, nil
	case apVendorStepTitle:
		if m.Type == tea.KeyEnter {
			if strings.TrimSpace(s.vendorTitle.Value()) == "" {
				s.vendorErr = "title is required"
				return s, nil
			}
			s.vendorErr = ""
			s.vendorStep = apVendorStepWorkType
			s.vendorTitle.Blur()
			return s, nil
		}
		var cmd tea.Cmd
		s.vendorTitle, cmd = s.vendorTitle.Update(m)
		return s, cmd
	case apVendorStepWorkType:
		switch m.String() {
		case " ", "right", "down", "j":
			s.cycleWorkType(+1)
		case "left", "up", "k":
			s.cycleWorkType(-1)
		case "enter":
			return s.submitVendor()
		}
		return s, nil
	}
	return s, nil
}

func (s *AssetProblemsScreen) cycleWorkType(delta int) {
	n := len(apWorkTypeOptions)
	s.workTypeIx = (s.workTypeIx + delta + n) % n
}

// selectedVendor returns the highlighted vendor, or false when the pick-list is
// empty / the index went stale against a reloaded list.
func (s *AssetProblemsScreen) selectedVendor() (omsapi.Vendor, bool) {
	if s.vendorIx < 0 || s.vendorIx >= len(s.vendors) {
		return omsapi.Vendor{}, false
	}
	return s.vendors[s.vendorIx], true
}

func (s *AssetProblemsScreen) submitVendor() (Screen, tea.Cmd) {
	vendor, ok := s.selectedVendor()
	if !ok {
		s.vendorStep = apVendorStepVendor
		s.vendorErr = "pick a vendor"
		return s, nil
	}
	title := strings.TrimSpace(s.vendorTitle.Value())
	if title == "" {
		s.vendorStep = apVendorStepTitle
		s.vendorTitle.Focus()
		s.vendorErr = "title is required"
		return s, textinput.Blink
	}
	workType := apWorkTypeOptions[s.workTypeIx].value
	s.submittingTP = true
	s.vendorErr = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.vendorTarget
	vendorID := vendor.ID
	return s, func() tea.Msg {
		out, err := deps.OMS.PromoteAssetProblemThirdParty(ctx, id, vendorID, title, workType)
		msg := assetProblemPromotedMsg{problem: out, err: err, label: vendor.Name}
		if out != nil && out.ThirdPartyWorkOrderShortID != "" {
			msg.label = out.ThirdPartyWorkOrderShortID
		}
		return msg
	}
}

// --- list plumbing ------------------------------------------------------------

func (s *AssetProblemsScreen) clampCursor() {
	if s.cursor >= len(s.visible()) {
		s.cursor = 0
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *AssetProblemsScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 18
	}
	n := len(s.visible())
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if n <= s.windowSize {
		s.windowStart = 0
	}
}

// --- views --------------------------------------------------------------------

func (s *AssetProblemsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading problems…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.vendorStep != apVendorStepNone {
		return s.viewVendor()
	}
	if s.resolving {
		return s.viewResolve()
	}
	if s.viewing {
		return s.viewDetail()
	}
	return s.viewList()
}

func (s *AssetProblemsScreen) viewList() string {
	var b strings.Builder
	header := s.assetName
	if header == "" {
		header = "Asset"
	}
	b.WriteString(StyleTitle.Render(header+" — problems") + "  " +
		StyleMuted.Render(fmt.Sprintf("[%s]  (%d open · %d total)", s.filter.label(), s.openCount(), len(s.rows))) + "\n")

	vis := s.visible()
	if len(vis) == 0 {
		b.WriteString("\n" + StyleMuted.Render("No "+s.filter.label()+" problems here.") + "\n\n")
		b.WriteString(StyleMuted.Render("f filter · r refresh · esc back (report a new one with p on the asset)"))
		return b.String()
	}

	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(vis) {
		end = len(vis)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(vis, i) + "\n")
	}
	if end < len(vis) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(vis)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · enter view · R resolve · w work order · t vendor · f filter · r refresh · esc back"))
	return b.String()
}

func (s *AssetProblemsScreen) renderRow(vis []omsapi.AssetProblem, i int) string {
	p := vis[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	desc := truncateOneLine(p.Description, 60)
	meta := []string{p.StatusLabel()}
	if !p.CreatedAt.IsZero() {
		meta = append(meta, p.CreatedAt.Format("2006-01-02"))
	}
	if p.ReportedBy != "" {
		meta = append(meta, "by "+p.ReportedBy)
	}
	line := fmt.Sprintf("%s%s %s %s", marker, apStatusStyle(p.Status).Render("["+strings.ToUpper(p.StatusLabel())+"]"),
		desc, StyleMuted.Render("("+strings.Join(meta, " · ")+")"))
	if short := apPromotedShortID(p); short != "" {
		line += " " + StyleMuted.Render("→ "+short)
	}
	if i == s.cursor {
		return StyleSidebarItemActive.Render(line)
	}
	return line
}

func (s *AssetProblemsScreen) viewDetail() string {
	p, ok := s.selected()
	if !ok {
		return StyleMuted.Render("No problem selected.") + "\n\n" + StyleMuted.Render("esc back")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(firstNonEmpty(p.AssetName, s.assetName)) + "\n")
	b.WriteString(apStatusStyle(p.Status).Render(p.StatusLabel()))
	if p.AssetTag != "" {
		b.WriteString("  " + StyleMuted.Render("asset tag: ") + p.AssetTag)
	}
	b.WriteString("\n")
	if p.ReportedBy != "" || !p.CreatedAt.IsZero() {
		who := p.ReportedBy
		if who == "" {
			who = "anonymous"
		}
		when := ""
		if !p.CreatedAt.IsZero() {
			when = " on " + p.CreatedAt.Format("2006-01-02 15:04")
		}
		b.WriteString(StyleMuted.Render("reported by "+who+when) + "\n")
	}
	b.WriteString("\n" + StyleTitle.Render("Description") + "\n")
	b.WriteString(firstNonEmpty(p.Description, StyleMuted.Render("(none)")) + "\n")

	if len(p.AffectedParts) > 0 {
		b.WriteString("\n" + StyleTitle.Render("Affected components") + "\n")
		for _, ap := range p.AffectedParts {
			name := ap.PartName
			if name == "" {
				name = fmt.Sprintf("%v", ap.ID)
			}
			line := "  · " + name
			if ap.PartSKU != "" {
				line += " " + StyleMuted.Render("("+ap.PartSKU+")")
			}
			if ap.QuantityNeeded > 0 {
				line += " " + StyleMuted.Render(fmt.Sprintf("qty %d", ap.QuantityNeeded))
			}
			b.WriteString(line + "\n")
		}
	}

	if p.IsResolved() || p.ResolutionNotes != "" {
		b.WriteString("\n" + StyleTitle.Render("Resolution") + "\n")
		if p.ResolutionNotes != "" {
			b.WriteString(p.ResolutionNotes + "\n")
		}
		if p.ResolvedBy != "" || p.ResolvedAt != nil {
			when := ""
			if p.ResolvedAt != nil {
				when = " on " + p.ResolvedAt.Format("2006-01-02 15:04")
			}
			b.WriteString(StyleMuted.Render("resolved by "+firstNonEmpty(p.ResolvedBy, "?")+when) + "\n")
		}
	}

	if p.IsPromoted() {
		b.WriteString("\n" + StyleTitle.Render("Promoted to") + "\n")
		if p.WorkOrderShortID != "" || p.WorkOrder != nil {
			b.WriteString(StyleMuted.Render("work order: ") + firstNonEmpty(p.WorkOrderShortID, derefOr(p.WorkOrder, "—")) + "\n")
		}
		if p.ThirdPartyWorkOrderShortID != "" || p.ThirdPartyWorkOrder != nil {
			b.WriteString(StyleMuted.Render("3rd-party work order: ") +
				firstNonEmpty(p.ThirdPartyWorkOrderShortID, derefOr(p.ThirdPartyWorkOrder, "—")) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(StyleMuted.Render(apDetailHints(p)))
	return b.String()
}

func (s *AssetProblemsScreen) viewResolve() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Resolve problem") + "\n")
	if p, ok := s.selected(); ok {
		b.WriteString(StyleMuted.Render(truncateOneLine(p.Description, 70)) + "\n")
	}
	b.WriteString("\n")
	if s.submittingResolve {
		b.WriteString(StyleMuted.Render("Resolving…"))
		return b.String()
	}
	b.WriteString(StyleTitle.Render("Resolution notes: ") + s.resolveNotes.View() + "\n")
	statusWord := "resolved"
	if s.resolveClosed {
		statusWord = "closed"
	}
	b.WriteString(StyleTitle.Render("Status: ") + "‹ " + statusWord + " ›" + "\n\n")
	b.WriteString(StyleMuted.Render("enter submit · tab toggle resolved/closed · esc cancel"))
	return b.String()
}

func (s *AssetProblemsScreen) viewVendor() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Send to vendor") + "\n")
	if p, ok := s.selected(); ok {
		b.WriteString(StyleMuted.Render(truncateOneLine(p.Description, 70)) + "\n")
	}
	b.WriteString("\n")
	if s.submittingTP {
		b.WriteString(StyleMuted.Render("Opening vendor work order…"))
		return b.String()
	}

	switch s.vendorStep {
	case apVendorStepVendor:
		b.WriteString(StyleTitle.Render("Vendor") + "\n")
		switch {
		case s.loadingVendors:
			b.WriteString(StyleMuted.Render("  loading vendors…") + "\n")
		case s.vendorsErr != "":
			b.WriteString(StyleStatusError.Render("  "+s.vendorsErr) + "\n")
		case len(s.vendors) == 0:
			b.WriteString(StyleMuted.Render("  (no active vendors — add one from the menu: Maintenance › Vendors)") + "\n")
		default:
			for i, v := range s.vendors {
				caret := "    "
				if i == s.vendorIx {
					caret = "  ▸ "
				}
				row := caret + v.Name
				if kind := firstNonEmpty(v.VendorKindDisplay, v.VendorKind); kind != "" {
					row += " " + StyleMuted.Render("("+kind+")")
				}
				if i == s.vendorIx {
					row = StyleSidebarItemActive.Render(row)
				}
				b.WriteString(row + "\n")
			}
		}
		b.WriteString("\n" + StyleMuted.Render("j/k move · enter pick · esc cancel"))
	case apVendorStepTitle:
		if v, ok := s.selectedVendor(); ok {
			b.WriteString(StyleMuted.Render("vendor: ") + v.Name + "\n\n")
		}
		b.WriteString(StyleTitle.Render("Title: ") + s.vendorTitle.View() + "\n")
		b.WriteString("\n" + StyleMuted.Render("enter next · esc cancel"))
	case apVendorStepWorkType:
		if v, ok := s.selectedVendor(); ok {
			b.WriteString(StyleMuted.Render("vendor: ") + v.Name + "\n")
		}
		b.WriteString(StyleMuted.Render("title: ") + strings.TrimSpace(s.vendorTitle.Value()) + "\n\n")
		b.WriteString(StyleTitle.Render("Work type: ") + elecSelectLabel(apWorkTypeOptions, s.workTypeIx) + "\n")
		b.WriteString("\n" + StyleMuted.Render("space/←→ change · enter send · esc cancel"))
	}

	if s.vendorErr != "" {
		b.WriteString("\n\n" + StyleStatusError.Render("✗ "+s.vendorErr))
	}
	return b.String()
}

// --- small helpers -------------------------------------------------------------

// apDetailHints tailors the detail overlay footer to what the report still
// allows: a terminal report offers nothing but back, and each promote drops off
// once its work order exists.
func apDetailHints(p omsapi.AssetProblem) string {
	if p.IsResolved() {
		return "esc/enter back"
	}
	hints := []string{"R resolve"}
	if p.WorkOrderShortID == "" && (p.WorkOrder == nil || *p.WorkOrder == "") {
		hints = append(hints, "w work order")
	}
	if p.ThirdPartyWorkOrderShortID == "" && (p.ThirdPartyWorkOrder == nil || *p.ThirdPartyWorkOrder == "") {
		hints = append(hints, "t vendor")
	}
	return strings.Join(append(hints, "esc/enter back"), " · ")
}

// apPromotedShortID is the short id of whichever work order the report was
// promoted to, for the list row's "→ WO-0042" tail. Empty when un-promoted.
func apPromotedShortID(p omsapi.AssetProblem) string {
	if p.WorkOrderShortID != "" {
		return p.WorkOrderShortID
	}
	return p.ThirdPartyWorkOrderShortID
}

func apStatusStyle(status string) lipgloss.Style {
	switch status {
	case omsapi.AssetProblemReported:
		return StyleStatusWarn
	case omsapi.AssetProblemInProgress:
		return StyleTitle
	case omsapi.AssetProblemResolved, omsapi.AssetProblemClosed:
		return StyleStatusOK
	default:
		return StyleMuted
	}
}

// activeVendors drops retired vendors from the pick-list — the backend accepts
// any vendor id, but sending new work to a deactivated one is never intended.
func activeVendors(rows []omsapi.Vendor) []omsapi.Vendor {
	out := make([]omsapi.Vendor, 0, len(rows))
	for _, v := range rows {
		if v.IsActive {
			out = append(out, v)
		}
	}
	return out
}

// labelSuffix renders " (WO-0042)" for a non-empty label, or "" — keeps the
// status line clean when the serializer had no short id to give.
func labelSuffix(label string) string {
	if strings.TrimSpace(label) == "" {
		return ""
	}
	return " (" + label + ")"
}

// derefOr reads a nullable string FK, falling back when it is nil/blank.
func derefOr(p *string, fallback string) string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return fallback
	}
	return *p
}
