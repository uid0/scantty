// AssetMeterReadingsScreen — one meter's append-only reading ledger.
//
// Reached with Ctrl-E from the meters grid, because a meter IS its ledger: the
// `current_value` on the grid is a cache of the newest row here, and this is
// where a number that looks wrong is traced back to whoever entered it.
//
// IT IS READ-ONLY BECAUSE THE LEDGER IS. OMS serves it through a
// ReadOnlyModelViewSet and every field on its serializer is read-only: rows are
// written only by `record-reading`, `adjust` and the rollup, and never edited.
// So this screen offers no write at all — the way to change what a meter says is
// a new row, which is the meters grid's two keys.
//
// WHAT THE GRID IS FOR is telling a MEASUREMENT from a CORRECTION. They are
// different operations and they land here as different `source` values, so the
// row draws the server's own label for each, and a correction's REASON — which
// the server required before it would take it — rides underneath, because the
// reason is the whole reason an adjustment is a separate action.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type AssetMeterReadingsScreen struct {
	deps      Deps
	assetID   string
	assetName string
	meter     omsapi.AssetMeter
	readings  []omsapi.AssetMeterReading
	loading   bool
	loadErr   string
	loadSeq   int
	jdeScreen

	cursor int
	note   string
}

type meterReadingsLoadedMsg struct {
	readings []omsapi.AssetMeterReading
	err      error
	seq      int
}

func NewAssetMeterReadingsScreen(deps Deps, assetID, assetName string, meter omsapi.AssetMeter) *AssetMeterReadingsScreen {
	return &AssetMeterReadingsScreen{
		deps: deps, assetID: assetID, assetName: assetName, meter: meter, loading: true,
	}
}

func (s *AssetMeterReadingsScreen) Title() string {
	if s.meter.Name != "" {
		return fmt.Sprintf("Readings · %s", s.meter.Name)
	}
	return "Meter readings"
}

func (s *AssetMeterReadingsScreen) Init() tea.Cmd { return s.load() }

func (s *AssetMeterReadingsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetMeterReadingsScreen) load() tea.Cmd {
	s.loadSeq++
	seq := s.loadSeq
	deps, id, ctx := s.deps, s.meter.ID, s.ctx()
	return func() tea.Msg {
		readings, err := deps.OMS.ListMeterReadings(ctx, id)
		return meterReadingsLoadedMsg{readings: readings, err: err, seq: seq}
	}
}

func (s *AssetMeterReadingsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case meterReadingsLoadedMsg:
		if m.seq != s.loadSeq {
			return s, nil
		}
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("load readings failed: "+m.err.Error(), StatusError)
		}
		s.loadErr = ""
		s.readings = m.readings
		if s.cursor >= len(s.readings) {
			s.cursor = len(s.readings) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		return s, nil

	case tea.KeyMsg:
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSAssets, NewAssetMetersScreen(s.deps, s.assetID, s.assetName))
		case "up":
			s.move(-1)
		case "down":
			s.move(+1)
		case "pgup":
			if s.names("PgUp/PgDn") {
				s.page(-1)
			}
		case "pgdown":
			if s.names("PgUp/PgDn") {
				s.page(+1)
			}
		case "r":
			s.loading = true
			s.loadErr = ""
			s.note = ""
			return s, s.load()
		}
	}
	return s, nil
}

func (s *AssetMeterReadingsScreen) names(key string) bool {
	for _, it := range s.bar() {
		if it.Key == key {
			return true
		}
	}
	return false
}

func (s *AssetMeterReadingsScreen) move(delta int) {
	next, ok := s.pickRow(s.cursor, len(s.readings), delta, len(s.header()), s.bar())
	if !ok {
		return
	}
	s.cursor = next
}

func (s *AssetMeterReadingsScreen) page(dir int) {
	next, ok := s.pageRow(s.lines(), s.cursor, len(s.readings), dir,
		len(s.header()), s.bar(), s.barItems(true))
	if !ok {
		return
	}
	s.cursor = next
}

// Grid columns. The DATE is an identifier that abbreviates; the two FIGURES
// never give, for the reason the meters grid gives: a cut number reads as a
// different number, and the whole subject of this screen is which number.
const (
	readingNumW  = 3
	readingDateW = 10
)

var readingRowIndent = strings.Repeat(" ", len(jdeIndent)+readingNumW+2)

