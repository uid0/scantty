// One storage slot — placement, marker, ownership and who is in it.
//
// Reached from the slot list (enter) and addressed by CODE, because the
// viewset's lookup_field is `code`: the code is what is printed on the rack and
// what a scanner reads, so it is the identifier every caller already has.
//
// Keys: E edit · x delete · a assign to a committee/crew/class · R release that
// assignment · p print this slot's card · v what the card encodes · enter open
// the occupying stint · r refresh · esc back. `a` collides with the global
// authorizations hotkey and is claimed via HandlesKey.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type StorageSlotDetailScreen struct {
	deps    Deps
	code    string
	slot    *omsapi.StorageSlot
	loading bool
	loadErr string

	scroller       *TextScroller
	terminalHeight int
	terminalWidth  int

	confirmingDelete bool
	deleting         bool

	// The C/L/E half of occupancy: `a` hands the slot to a committee / crew /
	// class and `R` takes it back. A project stint is NOT ended here — that is
	// the member's own lifecycle, reached with enter.
	confirmingRelease bool
	releasing         bool

	card slotCardPrompt

	// Card-contents panel: the card-preview endpoint returns a base64 PDF plus
	// the kiosk URL and marker id it encodes. A terminal can't render the PDF,
	// so the panel reports what was ENCODED (which is the part an operator can
	// actually verify against a scan) and points at `p` for the sheet itself.
	previewing bool
	preview    *omsapi.SlotCardPreview
	previewErr string
}

type storageSlotDetailLoadedMsg struct {
	slot *omsapi.StorageSlot
	err  error
}

type storageSlotDetailDeletedMsg struct {
	err error
}

type storageSlotPreviewMsg struct {
	preview *omsapi.SlotCardPreview
	err     error
}

type storageSlotReleasedMsg struct {
	occupant string
	err      error
}

func NewStorageSlotDetailScreen(deps Deps, code string) *StorageSlotDetailScreen {
	return &StorageSlotDetailScreen{
		deps:     deps,
		code:     code,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
		card:     newSlotCardPrompt(),
	}
}

func (s *StorageSlotDetailScreen) Title() string {
	if s.slot != nil {
		return "Slot " + s.slot.Code
	}
	return "Storage Slot"
}

// HandlesKey claims `G` (global categories) for bottom-of-scroll and `a`
// (global authorizations) for assign. The other keys are free in the global
// keymap and reach us via the root fall-through.
func (s *StorageSlotDetailScreen) HandlesKey(key string) bool {
	if s.WantsRawInput() {
		return false
	}
	return key == "G" || key == "a"
}

// WantsRawInput claims every key while the delete confirm, the release confirm,
// the print prompt or the card panel is up, so y/n, typed paths and esc land
// here rather than leaking to the globals (`n` = no would otherwise open
// notifications).
func (s *StorageSlotDetailScreen) WantsRawInput() bool {
	return s.confirmingDelete || s.confirmingRelease || s.card.active || s.previewing
}

func (s *StorageSlotDetailScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *StorageSlotDetailScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	code := s.code
	return func() tea.Msg {
		slot, err := deps.OMS.GetStorageSlot(ctx, code)
		return storageSlotDetailLoadedMsg{slot: slot, err: err}
	}
}

