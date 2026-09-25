// Fixture refills — one fixture and its pending refill requests, the queue of
// pending requests across every fixture, and the fixtures installed at one
// location.
//
// TUI counterpart to the web's FixtureScanPage (reached from a fixture's QR
// label and from LocationFixturesList's "Scan Fixture" link), with a queue the
// web does not have: the web only ever shows a fixture's requests from that
// fixture's own page, so a logistics volunteer finds out a dispenser is empty by
// walking to it. internal/omsapi/fixtures.go carries the measured contract; what
// this file decides on top of it:
//
//   - ONE SCREEN, TWO SCOPES. FixtureRefillScreen is the fixture detail when it
//     holds a fixture id (NewFixtureDetailScreen — the destination a scanned
//     fixture label opens) and the cross-fixture queue when it does not
//     (NewFixtureRefillQueueScreen, the Inventory workspace's "Fixture refills").
//     Both list PENDING requests off the paginated list route, the web's own
//     `status: 'pending'` read, and resolve them through one prompt; what differs
//     is the head, the row, and the three keys only a fixture has (`A`, `n`) or
//     only the queue has (`enter`).
//   - THE WRITES SIT BEHIND A PROMPT, and the prompt says what the write DOES
//     rather than what the web's button is called. Two facts about these
//     endpoints are invisible from either UI and both destroy something: typed
//     notes REPLACE the reporter's note on every request they close, and resolve
//     all closes whatever is pending when OMS RECEIVES it — including a report
//     filed after this list loaded. So the prompt carries a notes box (the web
//     offers notes on resolve-all), states both, and leaves a blank box meaning
//     "keep the reporters' notes", which the client honours by sending no key.
//   - NOTHING IS STAFF-GATED HERE. The web hides resolve-all unless its
//     localStorage says `is_staff`; OMS checks only that the caller is signed in,
//     and every ScanTTY operator is. A client-side copy of a rule the server does
//     not hold would refuse what the server accepts.
//   - A REFUSAL IS THE SERVER'S SENTENCE (omsapi.FixtureRefusal). Where it is a
//     fact about the record — the request already completed, the fixture gone —
//     the prompt closes and the list reloads, because the list the operator
//     acted on is what was wrong. Anything else keeps the prompt and what was
//     typed in it, so Enter is a retry.
//   - A NEW REFILL REQUEST (`n`) is offered only on an ACTIVE fixture: OMS refuses
//     the scan action on an inactive one ("This fixture is inactive"), and the
//     head says so where the key is withheld.
//
// The bars are records (prose_bar.go) and the lists are line-packed windows
// under a head (proseFlatListFrame), so a long queue keeps its bar on the pane
// and a request note taller than the pane is clipped with the cut marked.
package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// fixturePrompt is which write the prompt is about to make, or none.
type fixturePrompt int

const (
	fixturePromptNone fixturePrompt = iota
	fixturePromptResolve
	fixturePromptResolveAll
	fixturePromptRequest
)

// FixtureRefillScreen is the fixture detail (fixtureID set) or the refill queue
// across every fixture (fixtureID empty). See the file comment.
type FixtureRefillScreen struct {
	deps      Deps
	fixtureID string

	fixture        *omsapi.Fixture
	rows           []omsapi.FixtureRefillRequest
	cursor         int
	windowStart    int
	loading        bool
	loadErr        string
	terminalWidth  int
	terminalHeight int

	// note is the answer to the last write, drawn in the head across the reload
	// that write starts — the status bar's flash expires, and the operator who
	// looked away is still looking at the list.
	note    string
	noteErr bool

	prompt   fixturePrompt
	target   omsapi.FixtureRefillRequest
	notes    textinput.Model
	writing  bool
	writeErr string
}

type fixtureRefillLoadedMsg struct {
	fixture *omsapi.Fixture
	rows    []omsapi.FixtureRefillRequest
	// stage names which read failed, ahead of the server's sentence: a fixture
	// that loaded and a queue that did not are different failures.
	stage string
	err   error
}

