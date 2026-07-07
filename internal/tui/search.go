package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type SearchPalette struct {
	deps     Deps
	input    textinput.Model
	results  []omsapi.SearchResult
	cursor   int
	pending  bool
	lastErr  string
	lastQ    string
	debounce *time.Timer
}

type searchResponseMsg struct {
	query   string
	results []omsapi.SearchResult
	err     error
}

type searchTickMsg struct {
	query string
}

func NewSearchPalette(deps Deps) *SearchPalette {
	ti := textinput.New()
	ti.Prompt = "▸ "
	ti.Placeholder = "search items / assets / POs / suppliers / locations…"
	ti.CharLimit = 200
	ti.Focus()
	return &SearchPalette{deps: deps, input: ti}
}

func (s *SearchPalette) Title() string { return "Search" }

func (s *SearchPalette) WantsRawInput() bool { return true }

func (s *SearchPalette) Init() tea.Cmd { return textinput.Blink }

func (s *SearchPalette) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case searchResponseMsg:
		s.pending = false
		if m.query != s.input.Value() {
			return s, nil
		}
		if m.err != nil {
			s.lastErr = m.err.Error()
			s.results = nil
		} else {
			s.lastErr = ""
			s.results = m.results
			if s.cursor >= len(s.results) {
				s.cursor = 0
			}
		}
		return s, nil
	case searchTickMsg:
		if m.query != s.input.Value() {
			return s, nil
		}
		return s.runQuery(m.query)
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyDown, tea.KeyCtrlN:
			if s.cursor < len(s.results)-1 {
				s.cursor++
			}
			return s, nil
		case tea.KeyUp, tea.KeyCtrlP:
			if s.cursor > 0 {
				s.cursor--
			}
			return s, nil
		case tea.KeyEnter:
			return s.openSelected()
		case tea.KeyEsc:
			return s, SwitchTo(WSScan, NewScanScreen(s.deps))
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	q := strings.TrimSpace(s.input.Value())
	if q != s.lastQ {
		s.lastQ = q
		if q == "" {
			s.results = nil
			return s, cmd
		}
		return s, tea.Batch(cmd, tea.Tick(220*time.Millisecond, func(time.Time) tea.Msg {
			return searchTickMsg{query: q}
		}))
	}
	return s, cmd
}

func (s *SearchPalette) runQuery(q string) (Screen, tea.Cmd) {
	if s.deps.OMS == nil {
		return s, nil
	}
	s.pending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		resp, err := deps.OMS.Search(ctx, q)
		if resp == nil {
			return searchResponseMsg{query: q, err: err}
		}
		return searchResponseMsg{query: q, results: resp.Results, err: err}
	}
}

func (s *SearchPalette) openSelected() (Screen, tea.Cmd) {
	if s.cursor >= len(s.results) {
		return s, nil
	}
	r := s.results[s.cursor]
	id := fmt.Sprint(r.ID)
	switch r.Type {
	case "item", "inventory":
		// The OMS backend search (search/views.py) tags InventoryItems as
		// "inventory"; "item" is kept as a defensive alias in case the
		// backend ever emits it. Both open the inventory item detail.
		return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, id))
	case "asset":
		return s, SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, id))
	case "purchase_order":
		return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, id))
	case "work_order":
		return s, SwitchTo(WSMaintenance, NewWorkOrderDetailScreen(s.deps, id))
	case "location":
		return s, SwitchTo(WSInventory, NewLocationDetailScreen(s.deps, id))
	case "supplier":
		return s, SwitchTo(WSInventory, NewSupplierDetailScreen(s.deps, id))
	case "sig":
		return s, SwitchTo(WSSIGs, NewSIGDetailScreen(s.deps, id))
	}
	return s, Status(fmt.Sprintf("%s %v (no detail screen yet)", r.Type, r.ID), StatusWarn)
}

func (s *SearchPalette) View() string {
	var b strings.Builder
	b.WriteString(s.input.View() + "\n\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Searching…") + "\n")
	}
	if s.lastErr != "" {
		b.WriteString(StyleStatusError.Render("Error: ") + s.lastErr + "\n")
	}
	if len(s.results) == 0 {
		if strings.TrimSpace(s.input.Value()) != "" && !s.pending && s.lastErr == "" {
			b.WriteString(StyleMuted.Render("No matches.") + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("Type to search · ↑/↓ move · enter open · esc close"))
		return b.String()
	}
	for i, r := range s.results {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		line := fmt.Sprintf("%s%s · %s", caret, StyleMuted.Render(r.Type), r.Title)
		if i == s.cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
		if r.Subtitle != "" {
			b.WriteString("    " + StyleMuted.Render(r.Subtitle) + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("↑/↓ move · enter open · esc close"))
	return b.String()
}
