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
	return &AssetDetailScreen{deps: deps, assetID: id, loading: true}
}

func (s *AssetDetailScreen) Title() string {
	if s.asset != nil {
		return fmt.Sprintf("Asset: %s", s.asset.Name)
	}
	return "Asset"
}

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
	case assetDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.asset = m.asset
		s.maintenance = m.maintenance
		s.problems = m.problems
		s.workOrders = m.workOrders
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
	a := s.asset
	var b strings.Builder
	b.WriteString(StyleTitle.Render(a.Name))
	if a.IsCritical {
		b.WriteString("  " + StyleStatusError.Render("critical"))
	}
	b.WriteString("\n")
	tag := ""
	if a.AssetTag != "" {
		tag = " · " + a.AssetTag
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %v%s · status %s", a.ID, tag, a.Status)) + "\n")
	if a.LocationName != "" {
		b.WriteString(StyleMuted.Render("Location: ") + a.LocationName + "\n")
	}
	if a.Description != "" {
		b.WriteString("\n" + a.Description + "\n")
	}
	b.WriteString("\n")

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
		b.WriteString(StyleTitle.Render("Problems") + "\n")
		for _, p := range s.problems {
			marker := "  · "
			if p.Status == "reported" {
				marker = StyleStatusWarn.Render("  ● ")
			} else if p.Status == "in_progress" {
				marker = StyleMuted.Render("  ○ ")
			}
			line := marker + p.Description
			if p.ReportedBy != "" {
				line += " " + StyleMuted.Render("("+p.ReportedBy+")")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(s.maintenance) > 0 {
		b.WriteString(StyleTitle.Render("Maintenance items") + "\n")
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
		}
		b.WriteString("\n")
	}

	if len(s.workOrders) > 0 {
		b.WriteString(StyleTitle.Render("Work orders") + "\n")
		for _, w := range s.workOrders {
			line := "  · " + w.Title + " " + StyleMuted.Render("["+w.Status+"]")
			if w.Priority != "" && w.Priority != "normal" {
				line += " " + StyleMuted.Render("("+w.Priority+")")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if s.logging {
		b.WriteString(StyleTitle.Render("Log a problem") + "\n")
		b.WriteString(s.logInput.View() + "\n")
		if s.logResult != "" {
			b.WriteString(RenderStatus(s.logResult, s.logResultLvl) + "\n")
		}
		b.WriteString(StyleMuted.Render("\nenter submit · esc cancel"))
		return b.String()
	}
	if s.logResult != "" {
		b.WriteString(RenderStatus(s.logResult, s.logResultLvl) + "\n\n")
	}

	b.WriteString(StyleMuted.Render("p log problem · r refresh · esc back"))
	return b.String()
}