type fixtureRefillWroteMsg struct {
	prompt  fixturePrompt
	message string
	err     error
}

// NewFixtureDetailScreen opens one fixture and its pending refill requests. It
// is the destination a scanned fixture QR label (`/scan/fixture/<uuid>`,
// `/inventory/scan/fixture/<uuid>`) and a dispatcher `fixture` result should
// open, and the screen LocationFixturesScreen's enter opens.
func NewFixtureDetailScreen(deps Deps, fixtureID string) *FixtureRefillScreen {
	return newFixtureRefillScreen(deps, strings.TrimSpace(fixtureID))
}

// NewFixtureRefillQueueScreen opens the pending refill requests across every
// fixture, newest first.
func NewFixtureRefillQueueScreen(deps Deps) *FixtureRefillScreen {
	return newFixtureRefillScreen(deps, "")
}

func newFixtureRefillScreen(deps Deps, fixtureID string) *FixtureRefillScreen {
	notes := textinput.New()
	notes.Prompt = ""
	notes.CharLimit = 1000
	notes.Placeholder = "optional"
	return &FixtureRefillScreen{deps: deps, fixtureID: fixtureID, loading: true, notes: notes}
}

func (s *FixtureRefillScreen) isQueue() bool { return s.fixtureID == "" }

func (s *FixtureRefillScreen) Title() string {
	switch {
	case s.isQueue():
		return "Fixture refills"
	case s.fixture != nil:
		return "Fixture: " + s.fixture.Name
	}
	return "Fixture"
}

// WantsRawInput claims every key while a prompt is up, so the notes box gets
// its letters and esc cancels the prompt rather than leaving the screen.
func (s *FixtureRefillScreen) WantsRawInput() bool { return s.prompt != fixturePromptNone }

func (s *FixtureRefillScreen) Init() tea.Cmd { return s.load() }

func (s *FixtureRefillScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *FixtureRefillScreen) load() tea.Cmd {
	deps, ctx, id := s.deps, s.ctx(), s.fixtureID
	return func() tea.Msg {
		filter := omsapi.FixtureRefillRequestFilter{Status: omsapi.FixtureRefillPending}
		var fixture *omsapi.Fixture
		if id != "" {
			f, err := deps.OMS.GetFixture(ctx, id)
			if err != nil {
				return fixtureRefillLoadedMsg{err: err}
			}
			fixture = f
			filter.Fixture = f.ID
		}
		rows, err := deps.OMS.ListFixtureRefillRequests(ctx, filter)
		if err != nil {
			return fixtureRefillLoadedMsg{stage: "Reading the pending refill requests failed: ", err: err}
		}
		return fixtureRefillLoadedMsg{fixture: fixture, rows: rows}
	}
}

func (s *FixtureRefillScreen) selected() (omsapi.FixtureRefillRequest, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.FixtureRefillRequest{}, false
	}
	return s.rows[s.cursor], true
}

// canResolveAll: a fixture is loaded and its list holds something to close.
// Resolving nothing is a 200 the server would answer, and a key whose whole
// product is "Resolved 0" is not worth a name on the bar.
func (s *FixtureRefillScreen) canResolveAll() bool {
	return !s.isQueue() && s.fixture != nil && len(s.rows) > 0
}

// canRequest: a fixture is loaded and active — OMS refuses a refill request on
// an inactive one.
func (s *FixtureRefillScreen) canRequest() bool {
	return !s.isQueue() && s.fixture != nil && s.fixture.IsActive
}

// ---------------------------------------------------------------------------
// Bar
// ---------------------------------------------------------------------------

