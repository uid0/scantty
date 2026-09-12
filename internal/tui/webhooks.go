// Webhook CRUD — list/management + create/edit form + test-delivery.
//
// TUI counterpart to the web WebhookListPage / WebhookFormPage / WebhookDetailPage
// (Settings · Webhooks). ScanTTY had no webhook surface at all; this adds the
// full management surface so an operator can register, edit, delete and
// test-fire outbound event webhooks from the workstation without the browser
// ([[scantty-parity-program]], [[ship-complete-features]]).
//
// The form mirrors the FULL writable serializer field set (WebHookCreateSerializer:
// name, description, url, event_type, is_active, secret, headers). event_type is a
// single cycling select over the model's twelve EVENT_TYPE_CHOICES — the web
// form hardcodes only eleven (it omits location_problem_reported), but the
// serializer accepts every model choice, so the complete set is offered here.
// secret is a write-only field: the API never returns it, so it always starts
// blank and an empty secret is dropped from the payload ("leave blank to keep the
// current secret"), matching the web. There is NO regenerate-secret action on the
// backend — rotation is a plain secret write.
//
// WebhookFormScreen renders through the columnar "JD Edwards" layer
// (jde_form.go) as of sc-lmsi, sweep E of the sc-h412 redesign: right-aligned
// labels in one column, the event type as a "< value >" set with its options
// strip, and a persistent action bar naming exactly the keys that apply. The
// list below is a browse surface, not a form, and is untouched by that sweep.
//
// WebhookListScreen is reached with the global `W` hotkey (app.go), riding the
// same "uppercase letter = open a surface" convention as G (categories) / V
// (vendors) / B (maker boxes). n new, E/enter edit, x delete, t test-delivery,
// r refresh. n and G collide with global hotkeys so the screen implements
// LocalKeyScreen to claim them; it flips to raw input only while a confirm or the
// test-result panel is up so those keystrokes land here.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// webhookEventTypeOptions are the model's twelve EVENT_TYPE_CHOICES (exact codes
// — a wrong code 400s at save). The web form omits location_problem_reported;
// the serializer accepts it, so it is included here for completeness.
var webhookEventTypeOptions = []selectOption{
	{"reorder_request_created", "Reorder Request Created"},
	{"reorder_request_approved", "Reorder Request Approved"},
	{"reorder_request_ordered", "Reorder Request Ordered"},
	{"reorder_request_received", "Reorder Request Received"},
	{"item_low_stock", "Item Low Stock"},
	{"purchase_order_created", "Purchase Order Created"},
	{"delivery_received", "Delivery Received"},
	{"fixture_refill_requested", "Fixture Refill Requested"},
	{"location_checkin", "Location Check-in"},
	{"location_feedback", "Location Feedback"},
	{"security_report", "Security Report"},
	{"location_problem_reported", "Location Problem Reported"},
}

// ===========================================================================
// WebhookFormScreen
// ===========================================================================

const (
	whName = iota
	whDescription
	whURL
	whEventType // select
	whIsActive  // toggle
	whSecret    // text (password echo)
	whHeaders   // text (JSON object)
	whFieldMax
)

var webhookFieldLabel = map[int]string{
	whName:        "Name",
	whDescription: "Description",
	whURL:         "Webhook URL",
	whEventType:   "Event type",
	whIsActive:    "Active",
	// "(HMAC)" is not part of the label: a parenthetical widens the column every
	// row is aligned to and shoves every input area right (sc-ye0i). It says what
	// the secret is FOR, which is a hint.
	whSecret:  "Secret",
	whHeaders: "Custom headers",
}

// webhookLabelWidth is this sheet's own label column. The webhook form is
// opened from the webhook list and returns to it — it is never on screen beside
// another converted sheet, so there is no family to share a column with (the
// storage batch's rule, sc-6qsk).
var webhookLabelWidth = jdeLabelWidth(jdeLabelFields(webhookFieldLabel))

