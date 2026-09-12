package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type assetWriteForm int

const (
	formNone assetWriteForm = iota
	formLogProblem
	formMarkOOS
	formProblemParts // phase 2 of the problem flow: pick affected components
)

type AssetDetailScreen struct {
	deps         Deps
	assetID      string
	asset        *omsapi.Asset
	maintenance  []omsapi.MaintenanceItem
	problems     []omsapi.AssetProblem
	workOrders   []omsapi.WorkOrder
	powerChain   *omsapi.AssetPowerChain
	loto         *omsapi.AssetLOTORequirements
	reservations []omsapi.AssetReservation
	oos          []omsapi.AssetOutOfService
	components   []omsapi.SerializedComponent
	loading      bool
	loadErr      string

	activeForm   assetWriteForm
	input        textinput.Model
	logResult    string
	logResultLvl StatusLevel

	// report-problem "which components need attention?" checklist (phase 2).
	// problemDesc holds the description captured in phase 1; partSelected is
	// keyed by the stringified AssetPart id.
	problemDesc  string
	problemParts []omsapi.AssetPart
	partCursor   int
	partSelected map[string]bool

	confirmingDelete bool
	deleting         bool

	scroller       *TextScroller
	terminalHeight int
}

type assetDetailLoadedMsg struct {
	asset        *omsapi.Asset
	maintenance  []omsapi.MaintenanceItem
	problems     []omsapi.AssetProblem
	workOrders   []omsapi.WorkOrder
	powerChain   *omsapi.AssetPowerChain
	loto         *omsapi.AssetLOTORequirements
	reservations []omsapi.AssetReservation
	oos          []omsapi.AssetOutOfService
	components   []omsapi.SerializedComponent
	err          error
}

type problemLoggedMsg struct {
	problem *omsapi.AssetProblem
	err     error
}

type oosMarkedMsg struct {
	oos *omsapi.AssetOutOfService
	err error
}

type oosRestoredMsg struct {
	oos *omsapi.AssetOutOfService
	err error
}

type assetDeletedMsg struct {
	err error
}

func NewAssetDetailScreen(deps Deps, id string) *AssetDetailScreen {
	return &AssetDetailScreen{
		deps:     deps,
		assetID:  id,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *AssetDetailScreen) Title() string {
	if s.asset != nil {
		return fmt.Sprintf("Asset: %s", s.asset.Name)
	}
	return "Asset"
}

// WantsRawInput claims every keypress while an inline form is open OR the
// delete confirmation is up, so esc/y/n land here instead of the root's global
// hotkeys. In the normal view the screen stays non-raw so workspace switching
// and the global shortcuts keep working.
func (s *AssetDetailScreen) WantsRawInput() bool {
	return s.activeForm != formNone || s.confirmingDelete
}

// HandlesKey claims P (open this asset's problems list), which would otherwise
// be swallowed by the global PM-board hotkey. Only P is claimed: the screen's
// other letters either have no global twin or predate the LocalKeyScreen
// mechanism, and widening the claim would change keys this bead did not touch.
func (s *AssetDetailScreen) HandlesKey(key string) bool { return key == "P" }

func (s *AssetDetailScreen) Init() tea.Cmd { return s.load() }

func (s *AssetDetailScreen) load() tea.Cmd {
	deps := s.deps
	id := s.assetID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		a, err := deps.OMS.GetAsset(ctx, id)
		if err != nil {
			return assetDetailLoadedMsg{err: err}
		}
		out := assetDetailLoadedMsg{asset: a}
		q := url.Values{"asset": []string{id}}
		if miPage, err := deps.OMS.ListMaintenanceItems(ctx, q); err == nil && miPage != nil {
			out.maintenance = miPage.Results
		}
		if probPage, err := deps.OMS.ListAssetProblems(ctx, q); err == nil && probPage != nil {
			out.problems = probPage.Results
		}
		if woPage, err := deps.OMS.ListWorkOrders(ctx, q); err == nil && woPage != nil {
			out.workOrders = woPage.Results
		}
		// Power chain is staff-only — non-staff get 403; treat as "no
		// data" rather than an error so a member viewing the page still
		// sees everything else.
		if pc, err := deps.OMS.GetAssetPowerChain(ctx, id); err == nil {
			out.powerChain = pc
		}
		// LOTO requirements: 404 is the expected shape for an asset with
		// no isolation steps registered yet, so swallow errors here and
		// let the renderer decide whether to draw the section.
		if loto, err := deps.OMS.GetAssetLOTORequirements(ctx, id); err == nil {
			out.loto = loto
		}
		if resvPage, err := deps.OMS.ListAssetReservations(ctx, q); err == nil && resvPage != nil {
			out.reservations = resvPage.Results
		}
		if oosPage, err := deps.OMS.ListAssetOutOfService(ctx, q); err == nil && oosPage != nil {
			out.oos = oosPage.Results
		}
		// Serialized components installed in this asset. Read-only summary
		// here; press `i` for the actionable per-unit screen.
		if comps, err := deps.OMS.ListSerializedComponents(ctx, url.Values{"installed_in_asset": []string{id}}); err == nil {
			out.components = comps
		}
		return out
	}
}