// listBar names every key that acts on the list surface, as a record the
// honesty sweep presses (prose_bar.go). The movement pair needs a second row;
// the row actions need a row.
func (s *FixtureRefillScreen) listBar(rows int) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if rows > 0 {
		if s.isQueue() {
			out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter fixture"})
		}
		out = append(out, proseBarItem{Keys: []string{"R"}, Hint: "R resolve"})
		if !s.isQueue() {
			out = append(out, proseBarItem{Keys: []string{"A"}, Hint: "A resolve all"})
		}
	}
	if s.canRequest() {
		out = append(out, proseBarItem{Keys: []string{"n"}, Hint: "n request refill"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// fixturePromptBar is every prompt's bar: the box owns every other key.
func fixturePromptBar(p fixturePrompt) proseBar {
	verb := "enter resolve"
	switch p {
	case fixturePromptResolveAll:
		verb = "enter resolve all"
	case fixturePromptRequest:
		verb = "enter file request"
	}
	return proseBar{
		{Keys: []string{"enter"}, Hint: verb},
		{Keys: []string{"esc"}, Hint: "esc cancel"},
	}
}

// proseBar is the bar this screen is DRAWING: the prompt's while one is open,
// nil while its write is out (a working line with every key held, the
// precedent every converted prompt keeps), loadBar's while a load is out or has
// failed, and the list's otherwise.
func (s *FixtureRefillScreen) proseBar() proseBar {
	switch {
	case s.prompt != fixturePromptNone:
		if s.writing {
			return nil
		}
		return fixturePromptBar(s.prompt)
	case s.loading || s.loadErr != "":
		return s.loadBar()
	}
	return s.listBar(len(s.rows))
}

// loadBar is the bar while a load is out or has failed. Every row key is held
// there (proseLoadKeyHidden), because a refresh keeps the previous rows and a
// resolve against a row the frame no longer draws is a write nobody can see the
// subject of.
func (s *FixtureRefillScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

// ceilingBar is the tallest shape the list bar takes — the one the window is
// budgeted against, for the reason proseListWindow gives.
func (s *FixtureRefillScreen) ceilingBar() proseBar { return s.listBar(proseFlatCeilingRows) }

func (s *FixtureRefillScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *FixtureRefillScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil

	case fixtureRefillLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.stage + fixtureSentence(m.err)
			return s, nil
		}
		s.loadErr = ""
		if m.fixture != nil {
			s.fixture = m.fixture
		}
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = len(s.rows) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		return s, nil

	case fixtureRefillWroteMsg:
		return s.wrote(m)

	case tea.KeyMsg:
		if s.prompt != fixturePromptNone {
			return s.updatePrompt(m)
		}
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		return s.updateList(m)
	}
	if s.prompt != fixturePromptNone && !s.writing {
		var cmd tea.Cmd
		s.notes, cmd = s.notes.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *FixtureRefillScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "j", "down":
		if s.cursor < len(s.rows)-1 {
			s.cursor++
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "r":
		s.loading, s.loadErr, s.note = true, "", ""
		return s, s.load()
	case "enter":
		if row, ok := s.selected(); ok && s.isQueue() {
			return s, SwitchTo(WSInventory, NewFixtureDetailScreen(s.deps, row.Fixture))
		}
	case "R":
		if row, ok := s.selected(); ok {
			s.target = row
			return s, s.openPrompt(fixturePromptResolve)
		}
	case "A":
		if s.canResolveAll() {
			return s, s.openPrompt(fixturePromptResolveAll)
		}
	case "n":
		if s.canRequest() {
			return s, s.openPrompt(fixturePromptRequest)
		}
	}
	return s, nil
}

func (s *FixtureRefillScreen) openPrompt(p fixturePrompt) tea.Cmd {
	s.prompt = p
	s.writeErr = ""
	s.notes.SetValue("")
	s.notes.Placeholder = "optional"
	if p == fixturePromptRequest {
		s.notes.Placeholder = "optional: what you noticed"
	}
	s.notes.Focus()
	return textinput.Blink
}

func (s *FixtureRefillScreen) closePrompt() {
	s.prompt = fixturePromptNone
	s.writing = false
	s.writeErr = ""
	s.notes.Blur()
}

func (s *FixtureRefillScreen) updatePrompt(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.writing {
		return s, nil
	}
	switch m.String() {
	case "esc":
		s.closePrompt()
		return s, nil
	case "enter":
		return s, s.submit()
	}
	var cmd tea.Cmd
	s.notes, cmd = s.notes.Update(m)
	return s, cmd
}

// submit sends the prompt's write. The notes are passed as typed; the client
// sends no key for blank ones, which is what keeps the reporters' notes.
func (s *FixtureRefillScreen) submit() tea.Cmd {
	s.writing = true
	s.writeErr = ""
	deps, ctx, notes, p := s.deps, s.ctx(), s.notes.Value(), s.prompt
	switch p {
	case fixturePromptResolve:
		target := s.target
		return func() tea.Msg {
			_, err := deps.OMS.ResolveFixtureRefillRequest(ctx, target.ID, notes)
			return fixtureRefillWroteMsg{prompt: p, err: err,
				message: "Resolved the request from " + target.Requester()}
		}
	case fixturePromptResolveAll:
		id := s.fixture.ID
		return func() tea.Msg {
			res, err := deps.OMS.ResolveAllFixtureRefillRequests(ctx, id, notes)
			msg := fixtureRefillWroteMsg{prompt: p, err: err}
			if res != nil {
				msg.message = res.Message
			}
			return msg
		}
	default:
		id := s.fixture.ID
		return func() tea.Msg {
			_, err := deps.OMS.ScanFixture(ctx, id, notes)
			return fixtureRefillWroteMsg{prompt: p, err: err, message: "Refill request filed"}
		}
	}
}

// wrote answers a write. See the file comment for which refusals close the
// prompt: a 400 or a 404 is a fact about the record the list was showing, so
// the prompt closes and the list reloads; anything else keeps what was typed.
func (s *FixtureRefillScreen) wrote(m fixtureRefillWroteMsg) (Screen, tea.Cmd) {
	if m.prompt != s.prompt {
		return s, nil
	}
	s.writing = false
	if m.err != nil {
		sentence := fixtureSentence(m.err)
		var api *omsapi.APIError
		if errors.As(m.err, &api) && (api.Status == http.StatusBadRequest || api.Status == http.StatusNotFound) {
			s.closePrompt()
			s.note, s.noteErr = sentence, true
			s.loading = true
			return s, tea.Batch(Status(sentence, StatusError), s.load())
		}
		s.writeErr = sentence
		return s, Status(sentence, StatusError)
	}
	s.closePrompt()
	s.note, s.noteErr = m.message, false
	s.loading = true
	return s, tea.Batch(Status(m.message, StatusOK), s.load())
}

// fixtureSentence is what an operator reads for a failed fixture call: the
// server's own sentence where there is one, and the error otherwise.
func fixtureSentence(err error) string {
	if s, ok := omsapi.FixtureRefusal(err); ok {
		return s
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *FixtureRefillScreen) View() string {
	cells := s.paneCells()
	if s.prompt != fixturePromptNone {
		return s.viewPrompt(cells)
	}
	if s.loading {
		working := "Loading the pending fixture refill requests…"
		if !s.isQueue() {
			working = "Loading the fixture and its pending refill requests…"
		}
		return proseLoadingFrame(working, cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	head := s.head(cells)
	bar := s.proseBar()
	if len(s.rows) == 0 {
		empty := "No pending refill requests."
		if !s.isQueue() {
			empty = "No pending refill requests for this fixture."
		}
		return head + StyleMuted.Render(pickerClip(empty, cells)) + "\n\n" + bar.render(cells)
	}
	rows := make([]string, len(s.rows))
	for i, r := range s.rows {
		rows[i] = s.requestRow(i, r, cells)
	}
	return proseFlatListFrame(head, rows, s.cursor, &s.windowStart, s.terminalHeight, cells, s.ceilingBar(), bar)
}

// head is what the list opens with: the fixture's facts, or the queue's title,
// then the answer to the last write. Every line carrying an OMS value is one row
// of the pane with its cut marked, so the head's height is fixed by what it
// says rather than by what the server stored.
func (s *FixtureRefillScreen) head(cells int) string {
	var b strings.Builder
	line := func(text string) { b.WriteString(proseFormLine(text, cells) + "\n") }
	if s.isQueue() {
		b.WriteString(StyleTitle.Render(pickerClip(fmt.Sprintf("Pending fixture refill requests (%d)", len(s.rows)), cells)) + "\n")
	} else if f := s.fixture; f != nil {
		state := ""
		if !f.IsActive {
			state = "  " + StyleStatusWarn.Render("inactive")
		}
		room := cells
		if state != "" {
			room -= len("  inactive")
		}
		b.WriteString(StyleTitle.Render(proseFormLine(f.Name, room)) + state + "\n")
		// The short identifiers LEAD their rows: a tag is what the operator
		// matches against the label in front of them and a SKU what they pull
		// off the shelf, and a clip takes the tail — at 80 columns an ordinary
		// location name took the tag with it.
		where := f.LocationName
		if f.AssetTag != nil && strings.TrimSpace(*f.AssetTag) != "" {
			where = "tag " + *f.AssetTag + " · " + where
		}
		b.WriteString(StyleMuted.Render(proseFormLine(where, cells)) + "\n")
		refill := "Refill: " + f.RefillItemName
		if f.RefillItemSKU != "" {
			refill = "Refill: " + f.RefillItemSKU + " · " + f.RefillItemName
		}
		line(refill)
		if d := f.RefillItemDetails; d != nil {
			stock := fmt.Sprintf("Stock %d · minimum %d", d.CurrentStock, d.MinimumStock)
			if d.NeedsReorder {
				b.WriteString(StyleStatusWarn.Render(pickerClip(stock+" · needs reorder", cells)) + "\n")
			} else {
				b.WriteString(StyleMuted.Render(pickerClip(stock, cells)) + "\n")
			}
		}
		if strings.TrimSpace(f.Description) != "" {
			b.WriteString(StyleMuted.Render(proseFormLine(f.Description, cells)) + "\n")
		}
		if !f.IsActive {
			b.WriteString(StyleStatusWarn.Render(pickerClip("Inactive: OMS refuses new refill requests.", cells)) + "\n")
		}
		b.WriteString(StyleTitle.Render(pickerClip(fmt.Sprintf("Pending refill requests (%d)", len(s.rows)), cells)) + "\n")
	}
	if s.note != "" {
		if s.noteErr {
			b.WriteString(StyleStatusError.Render("✗ "+proseFormLine(s.note, cells-2)) + "\n")
		} else {
			b.WriteString(StyleStatusOK.Render("✓ "+proseFormLine(s.note, cells-2)) + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// fixtureWhen is a request's timestamp as an operator reads it: local time, to
// the minute.
func fixtureWhen(r omsapi.FixtureRefillRequest) string {
	if r.RequestedAt.IsZero() {
		return "time not recorded"
	}
	return r.RequestedAt.Local().Format("2006-01-02 15:04")
}

// requestRow is one pending request. On the queue it leads with the FIXTURE,
// because the queue is read to decide where to walk; on a fixture it leads with
// WHEN, because the fixture is already the head. The reporter's note follows,
// every line of it clipped with the cut marked; a note taller than the pane is
// clipped by the window.
func (s *FixtureRefillScreen) requestRow(i int, r omsapi.FixtureRefillRequest, cells int) string {
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	room := cells - StyleSidebarItemActive.GetHorizontalPadding()
	var first, second string
	if s.isQueue() {
		first = caret + r.FixtureName + " · " + r.Requester()
		second = jdeStatusOneLine(r.FixtureLocation) + " · " + fixtureWhen(r)
	} else {
		first = caret + fixtureWhen(r) + " · " + r.Requester()
	}
	line := proseClipEachLine(first, room)
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	if second != "" {
		line += "\n    " + StyleMuted.Render(pickerClip(second, cells-4))
	}
	if strings.TrimSpace(r.Notes) != "" {
		for _, n := range strings.Split(proseClipEachLine(r.Notes, cells-4), "\n") {
			line += "\n    " + n
		}
	}
	return line
}

// viewPrompt draws the prompt in place of the list: what is about to be written,
// what it will do to the record, the notes box, a failure that kept the prompt
// open, and the bar — or the working line while the write is out.
func (s *FixtureRefillScreen) viewPrompt(cells int) string {
	var b strings.Builder
	fold := func(text string) {
		b.WriteString(StyleMuted.Render(strings.Join(pickerWrap(text, cells), "\n")) + "\n")
	}
	name := ""
	if s.fixture != nil {
		name = s.fixture.Name
	}
	var working, caveat string
	switch s.prompt {
	case fixturePromptResolve:
		b.WriteString(StyleTitle.Render("Resolve refill request") + "\n")
		b.WriteString(proseFormLine(firstNonEmpty(s.target.FixtureName, name), cells) + "\n")
		b.WriteString(StyleMuted.Render(proseFormLine(fixtureWhen(s.target)+" · "+s.target.Requester(), cells)) + "\n")
		if strings.TrimSpace(s.target.Notes) != "" {
			b.WriteString(StyleMuted.Render(proseFormLine("Their note: "+s.target.Notes, cells)) + "\n")
		}
		caveat = "Typed notes replace the request's own note; leave blank to keep it."
		working = "Resolving…"
	case fixturePromptResolveAll:
		b.WriteString(StyleTitle.Render("Resolve all pending refill requests") + "\n")
		b.WriteString(proseFormLine(name, cells) + "\n")
		fold(fmt.Sprintf("%d pending on this list. OMS closes every request still pending when it "+
			"receives this, including any filed since the list loaded.", len(s.rows)))
		caveat = "Typed notes replace every request's own note; leave blank to keep them."
		working = "Resolving…"
	default:
		b.WriteString(StyleTitle.Render("Request a refill") + "\n")
		b.WriteString(proseFormLine(name, cells) + "\n")
		if s.fixture != nil {
			b.WriteString(StyleMuted.Render(proseFormLine("Refill: "+s.fixture.RefillItemName, cells)) + "\n")
		}
		caveat = "Filed under your sign-in; logistics is notified."
		working = "Filing…"
	}
	const label = "Notes: "
	b.WriteString("\n" + StyleTitle.Render(label) + woBoxView(s.notes, cells, label) + "\n")
	fold(caveat)
	if s.writing {
		b.WriteString("\n" + StyleMuted.Render(working))
		return b.String()
	}
	if s.writeErr != "" {
		b.WriteString(StyleStatusError.Render("✗ "+proseFormLine(s.writeErr, cells-2)) + "\n")
	}
	b.WriteString("\n" + s.proseBar().render(cells))
	return b.String()
}

// ===========================================================================
// LocationFixturesScreen
// ===========================================================================

// LocationFixturesScreen lists the fixtures installed at one location — the
// web's LocationFixturesList on the location detail page — and opens one.
//
// It lists what `GET /locations/{id}/fixtures/` serves, which is ACTIVE
// fixtures only, and says so: a location whose only fixture was retired reads
// "none" here while its detail's fixture count still counts it.
type LocationFixturesScreen struct {
	deps    Deps
	locID   int
	locName string

	rows           []omsapi.Fixture
	cursor         int
	windowStart    int
	loading        bool
	loadErr        string
	terminalWidth  int
	terminalHeight int
}

type locationFixturesLoadedMsg struct {
	rows []omsapi.Fixture
	err  error
}

func NewLocationFixturesScreen(deps Deps, locID int, locName string) *LocationFixturesScreen {
	return &LocationFixturesScreen{deps: deps, locID: locID, locName: strings.TrimSpace(locName), loading: true}
}

func (s *LocationFixturesScreen) Title() string {
	if s.locName != "" {
		return "Fixtures: " + s.locName
	}
	return "Location fixtures"
}

func (s *LocationFixturesScreen) Init() tea.Cmd { return s.load() }

func (s *LocationFixturesScreen) load() tea.Cmd {
	deps, id := s.deps, s.locID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListLocationFixtures(ctx, id)
		return locationFixturesLoadedMsg{rows: rows, err: err}
	}
}

// locationFixturesBar names every key that acts on a list of `rows` fixtures.
func locationFixturesBar(rows int) proseBar {
	out := proseNavStep(listNavMoves(rows))
	if rows > 0 {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter open"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this screen is DRAWING: loadBar's while a load is out or
// has failed, the list's otherwise.
func (s *LocationFixturesScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return locationFixturesBar(len(s.rows))
}

// loadBar holds `enter` and the movement pair, which act only on rows the load
// frame does not draw.
func (s *LocationFixturesScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

func (s *LocationFixturesScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *LocationFixturesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
	case locationFixturesLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = fixtureSentence(m.err)
			return s, nil
		}
		s.loadErr = ""
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = len(s.rows) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading, s.loadErr = true, ""
			return s, s.load()
		case "enter":
			if s.cursor >= 0 && s.cursor < len(s.rows) {
				return s, SwitchTo(WSInventory, NewFixtureDetailScreen(s.deps, s.rows[s.cursor].ID))
			}
		}
	}
	return s, nil
}

func (s *LocationFixturesScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Loading the fixtures at "+firstNonEmpty(s.locName, "this location")+"…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	var head strings.Builder
	title := firstNonEmpty(s.locName, "Location") + fmt.Sprintf(" — fixtures (%d)", len(s.rows))
	head.WriteString(StyleTitle.Render(proseFormLine(title, cells)) + "\n")
	head.WriteString(StyleMuted.Render(pickerClip("Active fixtures only; OMS does not list inactive ones here.", cells)) + "\n\n")
	bar := s.proseBar()
	if len(s.rows) == 0 {
		return head.String() + StyleMuted.Render("No active fixtures at this location.") + "\n\n" + bar.render(cells)
	}
	rows := make([]string, len(s.rows))
	for i, f := range s.rows {
		rows[i] = s.fixtureRow(i, f, cells)
	}
	return proseFlatListFrame(head.String(), rows, s.cursor, &s.windowStart, s.terminalHeight, cells,
		locationFixturesBar(proseFlatCeilingRows), bar)
}

// fixtureRow is one fixture, its pending count and its refill item. The count
// is a fact and never gives: the name is clipped to what it leaves.
func (s *LocationFixturesScreen) fixtureRow(i int, f omsapi.Fixture, cells int) string {
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	facts := ""
	if f.PendingRequestsCount > 0 {
		facts = fmt.Sprintf(" · %d pending", f.PendingRequestsCount)
	}
	room := cells - StyleSidebarItemActive.GetHorizontalPadding() - len(caret) - len(facts)
	line := caret + proseClipEachLine(f.Name, room) + facts
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	refill := "Refill: " + jdeStatusOneLine(f.RefillItemName)
	if f.RefillItemSKU != "" {
		refill = "Refill: " + f.RefillItemSKU + " · " + jdeStatusOneLine(f.RefillItemName)
	}
	return line + "\n    " + StyleMuted.Render(pickerClip(refill, cells-4))
}
