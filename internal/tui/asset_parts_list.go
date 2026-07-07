// AssetPartsScreen — the create/edit/delete + mark-replaced management list for
// one asset's parts (the web "Part Replacement Tracking" table). Reached by
// pressing S on the asset detail; drills asset → its parts the same way
// electrical_manage.go drills panel → breakers → circuits ([[scantty-parity-program]]).
//
// Keybindings follow the electrical_manage convention: n new, E/enter edit, x
// delete (y/n confirm), R mark-replaced (y/n confirm — a distinct lifecycle
// action that stamps last_replaced_at=now server-side), j/k/g/G nav, r refresh.
// When the marked part is SERIALIZED (part_details.is_serialized), the y/n
// confirm is followed by a single-field prompt for the replacement unit's serial
// (op-8nxe parity); a blank submit records none. n and G collide with global
// hotkeys (Notifications / Categories), so the screen implements LocalKeyScreen
// to claim them; WantsRawInput is asserted while any confirm/prompt is up so
// y/n and the serial keystrokes land here. Delete needs no child-count pre-check —
// the backend SET_NULLs / unlinks the only referencing rows, so it never
// FK-409s (unlike the electrical tiers).
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type partsConfirm int

const (
	partsConfirmNone partsConfirm = iota
	partsConfirmDelete
	partsConfirmReplace
	// partsConfirmReplaceSerial is the second step of mark-replaced for a
	// SERIALIZED part: after the y/n confirm, a single-field prompt captures the
	// replacement unit's serial (op-8nxe). Non-serialized parts skip it.
	partsConfirmReplaceSerial
)

type AssetPartsScreen struct {
	deps      Deps
	assetID   string
	assetName string

	rows           []omsapi.AssetPart
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirm partsConfirm
	working bool // a delete/mark-replaced request is in flight

	// serial-entry prompt (mark-replaced of a serialized part)
	serialInput       textinput.Model
	replaceTargetID   string // part id captured when the prompt opens (stale guard)
	replaceTargetName string
}

type assetPartsLoadedMsg struct {
	rows []omsapi.AssetPart
	err  error
}

type assetPartDeletedMsg struct {
	err error
}

type assetPartReplacedMsg struct {
	part *omsapi.AssetPart
	err  error
}

func NewAssetPartsScreen(deps Deps, assetID, assetName string) *AssetPartsScreen {
	serial := textinput.New()
	serial.Prompt = ""
	serial.CharLimit = 100
	serial.Placeholder = "replacement unit serial (blank to skip)"
	return &AssetPartsScreen{deps: deps, assetID: assetID, assetName: assetName, loading: true, windowSize: 18, serialInput: serial}
}

func (s *AssetPartsScreen) Title() string {
	if s.assetName != "" {
		return "Parts: " + s.assetName
	}
	return "Parts"
}

// WantsRawInput claims every key only while a confirm is up (y/n/esc).
func (s *AssetPartsScreen) WantsRawInput() bool { return s.confirm != partsConfirmNone }

// HandlesKey claims the two action keys that collide with global hotkeys (n new,
// G bottom) so they reach this screen instead of the global nav switch.
func (s *AssetPartsScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *AssetPartsScreen) Init() tea.Cmd { return s.load() }

func (s *AssetPartsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetPartsScreen) load() tea.Cmd {
	deps := s.deps
	assetID := s.assetID
	ctx := s.ctx()
	return func() tea.Msg {
		page, err := deps.OMS.ListAssetParts(ctx, assetID)
		if err != nil {
			return assetPartsLoadedMsg{err: err}
		}
		return assetPartsLoadedMsg{rows: page.Results}
	}
}

func (s *AssetPartsScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *AssetPartsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case assetPartsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case assetPartDeletedMsg:
		s.working = false
		s.confirm = partsConfirmNone
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("part deleted", StatusOK), s.load())
	case assetPartReplacedMsg:
		s.working = false
		s.confirm = partsConfirmNone
		if m.err != nil {
			return s, Status("mark-replaced failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("marked replaced", StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirm != partsConfirmNone {
			return s.updateConfirm(m)
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
		case "ctrl+d", "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "ctrl+u", "pgup":
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
			return s, SwitchTo(WSAssets, NewAssetPartFormScreen(s.deps, s.assetID, s.assetName, ""))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSAssets, NewAssetPartFormScreen(s.deps, s.assetID, s.assetName, row.IDString()))
			}
		case "R":
			if _, ok := s.selected(); ok {
				s.confirm = partsConfirmReplace
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirm = partsConfirmDelete
			}
		}
	}
	return s, nil
}

func (s *AssetPartsScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.working {
		return s, nil
	}
	if s.confirm == partsConfirmReplaceSerial {
		return s.updateReplaceSerial(m)
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirm = partsConfirmNone
			return s, nil
		}
		deps := s.deps
		ctx := s.ctx()
		id := row.IDString()
		switch s.confirm {
		case partsConfirmDelete:
			s.working = true
			return s, func() tea.Msg {
				return assetPartDeletedMsg{err: deps.OMS.DeleteAssetPart(ctx, id)}
			}
		case partsConfirmReplace:
			// A serialized part captures the replacement unit's serial first
			// (op-8nxe); a non-serialized part fires immediately with "" so its
			// one-click behavior is unchanged.
			if row.PartDetails.IsSerialized {
				s.openReplaceSerial(row)
				return s, textinput.Blink
			}
			s.working = true
			return s, func() tea.Msg {
				p, err := deps.OMS.MarkAssetPartReplaced(ctx, id, "")
				return assetPartReplacedMsg{part: p, err: err}
			}
		}
	case "n", "N", "esc":
		s.confirm = partsConfirmNone
	}
	return s, nil
}

