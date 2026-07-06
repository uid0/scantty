package tui

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

type listScreenSpec struct {
	kind   string
	loader func(ctx context.Context, deps Deps) ([]listRow, error)
	// searchLoader, when non-nil, gives the list a server-side search input
	// (opened with '/'). The typed query is forwarded to the backend loader —
	// e.g. as ?search= on the OMS list endpoint — and the returned rows replace
	// the list. When nil the list is load-once with local sort only, and '/'
	// falls through to the global search palette.
	searchLoader func(ctx context.Context, deps Deps, query string) ([]listRow, error)
	detail       func(id string, deps Deps) Screen
	// newScreen, when non-nil, gives the list an `n` (new) action that opens a
	// create form for this resource. The list claims 'n' via HandlesKey (it
	// collides with the global notifications hotkey) and advertises it in the
	// footer. Editing/deleting an existing row still lives on the detail screen
	// (or, for resources with no edit endpoint, nowhere) — this hook is create
	// only, so a list without a create form simply leaves it nil.
	newScreen func(deps Deps) Screen
}

type listRow struct {
	ID        string
	Title     string
	Subtitle  string
	Tag       string
	CreatedAt time.Time
	// Optional secondary timestamp shown in the date column when CreatedAt is
	// zero (e.g. for resources that only expose updated_at or last_seen).
	FallbackDate time.Time
}

func (r listRow) sortDate() time.Time {
	if !r.CreatedAt.IsZero() {
		return r.CreatedAt
	}
	return r.FallbackDate
}

type listSortMode int

const (
	sortDefault listSortMode = iota
	sortDateDesc
	sortDateAsc
	sortTitleAsc
)

func (m listSortMode) label() string {
	switch m {
	case sortDateDesc:
		return "newest"
	case sortDateAsc:
		return "oldest"
	case sortTitleAsc:
		return "title"
	}
	return "default"
}

type listLoadedMsg struct {
	rows []listRow
	err  error
}

// listSearchedMsg carries the result of a server-side search. seq lets the
// screen drop a stale response whose query the operator has already typed past.
type listSearchedMsg struct {
	seq  int
	rows []listRow
	err  error
}

const listWindowSize = 20

type ListScreen struct {
	deps           Deps
	title          string
	spec           listScreenSpec
	rawRows        []listRow
	rows           []listRow
	cursor         int
	windowStart    int
	windowSize     int
	sort           listSortMode
	loading        bool
	loadErr        string
	terminalHeight int

	// Server-side search (only when spec.searchLoader != nil). searching flips
	// the screen into raw-input mode so the query textinput gets every key;
	// searchQuery is the query currently reflected in rawRows; searchSeq guards
	// against stale async responses; searchPending shows a subtle indicator
	// while a reload is in flight (the prior rows stay visible meanwhile).
	searching     bool
	searchInput   textinput.Model
	searchQuery   string
	searchSeq     int
	searchPending bool
}

// computeWindowSize returns how many list ROWS the current terminal can
// show. The list view renders one row of content per visible item plus a
// "    subtitle" line under any row that has a subtitle. We size against
// the real subtitle population so a list of plain-title rows fills the
// pane instead of getting halved by an old worst-case heuristic.
//
// Chrome accounted for here, in addition to the global screen body math
// in layout.go:
//
//	1 row for the "Sort: … · N rows" header
//	1 row each for ↑/↓ indicators when the list overflows the window
//	1 blank separator above the hint
//	1 row for the hint line itself
func (s *ListScreen) computeWindowSize() int {
	const listHeaderRows = 1
	const listFooterRows = 2 // blank + hint
	// Reserve both indicator slots up front; we'd rather waste one row
	// when only one indicator shows than clip a row when the list
	// overflows.
	const listIndicatorRows = 2

	avail := screenBodyHeight(s.terminalHeight) - listHeaderRows - listFooterRows - listIndicatorRows
	// The search overlay adds an input line + its hint + a blank separator
	// above the list body; reserve those rows so results don't overflow.
	if s.searching {
		avail -= 3
	}
	if avail < 2 {
		avail = 2
	}
	// Each visible row takes 1 line of title, plus 1 more if it carries
	// a subtitle. Walk the actual rows from the current windowStart
	// forward, packing as many as fit. Falls back to a per-row estimate
	// when rows haven't loaded yet.
	if len(s.rows) == 0 {
		return avail
	}
	used, count, start := 0, 0, s.windowStart
	if start < 0 {
		start = 0
	}
	for i := start; i < len(s.rows); i++ {
		cost := 1
		if s.rows[i].Subtitle != "" {
			cost = 2
		}
		if used+cost > avail {
			break
		}
		used += cost
		count++
	}
	if count < 2 {
		count = 2
	}
	return count
}