func (s *AssetDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case assetDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.asset = m.asset
		s.maintenance = m.maintenance
		s.problems = m.problems
		s.workOrders = m.workOrders
		s.powerChain = m.powerChain
		s.loto = m.loto
		s.reservations = m.reservations
		s.oos = m.oos
		s.components = m.components
		s.scroller.Set(s.renderBody())
		return s, nil
	case problemLoggedMsg:
		s.activeForm = formNone
		if m.err != nil {
			s.logResult = "log failed: " + m.err.Error()
			s.logResultLvl = StatusError
			return s, Status(s.logResult, StatusError)
		}
		s.logResult = "problem logged"
		s.logResultLvl = StatusOK
		s.input.SetValue("")
		return s, tea.Batch(Status(s.logResult, StatusOK), s.load())
	case oosMarkedMsg:
		s.activeForm = formNone
		if m.err != nil {
			s.logResult = "OOS failed: " + m.err.Error()
			s.logResultLvl = StatusError
			return s, Status(s.logResult, StatusError)
		}
		s.logResult = "marked out of service"
		s.logResultLvl = StatusOK
		s.input.SetValue("")
		return s, tea.Batch(Status(s.logResult, StatusOK), s.load())
	case oosRestoredMsg:
		if m.err != nil {
			s.logResult = "restore failed: " + m.err.Error()
			s.logResultLvl = StatusError
			return s, Status(s.logResult, StatusError)
		}
		s.logResult = "restored"
		s.logResultLvl = StatusOK
		return s, tea.Batch(Status(s.logResult, StatusOK), s.load())
	case assetDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("asset deleted", StatusOK),
			SwitchTo(WSAssets, newScreenFor(WSAssets, s.deps)),
		)
	case tea.KeyMsg:
		if s.activeForm == formProblemParts {
			return s.updateProblemParts(m)
		}
		if s.activeForm != formNone {
			switch m.Type {
			case tea.KeyEsc:
				s.activeForm = formNone
				return s, nil
			case tea.KeyEnter:
				return s.submitForm()
			}
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			return s, cmd
		}
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "E":
			// Edit opens the create/edit form in edit mode. Uppercase E
			// because lowercase e is the global e-paper hotkey.
			if s.asset != nil {
				return s, SwitchTo(WSAssets, NewAssetFormScreen(s.deps, s.assetID))
			}
		case "x":
			// Delete (with confirm). The web supports asset delete; guard it
			// behind a y/n prompt since it's destructive.
			if s.asset != nil {
				s.confirmingDelete = true
				return s, nil
			}
		case "p":
			s.openForm(formLogProblem, "describe the problem", 1000)
			return s, textinput.Blink
		case "P":
			// Open the actionable problems list (resolve / promote to a work
			// order / send to a vendor). Uppercase P pairs with lowercase p =
			// report a problem, the same upper/lower pairing N/n and I/i use;
			// it shadows the global PM board on this screen only (see
			// HandlesKey). Always available so the empty state is reachable.
			if s.asset != nil {
				return s, SwitchTo(WSAssets, NewAssetProblemsScreen(s.deps, s.assetID, s.asset.Name))
			}
		case "o":
			if s.openOOS() != nil {
				s.logResult = "already out of service"
				s.logResultLvl = StatusWarn
				return s, Status(s.logResult, StatusWarn)
			}
			s.openForm(formMarkOOS, "reason (e.g. spindle bearing seized)", 500)
			return s, textinput.Blink
		case "R":
			open := s.openOOS()
			if open == nil {
				s.logResult = "no open OOS to restore"
				s.logResultLvl = StatusWarn
				return s, Status(s.logResult, StatusWarn)
			}
			return s, s.restoreOOS(open.ID)
		case "i":
			// Open the actionable serialized-components screen for this
			// asset (remove/consume/retire/dispose installed units).
			if len(s.components) == 0 {
				return s, Status("no serialized components installed", StatusWarn)
			}
			return s, SwitchTo(WSAssets, NewAssetComponentsScreen(s.deps, s.assetID, s.asset.Name))
		case "S":
			// Open the parts management list (create/edit/delete + mark
			// replaced). Uppercase S (for the web's "supplies" section) since
			// lowercase p is log-problem and P is the global PM board. Always
			// available so an asset with no parts yet can add its first one.
			if s.asset != nil {
				return s, SwitchTo(WSAssets, NewAssetPartsScreen(s.deps, s.assetID, s.asset.Name))
			}
		case "M":
			// Open the usage meters — record a reading, post a correction, read
			// the ledger. Uppercase M pairs with the uppercase sibling-surface
			// letters already here (P, S, R) rather than with the lowercase ones
			// that act on this sheet; no global claims it, since phase 3 of the
			// redesign left the root holding no letter at all. Always available
			// so an asset with no meters yet can have its first one defined.
			if s.asset != nil {
				return s, SwitchTo(WSAssets, NewAssetMetersScreen(s.deps, s.assetID, s.asset.Name))
			}
		case "D":
			// Open the document library. Uppercase D for the same reason, and
			// always available so an empty library is reachable to upload into.
			if s.asset != nil {
				return s, SwitchTo(WSAssets, NewAssetDocumentsScreen(s.deps, s.assetID, s.asset.Name))
			}
		}
	}
	return s, nil
}