// openReplaceSerial switches the replace confirm into the single-field
// serial-entry prompt, capturing the target part's id so the async result maps
// to the right row even if the list reloads underneath.
func (s *AssetPartsScreen) openReplaceSerial(row omsapi.AssetPart) {
	s.confirm = partsConfirmReplaceSerial
	s.replaceTargetID = row.IDString()
	name := row.PartName
	if name == "" {
		name = row.Part
	}
	if name == "" {
		name = "part #" + row.IDString()
	}
	s.replaceTargetName = name
	s.serialInput.SetValue("")
	s.serialInput.Focus()
}

// updateReplaceSerial drives the replacement-serial prompt. enter submits (a
// blank value is allowed → records no serial, mirroring the web so an operator
// without the serial isn't blocked); esc cancels the whole mark-replaced.
func (s *AssetPartsScreen) updateReplaceSerial(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.serialInput.Blur()
		s.confirm = partsConfirmNone
		return s, nil
	case "enter":
		serial := strings.TrimSpace(s.serialInput.Value())
		deps := s.deps
		ctx := s.ctx()
		id := s.replaceTargetID
		s.working = true
		s.serialInput.Blur()
		return s, func() tea.Msg {
			p, err := deps.OMS.MarkAssetPartReplaced(ctx, id, serial)
			return assetPartReplacedMsg{part: p, err: err}
		}
	}
	var cmd tea.Cmd
	s.serialInput, cmd = s.serialInput.Update(m)
	return s, cmd
}

func (s *AssetPartsScreen) selected() (omsapi.AssetPart, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.AssetPart{}, false
	}
	return s.rows[s.cursor], true
}

func (s *AssetPartsScreen) scrollIntoView() {
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

func (s *AssetPartsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading parts…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirm != partsConfirmNone {
		return s.viewConfirm()
	}
	var b strings.Builder
	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render("No parts on this asset yet.") + "\n\n")
		b.WriteString(StyleMuted.Render("n new part · esc back"))
		return b.String()
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d parts", len(s.rows))) + "\n")
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
	b.WriteString(StyleMuted.Render("j/k move · n new · E/enter edit · R mark replaced · x delete · r refresh · esc back"))
	return b.String()
}

func (s *AssetPartsScreen) viewConfirm() string {
	row, ok := s.selected()
	if !ok {
		return ""
	}
	name := row.PartName
	if name == "" {
		name = row.Part
	}
	if name == "" {
		name = "part #" + row.IDString()
	}
	switch s.confirm {
	case partsConfirmDelete:
		if s.working {
			return StyleMuted.Render("Deleting…")
		}
		return StyleStatusWarn.Render(fmt.Sprintf("Delete %s? This can't be undone.  y delete · n/esc cancel", name))
	case partsConfirmReplace:
		if s.working {
			return StyleMuted.Render("Marking replaced…")
		}
		return StyleStatusWarn.Render(fmt.Sprintf("Mark %s replaced now (resets its replacement clock)?  y confirm · n/esc cancel", name))
	case partsConfirmReplaceSerial:
		if s.working {
			return StyleMuted.Render("Marking replaced…")
		}
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Mark "+s.replaceTargetName+" replaced") + "\n")
		b.WriteString(StyleMuted.Render("This part is serialized — record the replacement unit's serial.") + "\n\n")
		b.WriteString(StyleTitle.Render("Replacement serial number: ") + s.serialInput.View() + "\n\n")
		b.WriteString(StyleMuted.Render("enter submit (blank = record none) · esc cancel"))
		return b.String()
	}
	return ""
}

func (s *AssetPartsScreen) renderRow(i int) string {
	p := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	name := p.PartName
	if name == "" {
		name = p.Part
	}
	if name == "" {
		name = "part #" + p.IDString()
	}
	line := marker + name
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	if p.PartSKU != "" {
		line += " " + StyleMuted.Render("("+p.PartSKU+")")
	}
	if p.NeedsReplacement {
		line += " " + StyleStatusWarn.Render("NEEDS REPLACEMENT")
	}

	meta := []string{}
	if p.QuantityNeeded > 0 {
		meta = append(meta, fmt.Sprintf("qty %d", p.QuantityNeeded))
	}
	if p.IsRequired {
		meta = append(meta, "required")
	}
	if p.MaintenanceIntervalDays != nil {
		meta = append(meta, fmt.Sprintf("every %dd", *p.MaintenanceIntervalDays))
	}
	if p.LastReplacedAt != nil {
		entry := "replaced " + p.LastReplacedAt.Format("2006-01-02")
		if p.DaysSinceReplacement != nil {
			entry += fmt.Sprintf(" (%dd ago)", *p.DaysSinceReplacement)
		}
		meta = append(meta, entry)
	} else {
		meta = append(meta, "never replaced")
	}
	if p.ReplacementSerialNumber != "" {
		meta = append(meta, "s/n "+p.ReplacementSerialNumber)
	}
	out := line
	if len(meta) > 0 {
		out += "\n    " + StyleMuted.Render(strings.Join(meta, " · "))
	}
	if p.Notes != "" {
		out += "\n    " + StyleMuted.Render(p.Notes)
	}
	return out
}