func NewListScreen(deps Deps, title string, spec listScreenSpec) *ListScreen {
	return &ListScreen{
		deps:       deps,
		title:      title,
		spec:       spec,
		loading:    true,
		windowSize: listWindowSize,
		sort:       sortDateDesc,
	}
}

func (s *ListScreen) Title() string { return s.title }

// HandlesKey claims lowercase 's' (cycle sort) so it beats the global
// s=settings nav; settings stays reachable from every screen that doesn't
// own a local 's'. Other list keys don't collide with globals, so they reach
// this screen through the normal fallthrough.
//
// When the list supports server-side search it also claims '/', overriding the
// global search-palette hotkey so '/' filters THIS list against the backend;
// ctrl+k still opens the universal palette from here.
func (s *ListScreen) HandlesKey(key string) bool {
	if key == "s" {
		return true
	}
	if key == "n" && s.spec.newScreen != nil {
		return true
	}
	return key == "/" && s.spec.searchLoader != nil
}

// WantsRawInput routes every keypress to the screen while the search input is
// open, so the global hotkey layer stops eating letters the operator is typing.
func (s *ListScreen) WantsRawInput() bool { return s.searching }

func (s *ListScreen) Init() tea.Cmd {
	if s.spec.loader == nil {
		s.loading = false
		return nil
	}
	loader := s.spec.loader
	ctx := s.deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	deps := s.deps
	return func() tea.Msg {
		rows, err := loader(ctx, deps)
		return listLoadedMsg{rows: rows, err: err}
	}
}

func (s *ListScreen) applySort() {
	rows := make([]listRow, len(s.rawRows))
	copy(rows, s.rawRows)
	switch s.sort {
	case sortDateDesc:
		sort.SliceStable(rows, func(i, j int) bool {
			return rows[i].sortDate().After(rows[j].sortDate())
		})
	case sortDateAsc:
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i].sortDate(), rows[j].sortDate()
			if a.IsZero() {
				return false
			}
			if b.IsZero() {
				return true
			}
			return a.Before(b)
		})
	case sortTitleAsc:
		sort.SliceStable(rows, func(i, j int) bool {
			return strings.ToLower(rows[i].Title) < strings.ToLower(rows[j].Title)
		})
	}
	s.rows = rows
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	s.scrollIntoView()
}

func (s *ListScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = listWindowSize
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
	if maxStart := len(s.rows) - s.windowSize; maxStart > 0 && s.windowStart > maxStart {
		s.windowStart = maxStart
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *ListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case listLoadedMsg:
		s.loading = false
		s.rawRows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		s.applySort()
		// Re-compute now that rows are known: computeWindowSize walks
		// the actual subtitle population, so the first sizing pass
		// from WindowSizeMsg used a 0-row fallback.
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case listSearchedMsg:
		if m.seq != s.searchSeq {
			return s, nil // a fresher query has been typed; drop this response
		}
		s.searchPending = false
		s.rawRows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		s.cursor = 0
		s.windowStart = 0
		s.applySort()
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case tea.KeyMsg:
		if s.searching {
			return s.updateSearch(m)
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
		case "s":
			s.sort = (s.sort + 1) % 4
			s.applySort()
		case "r":
			s.loading = true
			return s, s.Init()
		case "n":
			// Open the create form for this resource, when it has one. `n`
			// reaches us only because HandlesKey claims it (it's a global
			// hotkey otherwise); a list with no create form leaves newScreen
			// nil and never claims the key.
			if s.spec.newScreen == nil {
				return s, nil
			}
			return s, SwitchTo(workspaceForKind(s.spec.kind), s.spec.newScreen(s.deps))
		case "/":
			if s.spec.searchLoader == nil {
				return s, nil
			}
			return s.enterSearch()
		case "enter":
			return s.openSelected()
		}
	}
	return s, nil
}

