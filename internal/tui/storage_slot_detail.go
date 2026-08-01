// One storage slot — placement, marker, ownership and who is in it.
//
// Reached from the slot list (enter) and addressed by CODE, because the
// viewset's lookup_field is `code`: the code is what is printed on the rack and
// what a scanner reads, so it is the identifier every caller already has.
//
// Keys: E edit · x delete · p print this slot's card · v what the card encodes ·
// enter open the occupying stint · r refresh · esc back.
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

	confirmingDelete bool
	deleting         bool

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

// HandlesKey claims `G` (global categories) for bottom-of-scroll. The other
// keys are free in the global keymap and reach us via the root fall-through.
func (s *StorageSlotDetailScreen) HandlesKey(key string) bool {
	if s.WantsRawInput() {
		return false
	}
	return key == "G"
}

// WantsRawInput claims every key while the delete confirm, the print prompt or
// the card panel is up, so y/n, typed paths and esc land here.
func (s *StorageSlotDetailScreen) WantsRawInput() bool {
	return s.confirmingDelete || s.card.active || s.previewing
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

	case tea.KeyMsg:
		switch {
		case s.confirmingDelete:
			return s.updateConfirmDelete(m)
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
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))

	switch {
	case s.confirmingDelete:
		return s.scroller.View() + "\n\n" + s.deleteConfirmText()
	case s.card.active:
		return s.scroller.View() + "\n\n" + s.card.view()
	case s.previewing:
		return s.scroller.View() + "\n\n" + s.previewPanel()
	}

	hint := "j/k scroll · E edit · x delete · p print card · v card contents · r refresh · esc back"
	if s.slot.CurrentStint != nil {
		hint = "enter open stint · " + hint
	}
	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
}

func (s *StorageSlotDetailScreen) deleteConfirmText() string {
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	body := StyleStatusWarn.Render("Delete slot "+s.slot.Code+"? This RELEASES its AprilTag permanently.") + "\n"
	if s.slot.CurrentStint != nil {
		body += StyleMuted.Render("A live stint ("+s.slot.CurrentStint.StintID+") is in this slot — the backend will refuse.") + "\n"
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
	if slot.CurrentStint != nil {
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
	if st := slot.CurrentStint; st != nil {
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