func (s *StorageSlotDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		proseSizeScroller(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
		return s, nil

	case storageSlotDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = slotCardErrorText(m.err)
		} else {
			s.loadErr = ""
		}
		s.slot = m.slot
		if s.slot != nil {
			// A rack/level/position edit recomputes the code server-side, so
			// re-key off what came back rather than the code we asked with.
			s.code = s.slot.Code
		}
		s.scroller.Set(s.renderBody())
		return s, nil

	case storageSlotDetailDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+slotCardErrorText(m.err), StatusError)
		}
		return s, tea.Batch(
			Status("slot "+s.code+" deleted", StatusOK),
			SwitchTo(WSFacilities, NewStorageSlotsScreen(s.deps)),
		)

	case storageSlotPreviewMsg:
		s.preview = m.preview
		if m.err != nil {
			s.previewErr = slotCardErrorText(m.err)
		} else {
			s.previewErr = ""
		}
		return s, nil

	case storageSlotCardsRenderedMsg:
		s.card.close()
		if m.err != nil {
			return s, Status("card render failed: "+slotCardErrorText(m.err), StatusError)
		}
		path, replaced, err := saveSlotCardPDF(m.path, m.pdf, m.fallback)
		if err != nil {
			return s, Status("could not write PDF: "+err.Error(), StatusError)
		}
		return s, Status(slotCardSavedSummary(path, len(m.pdf.Data), replaced), StatusOK)

	case storageSlotReleasedMsg:
		s.releasing = false
		s.confirmingRelease = false
		if m.err != nil {
			// Includes the 409 `already_released` — someone else got there
			// first, which is a real answer and not a crash.
			return s, Status("release failed: "+slotCardErrorText(m.err), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status(storageReleasedText(s.code, m.occupant), StatusOK), s.Init())

	case tea.KeyMsg:
		switch {
		case s.confirmingDelete:
			return s.updateConfirmDelete(m)
		case s.confirmingRelease:
			return s.updateConfirmRelease(m)
		case s.card.active:
			return s.updateCardPrompt(m)
		case s.previewing:
			// Any key dismisses the panel; a pending response is dropped by the
			// previewing guard in the render path.
			s.previewing = false
			return s, nil
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		return s.updateActions(m)
	}
	return s, nil
}

func (s *StorageSlotDetailScreen) updateActions(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.Init()
	case "E":
		if s.slot != nil {
			return s, SwitchTo(WSFacilities, NewStorageSlotFormScreen(s.deps, s.slot.Code))
		}
	case "x":
		if s.slot != nil {
			s.confirmingDelete = true
		}
	case "a":
		return s.openAssign()
	case "R":
		return s.startRelease()
	case "enter":
		// The occupant is the only thing on this screen with somewhere else to
		// be; opening their stint is how a warden gets from "who is in 1A1?" to
		// the lifecycle actions.
		if s.slot != nil && s.slot.CurrentStint != nil && s.slot.CurrentStint.StintID != "" {
			return s, SwitchTo(WSFacilities, NewProjectStorageDetailScreen(s.deps, s.slot.CurrentStint.StintID))
		}
	case "p":
		if s.slot == nil {
			return s, nil
		}
		req := omsapi.SlotCardsForIDs([]int{s.slot.ID})
		s.card.open("slot "+s.slot.Code, req, "storage_slot_"+s.slot.Code+"_card.pdf")
		return s, nil
	case "v":
		if s.slot == nil {
			return s, nil
		}
		s.previewing = true
		s.preview = nil
		s.previewErr = ""
		deps := s.deps
		ctx := s.ctx()
		code := s.slot.Code
		return s, func() tea.Msg {
			pv, err := deps.OMS.PreviewStorageSlotCard(ctx, code)
			return storageSlotPreviewMsg{preview: pv, err: err}
		}
	}
	return s, nil
}

// openAssign refuses up front what the backend would refuse anyway, naming the
// remedy each time: a live stint is the member's to end, another holding has to
// be released first, and a retired slot is not on offer at all.
func (s *StorageSlotDetailScreen) openAssign() (Screen, tea.Cmd) {
	slot := s.slot
	if slot == nil {
		return s, nil
	}
	switch {
	case slot.CurrentStint != nil:
		return s, Status(slot.Code+" holds a live project stint — enter opens it, resolve it there first", StatusWarn)
	case slot.CurrentAssignment != nil:
		return s, Status(slot.Code+" is already assigned to "+
			firstNonEmpty(slot.CurrentAssignment.OccupantDisplay, "a committee/crew/class")+
			" — press R to release it first", StatusWarn)
	case slot.IsOccupied:
		return s, Status(slot.Code+" is occupied — it can't be assigned until it is free", StatusWarn)
	case !slot.IsActive:
		return s, Status(slot.Code+" is out of service — press E and turn Active back on before assigning it", StatusWarn)
	}
	code := slot.Code
	back := func(d Deps) Screen { return NewStorageSlotDetailScreen(d, code) }
	return s, SwitchTo(WSFacilities, NewStorageAssignFormScreen(s.deps, code, back))
}

func (s *StorageSlotDetailScreen) startRelease() (Screen, tea.Cmd) {
	if s.slot == nil {
		return s, nil
	}
	if s.slot.CurrentAssignment == nil {
		return s, Status(s.slot.Code+" is not assigned to a committee, crew or class", StatusWarn)
	}
	s.confirmingRelease = true
	return s, nil
}

