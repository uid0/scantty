// Authorization grant — the "create" half of ForgeKey authorizations
// create/revoke.
//
// TUI counterpart to the web AssetForgeKeyAccessCard's staff-only "Grant access"
// flow. That card lives on a single asset's detail page, so its asset is fixed
// context and it collects just {user, notes}; it POSTs {asset, user, notes} to
// /forgekey/authorizations/ (authorized_by is server-set). ScanTTY has no
// per-asset ForgeKey access card, so this reaches the same grant from the global
// Authorizations list (n) and adds an asset picker — the SAME three fields the web
// sends, no more (the serializer's expires_at is accepted by the API but the web
// UI never sets it, so — this being access control — it is deliberately omitted).
//
// Staff-gated: the caller (AuthorizationsScreen) only opens this for staff, matching
// the card's isStaff gate.
//
// The sheet renders through the columnar "JD Edwards" layer (jde_form.go) as of
// sc-lmsi, sweep E of the sc-h412 redesign: right-aligned labels in one column,
// a persistent action bar naming exactly the keys that apply, and the shared
// jdePickList — whose filter is always live — for the two foreign keys.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

const (
	agAsset = iota
	agUser
	agNotes
	agFieldMax
)

type authGrantPhase int

const (
	authGrantPhaseForm authGrantPhase = iota
	authGrantPhasePick
)

var authGrantFieldLabel = map[int]string{
	agAsset: "Asset",
	agUser:  "Member",
	agNotes: "Notes",
}

// authGrantLabelWidth is this sheet's own label column. The grant form is
// opened from the Authorizations list and returns to it; it is never on screen
// beside another converted sheet, so there is no family to share a column with
// (the storage batch's rule, sc-6qsk).
var authGrantLabelWidth = jdeLabelWidth(jdeLabelFields(authGrantFieldLabel))

// authGrantFieldHint marks what is required. Both foreign keys are — the
// serializer rejects a grant without either — and a columnar sheet says that
// rather than tagging everything else "(optional)" (sc-dnhx).
var authGrantFieldHint = map[int]string{
	agAsset: "required",
	agUser:  "required",
}

// authGrantFieldKind classifies the three rows for the bar and the key
// handling: two foreign keys Ctrl-E opens, and one free-text note.
func authGrantFieldKind(id int) assetFieldKind {
	switch id {
	case agAsset, agUser:
		return akPicker
	}
	return akText
}

type AuthorizationGrantScreen struct {
	deps Deps

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	assets []omsapi.Asset
	users  []omsapi.User

	notesInput textinput.Model
	assetID    *string
	userID     *int

	fields []int
	cursor int

	jdeScreen

	phase       authGrantPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []assetPickRow
}

type authGrantRefLoadedMsg struct {
	assets []omsapi.Asset
	users  []omsapi.User
	err    error
}

type authGrantSavedMsg struct {
	auth *forgekeyapi.Authorization
	err  error
}

func NewAuthorizationGrantScreen(deps Deps) *AuthorizationGrantScreen {
	s := &AuthorizationGrantScreen{deps: deps, loading: true}
	s.notesInput = textinput.New()
	s.notesInput.Prompt = ""
	s.notesInput.CharLimit = 500
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60
	s.fields = []int{agAsset, agUser, agNotes}
	s.syncFocus()
	return s
}

func (s *AuthorizationGrantScreen) Title() string { return "Grant access" }

func (s *AuthorizationGrantScreen) WantsRawInput() bool { return true }

func (s *AuthorizationGrantScreen) Init() tea.Cmd {
	return tea.Batch(s.loadRefData(), textinput.Blink)
}

func (s *AuthorizationGrantScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AuthorizationGrantScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		assets, err := deps.OMS.ListAllAssets(ctx)
		if err != nil {
			return authGrantRefLoadedMsg{err: err}
		}
		users, err := deps.OMS.ListAllUsers(ctx)
		if err != nil {
			return authGrantRefLoadedMsg{err: err}
		}
		return authGrantRefLoadedMsg{assets: assets, users: users}
	}
}