// readingGridPlan sizes the two figure columns to the widest each really needs
// and says whether they fit at all.
//
// Where they do not, BOTH are dropped together rather than one of them: a delta
// beside a missing total, or a total beside a missing delta, is a row that
// invites the reader to work out the other one — and the figures then ride the
// readings line underneath, whole and marked, which is the same trade the meters
// grid makes.
func (s *AssetMeterReadingsScreen) readingGridPlan() (deltaW, afterW int, dropped bool) {
	width := 76
	if w := assetPaneCells(s.terminalWidth); w > 0 {
		width = w
	}
	unit, typeLabel := s.meter.Unit, s.meter.MeterTypeDisplay
	for _, r := range s.readings {
		if n := lipgloss.Width(meterSignedFigure(r.Delta, unit, typeLabel)); n > deltaW {
			deltaW = n
		}
		if n := lipgloss.Width(meterFigure(r.ValueAfter, unit, typeLabel)); n > afterW {
			afterW = n
		}
	}
	if deltaW == 0 && afterW == 0 {
		return 0, 0, false
	}
	avail := width - (len(jdeIndent) + readingNumW + 2 + readingDateW + 2)
	if avail < deltaW+2+afterW {
		return 0, 0, true
	}
	return deltaW, afterW, false
}

func readingGridRow(num, when, delta, after string, deltaW, afterW int) string {
	row := jdeIndent + padCell(num, readingNumW, alignRight) + "  " +
		padCell(when, readingDateW, alignLeft)
	if deltaW > 0 || afterW > 0 {
		row += "  " + padCell(delta, deltaW, alignRight) + "  " + padCell(after, afterW, alignRight)
	}
	return strings.TrimRight(row, " ")
}

func (s *AssetMeterReadingsScreen) lines() *jdeLines {
	l := &jdeLines{}
	deltaW, afterW, dropped := s.readingGridPlan()
	unit, typeLabel := s.meter.Unit, s.meter.MeterTypeDisplay
	for i, r := range s.readings {
		when := ""
		if !r.ObservedAt.IsZero() {
			when = r.ObservedAt.Local().Format("2006-01-02")
		}
		delta := meterSignedFigure(r.Delta, unit, typeLabel)
		after := meterFigure(r.ValueAfter, unit, typeLabel)
		row := readingGridRow(strconv.Itoa(i+1), when, delta, after, deltaW, afterW)
		if i == s.cursor {
			row = StyleJDEFieldFocused.Render(row)
		}
		l.AddRow(i, row)

		if dropped {
			// The two figures move onto lines of their OWN at the row indent —
			// never jdeWrapTokens tokens, which CLIP what will not fit, and a
			// clipped figure here is a cut number. meterFigureLine owns what
			// gives where even that room is not enough.
			room := assetPaneCells(s.terminalWidth) - len(jdeIndent)
			if delta != "" {
				l.AddRow(i, jdeIndent+StyleJDEHeading.Render(
					meterFigureLine(delta, meterSignedFigure(r.Delta, "", ""), room)))
			}
			if after != "" {
				l.AddRow(i, jdeIndent+StyleJDEHeading.Render(
					meterFigureLine("now "+after, meterValueText(r.ValueAfter), room)))
			}
		}
		var tokens []jdeToken
		// A CORRECTION is marked as one. The source label is the server's own,
		// so "Manual correction" and "Manual entry" read the way they do on the
		// web rather than in two vocabularies.
		style := StyleMuted
		if r.Source == omsapi.MeterReadingSourceManualAdjust {
			style = StyleStatusWarn
		}
		if r.SourceDisplay != "" {
			tokens = append(tokens, jdeToken{text: r.SourceDisplay, style: style})
		}
		if r.IsEstimated {
			tokens = append(tokens, jdeToken{text: "estimated", style: StyleStatusWarn})
		}
		if r.RecordedByName != "" {
			tokens = append(tokens, jdeToken{text: "by " + r.RecordedByName, style: StyleMuted})
		} else if r.SourceRef != "" {
			// An automatic reading has no recorder — nobody entered it — so its
			// provenance string is the only answer to "where did this come
			// from", and dropping it would leave the row unattributed.
			tokens = append(tokens, jdeToken{text: r.SourceRef, style: StyleMuted})
		}
		if r.Notes != "" {
			tokens = append(tokens, jdeToken{text: r.Notes, style: StyleMuted})
		}
		for _, line := range jdeWrapTokens(tokens, readingRowIndent, s.bodyWidth()) {
			l.AddRow(i, line)
		}
	}
	return l
}

