package tui

import "context"

// reorder_reports.go adds the reorders/purchasing analytics reports that the
// #84 Reports work deferred, as tabs on the shared generic ReportTableScreen
// (report_table.go). They mirror the reorder_queue AnalyticsViewSet endpoints
// (/api/reorders/analytics/…): supplier_performance, lead_time_trends,
// transparency (summary + order ledger + PO ledger), and logistics_dashboard.
//
// A terminal can't draw the web's charts / TV tiles, so — like the other
// report pages — each surface renders its underlying DATA as an aligned table.
// The transparency + logistics envelopes are single objects rather than row
// arrays, so their loaders reshape the object into Metric/Value rows (summary,
// logistics) or pull out the embedded ledger arrays (orders, POs).

// --- small formatting helpers (companions to report_table.go's fmtMoney) -----

// fmtMoneyPtr formats a nullable float money value, "—" for nil. The reorders
// transparency endpoint serializes costs as float(...)-or-null.
func fmtMoneyPtr(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmtMoney(*p)
}

// reportWithheld is what a transparency cell says where OMS withheld the value
// from this reader. A word and not "—", because "—" in these columns already
// means "nothing recorded", and a withheld figure is one that WAS recorded:
// the two send an operator in opposite directions — one to go and record it,
// the other to sign in as somebody allowed to read it.
const reportWithheld = "withheld"

// vendorMoney is a transparency money cell on a row whose vendor block the
// server may have withheld. The marker decides it, never the nil: an omitted
// key and a null one both decode to nil, and only the marker says which.
func vendorMoney(withheld bool, p *float64) string {
	if withheld {
		return reportWithheld
	}
	return fmtMoneyPtr(p)
}

// vendorText is vendorMoney for a vendor NAME.
func vendorText(withheld bool, s string) string {
	if withheld {
		return reportWithheld
	}
	return orDash(s)
}

// fmtPct renders an already-computed percentage (the backend multiplies by 100
// itself — do NOT scale again) as e.g. "88.9%".
func fmtPct(f float64) string {
	return trimFloat(f) + "%"
}