func (s *StorageSlotDetailScreen) updateConfirmRelease(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.releasing {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.slot == nil || s.slot.CurrentAssignment == nil {
			s.confirmingRelease = false
			return s, nil
		}
		s.releasing = true
		deps := s.deps
		ctx := s.ctx()
		// The slot's own payload carries the assignment id, so unlike the
		// overview grid this needs no lookup round trip.
		a := *s.slot.CurrentAssignment
		return s, func() tea.Msg {
			_, err := deps.OMS.ReleaseStorageAssignment(ctx, a.ID)
			return storageSlotReleasedMsg{occupant: a.OccupantDisplay, err: err}
		}
	case "n", "N", "esc":
		s.confirmingRelease = false
	}
	return s, nil
}

func (s *StorageSlotDetailScreen) releaseConfirmText() string {
	if s.releasing {
		return StyleMuted.Render("Releasing…")
	}
	a := s.slot.CurrentAssignment
	if a == nil {
		return ""
	}
	body := StyleStatusWarn.Render("Release "+s.slot.Code+" from "+
		firstNonEmpty(a.OccupantDisplay, storageTypeName(a.TypeLetter))+
		" ("+storageTypeName(a.TypeLetter)+")?") + "\n"
	body += StyleMuted.Render("The holding is kept as history — the slot just becomes assignable again.") + "\n"
	body += StyleMuted.Render("y release · n/esc cancel")
	return body
}

func (s *StorageSlotDetailScreen) updateCardPrompt(m tea.KeyMsg) (Screen, tea.Cmd) {
	fire, cancel, cmd := s.card.handleKey(m)
	switch {
	case cancel:
		s.card.close()
		return s, nil
	case fire:
		deps := s.deps
		ctx := s.ctx()
		req := s.card.request()
		path := s.card.path.Value()
		fallback := "storage_slot_" + s.code + "_card.pdf"
		return s, func() tea.Msg {
			pdf, err := deps.OMS.RenderStorageSlotCards(ctx, req)
			return storageSlotCardsRenderedMsg{pdf: pdf, path: path, fallback: fallback, err: err}
		}
	}
	return s, cmd
}

