package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Detail screens for Location and Supplier. Both fetch by ID (GetLocation /
// GetSupplier — the retrieve serializers carry the richer field set the list
// endpoints omit) and expose the same edit (E) / delete (x, confirmed) actions
// as the item and asset detail screens. Location additionally offers generate-QR
// (g), mirroring the web LocationDetailPage. They stay plain (non-raw) screens
// so workspace switching keeps working, flipping to raw input only while the
// delete confirmation is up so y/n land here.
//
// Safety-sign and supplier lead-time / price-trend analytics are separate web
// pages tracked as follow-up beads. Location reconciliation is surfaced here
// through c and implemented by location_reconcile.go.

// ===========================================================================
// LocationDetailScreen
// ===========================================================================

type LocationDetailScreen struct {
	deps    Deps
	id      string
	loc     *omsapi.Location
	loading bool
	loadErr string

	generating       bool
	confirmingDelete bool
	deleting         bool
}

type locationDetailLoadedMsg struct {
	loc *omsapi.Location
	err error
}

type locationQRMsg struct {
	result *omsapi.LocationQRResult
	err    error
}

type locationDetailDeletedMsg struct {
	err error
}

func NewLocationDetailScreen(deps Deps, id string) *LocationDetailScreen {
	return &LocationDetailScreen{deps: deps, id: strings.TrimSpace(id), loading: true}
}

func (s *LocationDetailScreen) Title() string {
	if s.loc != nil {
		return "Location: " + s.loc.Name
	}
	return "Location"
}

func (s *LocationDetailScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *LocationDetailScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *LocationDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.id
	ctx := s.ctx()
	return func() tea.Msg {
		loc, err := deps.OMS.GetLocation(ctx, id)
		return locationDetailLoadedMsg{loc: loc, err: err}
	}
}

func (s *LocationDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case locationDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.loc = m.loc
		}
		return s, nil
	case locationQRMsg:
		s.generating = false
		if m.err != nil {
			return s, Status("QR generate failed: "+m.err.Error(), StatusError)
		}
		if m.result != nil && m.result.Error != "" {
			return s, Status("QR generate failed: "+m.result.Error, StatusError)
		}
		// Reload so the fresh qr_code_url shows.
		s.loading = true
		return s, tea.Batch(Status("QR code generated", StatusOK), s.Init())
	case locationDetailDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("location deleted", StatusOK),
			SwitchTo(WSInventory, NewLocationListScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "E":
			if s.loc != nil {
				return s, SwitchTo(WSInventory, NewLocationFormScreen(s.deps, strconv.Itoa(s.loc.ID)))
			}
		case "p":
			if s.loc != nil {
				return s, SwitchTo(WSInventory, NewLocationProblemsScreen(s.deps, s.loc.ID, s.loc.Name))
			}
		case "f":
			// The fixtures installed here (web LocationFixturesList), each of
			// which opens its refill requests.
			if s.loc != nil {
				return s, SwitchTo(WSInventory, NewLocationFixturesScreen(s.deps, s.loc.ID, s.loc.Name))
			}
		case "c":
			// Count the whole room (web /inventory/locations/:id/reconcile).
			// Lowercase c is free in the global hotkey map, so it falls through
			// to the screen — the same route the item detail's own cycle count
			// takes.
			if s.loc != nil {
				return s, SwitchTo(WSInventory,
					NewLocationReconcileScreen(s.deps, strconv.Itoa(s.loc.ID), s.loc.Name))
			}
		case "g":
			if s.loc != nil && !s.generating {
				s.generating = true
				deps := s.deps
				ctx := s.ctx()
				id := s.id
				return s, func() tea.Msg {
					res, err := deps.OMS.GenerateLocationQR(ctx, id)
					return locationQRMsg{result: res, err: err}
				}
			}
		case "x":
			if s.loc != nil {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *LocationDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.loc == nil {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		id := s.id
		return s, func() tea.Msg {
			return locationDetailDeletedMsg{err: deps.OMS.DeleteLocation(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
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
		return StyleMuted.Render("Location not found.") + "\n\n" + StyleMuted.Render("esc back")
	}
	if s.confirmingDelete {
		if s.deleting {
			return StyleMuted.Render("Deleting…")
		}
		return StyleStatusWarn.Render(fmt.Sprintf("Delete location %q? This can't be undone.  y delete · n/esc cancel", s.loc.Name))
	}

	loc := s.loc
	var b strings.Builder
	b.WriteString(StyleTitle.Render(loc.Name))
	if loc.IsActive {
		b.WriteString("  " + StyleStatusOK.Render("active"))
	} else {
		b.WriteString("  " + StyleMuted.Render("inactive"))
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %d", loc.ID)))
	if loc.ParentName != "" {
		b.WriteString(StyleMuted.Render(" · in " + loc.ParentName))
	}
	b.WriteString("\n\n")

	if loc.Description != "" {
		b.WriteString(loc.Description + "\n\n")
	}

	b.WriteString(StyleTitle.Render("Details") + "\n")
	b.WriteString(StyleMuted.Render("Fixtures: ") + fmt.Sprintf("%d\n", loc.FixtureCount))
	if loc.AccessCode != "" {
		b.WriteString(StyleMuted.Render("Access code: ") + loc.AccessCode + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("QR code") + "\n")
	if s.generating {
		b.WriteString(StyleMuted.Render("Generating…") + "\n")
	} else if loc.QRCodeURL != "" {
		b.WriteString(StyleMuted.Render("URL: ") + loc.QRCodeURL + "\n")
		b.WriteString(StyleMuted.Render("g to regenerate") + "\n")
	} else {
		b.WriteString(StyleMuted.Render("No QR code generated yet — g to generate") + "\n")
	}
	b.WriteString("\n")

	// FOLDED, not written straight to the pane. This legend is 69 cells and the
	// pane gives 51 at 80 columns, so clampToBox was already taking
	// "x delete · r refresh · esc back" off the end before the count key was
	// added to the front of it — a bar the operator cannot finish reading is not
	// honest, it is absent. pickerWrap folds at the `·` joints and indents the
	// continuation, which is the same folder every other legend in this program
	// goes through (AGENTS.md).
	b.WriteString(StyleMuted.Render(strings.Join(
		pickerWrap("c count · p problems · f fixtures · g gen-QR · E edit · x delete · r refresh · esc back",
			pickerPaneWidth), "\n")))
	return b.String()
}

// ===========================================================================
// SupplierDetailScreen
// ===========================================================================

type SupplierDetailScreen struct {
	deps           Deps
	id             string
	sup            *omsapi.Supplier
	loading        bool
	loadErr        string
	scroller       *TextScroller
	terminalHeight int
	terminalWidth  int

	confirmingDelete bool
	deleting         bool
}

type supplierLoadedMsg struct {
	sup *omsapi.Supplier
	err error
}

type supplierDetailDeletedMsg struct {
	err error
}

func NewSupplierDetailScreen(deps Deps, id string) *SupplierDetailScreen {
	return &SupplierDetailScreen{
		deps:     deps,
		id:       strings.TrimSpace(id),
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *SupplierDetailScreen) Title() string {
	if s.sup != nil {
		return "Supplier: " + s.sup.Name
	}
	return "Supplier"
}

func (s *SupplierDetailScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *SupplierDetailScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *SupplierDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.id
	ctx := s.ctx()
	return func() tea.Msg {
		sup, err := deps.OMS.GetSupplier(ctx, id)
		return supplierLoadedMsg{sup: sup, err: err}
	}
}

func (s *SupplierDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		proseSizeScroller(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
		return s, nil
	case supplierLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.sup = m.sup
			s.scroller.Set(s.renderBody())
		}
		return s, nil
	case supplierDetailDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("supplier deleted", StatusOK),
			SwitchTo(WSInventory, NewSupplierListScreen(s.deps)),
		)
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
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
			return s, s.Init()
		case "E":
			if s.sup != nil {
				return s, SwitchTo(WSInventory, NewSupplierFormScreen(s.deps, strconv.Itoa(s.sup.ID)))
			}
		case "x":
			if s.sup != nil {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *SupplierDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.sup == nil {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		id := s.id
		return s, func() tea.Msg {
			return supplierDetailDeletedMsg{err: deps.OMS.DeleteSupplier(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *SupplierDetailScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading supplier…", proseBarCells(s.terminalWidth), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, proseBarCells(s.terminalWidth), s.proseBar())
	}
	if s.sup == nil {
		return StyleMuted.Render("Supplier not found.") + "\n\n" + StyleMuted.Render("esc back")
	}
	// Sized here as well as inside proseBar, because the delete confirm below
	// draws the scrolled body under its own prompt and needs a viewport too.
	// Sizing is idempotent given the same pane.
	proseSizeScroller(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
	if s.confirmingDelete {
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Deleting…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Delete supplier %q? This can't be undone.  y delete · n/esc cancel", s.sup.Name))
		}
		return s.scroller.View() + "\n\n" + prompt
	}
	return s.scroller.View() + "\n\n" + s.proseBar().render(proseBarCells(s.terminalWidth))
}

// bar names every key that acts on this sheet, as a record the honesty sweep
// can press (prose_bar.go).
//
// The literal this replaced read "j/k scroll · E edit · x delete · r refresh ·
// esc back" — "j/k scroll" alone, while the arrows, pgup/pgdn, `g`/`G` and
// home/end all scrolled it.
func (s *SupplierDetailScreen) bar(scrolls bool) proseBar {
	return append(proseNavScroll(scrolls),
		proseBarItem{Keys: []string{"E"}, Hint: "E edit"},
		proseBarItem{Keys: []string{"x"}, Hint: "x delete"},
		proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this sheet is DRAWING — nil in the states that draw
// something else instead (the delete confirm, a supplier that was not found). A
// load in flight or failed draws loadBar's.
func (s *SupplierDetailScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	if s.sup == nil || s.confirmingDelete {
		return nil
	}
	return proseScrollBar(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
}

// loadBar is the sheet's bar while its load is out or has failed — what its key
// switch still answers with nothing drawn (prose_bar.go carries the defect and
// the decision). `E` still opens the edit form for the supplier a refresh kept,
// which the frame no longer draws: named because it acts, and a candidate for
// gating. `x` is not named: all it does here is arm a confirm the frame does
// not draw.
func (s *SupplierDetailScreen) loadBar() proseBar {
	var out proseBar
	if s.sup != nil {
		out = append(out, proseBarItem{Keys: []string{"E"}, Hint: "E edit"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *SupplierDetailScreen) renderBody() string {
	sup := s.sup
	var b strings.Builder
	b.WriteString(StyleTitle.Render(sup.Name) + "\n")
	headerParts := []string{fmt.Sprintf("ID %d", sup.ID)}
	if sup.SupplierType != "" {
		headerParts = append(headerParts, sup.SupplierType)
	}
	if sup.TaxFreePaperworkFiled {
		headerParts = append(headerParts, "tax-free filed")
	}
	b.WriteString(StyleMuted.Render(strings.Join(headerParts, " · ")) + "\n\n")

	if sup.Website != "" {
		b.WriteString(StyleMuted.Render("Website: ") + sup.Website + "\n")
	}
	if sup.AccountNumber != "" {
		b.WriteString(StyleMuted.Render("Account #: ") + sup.AccountNumber + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Activity") + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("Items supplied: %d", sup.ItemCount)) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("Purchase orders: %d", sup.PurchaseOrderCount)) + "\n")
	if !sup.TotalSpent.Empty() && sup.TotalSpent != "0.00" {
		b.WriteString(StyleMuted.Render("Total spent (received POs): $") + string(sup.TotalSpent) + "\n")
	}
	b.WriteString("\n")

	if len(sup.Items) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Items supplied (%d)", len(sup.Items))) + "\n")
		for _, it := range sup.Items {
			name := it.ItemName
			if name == "" {
				name = it.Item
			}
			line := "  · " + name
			if it.IsPreferred {
				line += " " + StyleStatusOK.Render("★ primary")
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if it.SupplierSKU != "" {
				meta = append(meta, "SKU "+it.SupplierSKU)
			}
			if !it.UnitCost.Empty() {
				meta = append(meta, "$"+string(it.UnitCost))
			}
			if it.LeadTimeDays > 0 {
				meta = append(meta, "lead "+leadTimeText(it.LeadTimeDays, it.LeadTimeSource))
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
		b.WriteString("\n")
	}

	if sup.Notes != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(sup.Notes + "\n\n")
	}

	if !sup.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Created: ") + sup.CreatedAt.Format("2006-01-02") + "\n")
	}
	if !sup.UpdatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Updated: ") + sup.UpdatedAt.Format("2006-01-02") + "\n")
	}
	return b.String()
}
