package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"net/url"

	"github.com/uid0/scantty/internal/omsapi"
)

// Lightweight detail screens for resources that don't have inline actions
// today — Location, Supplier, Category. Each fetches by ID and renders the
// headline fields with related-item summaries where useful.

type LocationDetailScreen struct {
	deps      Deps
	id        int
	loc       *omsapi.Location
	itemsHere int
	loading   bool
	loadErr   string
}

type locationDetailLoadedMsg struct {
	loc   *omsapi.Location
	items int
	err   error
}

func NewLocationDetailScreen(deps Deps, id string) *LocationDetailScreen {
	v, _ := strconv.Atoi(id)
	return &LocationDetailScreen{deps: deps, id: v, loading: true}
}

func (s *LocationDetailScreen) Title() string {
	if s.loc != nil {
		return "Location: " + s.loc.Name
	}
	return "Location"
}

func (s *LocationDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.id
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return locationDetailLoadedMsg{err: err}
		}
		var loc *omsapi.Location
		for i := range page.Results {
			if page.Results[i].ID == id {
				v := page.Results[i]
				loc = &v
				break
			}
		}
		itemCount := 0
		if loc != nil {
			q := url.Values{"location": []string{strconv.Itoa(id)}}
			if itemPage, _ := deps.OMS.ListItems(ctx, q); itemPage != nil {
				itemCount = itemPage.Count
				if itemCount == 0 {
					itemCount = len(itemPage.Results)
				}
			}
		}
		return locationDetailLoadedMsg{loc: loc, items: itemCount}
	}
}

func (s *LocationDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case locationDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.loc = m.loc
		s.itemsHere = m.items
		return s, nil
	case tea.KeyMsg:
		if m.String() == "r" {
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		}
	}
	return s, nil
}

func (s *LocationDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading location…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.loc == nil {
		return StyleMuted.Render("Location not found.")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(s.loc.Name) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %d", s.loc.ID)) + "\n")
	if s.loc.Code != "" {
		b.WriteString(StyleMuted.Render("Code: ") + s.loc.Code + "\n")
	}
	if s.loc.Capacity > 0 {
		b.WriteString(StyleMuted.Render("Capacity: ") + fmt.Sprintf("%d\n", s.loc.Capacity))
	}
	if s.loc.Parent != nil {
		b.WriteString(StyleMuted.Render("Parent: ") + fmt.Sprintf("#%d\n", *s.loc.Parent))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Items at this location: %d\n", s.itemsHere))
	b.WriteString("\n" + StyleMuted.Render("r refresh · esc back"))
	return b.String()
}

type SupplierDetailScreen struct {
	deps    Deps
	id      int
	row     *omsapi.Supplier
	loading bool
	loadErr string
}

type supplierLoadedMsg struct {
	row *omsapi.Supplier
	err error
}

func NewSupplierDetailScreen(deps Deps, id string) *SupplierDetailScreen {
	v, _ := strconv.Atoi(id)
	return &SupplierDetailScreen{deps: deps, id: v, loading: true}
}

func (s *SupplierDetailScreen) Title() string {
	if s.row != nil {
		return "Supplier: " + s.row.Name
	}
	return "Supplier"
}

func (s *SupplierDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.id
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListSuppliers(ctx, nil)
		if err != nil {
			return supplierLoadedMsg{err: err}
		}
		var row *omsapi.Supplier
		for i := range page.Results {
			if page.Results[i].ID == id {
				v := page.Results[i]
				row = &v
				break
			}
		}
		return supplierLoadedMsg{row: row}
	}
}

func (s *SupplierDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case supplierLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.row = m.row
		return s, nil
	case tea.KeyMsg:
		if m.String() == "r" {
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		}
	}
	return s, nil
}

func (s *SupplierDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading supplier…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.row == nil {
		return StyleMuted.Render("Supplier not found.")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(s.row.Name) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %d", s.row.ID)) + "\n")
	if s.row.URL != "" {
		b.WriteString(StyleMuted.Render("URL: ") + s.row.URL + "\n")
	}
	if s.row.Contact != "" {
		b.WriteString(StyleMuted.Render("Contact: ") + s.row.Contact + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("r refresh · esc back"))
	return b.String()
}
