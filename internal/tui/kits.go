// Browsing kits (op-8n0, web /inventory/kits).
//
// WHY A LIST OF ITS OWN, when every other catalogue row lives on the inventory
// list: `/api/inventory/items/` filters kits OUT in get_queryset, deliberately
// and permanently — a kit is a purchasing construct that decomposes on receipt,
// and showing one beside the parts it decomposes into would double-count the
// shelf. The consequence is that nothing which walks the item catalogue can
// ever show a kit, so before this screen the only terminal routes to one were
// naming it exactly in the global search palette (search/views.py queries
// InventoryItem unfiltered, so a kit comes back there), scanning its QR label
// or a supplier UPC (the scanner's resolvers filter no more than the search
// does), or already holding an id. None of those answers "which kits do we
// have?", which is the question a browse list exists for.
//
// It is a plain *ListScreen rather than a columnar sheet because it is a BROWSE
// surface, exactly like the item, asset and purchase-order lists it sits beside
// — which also means it is swept by list_bar_honesty_test.go's derivations for
// free: the nav tree reaches it (route.go's Inventory surfaces), so
// listBarSurfaces finds it and its footer has to name exactly the keys that
// work.
//
// WHAT ENTER OPENS is the ordinary item detail, not a kit-shaped one. A kit IS
// an InventoryItem, InventoryDetailScreen already fetches /kits/ to draw the
// bill of materials (inventory_detail_kit.go), and `E` there opens the item
// form whose kit band edits it (inventory_item_form_kit.go). Routing enter
// anywhere else would be a second detail screen for a record that already has
// one.
package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// kitListKind is the listScreenSpec kind for the kit browse list. It is a
// constant because listShortcuts switches on it and a literal in two places is
// how a kind quietly stops matching.
const kitListKind = "kits"

// NewKitListScreen builds the kit browse list.
//
// searchLoader is non-nil, which gives the list `/` and forwards the query to
// the server rather than filtering the page in hand. That is a correctness
// choice and not a convenience: the endpoint pages at 50, so a client-side
// filter would answer "no kits match" about page one while the kit sat on page
// two — and it is what makes the list SCANNER-USABLE, since a scanner is a
// keyboard burst plus Enter and KitViewSet matches ?search= against the
// supplier's own part number for the kit as well as its name, SKU and
// description.
func NewKitListScreen(deps Deps) *ListScreen {
	return NewListScreen(deps, "Kits", listScreenSpec{
		kind:         kitListKind,
		loader:       loadKits,
		searchLoader: searchKits,
		detail:       func(id string, d Deps) Screen { return NewInventoryDetailScreen(d, id) },
		newScreen:    func(d Deps) Screen { return NewKitFormScreen(d) },
	})
}

func loadKits(ctx context.Context, deps Deps) ([]listRow, error) {
	return kitListRows(ctx, deps, nil)
}

// searchKits forwards the operator's query as ?search=, which KitViewSet
// matches (icontains) against name / sku / description / item_suppliers__
// supplier_sku. The supplier SKU is the one worth knowing about: it is what is
// printed on the box a kit arrives in, so a scanned or typed vendor part number
// finds the kit the plain page-1 loader could not.
func searchKits(ctx context.Context, deps Deps, query string) ([]listRow, error) {
	var q url.Values
	if term := strings.TrimSpace(query); term != "" {
		q = url.Values{"search": []string{term}}
	}
	return kitListRows(ctx, deps, q)
}

func kitListRows(ctx context.Context, deps Deps, q url.Values) ([]listRow, error) {
	page, err := deps.OMS.ListKits(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, kit := range page.Results {
		tag := ""
		if !kit.IsActive {
			tag = "inactive"
		}
		rows = append(rows, listRow{
			ID:           kit.ID,
			Title:        kit.Name,
			Subtitle:     kitListSubtitle(kit),
			Tag:          tag,
			CreatedAt:    kit.CreatedAt,
			FallbackDate: kit.UpdatedAt,
		})
	}
	return rows, nil
}

// kitListSubtitle is the one line under a kit's name: how many components it
// decomposes into, then the SKU that identifies which kit that is.
//
// THE COUNT LEADS, and that is the same call kitFieldValue makes on the form
// for the same reason: a list row is clipped rather than folded, so whatever
// must survive has to be at the front — and the count is the fact that says
// what KIND of record this is, where a name alone reads like any other catalogue
// row. It is shown WHOLE or not at all: `component_count` has no omitempty
// upstream and a kit always has at least one, so a zero here means the server
// did not send the key rather than that the kit is empty, and the phrase is
// dropped instead of claiming a figure nobody reported.
//
// The SKU is the item's own, never the vendor's `supplier_sku` — that one is in
// InventoryItemSerializer's VENDOR_ONLY_FIELDS, so it is ABSENT rather than
// blank for a reader who may not see vendor data, and an absent key rendered as
// an empty cell would claim the kit has no part number when the truth is about
// the reader. A blank item SKU is simply left out for the same reason the
// asset list leaves out a blank tag.
func kitListSubtitle(kit omsapi.Kit) string {
	var parts []string
	if kit.ComponentCount > 0 {
		parts = append(parts, fmt.Sprintf("%d %s",
			kit.ComponentCount, plural("component", kit.ComponentCount)))
	}
	if sku := strings.TrimSpace(kit.SKU); sku != "" {
		parts = append(parts, "SKU "+sku)
	}
	return strings.Join(parts, " · ")
}