func (s *StorageSlotDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.slot == nil {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		code := s.slot.Code
		return s, func() tea.Msg {
			return storageSlotDetailDeletedMsg{err: deps.OMS.DeleteStorageSlot(ctx, code)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *StorageSlotDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading slot…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" +
			StyleMuted.Render("press r to retry · esc back")
	}
	if s.slot == nil {
		return StyleMuted.Render("Slot not found.")
	}
	// Sized here as well as inside proseBar, because the four modal branches
	// below draw the scrolled body under their own prompt and need a viewport
	// too. Sizing is idempotent given the same pane, so the second call costs
	// nothing and the two answers cannot disagree.
	proseSizeScroller(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)

	switch {
	case s.confirmingDelete:
		return s.scroller.View() + "\n\n" + s.deleteConfirmText()
	case s.confirmingRelease:
		return s.scroller.View() + "\n\n" + s.releaseConfirmText()
	case s.card.active:
		return s.scroller.View() + "\n\n" + s.card.view()
	case s.previewing:
		return s.scroller.View() + "\n\n" + s.previewPanel()
	}

	return s.scroller.View() + "\n\n" + s.proseBar().render(proseBarCells(s.terminalWidth))
}

// bar names every key that acts on this sheet, as a record the honesty sweep
// can press (prose_bar.go).
//
// THE WORST CUT OF THE SCROLLER SHEETS. It read "j/k scroll ·
// E edit · x delete · p print card · v card contents · r refresh · esc back" —
// 82 cells before the conditional `R release` / `a assign C/L/E` and
// `enter open stint` heads were prepended to it, against the 51 an 80-column
// pane gives. Unfolded and budgeted at a flat two rows, clampToBox took
// everything past the first line: on an occupied slot the operator read
// `enter open stint · R release · j/k scroll · E edit · x del` and no more,
// so `r refresh` and the way off the screen were named nowhere — while the
// arrows, pgup/pgdn, `g`/`G` and home/end scrolled it unannounced.
//
// The conditional arms are unchanged and they are the bar's half of a guard
// updateActions also applies: only ONE of assign / release is ever possible, and
// `enter` has somewhere to go only while a stint is in the slot. Keep them
// paired — an arm added here without its guard is the defect the record exists
// to report.
func (s *StorageSlotDetailScreen) bar(scrolls bool) proseBar {
	var out proseBar
	if s.slot != nil && s.slot.CurrentStint != nil {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter open stint"})
	}
	if s.slot != nil {
		switch {
		case s.slot.CurrentAssignment != nil:
			out = append(out, proseBarItem{Keys: []string{"R"}, Hint: "R release"})
		case !s.slot.IsOccupied && s.slot.IsActive:
			out = append(out, proseBarItem{Keys: []string{"a"}, Hint: "a assign C/L/E"})
		}
	}
	out = append(out, proseNavScroll(scrolls)...)
	return append(out,
		proseBarItem{Keys: []string{"E"}, Hint: "E edit"},
		proseBarItem{Keys: []string{"x"}, Hint: "x delete"},
		proseBarItem{Keys: []string{"p"}, Hint: "p print card"},
		proseBarItem{Keys: []string{"v"}, Hint: "v card contents"},
		proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this sheet is DRAWING — nil in the states that draw
// something else instead, which here is four modals as well as the two load
// states.
func (s *StorageSlotDetailScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" || s.slot == nil {
		return nil
	}
	if s.confirmingDelete || s.confirmingRelease || s.card.active || s.previewing {
		return nil
	}
	return proseScrollBar(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
}

func (s *StorageSlotDetailScreen) deleteConfirmText() string {
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	body := StyleStatusWarn.Render("Delete slot "+s.slot.Code+"? This RELEASES its AprilTag permanently.") + "\n"
	if s.slot.CurrentStint != nil {
		body += StyleMuted.Render("A live stint ("+s.slot.CurrentStint.StintID+") is in this slot — the backend will refuse.") + "\n"
	}
	if a := s.slot.CurrentAssignment; a != nil {
		// StorageAssignment.slot is CASCADE, so a delete would take the record
		// of the holding with it — which is why the backend refuses outright.
		body += StyleMuted.Render(storageTypeName(a.TypeLetter)+" storage ("+
			firstNonEmpty(a.OccupantDisplay, "unnamed")+") holds this slot — the backend will refuse; release it with R first.") + "\n"
	}
	body += StyleMuted.Render("Retiring it instead (E → Active off) keeps the tag and the history.") + "\n"
	body += StyleMuted.Render("y delete · n/esc cancel")
	return body
}

// previewPanel reports what the printed card ENCODES. It deliberately does not
// pretend to show the PDF: the useful, verifiable facts are the kiosk URL the
// QR carries (scanning the card lands a member on the claim page for this slot)
// and the AprilTag id, and those are exactly what the endpoint hands back
// alongside the bytes.
func (s *StorageSlotDetailScreen) previewPanel() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Card contents") + "\n")
	switch {
	case s.previewErr != "":
		b.WriteString(StyleStatusError.Render("✗ "+s.previewErr) + "\n")
	case s.preview == nil:
		b.WriteString(StyleMuted.Render("Rendering…") + "\n")
	default:
		pv := s.preview
		b.WriteString(StyleMuted.Render("QR target: ") + firstNonEmpty(pv.KioskURL, "—") + "\n")
		b.WriteString(StyleMuted.Render("AprilTag:  ") + slotTagText(pv.AprilTagID) + "\n")
		b.WriteString(StyleMuted.Render("File:      ") + pv.Filename + "\n")
		b.WriteString(StyleMuted.Render(fmt.Sprintf("A %d-character base64 PDF came back; a terminal can't draw it — press p to save the sheet.", len(pv.Preview))) + "\n")
	}
	b.WriteString(StyleMuted.Render("any key closes"))
	return b.String()
}

func (s *StorageSlotDetailScreen) renderBody() string {
	slot := s.slot
	if slot == nil {
		return ""
	}
	var b strings.Builder

	b.WriteString(StyleTitle.Render(slot.Code))
	// IsOccupied, not CurrentStint: a committee holding makes the slot just as
	// unavailable as a member's project, and a "[free]" badge over one would
	// get the shelf handed out twice.
	if slot.IsOccupied {
		b.WriteString("  " + StyleStatusWarn.Render("[occupied]"))
	} else if slot.IsActive {
		b.WriteString("  " + StyleStatusOK.Render("[free]"))
	}
	if !slot.IsActive {
		b.WriteString("  " + StyleMuted.Render("[retired]"))
	}
	b.WriteString("\n\n")

	b.WriteString(StyleTitle.Render("Placement") + "\n")
	b.WriteString(StyleMuted.Render("Rack:     ") + fmt.Sprintf("%d", slot.Rack) + "\n")
	b.WriteString(StyleMuted.Render("Level:    ") + slot.Level + "\n")
	b.WriteString(StyleMuted.Render("Position: ") + fmt.Sprintf("%d", slot.Position) +
		StyleMuted.Render("  (numbered from South/East toward North/West)") + "\n")
	b.WriteString(StyleMuted.Render("Reach:    "))
	if slot.RequiresPalletJack {
		b.WriteString("needs a pallet jack\n")
	} else {
		b.WriteString("ground-reachable\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Marker") + "\n")
	b.WriteString(StyleMuted.Render("AprilTag: ") + slotTagText(slot.AprilTagID) + "\n")
	if slot.AprilTagID == nil {
		b.WriteString(StyleMuted.Render("No marker allocated — the slot works by code, but there is nothing to scan.") + "\n")
	} else {
		b.WriteString(StyleMuted.Render("Permanent: a slot keeps its tag for life, so a scan always means this place.") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Occupancy") + "\n")
	if a := slot.CurrentAssignment; a != nil && slot.CurrentStint == nil {
		// The C/L/E half: staff gave this slot to a committee, the logistics
		// crew or a class, and it stays theirs until staff takes it back —
		// no expiry, no purgatory, so there are no dates to watch here.
		b.WriteString(firstNonEmpty(a.OccupantDisplay, storageTypeName(a.TypeLetter)) + "\n")
		b.WriteString(StyleMuted.Render("Type:     ") + storageTypeName(a.TypeLetter) +
			StyleMuted.Render(" storage ("+a.TypeLetter+" in the overview grid)") + "\n")
		if !a.AssignedAt.IsZero() {
			b.WriteString(StyleMuted.Render("Since:    ") + a.AssignedAt.Format("2006-01-02") + "\n")
		}
		b.WriteString(StyleMuted.Render("Held until staff releases it — press R.") + "\n")
	} else if st := slot.CurrentStint; st != nil {
		who := strings.TrimSpace(st.DisplayName)
		if who == "" {
			who = st.Username
		}
		b.WriteString(who)
		if st.Username != "" {
			b.WriteString(StyleMuted.Render(" · @" + st.Username))
		}
		b.WriteString("\n")
		b.WriteString(StyleMuted.Render("Stint:    ") + st.StintID + "\n")
		project := strings.TrimSpace(st.ProjectTitle)
		if project == "" {
			project = "(personal)"
		}
		b.WriteString(StyleMuted.Render("Project:  ") + project + "\n")
		if !st.StartedAt.IsZero() {
			b.WriteString(StyleMuted.Render("Started:  ") + st.StartedAt.Format("2006-01-02") + "\n")
		}
		if st.ExpiresAt != nil && !st.ExpiresAt.IsZero() {
			b.WriteString(StyleMuted.Render("Expires:  ") + st.ExpiresAt.Format("2006-01-02") + "\n")
		}
		if st.Status != "" {
			b.WriteString(StyleMuted.Render("Status:   ") + st.Status + "\n")
		}
	} else if slot.IsOccupied {
		// Neither half decoded but the server says occupied — say so rather
		// than inviting the warden to hand out a shelf that has something on it.
		b.WriteString("Occupied.\n")
	} else if slot.IsActive {
		b.WriteString("Free — available to reserve.\n")
	} else {
		b.WriteString("Free, but retired — not offered for new reservations.\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Reserved for") + "\n")
	if slot.OwningGroupName != "" {
		b.WriteString(slot.OwningGroupName + "\n")
	} else {
		b.WriteString(StyleMuted.Render("— (open to any member)") + "\n")
	}
	b.WriteString("\n")

	if strings.TrimSpace(slot.Notes) != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(slot.Notes + "\n\n")
	}

	if !slot.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Created " + slot.CreatedAt.Format("2006-01-02")))
		if !slot.UpdatedAt.IsZero() {
			b.WriteString(StyleMuted.Render(" · updated " + slot.UpdatedAt.Format("2006-01-02")))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func slotTagText(id *int) string {
	if id == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *id)
}
