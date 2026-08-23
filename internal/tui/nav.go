package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// navHeaderRows is what Nav.View draws above the menu itself: the "scantty"
// title, the "shop floor" subtitle and a blank separator. Subtracted from the
// pane height to size the window the rows scroll inside.
const navHeaderRows = 3

// navChildIndent prefixes a surface row so the tree reads as a tree. Two
// columns, so it costs the same as the "▸ " a cursor marker would and the
// labels below still fit the 24-column pane.
const navChildIndent = "· "

// navFocusHint is what the status bar flashes the moment `tab` moves the
// keyboard into the sidebar. The sidebar has no room for a help line of its
// own and the JD Edwards rule is that a key you can press is a key something
// on screen names — so the hint arrives with the focus.
const navFocusHint = "menu — ↑↓ move · ←→ workspace · enter open · tab/esc back to the screen"

// Nav is the sidebar: the workspace list plus, under whichever workspace the
// cursor is currently inside, that workspace's own surfaces. It is the app's
// menu — phase 3 of the JD Edwards redesign retired every global letter and
// digit accelerator, so this tree and the on-screen cursor menus are the whole
// of navigation.
//
// The cursor is stored as an IDENTITY (a workspace plus a surface index, -1 for
// the workspace's own row) rather than as a flat index, because the row list
// changes shape as the cursor moves between workspaces: only the cursor's
// workspace shows its children. Storing an index would mean re-deriving it
// against a row list that has already moved underneath it.
type Nav struct {
	items   []WorkspaceMeta
	active  Workspace
	isStaff bool
	width   int

	// focused is whether the sidebar holds the keyboard. It is also the
	// difference between the two visual states: focused, the cursor row is
	// reverse-video and its workspace is the one expanded; blurred, nothing is
	// reverse-video and the ACTIVE workspace is the one expanded, so the tree
	// always shows where you are.
	focused       bool
	cursorWS      Workspace
	cursorSurface int
}

// navRow is one rendered line of the tree — either a workspace's own row
// (surface < 0) or one of its child surfaces.
type navRow struct {
	ws       Workspace
	surface  int
	label    string
	child    bool
	disabled bool
}

// navRowKind is which of the four readings a row gets. It exists as an enum
// rather than as a style picked inline in View so that the focus treatment can
// be asserted as a decision.
//
// The reason it HAD to be was that lipgloss renders PLAIN in a test binary (no
// TTY, ASCII profile), so the cursor row and an ordinary row produce
// byte-identical strings. That is no longer a dead end: withColorProfile forces
// the profile and jdeCells (jde_cells_test.go) decodes the frame into attributed
// cells, so the OUTPUT is assertable too when what is being pinned is what the
// operator sees.
type navRowKind int

const (
	navRowPlain navRowKind = iota
	navRowCursor
	navRowActive
	navRowDisabled
)

// rowKind ranks the four readings. The cursor wins over everything, because
// while the sidebar holds the keyboard "where the next key goes" is the one
// thing the pane has to answer.
func rowKind(row navRow, isCursor, focused bool, active Workspace) navRowKind {
	switch {
	case focused && isCursor:
		return navRowCursor
	case row.disabled:
		return navRowDisabled
	case row.surface < 0 && row.ws == active:
		return navRowActive
	default:
		return navRowPlain
	}
}

func navRowStyle(kind navRowKind) lipgloss.Style {
	switch kind {
	case navRowCursor:
		return StyleSidebarCursor
	case navRowDisabled:
		return StyleSidebarItemDisabled
	case navRowActive:
		return StyleSidebarItemActive
	default:
		return StyleSidebarItem
	}
}

func NewNav() Nav {
	return Nav{items: Workspaces(), active: WSScan, cursorWS: WSScan, cursorSurface: -1}
}

func (n *Nav) SetWidth(w int)      { n.width = w }
func (n *Nav) SetStaff(staff bool) { n.isStaff = staff }
func (n Nav) Active() Workspace    { return n.active }
func (n Nav) Focused() bool        { return n.focused }