// webhookFieldHint carries what the placeholders and the label parenthetical
// used to say. A placeholder long enough to fill the input area leaves no
// underscores, so an empty green-screen row stops reading as empty (sc-dnhx) —
// and "optional" on a description is noise in a form that marks what is
// REQUIRED instead.
var webhookFieldHint = map[int]string{
	whName:   "required",
	whURL:    "required",
	whSecret: "HMAC · blank keeps current",
}

// webhookHeadersNote is the headers example, which is far too long to ride as a
// Hint on the row (the pane would clip it), so it sits as a note under it. It is
// worded exactly like parseWebhookHeaders' rejection message, so the sheet and
// the error the operator gets for guessing wrong say the same thing.
const webhookHeadersNote = `JSON object of string values, e.g. {"X-Key": "value"}`

// webhookFieldWidth sizes the input areas that are not the default.
func webhookFieldWidth(id int) int {
	switch id {
	case whURL, whHeaders:
		return 40
	case whName, whDescription:
		return 34
	case whSecret:
		return 24
	}
	return 0
}

func webhookFieldKind(id int) assetFieldKind {
	switch id {
	case whName, whDescription, whURL, whSecret, whHeaders:
		return akText
	case whIsActive:
		return akToggle
	case whEventType:
		return akSelect
	}
	return akText
}

type WebhookFormScreen struct {
	deps      Deps
	edit      bool
	webhookID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	webhook *omsapi.Webhook

	jdeScreen

	inputs       []textinput.Model
	eventTypeIdx int
	isActive     bool

	fields []int
	cursor int
}

type webhookRecordLoadedMsg struct {
	webhook *omsapi.Webhook
	err     error
}

type webhookSavedMsg struct {
	webhook *omsapi.Webhook
	err     error
}

// NewWebhookFormScreen opens create mode when webhookID == 0, else edit mode.
func NewWebhookFormScreen(deps Deps, webhookID int) *WebhookFormScreen {
	s := &WebhookFormScreen{
		deps:      deps,
		edit:      webhookID != 0,
		webhookID: webhookID,
		loading:   webhookID != 0, // create mode has no ref data to wait on
		isActive:  true,           // serializer default
	}
	s.inputs = make([]textinput.Model, whFieldMax)
	for id := 0; id < whFieldMax; id++ {
		if webhookFieldKind(id) != akText {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = webhookCharLimit(id)
		if id == whSecret {
			// Never echo the secret back on screen (matches the web password input).
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		s.inputs[id] = ti
	}
	s.fields = []int{whName, whDescription, whURL, whEventType, whIsActive, whSecret, whHeaders}
	s.syncFocus()
	return s
}

func webhookCharLimit(id int) int {
	switch id {
	case whName:
		return 200
	case whURL:
		return 500
	case whDescription:
		return 1000
	case whSecret:
		return 255
	case whHeaders:
		return 2000
	}
	return 200
}

func (s *WebhookFormScreen) Title() string {
	if s.edit {
		if s.webhook != nil && s.webhook.Name != "" {
			return "Edit webhook: " + s.webhook.Name
		}
		return "Edit webhook"
	}
	return "New webhook"
}

func (s *WebhookFormScreen) WantsRawInput() bool { return true }

func (s *WebhookFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadRecord())
	}
	return tea.Batch(cmds...)
}

func (s *WebhookFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *WebhookFormScreen) loadRecord() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.webhookID
	return func() tea.Msg {
		h, err := deps.OMS.GetWebhook(ctx, id)
		return webhookRecordLoadedMsg{webhook: h, err: err}
	}
}

func (s *WebhookFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case webhookRecordLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.webhook = m.webhook
			s.hydrate()
		}
		s.syncFocus()
		return s, nil
	case webhookSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		name := ""
		if m.webhook != nil {
			name = m.webhook.Name
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("webhook %s: %s", verb, name), StatusOK),
			SwitchTo(WSSettings, NewWebhookListScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		return s.updateFormPhase(m)
	}

	if id, ok := s.currentFieldID(); ok && webhookFieldKind(id) == akText {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *WebhookFormScreen) hydrate() {
	h := s.webhook
	s.inputs[whName].SetValue(h.Name)
	s.inputs[whDescription].SetValue(h.Description)
	s.inputs[whURL].SetValue(h.URL)
	s.eventTypeIdx = selectIndexOf(webhookEventTypeOptions, h.EventType)
	s.isActive = h.IsActive
	// secret is write-only — the API never returns it, so it stays blank and an
	// empty submit leaves the stored secret untouched.
	if len(h.Headers) > 0 {
		if raw, err := json.Marshal(h.Headers); err == nil {
			s.inputs[whHeaders].SetValue(string(raw))
		}
	}
}

