package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type AssetDetailScreen struct {
	deps        Deps
	assetID     string
	asset       *omsapi.Asset
	maintenance []omsapi.MaintenanceItem
	problems    []omsapi.AssetProblem
	workOrders  []omsapi.WorkOrder
	loading     bool
	loadErr     string

	logging      bool
	logInput     textinput.Model
	logResult    string
	logResultLvl StatusLevel

	scroller *TextScroller
}

type assetDetailLoadedMsg struct {
	asset       *omsapi.Asset
	maintenance []omsapi.MaintenanceItem
	problems    []omsapi.AssetProblem
	workOrders  []omsapi.WorkOrder
	err         error
}

type problemLoggedMsg struct {
	problem *omsapi.AssetProblem
	err     error
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

func (s *AssetDetailScreen) WantsRawInput() bool { return s.logging }

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
		return out
	}
}

func (s *AssetDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.scroller.SetViewHeight(m.Height - detailChromeRows)
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
		s.scroller.Set(s.renderBody())
		return s, nil
	case problemLoggedMsg:
		s.logging = false
		if m.err != nil {
			s.logResult = "log failed: " + m.err.Error()
			s.logResultLvl = StatusError
			return s, Status(s.logResult, StatusError)
		}
		s.logResult = "problem logged"
		s.logResultLvl = StatusOK
		s.logInput.SetValue("")
		return s, tea.Batch(Status(s.logResult, StatusOK), s.load())
	case tea.KeyMsg:
		if s.logging {
			switch m.Type {
			case tea.KeyEsc:
				s.logging = false
				return s, nil
			case tea.KeyEnter:
				return s.submitProblem()
			}
			var cmd tea.Cmd
			s.logInput, cmd = s.logInput.Update(msg)
			return s, cmd
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "p":
			ti := textinput.New()
			ti.Prompt = ""
			ti.Placeholder = "describe the problem"
			ti.CharLimit = 1000
			ti.Focus()
			s.logInput = ti
			s.logging = true
			return s, textinput.Blink
		}
	}
	return s, nil
}

func (s *AssetDetailScreen) submitProblem() (Screen, tea.Cmd) {
	desc := strings.TrimSpace(s.logInput.Value())
	if desc == "" {
		s.logResult = "description required"
		s.logResultLvl = StatusError
		return s, nil
	}
	s.logResult = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	req := omsapi.AssetProblemCreate{Asset: s.assetID, Description: desc}
	return s, func() tea.Msg {
		out, err := deps.OMS.CreateAssetProblem(ctx, req)
		return problemLoggedMsg{problem: out, err: err}
	}
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

	if s.logging {
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Log a problem") + "\n")
		b.WriteString(s.logInput.View() + "\n")
		if s.logResult != "" {
			b.WriteString(RenderStatus(s.logResult, s.logResultLvl) + "\n")
		}
		b.WriteString(StyleMuted.Render("\nenter submit · esc cancel"))
		return b.String()
	}

	body := s.scroller.View()
	footer := ""
	if s.logResult != "" {
		footer += RenderStatus(s.logResult, s.logResultLvl) + "\n\n"
	}
	footer += StyleMuted.Render("j/k scroll · pgup/pgdn page · p log problem · r refresh · esc back")
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
		if a.PowerDrawWatts != nil && *a.PowerDrawWatts > 0 {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("Power draw: %d W", *a.PowerDrawWatts)) + "\n")
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
			if p.Status == "reported" || p.Status == "in_progress" {
				open++
			}
		}
		if open > 0 {
			b.WriteString(StyleStatusWarn.Render(fmt.Sprintf("⚠ %d open problem(s)", open)) + "\n")
		}
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Problems (%d)", len(s.problems))) + "\n")
		for _, p := range s.problems {
			marker := "  · "
			if p.Status == "reported" {
				marker = StyleStatusWarn.Render("  ● ")
			} else if p.Status == "in_progress" {
				marker = StyleMuted.Render("  ○ ")
			} else if p.Status == "resolved" || p.Status == "closed" {
				marker = StyleStatusOK.Render("  ✓ ")
			}
			line := marker + p.Description
			if p.ReportedBy != "" {
				line += " " + StyleMuted.Render("("+p.ReportedBy+")")
			}
			b.WriteString(line + "\n")
			if !p.CreatedAt.IsZero() {
				b.WriteString("    " + StyleMuted.Render(p.CreatedAt.Format("2006-01-02 15:04")) + "\n")
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
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Parts (%d)", len(a.Parts))) + "\n")
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

func hasOperationalReqs(a *omsapi.Asset) bool {
	if a.Circuit != "" || a.NeedsCompressedAir || a.NeedsVentilation || a.IsChargeable {
		return true
	}
	if a.PowerDrawWatts != nil && *a.PowerDrawWatts > 0 {
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