// fmtYesNo renders a bool as yes/no for a table cell.
func fmtYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// NewReorderAnalyticsReportScreen mirrors the reorders/purchasing analytics
// surfaces the web splits across the supplier-performance / lead-time /
// transparency / logistics pages, as one tabbed report. supplier_performance +
// lead_time_trends are staff/auth-only API endpoints with no dedicated web
// page; transparency + logistics are the public accountability/TV surfaces.
func NewReorderAnalyticsReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "Reorders analytics", []reportTab{
		{
			label: "Supplier perf",
			// On-time and Late are counted off variance_days, so both are scored
			// against the supplier link's standing quote and neither is a bare
			// lateness — hence the mark, which the legend above the table
			// explains. Lead d is not marked: it is what the deliveries took,
			// with no promise in it. The "%" the old headers carried is gone
			// because every value in those columns already ends in one, and the
			// cells it frees are what keeps both marked columns on an 80-column
			// pane.
			columns: []reportColumn{
				{"Supplier", alignLeft}, {"Orders", alignRight}, {"Done", alignRight},
				{"Lead d", alignRight}, {"On-time" + reportYardstickMark, alignRight},
				{"Late" + reportYardstickMark, alignRight},
				{"Damage", alignRight}, {"Order value", alignRight},
			},
			// "average lead time in days" and not "lead time in days": Lead d is
			// average_lead_time_days, a MEAN over the supplier's deliveries, and
			// the header lost the word to buy the cells the marks cost. The note
			// is the surface that already carries the units, so it is where the
			// mean is named — and the sibling Lead-time trends tab says
			// "Monthly averages in days" for the same figure, so leaving it out
			// here had two tabs describing one kind of figure differently.
			note: "Rates are percentages · average lead time in days · Order value = Σ estimated PO total · sorted by value desc.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.ReorderSupplierPerformance(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				yards := make([]string, len(rows))
				for i, r := range rows {
					out[i] = []string{
						orDash(r.SupplierName), itoa(r.TotalOrders), itoa(r.CompletedOrders),
						trimFloat(r.AverageLeadTimeDays), fmtPct(r.OnTimeDeliveryRate),
						fmtPct(r.LateDeliveryRate), fmtPct(r.DamageRate),
						// total_order_value is a DecimalField → string on the wire.
						fmtMoneyStr(r.TotalOrderValue),
					}
					yards[i] = r.VarianceMeasuredAgainst
				}
				return reportBody{rows: out, yardstick: reportYardstickOf(yards)}, nil
			},
		},
		{
			label: "Lead-time trends",
			// Var d and On-time are both derived from variance_days; Lead d is
			// the month's actual average and carries no promise, so it is not
			// marked.
			columns: []reportColumn{
				{"Month", alignLeft}, {"Lead d", alignRight},
				{"Var d" + reportYardstickMark, alignRight},
				{"Deliveries", alignRight}, {"On-time" + reportYardstickMark, alignRight},
			},
			note: "Monthly averages in days · trailing 6 months · rate is a percentage.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				rows, err := deps.OMS.ReorderLeadTimeTrends(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(rows))
				yards := make([]string, len(rows))
				for i, r := range rows {
					out[i] = []string{
						orDash(r.Month), trimFloat(r.AverageLeadTimeDays),
						trimFloat(r.AverageVarianceDays), itoa(r.TotalDeliveries),
						fmtPct(r.OnTimeDeliveryRate),
					}
					yards[i] = r.VarianceMeasuredAgainst
				}
				return reportBody{rows: out, yardstick: reportYardstickOf(yards)}, nil
			},
		},
		{
			label: "Transparency",
			columns: []reportColumn{
				{"Metric", alignLeft}, {"Value", alignRight},
			},
			note: "Public financial transparency roll-up (open books). Orders / POs are the next two tabs.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				tr, err := deps.OMS.ReorderTransparency(ctx)
				if err != nil {
					return reportBody{}, err
				}
				s := tr.Summary
				return reportBody{rows: [][]string{
					{"Orders with financial data", itoa(s.TotalOrdersWithFinancialData)},
					{"Total amount spent", fmtMoney(s.TotalAmountSpent)},
					{"Purchase orders", itoa(s.TotalPurchaseOrders)},
					{"Total PO amount spent", fmtMoney(s.TotalPOAmountSpent)},
					{"Last updated", orDash(dateOnly(s.LastUpdated))},
				}}, nil
			},
		},
		{
			label: "Trans. orders",
			// NO SUPPLIER AND NO ESTIMATE COLUMN, because a reorder request
			// records neither. The Est and Supplier this tab used to draw were
			// the ITEM's current best supplier and a live quote at its current
			// price, published under the order's name until OMS #1057 withdrew
			// them — a fabricated attribution rather than a wrong one. Both went
			// with it, not onto another field: what #1057 publishes instead
			// (item_supplier_choice, item_estimated_cost_today) is true of the
			// ITEM as of this response, and on a row per ORDER a supplier name
			// beside the Actual figure reads as who was paid, and a price beside
			// it as the order's budget — the two claims #1057 removed. The web's
			// signed-in ledger keeps the item's supplier under a full-width
			// "Item supplier today", but here that header would head an
			// identifier column, which is what fitReportTable abbreviates — from
			// the right — whenever the pane is short, and "today", the one word
			// that stops it reading as this order's supplier, is the tail the
			// abbreviation takes first. The note states the FACT — a request
			// records neither — rather than naming columns that are no longer
			// drawn, so an operator who remembers them learns why they went.
			//
			// Actual keeps three states apart (vendorMoney): a recorded figure
			// ($0.00 for a donation among them), "—" where nothing was recorded,
			// and "withheld" where the server did not tell this reader.
			columns: []reportColumn{
				{"Item", alignLeft}, {"Category", alignLeft}, {"Qty", alignRight},
				{"Status", alignLeft}, {"Ordered", alignLeft}, {"Actual", alignRight},
			},
			note: "Public order ledger · last 100 reorder requests with financial data · " +
				"— under Actual: no cost recorded · " +
				"a request records no supplier and no estimate.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				tr, err := deps.OMS.ReorderTransparency(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(tr.Orders))
				for i, o := range tr.Orders {
					out[i] = []string{
						orDash(o.ItemName), orDash(o.ItemCategory), itoa(o.QuantityOrdered),
						orDash(o.Status), orDash(dateOnly(o.OrderedAt)),
						vendorMoney(o.VendorDataWithheld, o.ActualCost),
					}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label: "Trans. POs",
			// Every column stays: a purchase order HAS a supplier FK and records
			// its own totals, so these are the order's facts. What changes is
			// the three the server withholds from a reader not shown vendor data,
			// which say so rather than drawing the "—" that means none recorded.
			columns: []reportColumn{
				{"PO #", alignLeft}, {"Supplier", alignLeft}, {"Status", alignLeft},
				{"Ordered", alignLeft}, {"Expected", alignLeft}, {"Est total", alignRight},
				{"Actual total", alignRight}, {"Recv", alignRight},
			},
			note: "Public PO ledger · last 50 sent/received purchase orders.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				tr, err := deps.OMS.ReorderTransparency(ctx)
				if err != nil {
					return reportBody{}, err
				}
				out := make([][]string, len(tr.PurchaseOrders))
				for i, p := range tr.PurchaseOrders {
					out[i] = []string{
						orDash(p.PONumber), vendorText(p.VendorDataWithheld, p.SupplierName),
						orDash(p.StatusLabel),
						orDash(dateOnly(p.OrderDate)), orDash(dateOnly(p.ExpectedDeliveryDate)),
						vendorMoney(p.VendorDataWithheld, p.EstimatedTotal),
						vendorMoney(p.VendorDataWithheld, p.ActualTotal),
						fmtYesNo(p.IsFullyReceived),
					}
				}
				return reportBody{rows: out}, nil
			},
		},
		{
			label: "Logistics",
			columns: []reportColumn{
				{"Metric", alignLeft}, {"Value", alignRight},
			},
			note: "Public logistics / TV dashboard · QR scans = unique assets+items scanned per day.",
			loader: func(ctx context.Context, deps Deps) (reportBody, error) {
				d, err := deps.OMS.ReorderLogisticsDashboard(ctx)
				if err != nil {
					return reportBody{}, err
				}
				rows := [][]string{
					{"Open item requests", itoa(d.OpenItemRequests)},
					{"Open locations with problems", itoa(d.OpenLocationsWithProblems)},
					{"Urgent location problems", itoa(d.UrgentLocationProblems)},
					{"Alert active", fmtYesNo(d.AlertActive)},
					{"Assets overdue maintenance", itoa(d.AssetsOverdueMaintenance)},
					{"PM overdue", itoa(d.PMOverdue)},
					{"PM due this week", itoa(d.PMDueThisWeek)},
					{"QR scans (7-day total)", itoa(d.QRScansTotal)},
				}
				for _, day := range d.QRScansByDay {
					rows = append(rows, []string{"QR scans " + orDash(day.Date), itoa(day.Count)})
				}
				return reportBody{rows: rows}, nil
			},
		},
	})
}