// enterSearch opens the server-side search input over the current list. The
// already-loaded rows stay visible until the operator types a query.
func (s *ListScreen) enterSearch() (Screen, tea.Cmd) {
	s.searching = true
	in := textinput.New()
	in.Prompt = "search ▸ "
	in.Placeholder = "name / tag / serial…"
	in.CharLimit = 120
	in.SetValue(s.searchQuery)
	in.CursorEnd()
	in.Focus()
	s.searchInput = in
	s.windowSize = s.computeWindowSize()
	s.scrollIntoView()
	return s, textinput.Blink
}

// updateSearch owns key handling while the search input is open. Arrow keys
// move the result cursor and enter opens the highlighted row (mirroring the
// search palette); esc closes search and restores the unfiltered list; every
// other key edits the query and, on change, fires a fresh backend search.
func (s *ListScreen) updateSearch(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.searching = false
		s.searchInput.Blur()
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		if s.searchQuery != "" {
			// Restore the full list the plain loader produces.
			s.searchQuery = ""
			s.searchPending = false
			s.loading = true
			return s, s.Init()
		}
		return s, nil
	case tea.KeyUp, tea.KeyCtrlP:
		if s.cursor > 0 {
			s.cursor--
			s.scrollIntoView()
		}
		return s, nil
	case tea.KeyDown, tea.KeyCtrlN:
		if s.cursor < len(s.rows)-1 {
			s.cursor++
			s.scrollIntoView()
		}
		return s, nil
	case tea.KeyEnter:
		return s.openSelected()
	}
	prev := s.searchInput.Value()
	var cmd tea.Cmd
	s.searchInput, cmd = s.searchInput.Update(m)
	if s.searchInput.Value() != prev {
		s.searchQuery = strings.TrimSpace(s.searchInput.Value())
		s.searchPending = true
		return s, tea.Batch(cmd, s.runSearch())
	}
	return s, cmd
}

// runSearch forwards the current query to the backend via spec.searchLoader.
// The bumped seq is stamped on the response so a slow reply for a query the
// operator has already typed past is dropped on arrival.
func (s *ListScreen) runSearch() tea.Cmd {
	if s.spec.searchLoader == nil {
		return nil
	}
	s.searchSeq++
	seq := s.searchSeq
	query := s.searchQuery
	loader := s.spec.searchLoader
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := loader(ctx, deps, query)
		return listSearchedMsg{seq: seq, rows: rows, err: err}
	}
}

// openSelected opens the detail screen for the row under the cursor, if the
// list has a detail builder. Shared by the plain list and the search overlay.
func (s *ListScreen) openSelected() (Screen, tea.Cmd) {
	if s.spec.detail == nil || s.cursor < 0 || s.cursor >= len(s.rows) {
		return s, nil
	}
	next := s.spec.detail(s.rows[s.cursor].ID, s.deps)
	if next == nil {
		return s, nil
	}
	return s, SwitchTo(workspaceForKind(s.spec.kind), next)
}

func workspaceForKind(kind string) Workspace {
	switch kind {
	case "inventory_items":
		return WSInventory
	case "assets":
		return WSAssets
	case "purchase_orders":
		return WSPurchasing
	case "work_orders":
		return WSMaintenance
	case "sigs":
		return WSSIGs
	case "fk_devices":
		return WSForgeKey
	case "project_storage":
		// Opened from the Facilities menu; keep the nav highlight there
		// when drilling into a stint detail.
		return WSFacilities
	}
	return WSDashboard
}

func (s *ListScreen) View() string {
	// The search overlay renders above whatever body state follows, so the
	// operator can keep editing the query even when a search returns nothing.
	if s.searching {
		var head strings.Builder
		head.WriteString(s.searchInput.View())
		switch {
		case s.searchPending:
			head.WriteString("  " + StyleMuted.Render("searching…"))
		case s.searchQuery != "" && s.loadErr == "":
			head.WriteString("  " + StyleMuted.Render(fmt.Sprintf("%d match(es)", len(s.rows))))
		}
		head.WriteString("\n")
		head.WriteString(StyleMuted.Render("↑/↓ move · enter open · esc cancel") + "\n\n")
		return head.String() + s.bodyView()
	}
	return s.bodyView()
}

