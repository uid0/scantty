package tui

import (
	"fmt"
	"strings"
)

type Nav struct {
	items   []WorkspaceMeta
	active  Workspace
	isStaff bool
	width   int
}

func NewNav() Nav {
	return Nav{items: Workspaces(), active: WSScan}
}

func (n *Nav) SetWidth(w int)            { n.width = w }
func (n *Nav) SetActive(ws Workspace)    { n.active = ws }
func (n *Nav) SetStaff(staff bool)       { n.isStaff = staff }
func (n *Nav) Active() Workspace         { return n.active }

func (n Nav) ItemForHotkey(r rune) (WorkspaceMeta, bool) {
	for _, item := range n.items {
		if item.Hotkey == r {
			return item, true
		}
	}
	return WorkspaceMeta{}, false
}

func (n Nav) View(height int) string {
	var b strings.Builder
	header := StyleTitle.Render("scantty")
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("shop floor"))
	b.WriteString("\n\n")

	for _, item := range n.items {
		label := fmt.Sprintf("[%c] %s", item.Hotkey, item.Label)
		switch {
		case item.StaffOnly && !n.isStaff:
			b.WriteString(StyleSidebarItemDisabled.Render(label))
		case item.Key == n.active:
			b.WriteString(StyleSidebarItemActive.Render(label))
		default:
			b.WriteString(StyleSidebarItem.Render(label))
		}
		b.WriteString("\n")
	}

	body := b.String()
	if height <= 0 {
		return body
	}
	return StyleSidebar.Width(n.width).Height(height).Render(body)
}
