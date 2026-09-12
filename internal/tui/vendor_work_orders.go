// The VENDOR work-order list — ScanTTY's door into the `maintenance_orders`
// app, which until now the terminal could only WRITE to and never read.
//
// THE DEAD END THIS CLOSES. asset_problems.go promotes a reported problem to a
// third-party work order (PromoteAssetProblemThirdParty), and from there the
// whole seven-step vendor workflow lived in the browser: the terminal created
// work it could not list, advance or close. omsapi.ListMaintenanceOrders had
// existed with no caller anywhere. This list is the caller, and
// VendorWorkOrderDetailScreen is what an operator reaches from it.
//
// IT IS A ListScreen rather than a bespoke screen, which is a decision and not
// a convenience: ListScreen carries the footer-honesty sweep, the folded bar,
// the row-budget arithmetic and the short-pane refusal that every hand-rolled
// list in this package is still outside of (list_nav.go's
// listNavUnsweptReceivers records that debt). A new surface has no business
// joining it.
//
// WHY THE ROWS CARRY NO MONEY. A vendor work order's figures — the NTE, the
// invoice total, the variance — are the whole reason this workflow is gated,
// and a list row is drawn straight into the pane where clampToBox CUTS from
// the right with no mark. A cut money figure reads as a smaller one ($1,425.50
// as $1,42), which is the price-column defect AGENTS.md records from the
// picker rows. So money lives on the detail sheet, where it is laid out in
// columns that fit, and the row carries identity and state instead.
package tui

import (
	"context"
	"net/url"
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// vendorWorkOrderKind is the listScreenSpec kind, and it is what
// workspaceForKind reads to keep the sidebar highlight on Maintenance when an
// operator drills into a vendor order.
const vendorWorkOrderKind = "vendor_work_orders"

// vendorWorkOrderFilters are the SERVER-side status views (`f` cycles them).
// ThirdPartyWorkOrderViewSet.filterset_fields carries `status`, so a view is a
// real ?status= narrowing of the whole set rather than a local filter over
// whichever rows page one happened to contain — the same reason the purchase
// order list is filter-driven.
//
// filters[0] is unfiltered, so the landing list is everything. The order after
// it is the STATE MACHINE's own order (transitions.py step 1 through step 7),
// so cycling walks a work order's life rather than an alphabet. `cancelled` is
// last because it is the only status no transition leads to — nothing in
// transitions.py ever writes it — so it is reachable only from the Django
// admin and belongs at the end rather than beside `closed`.
var vendorWorkOrderFilters = []listFilter{
	{label: "all"},
	{label: "requested", query: url.Values{"status": {omsapi.MaintenanceOrderRequested}}},
	{label: "sourcing", query: url.Values{"status": {omsapi.MaintenanceOrderSourcing}}},
	{label: "scheduled", query: url.Values{"status": {omsapi.MaintenanceOrderScheduled}}},
	{label: "in progress", query: url.Values{"status": {omsapi.MaintenanceOrderInProgress}}},
	{label: "validated", query: url.Values{"status": {omsapi.MaintenanceOrderValidated}}},
	{label: "financial review", query: url.Values{"status": {omsapi.MaintenanceOrderFinancialReview}}},
	{label: "closed", query: url.Values{"status": {omsapi.MaintenanceOrderClosed}}},
	{label: "cancelled", query: url.Values{"status": {omsapi.MaintenanceOrderCancelled}}},
}

// NewVendorWorkOrderListScreen builds the vendor work-order list.
func NewVendorWorkOrderListScreen(deps Deps) *ListScreen {
	return NewListScreen(deps, "Vendor work orders", listScreenSpec{
		kind:         vendorWorkOrderKind,
		filters:      vendorWorkOrderFilters,
		filterLoader: vendorWorkOrderRows,
		detail: func(id string, d Deps) Screen {
			return NewVendorWorkOrderDetailScreen(d, id)
		},
	})
}

// vendorWorkOrderRows loads the vendor work orders under the active filter.
//
// The row's TITLE is what the work is, because that is what an operator scans
// a list for; the short id, the vendor and the asset ride in the subtitle. The
// short id is the server's own handle (ThirdPartyWorkOrder.short_id) and the
// one the audit log, the recovery tasks and the web hero all use — it is not
// this side abbreviating the UUID, which is shown WHOLE on the detail sheet
// and nowhere clipped.
func vendorWorkOrderRows(ctx context.Context, deps Deps, q url.Values) ([]listRow, error) {
	page, err := deps.OMS.ListMaintenanceOrders(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, wo := range page.Results {
		title := wo.Title
		if title == "" {
			// A work order with no title is a data defect rather than a state,
			// but a blank row is unreachable: the short id at least names it.
			title = wo.ShortID
		}
		created := wo.OpenedAt
		if created.IsZero() {
			created = wo.CreatedAt
		}
		rows = append(rows, listRow{
			ID:           wo.ID,
			Title:        title,
			Subtitle:     vendorWorkOrderSubtitle(wo),
			Tag:          wo.Status,
			CreatedAt:    created,
			FallbackDate: wo.UpdatedAt,
		})
	}
	return rows, nil
}

// vendorWorkOrderSubtitle is the row's second line: which order, whose vendor,
// which asset — and the EMERGENCY flag, which is the one piece of state the
// status tag cannot carry.
//
// is_emergency is not a status: it rides ALONGSIDE every status and is what
// says the NTE and three-quote gates were bypassed. An order reading
// "(sourcing)" beside three others reading the same is a different kind of
// order when it carries this, and it is the kind whose money nobody capped.
func vendorWorkOrderSubtitle(wo omsapi.MaintenanceOrder) string {
	parts := make([]string, 0, 4)
	if wo.ShortID != "" {
		parts = append(parts, wo.ShortID)
	}
	if wo.IsEmergency {
		parts = append(parts, "emergency")
	}
	if wo.VendorName != "" {
		parts = append(parts, wo.VendorName)
	}
	switch {
	case wo.AssetName != "":
		parts = append(parts, wo.AssetName)
	case wo.LocationName != "":
		parts = append(parts, wo.LocationName)
	}
	return strings.Join(parts, " · ")
}