func (s *WebhookFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *WebhookFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if webhookFieldKind(id) == akText {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && webhookFieldKind(id) == akText {
		s.inputs[id].Focus()
	}
}

func (s *WebhookFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "pgdown":
		s.pageCursor(+1)
		return s, textinput.Blink
	case "pgup":
		s.pageCursor(-1)
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s.submit()
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch webhookFieldKind(id) {
	case akToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			s.isActive = !s.isActive
		}
		return s, nil
	case akSelect:
		switch m.String() {
		case " ", "right":
			s.cycleEventType(+1)
		case "left":
			s.cycleEventType(-1)
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *WebhookFormScreen) moveCursor(delta int) {
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, len(s.fields), delta, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *WebhookFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *WebhookFormScreen) cycleEventType(delta int) {
	n := len(webhookEventTypeOptions)
	s.eventTypeIdx = (s.eventTypeIdx + delta + n) % n
}

func (s *WebhookFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	edit := s.edit
	id := s.webhookID
	return s, func() tea.Msg {
		var h *omsapi.Webhook
		var e error
		if edit {
			h, e = deps.OMS.UpdateWebhook(ctx, id, body)
		} else {
			h, e = deps.OMS.CreateWebhook(ctx, body)
		}
		return webhookSavedMsg{webhook: h, err: e}
	}
}

func (s *WebhookFormScreen) buildPayload() (omsapi.WebhookWrite, error) {
	var w omsapi.WebhookWrite
	name := strings.TrimSpace(s.inputs[whName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	if err := validWebhookURL(s.inputs[whURL].Value()); err != nil {
		return w, err
	}
	headers, err := parseWebhookHeaders(s.inputs[whHeaders].Value())
	if err != nil {
		return w, err
	}
	// Drop a blank secret so an edit keeps the stored one (omitempty on the wire).
	secret := s.inputs[whSecret].Value()
	if strings.TrimSpace(secret) == "" {
		secret = ""
	}
	w = omsapi.WebhookWrite{
		Name:        name,
		Description: strings.TrimSpace(s.inputs[whDescription].Value()),
		URL:         strings.TrimSpace(s.inputs[whURL].Value()),
		EventType:   webhookEventTypeOptions[s.eventTypeIdx].value,
		IsActive:    s.isActive,
		Secret:      secret,
		Headers:     headers,
	}
	return w, nil
}

// validWebhookURL requires a non-empty absolute http(s) URL, mirroring the web
// form's zod .url() so an obviously-bad URL is caught before the round-trip. The
// backend URLField is the authority; this is just a fast, clear pre-check.
func validWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("URL is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" {
		return errors.New("URL must be a full http(s):// address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL must start with http:// or https://")
	}
	return nil
}

// parseWebhookHeaders turns the headers text field into a JSON object of string
// values. Empty → an empty (non-nil) map so the payload sends {} not null (the
// model's JSONField is null=False). Anything that isn't a flat string→string
// object is rejected with a clear message.
func parseWebhookHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, errors.New(`headers must be a JSON object of string values, e.g. {"X-Key": "value"}`)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

func (s *WebhookFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSSettings, NewWebhookListScreen(s.deps))
}

func (s *WebhookFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	return s.viewForm()
}

func (s *WebhookFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the sheet as columnar rows: one bounded set of event
// types, one toggle, and the rest typed into.
func (s *WebhookFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   webhookFieldLabel[id],
			Width:   webhookFieldWidth(id),
			Hint:    webhookFieldHint[id],
			Focused: i == s.cursor,
		}
		switch webhookFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.isActive)
		case akSelect:
			f.Kind, f.Value = jdeChoice, s.eventTypeValue()
		default:
			// The secret is masked whether or not the cursor is on it —
			// jdeInputValue applies the box's echo mode to a blurred row too.
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

// eventTypeValue is the BARE label between the row's angle brackets — the
// renderer owns the brackets (sc-0zvi: elecSelectLabel's own "‹ ›" nested a
// second pair inside them).
func (s *WebhookFormScreen) eventTypeValue() string {
	if s.eventTypeIdx >= 0 && s.eventTypeIdx < len(webhookEventTypeOptions) {
		return webhookEventTypeOptions[s.eventTypeIdx].label
	}
	return ""
}

func (s *WebhookFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Webhook"))
	for i, id := range s.fields {
		l.AddFittedField(i, fields[i], webhookLabelWidth, s.bodyWidth())
		switch {
		case i == s.cursor && id == whEventType:
			// Twelve event types is more than a "< value >" row can say on its
			// own, so the focused row lists the set — windowed around the current
			// entry, which is what keeps the bracketed one on screen.
			labels := make([]string, len(webhookEventTypeOptions))
			for j, o := range webhookEventTypeOptions {
				labels[j] = o.label
			}
			strip := jdeOptionStrip(labels, s.eventTypeIdx, jdeStripWidth(s.bodyWidth(), webhookLabelWidth))
			if strip != "" {
				l.AddRow(i, jdeStripIndent(webhookLabelWidth)+StyleMuted.Render(strip))
			}
		case id == whHeaders:
			for _, line := range jdeNoteLines(webhookHeadersNote, webhookLabelWidth, s.bodyWidth()) {
				l.AddRow(i, line)
			}
		}
	}
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *WebhookFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, len(s.fields), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here. Nothing on this sheet
// OPENS, so Ctrl-E is never offered.
func (s *WebhookFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch webhookFieldKind(id) {
		case akToggle, akSelect:
			items = append(items, actionBarItem{"←→", "Change"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// ===========================================================================
// WebhookListScreen
// ===========================================================================

const webhookTestMaxPolls = 30

type WebhookListScreen struct {
	deps           Deps
	rows           []omsapi.Webhook
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool

	confirmingTest bool
	testing        bool
	showTestResult bool
	testResult     *omsapi.WebhookTestResult
	testName       string
	pollTaskID     string
	pollAttempts   int
}

type webhookListLoadedMsg struct {
	rows []omsapi.Webhook
	err  error
}

type webhookDeletedMsg struct {
	err error
}

type webhookTestStartedMsg struct {
	result *omsapi.WebhookTestResult
	err    error
}

type webhookTestPolledMsg struct {
	result *omsapi.WebhookTestResult
	err    error
}

func NewWebhookListScreen(deps Deps) *WebhookListScreen {
	return &WebhookListScreen{deps: deps, loading: true, windowSize: 18}
}

func (s *WebhookListScreen) Title() string { return "Webhooks" }

// WantsRawInput claims every key while a confirm or the test-result panel is up,
// so y/n/esc/dismiss land here instead of the root's global hotkeys.
func (s *WebhookListScreen) WantsRawInput() bool {
	return s.confirmingDelete || s.confirmingTest || s.showTestResult
}

// HandlesKey claims the two action keys that collide with global hotkeys (n new,
// G bottom) so they reach this screen instead of the global nav switch.
func (s *WebhookListScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *WebhookListScreen) Init() tea.Cmd { return s.load() }

func (s *WebhookListScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *WebhookListScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		rows, err := deps.OMS.ListWebhooks(ctx)
		return webhookListLoadedMsg{rows: rows, err: err}
	}
}

func (s *WebhookListScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *WebhookListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case webhookListLoadedMsg:
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
	case webhookDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("webhook deleted", StatusOK), s.load())
	case webhookTestStartedMsg:
		return s.onTestStarted(m)
	case webhookTestPolledMsg:
		return s.onTestPolled(m)
	case tea.KeyMsg:
		if s.showTestResult {
			return s.updateTestResult(m)
		}
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.confirmingTest {
			return s.updateConfirmTest(m)
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
		case "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "pgup":
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
			return s, SwitchTo(WSSettings, NewWebhookFormScreen(s.deps, 0))
		case "E", "enter":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSSettings, NewWebhookFormScreen(s.deps, row.ID))
			}
		case "t":
			if _, ok := s.selected(); ok {
				s.confirmingTest = true
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *WebhookListScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		id := row.ID
		return s, func() tea.Msg {
			return webhookDeletedMsg{err: deps.OMS.DeleteWebhook(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *WebhookListScreen) updateConfirmTest(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.testing {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingTest = false
			return s, nil
		}
		s.testing = true
		s.testName = row.Name
		deps := s.deps
		ctx := s.ctx()
		id := row.ID
		return s, func() tea.Msg {
			res, err := deps.OMS.TestWebhook(ctx, id)
			return webhookTestStartedMsg{result: res, err: err}
		}
	case "n", "N", "esc":
		s.confirmingTest = false
	}
	return s, nil
}

func (s *WebhookListScreen) onTestStarted(m webhookTestStartedMsg) (Screen, tea.Cmd) {
	s.testing = false
	s.confirmingTest = false
	if m.err != nil {
		return s, Status("test failed: "+m.err.Error(), StatusError)
	}
	s.testResult = m.result
	s.showTestResult = true
	s.pollAttempts = 0
	// Async (Celery) mode returns a task to poll; eager mode is already terminal.
	if !webhookTestTerminal(m.result) && m.result.TaskID != "" {
		s.pollTaskID = m.result.TaskID
		return s, s.pollTestCmd()
	}
	s.pollTaskID = ""
	return s, nil
}

func (s *WebhookListScreen) onTestPolled(m webhookTestPolledMsg) (Screen, tea.Cmd) {
	// The user may have dismissed the panel while a poll tick was in flight.
	if !s.showTestResult || s.pollTaskID == "" {
		return s, nil
	}
	if m.err != nil {
		return s, Status("test status check failed: "+m.err.Error(), StatusError)
	}
	s.testResult = m.result
	s.pollAttempts++
	if webhookTestTerminal(m.result) {
		s.pollTaskID = ""
		return s, nil
	}
	if s.pollAttempts >= webhookTestMaxPolls {
		s.pollTaskID = ""
		return s, Status("test still pending — check the webhook detail later", StatusWarn)
	}
	return s, s.pollTestCmd()
}

func (s *WebhookListScreen) pollTestCmd() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	taskID := s.pollTaskID
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		res, err := deps.OMS.GetWebhookTestStatus(ctx, taskID)
		return webhookTestPolledMsg{result: res, err: err}
	})
}

func (s *WebhookListScreen) updateTestResult(m tea.KeyMsg) (Screen, tea.Cmd) {
	// Any key dismisses the result panel (and abandons any in-flight poll).
	s.showTestResult = false
	s.testResult = nil
	s.pollTaskID = ""
	return s, nil
}

// webhookTestTerminal reports whether a test result has resolved (no more polling
// needed). A nil result or any non-PENDING status is terminal; eager-mode results
// come back already SUCCESS/FAILURE.
func webhookTestTerminal(r *omsapi.WebhookTestResult) bool {
	return r == nil || r.TaskStatus != "PENDING"
}

func (s *WebhookListScreen) selected() (omsapi.Webhook, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.Webhook{}, false
	}
	return s.rows[s.cursor], true
}

func (s *WebhookListScreen) scrollIntoView() {
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

func (s *WebhookListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading webhooks…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.showTestResult {
		return s.viewTestResult()
	}
	if s.confirmingDelete {
		return s.viewConfirmDelete()
	}
	if s.confirmingTest {
		return s.viewConfirmTest()
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No webhooks configured.") + "\n\n" + StyleMuted.Render("n new webhook · esc back")
	}

	var b strings.Builder
	// Warn, but deliberately do NOT disable the test. The status endpoint
	// aggregates the whole webhook family — it cannot say whether THIS
	// endpoint's breaker is the open one — and re-testing an endpoint is
	// exactly what a staffer does during a delivery outage. Same call the web
	// makes on WebhookDetailPage; the two device/billing gates above it
	// disable, this one only tells you what to expect.
	if notice := serviceUnavailableLine(s.deps.Health, omsapi.ServiceKeyWebhooks, webhookDeliveryDegraded); notice != "" {
		b.WriteString(notice)
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d webhooks", len(s.rows))) + "\n")
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
	b.WriteString(StyleMuted.Render("j/k move · n new · E/enter edit · t test · x delete · r refresh · esc back"))
	return b.String()
}

func (s *WebhookListScreen) viewConfirmDelete() string {
	name := ""
	if row, ok := s.selected(); ok {
		name = row.Name
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Delete webhook %q? This can't be undone.  y delete · n/esc cancel", name))
}

func (s *WebhookListScreen) viewConfirmTest() string {
	name := ""
	if row, ok := s.selected(); ok {
		name = row.Name
	}
	if s.testing {
		return StyleMuted.Render("Sending test delivery…")
	}
	prompt := StyleStatusWarn.Render(fmt.Sprintf("Send a test delivery to %q now?  y send · n/esc cancel", name))
	// The confirm is the last thing between the operator and the request, so
	// the "this may fail or be retried" caveat belongs here too — still a
	// warning, still not a block.
	if notice := serviceUnavailableNotice(s.deps.Health, omsapi.ServiceKeyWebhooks, webhookDeliveryDegraded); notice != "" {
		return notice + "\n\n" + prompt
	}
	return prompt
}

func (s *WebhookListScreen) viewTestResult() string {
	var b strings.Builder
	name := s.testName
	if name == "" {
		name = "webhook"
	}
	b.WriteString(StyleTitle.Render("Test result — "+name) + "\n\n")
	r := s.testResult
	if r == nil {
		b.WriteString(StyleMuted.Render("(no result)") + "\n")
	} else if !webhookTestTerminal(r) {
		b.WriteString(StyleStatusWarn.Render("⏳ Pending — waiting for delivery to complete…") + "\n")
	} else if r.Success != nil && *r.Success {
		b.WriteString(StyleStatusOK.Render("✓ Delivered successfully") + "\n")
	} else {
		b.WriteString(StyleStatusError.Render("✗ Delivery failed") + "\n")
	}
	if r != nil {
		meta := []string{}
		if r.StatusCode != nil {
			meta = append(meta, fmt.Sprintf("HTTP %d", *r.StatusCode))
		}
		if r.ResponseTimeMs != nil {
			meta = append(meta, fmt.Sprintf("%.0f ms", *r.ResponseTimeMs))
		}
		if r.TestedAt != nil {
			meta = append(meta, r.TestedAt.Format("2006-01-02 15:04:05"))
		}
		if len(meta) > 0 {
			b.WriteString(StyleMuted.Render("  "+strings.Join(meta, " · ")) + "\n")
		}
		if r.ErrorMessage != "" {
			b.WriteString(StyleStatusError.Render("  error: ") + truncateOneLine(r.ErrorMessage, 200) + "\n")
		}
		if r.ResponseBody != "" {
			b.WriteString(StyleMuted.Render("  response: ") + truncateOneLine(r.ResponseBody, 200) + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("press any key to dismiss"))
	return b.String()
}

// truncateOneLine collapses newlines and clips to n runes so a webhook's error /
// response body can't blow up the result panel's height.
func truncateOneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func (s *WebhookListScreen) renderRow(i int) string {
	h := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	title := h.Name
	if h.EventTypeDisplay != "" {
		title += " " + StyleMuted.Render("→ "+h.EventTypeDisplay)
	}
	line := marker + title
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	badges := []string{}
	if h.IsActive {
		badges = append(badges, "active")
	} else {
		badges = append(badges, "inactive")
	}
	if h.SuccessRate != nil {
		badges = append(badges, fmt.Sprintf("%.0f%%", *h.SuccessRate))
	}
	if h.TotalTriggers > 0 {
		badges = append(badges, fmt.Sprintf("%d fired", h.TotalTriggers))
	}
	if h.LastError != "" {
		badges = append(badges, "last errored")
	}
	line += " " + StyleMuted.Render("("+strings.Join(badges, " · ")+")")
	return line
}
