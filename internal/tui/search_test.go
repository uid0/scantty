package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// routeSearchResult drives openSelected for a single result and returns the
// message the emitted command produces (nil if there was no command).
func routeSearchResult(t *testing.T, r omsapi.SearchResult) tea.Msg {
	t.Helper()
	sp := NewSearchPalette(Deps{})
	sp.results = []omsapi.SearchResult{r}
	sp.cursor = 0
	_, cmd := sp.openSelected()
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestSearchPalette_InventoryResultRoutesToDetail is the guard for issue-B B1:
// the OMS backend tags InventoryItems as type "inventory" (search/views.py), so
// selecting one must open the inventory item detail — not fall through to the
// "no detail screen yet" warning the old switch produced.
func TestSearchPalette_InventoryResultRoutesToDetail(t *testing.T) {
	msg := routeSearchResult(t, omsapi.SearchResult{Type: "inventory", ID: "42", Title: "Widget"})
	sw, ok := msg.(SwitchScreenMsg)
	if !ok {
		t.Fatalf("inventory result fell through to the fallback; got %T (%v)", msg, msg)
	}
	if sw.Workspace != WSInventory {
		t.Fatalf("workspace = %v, want WSInventory", sw.Workspace)
	}
	if _, ok := sw.Screen.(*InventoryDetailScreen); !ok {
		t.Fatalf("screen = %T, want *InventoryDetailScreen", sw.Screen)
	}
}

// TestSearchPalette_ItemAliasRoutesToDetail: the defensive "item" alias still
// opens the inventory detail, so nothing regresses if the backend ever emits it.
func TestSearchPalette_ItemAliasRoutesToDetail(t *testing.T) {
	msg := routeSearchResult(t, omsapi.SearchResult{Type: "item", ID: "7", Title: "Bolt"})
	sw, ok := msg.(SwitchScreenMsg)
	if !ok {
		t.Fatalf(`"item" alias no longer routes to a detail; got %T (%v)`, msg, msg)
	}
	if _, ok := sw.Screen.(*InventoryDetailScreen); !ok {
		t.Fatalf("screen = %T, want *InventoryDetailScreen", sw.Screen)
	}
}

// TestSearchPalette_UnknownTypeStillFallsThrough: an unhandled result type must
// still surface the warning (not switch screens) — the fallback is preserved.
func TestSearchPalette_UnknownTypeStillFallsThrough(t *testing.T) {
	msg := routeSearchResult(t, omsapi.SearchResult{Type: "mystery", ID: "1", Title: "?"})
	if _, ok := msg.(SwitchScreenMsg); ok {
		t.Fatalf("unknown type should not switch screens")
	}
	if _, ok := msg.(StatusMsg); !ok {
		t.Fatalf("unknown type should surface a StatusMsg warning; got %T (%v)", msg, msg)
	}
}