func (s *AssetDetailScreen) openForm(kind assetWriteForm, placeholder string, limit int) {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.CharLimit = limit
	ti.Focus()
	s.input = ti
	s.activeForm = kind
	s.logResult = ""
}

// updateConfirmDelete handles the y/n prompt shown before deleting an asset.
// The screen is in raw-input mode here (WantsRawInput), so esc/n reach us
// instead of the root's global handlers.
func (s *AssetDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.asset == nil {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := s.assetID
		return s, func() tea.Msg {
			return assetDeletedMsg{err: deps.OMS.DeleteAsset(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *AssetDetailScreen) openOOS() *omsapi.AssetOutOfService {
	for i := range s.oos {
		if s.oos[i].IsOpen {
			return &s.oos[i]
		}
	}
	return nil
}

func (s *AssetDetailScreen) submitForm() (Screen, tea.Cmd) {
	switch s.activeForm {
	case formLogProblem:
		return s.submitProblem()
	case formMarkOOS:
		return s.submitOOS()
	}
	return s, nil
}

func (s *AssetDetailScreen) submitOOS() (Screen, tea.Cmd) {
	reason := strings.TrimSpace(s.input.Value())
	if reason == "" {
		s.logResult = "reason required"
		s.logResultLvl = StatusError
		return s, nil
	}
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	body := omsapi.OpenAssetOOS{Asset: s.assetID, Reason: reason}
	return s, func() tea.Msg {
		out, err := deps.OMS.OpenAssetOutOfService(ctx, body)
		return oosMarkedMsg{oos: out, err: err}
	}
}

func (s *AssetDetailScreen) restoreOOS(id string) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		out, err := deps.OMS.RestoreAssetOutOfService(ctx, id)
		return oosRestoredMsg{oos: out, err: err}
	}
}

func (s *AssetDetailScreen) submitProblem() (Screen, tea.Cmd) {
	desc := strings.TrimSpace(s.input.Value())
	if desc == "" {
		s.logResult = "description required"
		s.logResultLvl = StatusError
		return s, nil
	}
	s.logResult = ""
	// When the asset carries parts, offer the optional "which components need
	// attention?" checklist before sending. Otherwise report description-only.
	if parts := s.reportableParts(); len(parts) > 0 {
		s.problemDesc = desc
		s.problemParts = parts
		s.partCursor = 0
		s.partSelected = make(map[string]bool, len(parts))
		s.activeForm = formProblemParts
		return s, nil
	}
	return s, s.sendProblem(desc, nil)
}

// reportableParts returns the asset's parts, already loaded nested on the Asset
// payload, so the report-problem checklist can list selectable components
// without a second round-trip.
func (s *AssetDetailScreen) reportableParts() []omsapi.AssetPart {
	if s.asset == nil {
		return nil
	}
	return s.asset.Parts
}

// updateProblemParts drives the optional "which components need attention?"
// checklist shown after the problem description. space toggles the highlighted
// part, enter submits (with or without a selection), esc cancels the report.
func (s *AssetDetailScreen) updateProblemParts(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.activeForm = formNone
		return s, nil
	case "up", "k":
		if s.partCursor > 0 {
			s.partCursor--
		}
		return s, nil
	case "down", "j":
		if s.partCursor < len(s.problemParts)-1 {
			s.partCursor++
		}
		return s, nil
	case " ":
		if s.partCursor >= 0 && s.partCursor < len(s.problemParts) {
			key := assetPartKey(s.problemParts[s.partCursor])
			s.partSelected[key] = !s.partSelected[key]
		}
		return s, nil
	case "enter":
		ids := make([]string, 0, len(s.partSelected))
		for _, p := range s.problemParts {
			if key := assetPartKey(p); s.partSelected[key] {
				ids = append(ids, key)
			}
		}
		return s, s.sendProblem(s.problemDesc, ids)
	}
	return s, nil
}

// sendProblem fires the report_problem request with the captured description
// and any flagged part ids. The form stays visible until problemLoggedMsg
// lands (mirroring the description-only path).
func (s *AssetDetailScreen) sendProblem(desc string, partIDs []string) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	req := omsapi.AssetProblemCreate{Asset: s.assetID, Description: desc, PartIds: partIDs}
	return func() tea.Msg {
		out, err := deps.OMS.CreateAssetProblem(ctx, req)
		return problemLoggedMsg{problem: out, err: err}
	}
}

// assetPartKey stringifies an AssetPart's polymorphic id for use as a
// selection-map key and as the part_ids value the backend coerces back to an
// int. Mirrors the wo_detail task/material toggle idiom.
//
// %v over the `any` rather than a type switch, because the key only has to be
// STABLE within a frame and round-trip to something the backend can coerce —
// which it is for any representation the decoder picks. This comment named
// float64 as that representation until omsapi's jsonDecoder set UseNumber; the
// site got quietly BETTER rather than worse, since %v on a json.Number is the
// server's own digits where %v on a float64 would have given "1.234567e+06" for
// a seven-digit part pk and posted a part_ids value naming no part.
func assetPartKey(p omsapi.AssetPart) string {
	return fmt.Sprintf("%v", p.ID)
}