func (s *AuthorizationGrantScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case authGrantRefLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.assets = m.assets
			s.users = m.users
		}
		s.syncFocus()
		return s, nil
	case authGrantSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("grant failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("access granted", StatusOK),
			SwitchTo(WSAuthorizations, NewAuthorizationsScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == authGrantPhasePick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == authGrantPhasePick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && id == agNotes {
		var cmd tea.Cmd
		s.notesInput, cmd = s.notesInput.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *AuthorizationGrantScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *AuthorizationGrantScreen) syncFocus() {
	s.notesInput.Blur()
	if id, ok := s.currentFieldID(); ok && id == agNotes {
		s.notesInput.Focus()
	}
}

func (s *AuthorizationGrantScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	case "ctrl+e":
		// EDIT opens whatever the highlighted row IS — here, either foreign key.
		if id, ok := s.currentFieldID(); ok && authGrantFieldKind(id) == akPicker {
			s.openPicker(id)
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch authGrantFieldKind(id) {
	case akPicker:
		// A picker row has nothing to type into and no accelerators left: space
		// used to open it and is now filter text inside the picker itself.
		return s, nil
	default:
		var cmd tea.Cmd
		s.notesInput, cmd = s.notesInput.Update(m)
		return s, cmd
	}
}

func (s *AuthorizationGrantScreen) moveCursor(delta int) {
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
func (s *AuthorizationGrantScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *AuthorizationGrantScreen) openPicker(field int) {
	s.phase = authGrantPhasePick
	s.pickField = field
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
	s.applyPickFilter()
	s.pickCursor = 0
	// Rest the cursor on the current selection so re-opening is a no-op.
	var selKey string
	switch field {
	case agAsset:
		if s.assetID != nil {
			selKey = *s.assetID
		}
	case agUser:
		if s.userID != nil {
			selKey = strconv.Itoa(*s.userID)
		}
	}
	if selKey != "" {
		for i, o := range s.pickOptions {
			if !o.clear && o.key == selKey {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *AuthorizationGrantScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	var opts []assetPickRow
	// Both fields are required, so neither offers a "(none)" clear row.
	add := func(key, label string) {
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, assetPickRow{key: key, label: label})
		}
	}
	switch s.pickField {
	case agAsset:
		for _, a := range s.assets {
			add(thermostatAssetKey(a), thermostatAssetLabel(a))
		}
	case agUser:
		for _, u := range s.users {
			add(strconv.Itoa(u.ID), assetUserLabel(u))
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *AuthorizationGrantScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		s.commitPick()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		if next, ok := s.pageRow(body, s.pickCursor, len(s.pickOptions), delta, len(header),
			s.pickBar(header, body), jdePickBarCeiling("Select", "Cancel")); ok {
			s.pickCursor = next
		}
	default:
		// Anything else is filter text: the box is always live, so there is no
		// mode to enter and no "/" to remember.
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}
	return s, nil
}

func (s *AuthorizationGrantScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *AuthorizationGrantScreen) closePicker() {
	s.phase = authGrantPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *AuthorizationGrantScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		if !opt.clear {
			switch s.pickField {
			case agAsset:
				v := opt.key
				s.assetID = &v
			case agUser:
				s.userID = pickInt(opt)
			}
		}
	}
	s.closePicker()
}

func (s *AuthorizationGrantScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	return s, func() tea.Msg {
		auth, e := deps.ForgeKey.GrantAuthorization(ctx, body)
		return authGrantSavedMsg{auth: auth, err: e}
	}
}

func (s *AuthorizationGrantScreen) buildPayload() (forgekeyapi.AuthorizationWrite, error) {
	var w forgekeyapi.AuthorizationWrite
	if s.assetID == nil {
		return w, errors.New("asset is required")
	}
	if s.userID == nil {
		return w, errors.New("member is required")
	}
	w = forgekeyapi.AuthorizationWrite{
		Asset: *s.assetID,
		User:  *s.userID,
		Notes: strings.TrimSpace(s.notesInput.Value()),
	}
	return w, nil
}

func (s *AuthorizationGrantScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSAuthorizations, NewAuthorizationsScreen(s.deps))
}

func (s *AuthorizationGrantScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading assets and members…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == authGrantPhasePick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *AuthorizationGrantScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Granting…", s.errMsg), s.formBar(body))
}

// formFields describes the sheet as columnar rows: the two foreign keys Ctrl-E
// opens, and the note typed into.
func (s *AuthorizationGrantScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   authGrantFieldLabel[id],
			Width:   authGrantFieldWidth(id),
			Hint:    authGrantFieldHint[id],
			Focused: i == s.cursor,
		}
		switch authGrantFieldKind(id) {
		case akPicker:
			value, dim := s.pickerValue(id)
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		default:
			f.Kind, f.Input = jdeText, &s.notesInput
		}
		out[i] = f
	}
	return out
}

// authGrantFieldWidth sizes the one input area that is not the default.
func authGrantFieldWidth(id int) int {
	if id == agNotes {
		return 40
	}
	return 0
}

func (s *AuthorizationGrantScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Grant access"))
	l.AddFittedFields(fields, authGrantLabelWidth, s.bodyWidth(), 0)
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *AuthorizationGrantScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, len(s.fields), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *AuthorizationGrantScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Grant"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok && authGrantFieldKind(id) == akPicker {
		items = append(items, actionBarItem{"Ctrl-E", "Pick"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// pickerValue is a foreign-key row's text, and whether it is an empty state
// rather than a value. It returns PLAIN text with a flag instead of pre-styled
// muted text, because a focused row has to be able to reverse-video the whole
// field (sc-h412).
func (s *AuthorizationGrantScreen) pickerValue(id int) (string, bool) {
	switch id {
	case agAsset:
		if s.assetID == nil {
			return "(not set)", true
		}
		for _, a := range s.assets {
			if thermostatAssetKey(a) == *s.assetID {
				return thermostatAssetLabel(a), false
			}
		}
		return *s.assetID, false
	case agUser:
		if s.userID == nil {
			return "(not set)", true
		}
		for _, u := range s.users {
			if u.ID == *s.userID {
				return assetUserLabel(u), false
			}
		}
		return fmt.Sprintf("#%d", *s.userID), false
	}
	return "", false
}

// pickView builds the open picker's pinned header and its option list.
func (s *AuthorizationGrantScreen) pickView() (jdeHeader, *jdeLines) {
	title, empty := "Asset", "(no matching assets)"
	if s.pickField == agUser {
		title, empty = "Member", "(no matching members)"
	}
	return jdePickList{
		Title:  title,
		Note:   "Both are required, so neither list offers a row that clears it.",
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Cursor: s.pickCursor,
		Empty:  empty,
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *AuthorizationGrantScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", len(s.pickOptions), s.bodyPagesForBar(body, len(s.pickOptions), len(header), jdePickBarCeiling("Select", "Cancel")))
}

func (s *AuthorizationGrantScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}