// header pins the meter, what it now reads, and the column header — above the
// body, because a line ahead of the first navigable row is a line no key can
// reach once the body overflows.
func (s *AssetMeterReadingsScreen) header() jdeHeader {
	h := jdeHeader(nil)
	if s.loadErr != "" {
		h = h.add(jdeHeadContext,
			StyleStatusError.Render("Error: ")+fitCellIf(s.loadErr, s.bodyWidth()-poErrPrefixW), "")
	}
	h = h.add(jdeHeadContext, StyleJDEHeading.Render(s.titleRow()))

	if len(s.readings) == 0 {
		if s.loadErr != "" {
			return h.add(jdeHeadEssential, jdeIndent+StyleMuted.Render(
				fitCellIf("Could not read the ledger — there may still be readings.",
					s.bodyWidth()-len(jdeIndent))))
		}
		return h.add(jdeHeadEssential, jdeIndent+StyleMuted.Render(
			fitCellIf("No readings on this meter yet.", s.bodyWidth()-len(jdeIndent))))
	}
	deltaW, afterW, dropped := s.readingGridPlan()
	if dropped {
		// The note takes the ESSENTIAL row and the column header gives it up,
		// for the reason the meter grid's does: without the two figure columns
		// the header reads "#  Observed", which says nothing an operator could
		// not see, while the note is the only thing on the pane saying where the
		// figures went. FOLDED, not written straight to the pane — a
		// hand-counted note is the defect pane_text.go exists for.
		return h.add(jdeHeadContext, StyleMuted.Render(
			readingGridRow("#", "Observed", "Change", "Total", deltaW, afterW))).
			add(jdeHeadEssential, jdeIndent+StyleMuted.Render(fitCellIf(
				"Values "+meterDropMark+" below", s.bodyWidth()-len(jdeIndent)))).
			addBlock(jdeHeadContext, jdeCaveatLines(
				"The pane is too narrow to hold each row's change and total beside it, "+
					"so they are drawn whole underneath instead.", s.bodyWidth()))
	}
	return h.add(jdeHeadEssential, StyleMuted.Render(
		readingGridRow("#", "Observed", "Change", "Total", deltaW, afterW)))
}

// titleRow names the meter and what it now reads, bounded AS ASSEMBLED — and
// the figure is the part that never gives.
//
// It used to be `fitCellIf(name + " · now " + figure, pane)`, which is a bound
// applied to a row that carries a NUMBER at its tail: at 55 columns it drew
// "Spindle runtime · now 128…", a cut reading on the one row that says what this
// ledger is about. The NAME abbreviates instead, and where even that leaves the
// figure no room the figure is DROPPED whole rather than cut — the rule
// meterFigureLine states, applied to the one row a header cannot fold.
func (s *AssetMeterReadingsScreen) titleRow() string {
	room := assetPaneCells(s.terminalWidth)
	now := meterFigure(s.meter.CurrentValue, s.meter.Unit, s.meter.MeterTypeDisplay)
	if now == "" {
		return fitCellIf(s.meter.Name, room)
	}
	const joint = " · now "
	if room <= 0 {
		return s.meter.Name + joint + now
	}
	// The figure and the joint are reserved BEFORE the name is clipped: a bound
	// applied to one part of a row that is afterwards added to is not a bound.
	left := room - lipgloss.Width(joint+now)
	if left >= meterNameFloor {
		return fitCell(s.meter.Name, left) + joint + now
	}
	// No room for both. The NAME is what this screen was opened from and is on
	// the row above it in the grid; the figure is the fact.
	return fitCellIf(meterFigureLine(now, meterValueText(s.meter.CurrentValue), room), room)
}

func (s *AssetMeterReadingsScreen) bar() []actionBarItem {
	return s.barItems(s.pages())
}

func (s *AssetMeterReadingsScreen) barItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Esc", "Back"}}
	if jdeRowMoves(len(s.readings)) {
		items = append(items, actionBarItem{"UP/DN", "Move"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

func (s *AssetMeterReadingsScreen) pages() bool {
	if len(s.readings) == 0 {
		return false
	}
	return s.bodyPagesForBar(s.lines(), len(s.readings), len(s.header()), s.barItems(true))
}

func (s *AssetMeterReadingsScreen) View() string {
	status := s.statusRow(s.loading, "Reading this meter's ledger…", "")
	if !s.loading && s.note != "" {
		status = s.statusAnswer(StatusInfo, s.note)
	}
	return s.frameWrapped(s.header(), s.lines(), s.cursor, status, s.bar())
}