// SetActive marks the workspace the operator is in. It also parks the cursor
// there, so the next `tab` into the sidebar starts from where the screen is
// rather than from wherever the cursor was last left.
func (n *Nav) SetActive(ws Workspace) {
	n.active = ws
	n.cursorWS = ws
	n.cursorSurface = -1
}

// Focus moves the keyboard into the sidebar, starting on the active workspace.
func (n *Nav) Focus() {
	n.focused = true
	n.cursorWS = n.active
	n.cursorSurface = -1
}

// Blur hands the keyboard back to the screen.
func (n *Nav) Blur() { n.focused = false }

// expanded is the one workspace showing its surfaces: the cursor's while the
// sidebar has focus, otherwise the active one.
func (n Nav) expanded() Workspace {
	if n.focused {
		return n.cursorWS
	}
	return n.active
}

// rows is the visible tree, top to bottom.
func (n Nav) rows() []navRow {
	expanded := n.expanded()
	out := make([]navRow, 0, len(n.items)+len(workspaceSurfaces(expanded)))
	for _, item := range n.items {
		// A staff-only workspace has always rendered muted for a non-staff
		// session while staying openable (the backend is the real gate). That
		// is unchanged here: dimming a row the operator can still use is a
		// heads-up, and locking it out would be a new denial this bead has no
		// business inventing.
		dim := item.StaffOnly && !n.isStaff
		out = append(out, navRow{ws: item.Key, surface: -1, label: item.Label, disabled: dim})
		if item.Key != expanded {
			continue
		}
		for i, sf := range workspaceSurfaces(item.Key) {
			out = append(out, navRow{ws: item.Key, surface: i, label: sf.label, child: true, disabled: dim})
		}
	}
	return out
}

// cursorIndex locates the cursor in a row list. A cursor whose row has gone
// (a workspace that is no longer listed) falls back to the top rather than off
// the end.
func (n Nav) cursorIndex(rows []navRow) int {
	for i, row := range rows {
		if row.ws == n.cursorWS && row.surface == n.cursorSurface {
			return i
		}
	}
	return 0
}

func (n *Nav) setCursorTo(row navRow) {
	n.cursorWS = row.ws
	n.cursorSurface = row.surface
}

// Move steps the cursor `delta` rows through the flat tree, clamping at both
// ends rather than wrapping — a menu that wraps makes `end` and "one past the
// last thing" look identical.
func (n *Nav) Move(delta int) {
	rows := n.rows()
	if len(rows) == 0 {
		return
	}
	i := n.cursorIndex(rows) + delta
	if i < 0 {
		i = 0
	}
	if i >= len(rows) {
		i = len(rows) - 1
	}
	n.setCursorTo(rows[i])
}