// viewProblemParts renders the optional affected-components checklist. Mirrors
// the Tier-1 form multi-picker idiom: a ▸ caret on the highlighted row, an
// [x]/[ ] box per part, part name (falling back to the raw part id) plus SKU
// and a NEEDS REPLACEMENT flag.
func (s *AssetDetailScreen) viewProblemParts() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Which components need attention?") + "\n")
	b.WriteString(StyleMuted.Render("optional · j/k move · space toggle · enter submit · esc cancel") + "\n\n")
	for i, p := range s.problemParts {
		caret := "    "
		if i == s.partCursor {
			caret = "  ▸ "
		}
		box := "[ ] "
		if s.partSelected[assetPartKey(p)] {
			box = "[x] "
		}
		name := p.PartName
		if name == "" {
			name = p.Part
		}
		row := caret + box + name
		if i == s.partCursor {
			row = StyleSidebarItemActive.Render(row)
		}
		if p.PartSKU != "" {
			row += " " + StyleMuted.Render("("+p.PartSKU+")")
		}
		if p.NeedsReplacement {
			row += " " + StyleStatusWarn.Render("NEEDS REPLACEMENT")
		}
		b.WriteString(row + "\n")
	}
	if s.logResult != "" {
		b.WriteString("\n" + RenderStatus(s.logResult, s.logResultLvl))
	}
	return b.String()
}

func (s *AssetDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading asset…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	if s.asset == nil {
		return StyleMuted.Render("Asset not found.")
	}

	if s.activeForm == formProblemParts {
		return s.viewProblemParts()
	}
	if s.activeForm != formNone {
		var b strings.Builder
		switch s.activeForm {
		case formLogProblem:
			b.WriteString(StyleTitle.Render("Log a problem") + "\n")
		case formMarkOOS:
			b.WriteString(StyleTitle.Render("Mark out of service") + "\n")
		}
		b.WriteString(s.input.View() + "\n")
		if s.logResult != "" {
			b.WriteString(RenderStatus(s.logResult, s.logResultLvl) + "\n")
		}
		b.WriteString(StyleMuted.Render("\nenter submit · esc cancel"))
		return b.String()
	}

	// Resize the scroller against the current terminal height + footer
	// shape at render time, not just on WindowSizeMsg. That way the
	// scroller automatically reflows when the action banner appears or
	// clears without each state change having to re-budget.
	footerRows := detailFooterRows
	if s.logResult != "" {
		footerRows = detailFooterRowsWithAction
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, footerRows))

	if s.confirmingDelete {
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Deleting…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Delete %q? This can't be undone.  y delete · n/esc cancel", s.asset.Name))
		}
		return s.scroller.View() + "\n\n" + prompt
	}

	body := s.scroller.View()
	footer := ""
	if s.logResult != "" {
		footer += RenderStatus(s.logResult, s.logResultLvl) + "\n\n"
	}
	// M and D are NAMED here because they act here. This footer is a muted
	// literal written straight into View and is already past the 51 cells an
	// 80-column pane gives — the prose-footer gap AGENTS.md records for every
	// receiver in listNavUnsweptReceivers, which no bar sweep can read. Naming
	// two more keys does not close that gap and is not meant to; leaving a key
	// that acts unnamed would break the rule outright rather than inherit it.
	hint := "j/k scroll · p report · P problems · o OOS · R restore · M meters · D documents · S parts · E edit · x delete · r refresh · esc back"
	if len(s.components) > 0 {
		hint = "j/k scroll · p report · P problems · o OOS · R restore · i components · M meters · D documents · S parts · E edit · x delete · r refresh · esc back"
	}
	footer += StyleMuted.Render(hint)
	return body + "\n\n" + footer
}