func (s *ListScreen) bodyView() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry")
	}
	if len(s.rows) == 0 {
		if s.searching {
			return StyleMuted.Render("No matches.")
		}
		return StyleMuted.Render("No rows.")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("Sort: %s · %d rows", s.sort.label(), len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}

	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		row := s.rows[i]
		marker := "  "
		if i == s.cursor {
			marker = "▸ "
		}
		title := row.Title
		if row.Tag != "" {
			title = title + " " + StyleMuted.Render("("+row.Tag+")")
		}
		date := ""
		if d := row.sortDate(); !d.IsZero() {
			date = StyleMuted.Render(d.Format("2006-01-02") + "  ")
		}
		line := marker + date + title
		if i == s.cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
		if row.Subtitle != "" {
			b.WriteString("    ")
			b.WriteString(StyleMuted.Render(row.Subtitle))
			b.WriteString("\n")
		}
	}

	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}

	b.WriteString("\n")
	hint := "j/k move · pgup/pgdn page · g/G top/bottom · s sort · r refresh"
	if s.spec.detail != nil {
		hint += " · enter open"
	}
	if s.spec.searchLoader != nil {
		hint += " · / search"
	}
	if s.spec.newScreen != nil {
		hint += " · n new"
	}
	// Surface the per-workspace create shortcuts so an operator doesn't
	// have to memorize them. `N` is the global hotkey for the
	// PurchaseOrderCreateScreen (app.go:194) but the prompt was never
	// rendered, so the create form was effectively invisible. Same
	// thing for `Q` (the existing pending-reorders queue), advertised
	// here from the Purchasing list so an operator looking for
	// in-flight reorders can find them.
	switch s.spec.kind {
	case "purchase_orders":
		hint += " · N new PO · Q pending reorders"
	case "inventory_items":
		// `I` is a global hotkey (app.go) that opens the create-item form
		// from anywhere; advertise it here where an operator looks for it.
		// Editing/deleting an item lives on its detail screen (E / x).
		hint += " · I new item · G categories · L locations · U suppliers"
	case "work_orders":
		// The Maintenance landing lists work orders; `M` (global) opens the
		// PM-item list, where PM items are created/edited and their actions
		// (complete / clone / generate-WO) live.
		hint += " · M PM items"
	case "assets":
		// `A` is the global new-asset hotkey (app.go). Edit/delete of an
		// existing asset live on its detail screen (E / x).
		hint += " · A new asset"
	}
	b.WriteString(StyleMuted.Render(hint))
	return b.String()
}

