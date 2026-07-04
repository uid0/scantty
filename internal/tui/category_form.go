// Category CRUD — list + create/edit form.
//
// TUI counterpart to the web CategoryFormPage.tsx (+ the /inventory/categories
// list). ScanTTY already surfaced categories as a picker inside the item/asset
// forms; this adds the missing management surface so an operator can create,
// edit and delete categories without switching to the browser
// ([[ship-complete-features]]).
//
// The form mirrors the FULL writable field set of the web form + serializer:
// name, description, color (hex) and parent. slug is auto-generated + read-only
// server-side, so it is shown as a derived preview but never sent. The parent
// picker reuses the item form's searchable sub-phase idiom and excludes the
// category itself in edit mode (a category can't be its own parent).
//
// CategoryListScreen is reached with the global `G` hotkey (app.go). It is a
// plain (non-raw) screen so workspace switching keeps working; c/E/x/enter are
// not global hotkeys so they reach us via the root's fall-through. It flips to
// raw input only while the delete confirmation is up so y/n land here.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ---------------------------------------------------------------------------
// slugify — a small stand-in for Django's slugify, used only for the read-only
// slug preview in the form. The real slug is generated server-side on save.
// ---------------------------------------------------------------------------

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '-' || r == '_':
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ===========================================================================
// CategoryFormScreen
// ===========================================================================

const (
	cfName = iota
	cfDescription
	cfColor
	cfParent
	cfFieldMax
)

type categoryFormPhase int

const (
	categoryPhaseForm categoryFormPhase = iota
	categoryPhaseParentPick
)

var categoryFieldLabel = map[int]string{
	cfName:        "Name",
	cfDescription: "Description",
	cfColor:       "Color (hex)",
	cfParent:      "Parent category",
}

type CategoryFormScreen struct {
	deps  Deps
	edit  bool
	catID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	categories []omsapi.Category
	cat        *omsapi.Category
	refArrived bool
	catArrived bool

	terminalHeight int

	inputs   []textinput.Model
	parentID *int

	fields []int
	cursor int

	phase       categoryFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []itemPickOption
}

type categoryRefLoadedMsg struct {
	categories []omsapi.Category
	err        error
}

type categoryLoadedMsg struct {
	cat *omsapi.Category
	err error
}

type categorySavedMsg struct {
	cat *omsapi.Category
	err error
}

// NewCategoryFormScreen opens create mode when catID is empty, otherwise edit
// mode (hydrating from the fetched category).
func NewCategoryFormScreen(deps Deps, catID string) *CategoryFormScreen {
	edit := strings.TrimSpace(catID) != ""
	s := &CategoryFormScreen{
		deps:    deps,
		edit:    edit,
		catID:   strings.TrimSpace(catID),
		loading: true,
	}
	s.inputs = make([]textinput.Model, cfFieldMax)
	for id := 0; id < cfFieldMax; id++ {
		if id == cfParent {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = categoryCharLimit(id)
		ti.Placeholder = categoryPlaceholder(id)
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{cfName, cfDescription, cfColor, cfParent}
	s.syncFocus()
	return s
}

func categoryCharLimit(id int) int {
	switch id {
	case cfName:
		return 100
	case cfDescription:
		return 500
	case cfColor:
		return 7
	}
	return 100
}

func categoryPlaceholder(id int) string {
	switch id {
	case cfName:
		return "category name"
	case cfDescription:
		return "optional"
	case cfColor:
		return "#RRGGBB (optional)"
	}
	return ""
}

func (s *CategoryFormScreen) Title() string {
	if s.edit {
		if s.cat != nil && s.cat.Name != "" {
			return "Edit category: " + s.cat.Name
		}
		return "Edit category"
	}
	return "New category"
}

func (s *CategoryFormScreen) WantsRawInput() bool { return true }

func (s *CategoryFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadCategory())
	}
	return tea.Batch(cmds...)
}

func (s *CategoryFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *CategoryFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		cats, err := deps.OMS.ListCategories(ctx, nil)
		if err != nil {
			return categoryRefLoadedMsg{err: err}
		}
		return categoryRefLoadedMsg{categories: cats.Results}
	}
}