func (s *AssetDetailScreen) renderBody() string {
	a := s.asset
	var b strings.Builder

	b.WriteString(StyleTitle.Render(a.Name))
	if a.IsCritical {
		b.WriteString("  " + StyleStatusError.Render("critical"))
	}
	if a.IsLocked {
		b.WriteString("  " + StyleStatusWarn.Render("locked"))
	}
	if a.IsForgeKeyManaged {
		b.WriteString("  " + StyleMuted.Render("[ForgeKey]"))
	}
	b.WriteString("\n")

	idLine := []string{fmt.Sprintf("ID %v", a.ID)}
	if a.AssetTag != "" {
		idLine = append(idLine, a.AssetTag)
	}
	if a.Status != "" {
		idLine = append(idLine, "status "+a.Status)
	}
	if a.CategoryName != "" {
		idLine = append(idLine, a.CategoryName)
	}
	b.WriteString(StyleMuted.Render(strings.Join(idLine, " · ")) + "\n")

	if a.LocationName != "" {
		b.WriteString(StyleMuted.Render("Location: ") + a.LocationName + "\n")
	}
	if a.DisplayManufacturer != "" {
		b.WriteString(StyleMuted.Render("Manufacturer: ") + a.DisplayManufacturer + "\n")
	}
	if a.InventoryItemName != "" {
		b.WriteString(StyleMuted.Render("Type: ") + a.InventoryItemName + "\n")
	}
	if a.SerialNumber != "" {
		b.WriteString(StyleMuted.Render("Serial #: ") + a.SerialNumber + "\n")
	}
	if a.MACAddress != "" {
		b.WriteString(StyleMuted.Render("MAC: ") + a.MACAddress + "\n")
	}

	if a.Description != "" {
		b.WriteString("\n" + a.Description + "\n")
	}
	b.WriteString("\n")

	// OOS banner — surface before anything else so a tech walking up
	// with a scanner sees "this is broken" before they read serials.
	if open := s.openOOS(); open != nil {
		b.WriteString(StyleStatusError.Render("OUT OF SERVICE") + "\n")
		meta := []string{}
		if !open.PlacedOutAt.IsZero() {
			meta = append(meta, "since "+open.PlacedOutAt.Format("2006-01-02 15:04"))
		}
		if open.PlacedByUsername != "" {
			meta = append(meta, "by "+open.PlacedByUsername)
		}
		if open.ExpectedReturnAt != nil {
			meta = append(meta, "back "+open.ExpectedReturnAt.Format("2006-01-02"))
		}
		if len(meta) > 0 {
			b.WriteString(StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
		}
		if open.Reason != "" {
			b.WriteString(open.Reason + "\n")
		}
		b.WriteString("\n")
	}

	// Reservations — active / upcoming entries above scheduled
	// maintenance so a tech sees "this is reserved for welding class
	// in 2h" before booking themselves into an hour-long repair.
	if active := s.activeReservations(); len(active) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Reservations (%d)", len(active))) + "\n")
		for _, r := range active {
			marker := "  · "
			if r.IsCurrent {
				marker = StyleStatusWarn.Render("  ● ")
			}
			line := marker + r.Title
			if r.IsCurrent {
				line += " " + StyleStatusWarn.Render("NOW")
			}
			b.WriteString(line + "\n")
			window := r.StartsAt.Format("2006-01-02 15:04") + " → " + r.EndsAt.Format("2006-01-02 15:04")
			meta := []string{window}
			if r.ReservedByUsername != "" {
				meta = append(meta, "by "+r.ReservedByUsername)
			}
			b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			if r.Notes != "" {
				b.WriteString("    " + StyleMuted.Render(r.Notes) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// Training / certification gate — surface near the top so a tech
	// walking up with a scanner sees "this needs the laser cert"
	// before they start an unlock flow that's going to 403. Matches
	// the e-paper render: cert names when wired, generic
	// "TRAINING REQUIRED" when only the boolean is set.
	if a.TrainingRequired || len(a.RequiredCertificationDetails) > 0 {
		if len(a.RequiredCertificationDetails) > 0 {
			names := make([]string, 0, len(a.RequiredCertificationDetails))
			for _, c := range a.RequiredCertificationDetails {
				names = append(names, c.Name)
			}
			b.WriteString(StyleStatusError.Render("REQ: "+strings.Join(names, " · ")) + "\n\n")
		} else {
			b.WriteString(StyleStatusError.Render("TRAINING REQUIRED") + "\n\n")
		}
	}

	// Cost / acquisition
	if !a.AmountPaid.Empty() || a.IsDonation || a.DateReceived != "" || a.AgeInDays != nil || a.AcquisitionDisplay != "" {
		b.WriteString(StyleTitle.Render("Cost & Acquisition") + "\n")
		if a.AcquisitionDisplay != "" {
			b.WriteString(StyleMuted.Render("Acquired: ") + a.AcquisitionDisplay + "\n")
		}
		if !a.AmountPaid.Empty() {
			b.WriteString(StyleMuted.Render("Amount paid: ") + "$" + string(a.AmountPaid) + "\n")
		}
		if a.IsDonation {
			donation := "Yes"
			if a.DonorName != "" {
				donation += " (" + a.DonorName + ")"
			}
			b.WriteString(StyleMuted.Render("Donation: ") + donation + "\n")
		}
		if a.DateReceived != "" {
			b.WriteString(StyleMuted.Render("Date received: ") + a.DateReceived + "\n")
		}
		if a.AgeInDays != nil {
			years := *a.AgeInDays / 365
			days := *a.AgeInDays % 365
			if years > 0 {
				b.WriteString(StyleMuted.Render(fmt.Sprintf("Age: %d years, %d days", years, days)) + "\n")
			} else {
				b.WriteString(StyleMuted.Render(fmt.Sprintf("Age: %d days", days)) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// Operational requirements
	if hasOperationalReqs(a) {
		b.WriteString(StyleTitle.Render("Operational Requirements") + "\n")
		if a.Circuit != "" {
			b.WriteString(StyleMuted.Render("Circuit: ") + a.Circuit + "\n")
		}
		if !a.PowerDrawWatts.Empty() {
			b.WriteString(StyleMuted.Render("Power draw: ") + string(a.PowerDrawWatts) + " W\n")
		}
		if a.WiringType != "" {
			b.WriteString(StyleMuted.Render("Wiring: ") + a.WiringType + "\n")
		}
		if a.ElectricalBox != "" {
			b.WriteString(StyleMuted.Render("Electrical box: ") + a.ElectricalBox + "\n")
		}
		if a.BreakerLocation != "" {
			b.WriteString(StyleMuted.Render("Breaker: ") + a.BreakerLocation + "\n")
		}
		if a.NeedsCompressedAir {
			b.WriteString(StyleMuted.Render("Needs compressed air: Yes") + "\n")
		}
		if a.NeedsVentilation {
			b.WriteString(StyleMuted.Render("Needs ventilation: Yes") + "\n")
		}
		if a.GeneratesHeatOrFlame {
			b.WriteString(StyleMuted.Render("Generates heat or flame: Yes") + "\n")
		}
		if a.NeedsChilling {
			b.WriteString(StyleMuted.Render("Needs chilling: Yes") + "\n")
		}
		if a.IsChargeable {
			b.WriteString(StyleMuted.Render("Chargeable: Yes") + "\n")
		}
		if a.HasNetworkDrop {
			line := "Network drop: Yes"
			if a.NetworkDropLocation != "" {
				line += " (" + a.NetworkDropLocation + ")"
			}
			b.WriteString(StyleMuted.Render(line) + "\n")
		}
		if a.HasInterlock {
			line := "Interlock: " + a.InterlockType
			if a.InterlockResponsible != "" {
				line += " (resp: " + a.InterlockResponsible + ")"
			}
			b.WriteString(StyleMuted.Render(strings.TrimSpace(line)) + "\n")
		}
		if a.LockoutType != "" {
			b.WriteString(StyleMuted.Render("Lockout type: ") + a.LockoutType + "\n")
		}
		if a.LockoutResponsible != "" {
			b.WriteString(StyleMuted.Render("Lockout responsible: ") + a.LockoutResponsible + "\n")
		}
		if a.LockoutInstructions != "" {
			b.WriteString(StyleMuted.Render("Lockout instructions:") + "\n")
			for _, line := range strings.Split(a.LockoutInstructions, "\n") {
				b.WriteString("  " + line + "\n")
			}
		}
		if a.SpecialRequirements != "" {
			b.WriteString(StyleMuted.Render("Special requirements:") + "\n")
			for _, line := range strings.Split(a.SpecialRequirements, "\n") {
				b.WriteString("  " + line + "\n")
			}
		}
		if a.WorkSafetyNotes != "" {
			b.WriteString(StyleMuted.Render("Work safety notes:") + "\n")
			for _, line := range strings.Split(a.WorkSafetyNotes, "\n") {
				b.WriteString("  " + line + "\n")
			}
		}
		b.WriteString("\n")
	}

	// Power chain — the resolved upstream path from this asset back to
	// its main feed. Answers "which breaker do I throw to kill this
	// machine" without making someone interpret BreakerLocation free
	// text. Staff-only on the backend; non-staff render as nil here.
	if s.powerChain != nil && len(s.powerChain.Chain) > 0 {
		b.WriteString(StyleTitle.Render("Power chain") + "\n")
		for i, hop := range s.powerChain.Chain {
			marker := "  "
			if i > 0 {
				marker = "    ↳ "
			}
			line := marker + hop.Label
			meta := strings.TrimSpace(hop.Kind)
			if meta != "" && meta != hop.Label {
				line += " " + StyleMuted.Render("["+meta+"]")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// Ownership
	if a.OwningGroupName != "" || a.OwningUserName != "" || len(a.GroupsCanEnable) > 0 {
		b.WriteString(StyleTitle.Render("Ownership") + "\n")
		if a.OwningGroupName != "" {
			b.WriteString(StyleMuted.Render("Owning group: ") + a.OwningGroupName + "\n")
		}
		if a.OwningUserName != "" {
			b.WriteString(StyleMuted.Render("Owning user: ") + a.OwningUserName + "\n")
		}
		if len(a.GroupsCanEnable) > 0 {
			ids := make([]string, len(a.GroupsCanEnable))
			for i, g := range a.GroupsCanEnable {
				ids[i] = fmt.Sprintf("%d", g)
			}
			b.WriteString(StyleMuted.Render("Groups can enable: ") + strings.Join(ids, ", ") + "\n")
		}
		b.WriteString("\n")
	}

	// Lock status
	if a.IsLocked && a.LockoutInfo != nil {
		b.WriteString(StyleStatusWarn.Render("⚠ Locked out") + "\n")
		info := a.LockoutInfo
		if info.LockedBy != "" {
			b.WriteString(StyleMuted.Render("Locked by: ") + info.LockedBy + "\n")
		}
		if info.LockedAt != "" {
			b.WriteString(StyleMuted.Render("Locked at: ") + info.LockedAt + "\n")
		}
		if info.LockoutLevel != "" {
			b.WriteString(StyleMuted.Render("Level: ") + info.LockoutLevel + "\n")
		}
		if info.Reason != "" {
			b.WriteString(StyleMuted.Render("Reason: ") + info.Reason + "\n")
		}
		b.WriteString("\n")
	}

	// Problems
	if len(s.problems) > 0 {
		open := 0
		for _, p := range s.problems {
			if !p.IsResolved() {
				open++
			}
		}
		if open > 0 {
			b.WriteString(StyleStatusWarn.Render(fmt.Sprintf("⚠ %d open problem(s)", open)) + "\n")
		}
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Problems (%d)", len(s.problems))))
		b.WriteString("  " + StyleMuted.Render("press P to resolve / promote") + "\n")
		for _, p := range s.problems {
			marker := "  · "
			switch p.Status {
			case omsapi.AssetProblemReported:
				marker = StyleStatusWarn.Render("  ● ")
			case omsapi.AssetProblemInProgress:
				marker = StyleMuted.Render("  ○ ")
			case omsapi.AssetProblemResolved, omsapi.AssetProblemClosed:
				marker = StyleStatusOK.Render("  ✓ ")
			}
			line := marker + p.Description
			if p.ReportedBy != "" {
				line += " " + StyleMuted.Render("("+p.ReportedBy+")")
			}
			if short := apPromotedShortID(p); short != "" {
				line += " " + StyleMuted.Render("→ "+short)
			}
			b.WriteString(line + "\n")
			if len(p.AffectedParts) > 0 {
				names := make([]string, 0, len(p.AffectedParts))
				for _, ap := range p.AffectedParts {
					n := ap.PartName
					if n == "" {
						n = fmt.Sprintf("%v", ap.ID)
					}
					names = append(names, n)
				}
				b.WriteString("    " + StyleMuted.Render("affected: ") + strings.Join(names, ", ") + "\n")
			}
			if !p.CreatedAt.IsZero() {
				b.WriteString("    " + StyleMuted.Render(p.CreatedAt.Format("2006-01-02 15:04")) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// LOTO requirements — show the rolled-up isolation procedure (legacy
	// lockout fields + structured energy sources + required devices).
	// Maintenance techs see this between Operational Requirements and
	// Scheduled maintenance so the "what do I lock out first" answer
	// sits right next to "what am I about to do".
	if s.loto != nil && (s.loto.LockoutInstructions != "" || len(s.loto.EnergySources) > 0 || s.loto.IsRequired) {
		title := "Lockout / Tagout"
		if s.loto.IsRequired {
			title += " " + StyleStatusError.Render("REQUIRED")
		}
		b.WriteString(StyleTitle.Render(title) + "\n")

		header := []string{}
		if s.loto.LockoutTypeDisplay != "" {
			header = append(header, s.loto.LockoutTypeDisplay)
		}
		if s.loto.LockoutResponsible != "" {
			header = append(header, "responsible: "+s.loto.LockoutResponsible)
		}
		if len(header) > 0 {
			b.WriteString("  " + StyleMuted.Render(strings.Join(header, " · ")) + "\n")
		}

		if s.loto.LockoutInstructions != "" {
			for _, line := range strings.Split(s.loto.LockoutInstructions, "\n") {
				b.WriteString("  " + line + "\n")
			}
		}

		for _, src := range s.loto.EnergySources {
			label := src.SourceTypeDisplay
			if label == "" {
				label = src.SourceType
			}
			line := "  · " + label
			if src.Magnitude != "" {
				line += "  " + StyleMuted.Render(src.Magnitude)
			}
			if src.IsStale {
				line += "  " + StyleStatusWarn.Render("STALE")
			}
			b.WriteString(line + "\n")
			if src.IsolationPoint != "" {
				b.WriteString("    " + StyleMuted.Render("isolate at: ") + src.IsolationPoint + "\n")
			}
			for _, dev := range src.RequiredDevicesDetail {
				devLabel := dev.Label
				if devLabel == "" {
					devLabel = dev.DeviceTypeDisplay
				}
				status := ""
				if dev.Status != "" && dev.Status != "available" {
					status = "  " + StyleStatusWarn.Render(dev.Status)
				}
				b.WriteString("    " + StyleMuted.Render("device: ") + devLabel + status + "\n")
			}
			if src.Notes != "" {
				b.WriteString("    " + StyleMuted.Render(src.Notes) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// Maintenance items (scheduled)
	if len(s.maintenance) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Scheduled maintenance (%d)", len(s.maintenance))) + "\n")
		for _, m := range s.maintenance {
			line := "  · " + m.Title
			if m.IntervalDays != nil {
				line += " " + StyleMuted.Render(fmt.Sprintf("(every %dd)", *m.IntervalDays))
			}
			if m.IsOverdue {
				line += " " + StyleStatusWarn.Render("OVERDUE")
				if m.DaysOverdue != nil {
					line += StyleStatusWarn.Render(fmt.Sprintf(" by %dd", *m.DaysOverdue))
				}
			}
			b.WriteString(line + "\n")
			if m.NextDueAt != nil {
				b.WriteString("    " + StyleMuted.Render("next due: "+m.NextDueAt.Format("2006-01-02")) + "\n")
			}
			if m.LastCompletedAt != nil {
				b.WriteString("    " + StyleMuted.Render("last completed: "+m.LastCompletedAt.Format("2006-01-02")) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// Parts
	if len(a.Parts) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Parts (%d)", len(a.Parts))))
		b.WriteString("  " + StyleMuted.Render("press S to manage") + "\n")
		for _, p := range a.Parts {
			name := p.PartName
			if name == "" {
				name = p.Part
			}
			line := "  · " + name
			if p.PartSKU != "" {
				line += " " + StyleMuted.Render("("+p.PartSKU+")")
			}
			if p.NeedsReplacement {
				line += " " + StyleStatusWarn.Render("NEEDS REPLACEMENT")
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if p.MaintenanceIntervalDays != nil {
				meta = append(meta, fmt.Sprintf("every %dd", *p.MaintenanceIntervalDays))
			}
			if p.LastReplacedAt != nil {
				meta = append(meta, "last replaced "+p.LastReplacedAt.Format("2006-01-02"))
			}
			if p.DaysSinceReplacement != nil {
				meta = append(meta, fmt.Sprintf("%dd ago", *p.DaysSinceReplacement))
			}
			if p.QuantityNeeded > 0 {
				meta = append(meta, fmt.Sprintf("qty %d", p.QuantityNeeded))
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
			if p.Notes != "" {
				b.WriteString("    " + StyleMuted.Render(p.Notes) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// Serialized components installed in this asset. A read-only roll-up;
	// `i` opens the actionable per-unit screen (remove/consume/retire/…).
	if len(s.components) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Serialized components (%d)", len(s.components))))
		b.WriteString("  " + StyleMuted.Render("press i to manage") + "\n")
		for _, c := range s.components {
			serial := c.SerialNumber
			if serial == "" {
				serial = "(no serial)"
			}
			line := "  · " + serial + " " + renderStatusBadge(c.Status, statusLabel(&c))
			b.WriteString(line + "\n")
			meta := []string{}
			if c.ItemName != "" {
				meta = append(meta, c.ItemName)
			}
			if c.Lot != "" {
				meta = append(meta, "lot "+c.Lot)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
		b.WriteString("\n")
	}

	// Work orders
	if len(s.workOrders) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Work orders (%d)", len(s.workOrders))) + "\n")
		for _, w := range s.workOrders {
			line := "  · " + w.Title + " " + StyleMuted.Render("["+w.Status+"]")
			if w.Priority != "" && w.Priority != "normal" {
				line += " " + StyleMuted.Render("("+w.Priority+")")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// Links
	if a.ProductURL != "" || a.WikiPageURL != "" || a.ManualPDFURL != "" || a.QRCodeScanURL != "" {
		b.WriteString(StyleTitle.Render("Links") + "\n")
		if a.ProductURL != "" {
			b.WriteString(StyleMuted.Render("Product: ") + a.ProductURL + "\n")
		}
		if a.WikiPageURL != "" {
			b.WriteString(StyleMuted.Render("Wiki: ") + a.WikiPageURL + "\n")
		}
		if a.ManualPDFURL != "" {
			b.WriteString(StyleMuted.Render("Manual: ") + a.ManualPDFURL + "\n")
		}
		if a.QRCodeScanURL != "" {
			b.WriteString(StyleMuted.Render("QR scan: ") + a.QRCodeScanURL + "\n")
		}
		if a.ImageURL != "" {
			b.WriteString(StyleMuted.Render("Image: ") + a.ImageURL + "\n")
		}
		b.WriteString("\n")
	}

	// Notes / condition
	if a.Notes != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(a.Notes + "\n\n")
	}
	if a.ConditionNotes != "" {
		b.WriteString(StyleTitle.Render("Condition notes") + "\n")
		b.WriteString(a.ConditionNotes + "\n\n")
	}
	if a.MaintenancePlan != "" {
		b.WriteString(StyleTitle.Render("Maintenance plan") + "\n")
		b.WriteString(a.MaintenancePlan + "\n\n")
	}

	// Metadata footer
	b.WriteString(StyleTitle.Render("Metadata") + "\n")
	if a.LastScannedAt != nil && !a.LastScannedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Last scanned: ") + a.LastScannedAt.Format("2006-01-02 15:04") + "\n")
	}
	if !a.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Created: ") + a.CreatedAt.Format("2006-01-02 15:04") + "\n")
	}
	if !a.UpdatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Updated: ") + a.UpdatedAt.Format("2006-01-02 15:04") + "\n")
	}
	if a.ReportOnly {
		b.WriteString(StyleMuted.Render("Report-only: Yes") + "\n")
	}
	if !a.IsActive {
		b.WriteString(StyleMuted.Render("Active: No") + "\n")
	}

	return b.String()
}

// activeReservations returns reservations that are not cancelled and
// have not yet ended, in chronological order. The backend orders by
// starts_at; we filter here so we don't refetch when the screen state
// is already in hand.
func (s *AssetDetailScreen) activeReservations() []omsapi.AssetReservation {
	now := time.Now()
	out := []omsapi.AssetReservation{}
	for _, r := range s.reservations {
		if r.CancelledAt != nil {
			continue
		}
		if r.EndsAt.Before(now) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func hasOperationalReqs(a *omsapi.Asset) bool {
	if a.Circuit != "" || a.NeedsCompressedAir || a.NeedsVentilation || a.IsChargeable {
		return true
	}
	if a.GeneratesHeatOrFlame || a.NeedsChilling || a.SpecialRequirements != "" || a.WorkSafetyNotes != "" {
		return true
	}
	if !a.PowerDrawWatts.Empty() {
		return true
	}
	if a.WiringType != "" || a.ElectricalBox != "" || a.BreakerLocation != "" {
		return true
	}
	if a.HasInterlock || a.LockoutType != "" || a.HasNetworkDrop {
		return true
	}
	return false
}