func loadInventoryItems(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListItems(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, it := range page.Results {
		subtitle := fmt.Sprintf("SKU %s · stock %d", it.SKU, it.Stock)
		tag := ""
		if it.NeedsReorder {
			tag = "needs-reorder"
		}
		rows = append(rows, listRow{
			ID:           it.ID,
			Title:        it.Name,
			Subtitle:     subtitle,
			Tag:          tag,
			CreatedAt:    it.CreatedAt,
			FallbackDate: it.UpdatedAt,
		})
	}
	return rows, nil
}

func loadAssets(ctx context.Context, deps Deps) ([]listRow, error) {
	return assetRows(ctx, deps, nil)
}

// searchAssets forwards the operator's query to the OMS asset endpoint as
// ?search=, which AssetViewSet.get_queryset matches (icontains) against name /
// description / serial_number / asset_tag / manufacturer_name. This is what
// makes an asset findable from the list by its DMS-YYANNNSS asset_tag — the
// plain page-1 loader (loadAssets) can't surface a tag past the first page.
func searchAssets(ctx context.Context, deps Deps, query string) ([]listRow, error) {
	var q url.Values
	if term := strings.TrimSpace(query); term != "" {
		q = url.Values{"search": []string{term}}
	}
	return assetRows(ctx, deps, q)
}

func assetRows(ctx context.Context, deps Deps, q url.Values) ([]listRow, error) {
	page, err := deps.OMS.ListAssets(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, a := range page.Results {
		subtitle := a.Description
		if a.AssetTag != "" {
			if subtitle != "" {
				subtitle = a.AssetTag + " · " + subtitle
			} else {
				subtitle = a.AssetTag
			}
		}
		fallback := a.UpdatedAt
		if a.LastScannedAt != nil && !a.LastScannedAt.IsZero() {
			fallback = *a.LastScannedAt
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(a.ID),
			Title:        a.Name,
			Subtitle:     subtitle,
			Tag:          a.Status,
			CreatedAt:    a.CreatedAt,
			FallbackDate: fallback,
		})
	}
	return rows, nil
}

func loadPurchaseOrders(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListPurchaseOrders(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, po := range page.Results {
		title := po.Number
		if title == "" {
			title = fmt.Sprintf("PO #%v", po.ID)
		}
		subtitle := poListSubtitle(po)
		created := po.CreatedAt
		if created.IsZero() {
			created = po.OrderDate
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(po.ID),
			Title:        title,
			Subtitle:     subtitle,
			Tag:          po.Status,
			CreatedAt:    created,
			FallbackDate: po.UpdatedAt,
		})
	}
	return rows, nil
}

func loadWorkOrders(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListWorkOrders(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, wo := range page.Results {
		rows = append(rows, listRow{
			ID:           fmt.Sprint(wo.ID),
			Title:        wo.Title,
			Subtitle:     wo.AssetName,
			Tag:          wo.Status,
			CreatedAt:    wo.CreatedAt,
			FallbackDate: wo.UpdatedAt,
		})
	}
	return rows, nil
}

func loadSIGs(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListSIGs(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, sig := range page.Results {
		subtitleParts := []string{}
		if sig.MemberCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d members", sig.MemberCount))
		}
		if sig.AssetCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d assets", sig.AssetCount))
		}
		if sig.InventoryCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d items", sig.InventoryCount))
		}
		tag := ""
		if sig.IsUserAdmin {
			tag = "admin"
		}
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", sig.ID),
			Title:    sig.Name,
			Subtitle: strings.Join(subtitleParts, " · "),
			Tag:      tag,
		})
	}
	return rows, nil
}

func loadForgeKeyDevices(ctx context.Context, deps Deps) ([]listRow, error) {
	devices, err := deps.ForgeKey.ListDevices(ctx, nil)
	if err != nil {
		return nil, err
	}
	return forgekeyDeviceRows(devices), nil
}

func forgekeyDeviceRows(devices []forgekeyapi.Device) []listRow {
	rows := make([]listRow, 0, len(devices))
	for _, d := range devices {
		tag := "offline"
		if d.IsOnline {
			tag = "online"
		}
		typeLabel := d.DeviceTypeName
		if typeLabel == "" {
			typeLabel = fmt.Sprintf("%v", d.DeviceType)
		}
		subtitle := fmt.Sprintf("%s · %s", typeLabel, d.MACAddress)
		if d.Location != nil {
			subtitle += fmt.Sprintf(" · loc #%d", *d.Location)
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(d.ID),
			Title:        d.Name,
			Subtitle:     subtitle,
			Tag:          tag,
			FallbackDate: d.LastSeen,
		})
	}
	return rows
}

func poListSubtitle(po omsapi.PurchaseOrder) string {
	parts := []string{}
	supplier := po.SupplierName
	if supplier == "" {
		supplier = po.SupplierDetails
	}
	if supplier != "" {
		parts = append(parts, supplier)
	}
	if !po.EstimatedTotal.Empty() {
		curr := po.Currency
		if curr == "" {
			curr = "USD"
		}
		parts = append(parts, "$"+string(po.EstimatedTotal)+" "+curr)
	} else if po.Total > 0 {
		curr := po.Currency
		if curr == "" {
			curr = "USD"
		}
		parts = append(parts, fmt.Sprintf("%.2f %s", po.Total, curr))
	}
	if po.TotalItems > 0 {
		parts = append(parts, fmt.Sprintf("%d items", po.TotalItems))
	}
	return strings.Join(parts, " · ")
}