func (s *CategoryFormScreen) loadCategory() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.catID
	return func() tea.Msg {
		cat, err := deps.OMS.GetCategory(ctx, id)
		return categoryLoadedMsg{cat: cat, err: err}
	}
}

func (s *CategoryFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case categoryRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.categories = m.categories
		}
		return s, s.maybeFinalizeLoad()
	case categoryLoadedMsg:
		s.catArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.cat = m.cat
		}
		return s, s.maybeFinalizeLoad()
	case categorySavedMsg:
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
		if m.cat != nil {
			name = m.cat.Name
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("category %s: %s", verb, name), StatusOK),
			SwitchTo(WSInventory, NewCategoryListScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == categoryPhaseParentPick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == categoryPhaseParentPick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && id != cfParent {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *CategoryFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.catArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.cat != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *CategoryFormScreen) hydrate() {
	c := s.cat
	s.inputs[cfName].SetValue(c.Name)
	s.inputs[cfDescription].SetValue(c.Description)
	s.inputs[cfColor].SetValue(c.Color)
	s.parentID = c.Parent
}

func (s *CategoryFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *CategoryFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if id != cfParent {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && id != cfParent {
		s.inputs[id].Focus()
	}
}

func (s *CategoryFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	if id == cfParent {
		if m.String() == " " {
			s.openParentPicker()
			return s, textinput.Blink
		}
		return s, nil
	}
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

func (s *CategoryFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *CategoryFormScreen) openParentPicker() {
	s.phase = categoryPhaseParentPick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyParentFilter()
	s.pickCursor = 0
	if s.parentID != nil {
		for i, o := range s.pickOptions {
			if !o.clear && o.id == *s.parentID {
				s.pickCursor = i
				break
			}
		}
	}
}

// applyParentFilter builds the parent-picker rows, excluding this category in
// edit mode (a category can't be its own parent — mirrors the web excludeId).
func (s *CategoryFormScreen) applyParentFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	selfID := -1
	if s.edit {
		if v, err := strconv.Atoi(s.catID); err == nil {
			selfID = v
		}
	}
	opts := []itemPickOption{{clear: true, label: "(none — top level)"}}
	for _, c := range s.categories {
		if c.ID == selfID {
			continue
		}
		label := c.Name
		if c.ParentName != "" {
			label = fmt.Sprintf("%s (in %s)", c.Name, c.ParentName)
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemPickOption{id: c.ID, label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *CategoryFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyParentFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyParentFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = categoryPhaseForm
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
		s.commitParent()
	}
	return s, nil
}

func (s *CategoryFormScreen) commitParent() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		if opt.clear {
			s.parentID = nil
		} else {
			id := opt.id
			s.parentID = &id
		}
	}
	s.phase = categoryPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *CategoryFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.catID
	return s, func() tea.Msg {
		var cat *omsapi.Category
		var e error
		if edit {
			cat, e = deps.OMS.UpdateCategory(ctx, id, body)
		} else {
			cat, e = deps.OMS.CreateCategory(ctx, body)
		}
		return categorySavedMsg{cat: cat, err: e}
	}
}

func (s *CategoryFormScreen) buildPayload() (omsapi.CategoryWrite, error) {
	var w omsapi.CategoryWrite
	name := strings.TrimSpace(s.inputs[cfName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	color := strings.TrimSpace(s.inputs[cfColor].Value())
	if err := validateHexColor(color); err != nil {
		return w, err
	}
	w = omsapi.CategoryWrite{
		Name:        name,
		Description: strings.TrimSpace(s.inputs[cfDescription].Value()),
		Color:       color,
		Parent:      s.parentID,
	}
	return w, nil
}

// validateHexColor accepts an empty string (no color) or a #RGB / #RRGGBB hex
// code, matching the model's max_length=7 color column.
func validateHexColor(v string) error {
	if v == "" {
		return nil
	}
	if !strings.HasPrefix(v, "#") || (len(v) != 4 && len(v) != 7) {
		return errors.New("color must be a hex code like #FF5733")
	}
	for _, r := range v[1:] {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return errors.New("color must be a hex code like #FF5733")
		}
	}
	return nil
}

func (s *CategoryFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSInventory, NewCategoryListScreen(s.deps))
}

func (s *CategoryFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == categoryPhaseParentPick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *CategoryFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n")
	// Read-only slug preview (server auto-generates the real slug on save).
	slug := ""
	if s.edit && s.cat != nil && s.cat.Slug != "" && strings.TrimSpace(s.inputs[cfName].Value()) == s.cat.Name {
		slug = s.cat.Slug
	} else {
		slug = slugify(s.inputs[cfName].Value())
	}
	if slug == "" {
		slug = "(from name)"
	}
	b.WriteString(StyleMuted.Render("slug: "+slug) + "\n\n")

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

func (s *CategoryFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := categoryFieldLabel[id]
	var value string
	if id == cfParent {
		value = s.parentLabel()
	} else {
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *CategoryFormScreen) parentLabel() string {
	if s.parentID == nil {
		return StyleMuted.Render("(none — top level)")
	}
	for _, c := range s.categories {
		if c.ID == *s.parentID {
			return c.Name
		}
	}
	return fmt.Sprintf("#%d", *s.parentID)
}

func (s *CategoryFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok && id == cfParent {
		kindHelp = "space to pick parent"
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *CategoryFormScreen) viewPick() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Pick parent category — j/k move · / filter · enter select · esc back") + "\n\n")
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
		} else if opt.clear {
			b.WriteString(caret + StyleMuted.Render(opt.label) + "\n")
		} else {
			b.WriteString(caret + opt.label + "\n")
		}
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}

// ===========================================================================
// CategoryListScreen
// ===========================================================================

type CategoryListScreen struct {
	deps           Deps
	rows           []omsapi.Category
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type categoryListLoadedMsg struct {
	rows []omsapi.Category
	err  error
}

type categoryDeletedMsg struct {
	err error
}

func NewCategoryListScreen(deps Deps) *CategoryListScreen {
	return &CategoryListScreen{deps: deps, loading: true, windowSize: 20}
}

func (s *CategoryListScreen) Title() string { return "Categories" }

// WantsRawInput claims keys only while the delete confirmation is up, so y/n/esc
// land here instead of the root's global hotkeys.
func (s *CategoryListScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *CategoryListScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListCategories(ctx, nil)
		if err != nil {
			return categoryListLoadedMsg{err: err}
		}
		return categoryListLoadedMsg{rows: page.Results}
	}
}

func (s *CategoryListScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *CategoryListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case categoryListLoadedMsg:
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
	case categoryDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("category deleted", StatusOK), s.Init())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
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
			return s, s.Init()
		case "c":
			return s, SwitchTo(WSInventory, NewCategoryFormScreen(s.deps, ""))
		case "E", "enter":
			// Categories have no separate detail page (mirrors the web, which
			// links the list straight to the edit form), so both edit and open
			// go to the form.
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSInventory, NewCategoryFormScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *CategoryListScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := strconv.Itoa(row.ID)
		return s, func() tea.Msg {
			return categoryDeletedMsg{err: deps.OMS.DeleteCategory(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *CategoryListScreen) selected() (omsapi.Category, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.Category{}, false
	}
	return s.rows[s.cursor], true
}

func (s *CategoryListScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 20
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

func (s *CategoryListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading categories…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · c new · esc back")
	}
	if s.confirmingDelete {
		name := ""
		if row, ok := s.selected(); ok {
			name = row.Name
		}
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Deleting…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Delete category %q? This can't be undone.  y delete · n/esc cancel", name))
		}
		return prompt
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No categories.") + "\n\n" + StyleMuted.Render("c new category · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d categories", len(s.rows))) + "\n")
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
	b.WriteString(StyleMuted.Render("j/k move · c new · E/enter edit · x delete · r refresh · esc back"))
	return b.String()
}

func (s *CategoryListScreen) renderRow(i int) string {
	c := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	meta := []string{}
	if c.ParentName != "" {
		meta = append(meta, "in "+c.ParentName)
	}
	if c.ItemCount > 0 {
		meta = append(meta, fmt.Sprintf("%d items", c.ItemCount))
	}
	if c.Color != "" {
		meta = append(meta, c.Color)
	}
	line := marker + c.Name
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	return line
}