// MoveWorkspace jumps to the previous/next WORKSPACE row, skipping surfaces.
// Without it, crossing ForgeKey's seven children to reach Settings costs eight
// presses; with it the coarse axis is one press per workspace and up/down stays
// the fine one.
func (n *Nav) MoveWorkspace(delta int) {
	idx := -1
	for i, item := range n.items {
		if item.Key == n.cursorWS {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	// From inside a workspace's surfaces, "previous" means that workspace's own
	// row — the operator is already past it.
	if delta < 0 && n.cursorSurface >= 0 {
		n.cursorSurface = -1
		return
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(n.items) {
		idx = len(n.items) - 1
	}
	n.cursorWS = n.items[idx].Key
	n.cursorSurface = -1
}

// MoveToEdge parks the cursor on the first (-1) or last (+1) row of the tree.
//
// It settles rather than jumping once: the row list depends on where the cursor
// IS, so landing on the last workspace's own row makes that workspace's
// surfaces appear BELOW it, and the bottom of the tree has moved. Two passes is
// enough (expansion only ever adds rows under the row just landed on), but the
// loop is written as a fixpoint so a deeper tree later can't quietly stop short.
func (n *Nav) MoveToEdge(dir int) {
	for i := 0; i < 4; i++ {
		rows := n.rows()
		if len(rows) == 0 {
			return
		}
		target := rows[0]
		if dir >= 0 {
			target = rows[len(rows)-1]
		}
		if target.ws == n.cursorWS && target.surface == n.cursorSurface {
			return
		}
		n.setCursorTo(target)
	}
}

// Selected is what `enter` would open: the workspace to activate and a builder
// for its screen. A nil builder means "this workspace's default screen", which
// only Root can resolve (newScreenFor lives beside the screen constructors).
func (n Nav) Selected() (Workspace, func(Deps) Screen) {
	rows := n.rows()
	if len(rows) == 0 {
		return n.active, nil
	}
	row := rows[n.cursorIndex(rows)]
	if row.surface < 0 {
		return row.ws, nil
	}
	surfaces := workspaceSurfaces(row.ws)
	if row.surface >= len(surfaces) {
		return row.ws, nil
	}
	return row.ws, surfaces[row.surface].build
}

// navMarkerMinRows is the shortest pane that can afford the "⋯" markers. Below
// it every row is worth more than the news that there are more of them, so the
// window drops the markers rather than spending two thirds of a three-row pane
// saying "there is more".
const navMarkerMinRows = 3

// navWindowRange picks the slice of rows to draw so the cursor is always
// visible. Returns the half-open range plus whether the caller should draw a
// "⋯" marker at each CLIPPED end (one before the slice when start > 0, one
// after when end < total). Total lines drawn — rows plus markers — never exceed
// avail, which is what keeps the sidebar from growing the frame and scrolling
// the whole layout off the top of the terminal.
func navWindowRange(total, cursor, avail int) (int, int, bool) {
	if avail <= 0 || total <= avail {
		return 0, total, false
	}
	markers := avail >= navMarkerMinRows
	body := avail
	if markers {
		body = avail - 2
	}
	start := cursor - body/2
	if start+body > total {
		start = total - body
	}
	if start < 0 {
		start = 0
	}
	end := start + body
	if end > total {
		end = total
	}
	// An end that turned out NOT to be clipped hands its reserved marker row
	// back to the content.
	if markers {
		switch {
		case start == 0 && end < total:
			end++
		case end == total && start > 0:
			start--
		}
	}
	return start, end, markers
}

func (n Nav) View(height int) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("scantty"))
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("shop floor"))
	b.WriteString("\n\n")

	rows := n.rows()
	cursor := n.cursorIndex(rows)
	avail := len(rows)
	if height > 0 {
		avail = height - navHeaderRows
		if avail < 1 {
			avail = 1
		}
	}
	start, end, markers := navWindowRange(len(rows), cursor, avail)

	more := StyleMuted.Render("⋯")
	if markers && start > 0 {
		b.WriteString(more + "\n")
	}
	for i := start; i < end; i++ {
		row := rows[i]
		label := row.label
		if row.child {
			label = navChildIndent + label
		}
		// The cursor row is the one loud thing on the pane, and only while the
		// sidebar holds the keyboard — the same reverse-video treatment a
		// focused field gets on a columnar sheet, so "this is where the keys
		// go" reads the same everywhere.
		b.WriteString(navRowStyle(rowKind(row, i == cursor, n.focused, n.active)).Render(label))
		b.WriteString("\n")
	}
	if markers && end < len(rows) {
		b.WriteString(more + "\n")
	}

	// Drop the trailing newline the last row wrote. Height() PADS a short block
	// but never truncates a tall one, so leaving it would make the pane one row
	// taller than we were given — which grows the frame past the terminal and
	// scrolls the whole layout off the top.
	body := strings.TrimRight(b.String(), "\n")
	if height <= 0 {
		return body
	}
	return StyleSidebar.Width(n.width).Height(height).Render(body)
}
