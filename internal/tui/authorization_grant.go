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
// the card's isStaff gate. The picker sub-phase mirrors the thermostat form.
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

	fields         []int
	cursor         int
	terminalHeight int

	phase       authGrantPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
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
	s.notesInput.Placeholder = "optional"
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
		s.terminalHeight = m.Height
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
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
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
	switch id {
	case agAsset, agUser:
		if m.String() == " " {
			s.openPicker(id)
			return s, textinput.Blink
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.notesInput, cmd = s.notesInput.Update(m)
		return s, cmd
	}
}

func (s *AuthorizationGrantScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *AuthorizationGrantScreen) openPicker(field int) {
	s.phase = authGrantPhasePick
	s.pickField = field
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
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
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyPickFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = authGrantPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.pickCursor < len(s.pickOptions)-1 {
			s.pickCursor++
		}
	case "k", "up":
		if s.pickCursor > 0 {
			s.pickCursor--
		}
	case "/":
		s.pickTyping = true
		s.pickSearch.Focus()
		return s, textinput.Blink
	case "enter":
		s.commitPick()
	}
	return s, nil
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
	s.phase = authGrantPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
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
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")
	for i := range s.fields {
		b.WriteString(s.renderField(i) + "\n")
	}
	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *AuthorizationGrantScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case agAsset:
			kindHelp = "space to pick asset"
		case agUser:
			kindHelp = "space to pick member"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter grant · esc cancel"
}

func (s *AuthorizationGrantScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := authGrantFieldLabel[id]
	var value string
	switch id {
	case agAsset:
		value = s.assetLabel()
	case agUser:
		value = s.userLabel()
	default:
		value = s.notesInput.View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *AuthorizationGrantScreen) assetLabel() string {
	if s.assetID == nil {
		return StyleMuted.Render("(required — space to pick)")
	}
	for _, a := range s.assets {
		if thermostatAssetKey(a) == *s.assetID {
			return thermostatAssetLabel(a)
		}
	}
	return *s.assetID
}

func (s *AuthorizationGrantScreen) userLabel() string {
	if s.userID == nil {
		return StyleMuted.Render("(required — space to pick)")
	}
	for _, u := range s.users {
		if u.ID == *s.userID {
			return assetUserLabel(u)
		}
	}
	return fmt.Sprintf("#%d", *s.userID)
}

func (s *AuthorizationGrantScreen) viewPick() string {
	var b strings.Builder
	title := "Pick asset"
	if s.pickField == agUser {
		title = "Pick member"
	}
	b.WriteString(StyleMuted.Render(title+" — j/k move · / filter · enter select · esc back") + "\n\n")
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n\n")
	}
	if len(s.pickOptions) == 0 {
		b.WriteString(StyleMuted.Render("(no matches)"))
		return b.String()
	}
	const window = 12
	start, end := fieldWindow(s.pickCursor, len(s.pickOptions), window)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		opt := s.pickOptions[i]
		if i == s.pickCursor {
			b.WriteString(StyleSidebarItemActive.Render(caret+opt.label) + "\n")
		} else {
			b.WriteString(caret + opt.label + "\n")
		}
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}
