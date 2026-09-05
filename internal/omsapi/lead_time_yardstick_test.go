package omsapi

import (
	"context"
	"testing"
)

// TestLeadTimeYardstick_EveryLatenessPayloadCarriesIt pins the wire half of the
// naming rule: all three payloads that carry a lateness or variance figure also
// carry variance_measured_against, and all three decode it through the one
// embedded LeadTimeYardstick rather than through three copies of the key.
//
// A payload WITHOUT the key decodes to "" and must stay "". That is the whole
// point of reading it: an OMS older than the change that added it has not said
// these figures are scored against the standing quote, and a client that
// defaults the answer in is asserting a promise nobody made. "Could not tell"
// and "found nothing" are different facts.
func TestLeadTimeYardstick_EveryLatenessPayloadCarriesIt(t *testing.T) {
	cases := []struct {
		label  string
		served string
		silent string
		get    func(*Client) (string, error)
	}{
		{
			label: "supplier_performance",
			served: `[{"supplier_id":2,"supplier_name":"Acme","total_orders":1,"completed_orders":1,
				"active_orders":0,"average_lead_time_days":4.5,"on_time_delivery_rate":88.9,
				"early_delivery_rate":0.0,"late_delivery_rate":11.1,"total_order_value":"1.00",
				"damage_rate":0.0,"variance_measured_against":"quoted_lead_time"}]`,
			silent: `[{"supplier_id":2,"supplier_name":"Acme","total_orders":1,"completed_orders":1,
				"active_orders":0,"average_lead_time_days":4.5,"on_time_delivery_rate":88.9,
				"early_delivery_rate":0.0,"late_delivery_rate":11.1,"total_order_value":"1.00",
				"damage_rate":0.0}]`,
			get: func(c *Client) (string, error) {
				rows, err := c.ReorderSupplierPerformance(context.Background())
				if err != nil || len(rows) == 0 {
					return "", err
				}
				return rows[0].VarianceMeasuredAgainst, nil
			},
		},
		{
			label: "lead_time_trends",
			served: `[{"month":"2026-07","average_lead_time_days":6.4,"average_variance_days":1.4,
				"total_deliveries":11,"on_time_delivery_rate":63.6,
				"variance_measured_against":"quoted_lead_time"}]`,
			silent: `[{"month":"2026-07","average_lead_time_days":6.4,"average_variance_days":1.4,
				"total_deliveries":11,"on_time_delivery_rate":63.6}]`,
			get: func(c *Client) (string, error) {
				rows, err := c.ReorderLeadTimeTrends(context.Background())
				if err != nil || len(rows) == 0 {
					return "", err
				}
				return rows[0].VarianceMeasuredAgainst, nil
			},
		},
		{
			label: "lead_time_analysis",
			served: `[{"supplier_id":2,"supplier_name":"Acme","item_name":"Bolt","total_orders":4,
				"avg_estimated_lead_time":5.0,"avg_actual_lead_time":6.5,"avg_variance":1.5,
				"on_time_rate":75.0,"variance_measured_against":"quoted_lead_time"}]`,
			silent: `[{"supplier_id":2,"supplier_name":"Acme","item_name":"Bolt","total_orders":4,
				"avg_estimated_lead_time":5.0,"avg_actual_lead_time":6.5,"avg_variance":1.5,
				"on_time_rate":75.0}]`,
			get: func(c *Client) (string, error) {
				rows, err := c.PurchasingLeadTimeAnalysis(context.Background())
				if err != nil || len(rows) == 0 {
					return "", err
				}
				return rows[0].VarianceMeasuredAgainst, nil
			},
		},
	}
	for _, tc := range cases {
		c, _ := reportSrv(t, tc.served)
		got, err := tc.get(c)
		if err != nil {
			t.Fatalf("%s: %v", tc.label, err)
		}
		if got != VarianceYardstickQuotedLeadTime {
			t.Errorf("%s: variance_measured_against decoded as %q, want %q",
				tc.label, got, VarianceYardstickQuotedLeadTime)
		}
		c, _ = reportSrv(t, tc.silent)
		got, err = tc.get(c)
		if err != nil {
			t.Fatalf("%s (silent): %v", tc.label, err)
		}
		if got != "" {
			t.Errorf("%s: a payload with no variance_measured_against decoded as %q. "+
				"An absent key says nothing and must stay nothing", tc.label, got)
		}
	}
}

// TestPurchasingLeadTime_OnTimeRateIsAPercentage is the wire-level half of what
// TestPurchasingLeadTime_OnTimeRateNotDoubled holds on the screen. The doc
// comment on PurchasingLeadTime used to call on_time_rate "a fraction 0..1"
// while reorder_queue/views.py computes on_time_count/total*100 — the render
// has always been right and only the comment was wrong, which is exactly the
// kind of false claim that gets a correct render "fixed" into a 7500% rate.
//
// WHAT THIS PROVES is only that NOTHING ON THIS SIDE SCALES THE VALUE: a served
// 75.0 arrives as 75. It does NOT pin the server's direction and could not —
// reverting the struct comment leaves this green, because the fixture is what
// declares what the wire carries. That direction is read from OMS's own source
// and nowhere else, so no prose here may claim a check holds it.
func TestPurchasingLeadTime_OnTimeRateIsAPercentage(t *testing.T) {
	c, _ := reportSrv(t, `[{"supplier_id":2,"supplier_name":"Acme","item_name":"Bolt",
		"total_orders":4,"avg_estimated_lead_time":5.0,"avg_actual_lead_time":6.5,
		"avg_variance":1.5,"on_time_rate":75.0}]`)
	rows, err := c.PurchasingLeadTimeAnalysis(context.Background())
	if err != nil {
		t.Fatalf("PurchasingLeadTimeAnalysis: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].OnTimeRate != 75.0 {
		t.Errorf("on_time_rate = %v, want 75 — three quarters on time arrives as 75, "+
			"not as 0.75, so nothing on this side may scale it", rows[0].OnTimeRate)
	}
}
