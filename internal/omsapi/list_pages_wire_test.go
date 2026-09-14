package omsapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Decode tests for the four paged workspace lists, built from RECORDED OMS
// responses (testdata/{inventory_items,assets,work_orders,purchase_orders}_*.json,
// provenance in testdata/README.md): one seeded backend whose every list runs
// past OMS's PAGE_SIZE of 50, so each endpoint has a page 1 that names a next
// page and a page 2 that does not.
//
// WHAT THE TERMINAL READS OFF A PAGE, and so what these pin: `count` is the
// server's total over every page, `next` is the ONLY statement that another page
// exists (the terminal never infers one from count against rows), and each row's
// id is spent as a list row's identity through fmt.Sprint — so a numeric pk must
// arrive as its digits and a UUID as itself. The guards read the RAW bytes
// alongside the decode, so a fixture "fixed" to match a struct fails.

type listPageCase struct {
	name     string
	path     string
	page1    string
	page2    string
	numberID bool // the builder writes a bare integer pk rather than a string
	list     func(c *Client, q url.Values) (count int, next *string, ids []string, err error)
}

func pageIDs[T any](p *Page[T], id func(T) any) (int, *string, []string) {
	ids := make([]string, 0, len(p.Results))
	for _, r := range p.Results {
		ids = append(ids, fmt.Sprint(id(r)))
	}
	return p.Count, p.Next, ids
}

func listPageCases() []listPageCase {
	ctx := context.Background()
	return []listPageCase{
		{"inventory items", "/api/inventory/items/", "inventory_items_page1.json", "inventory_items_page2.json", false,
			func(c *Client, q url.Values) (int, *string, []string, error) {
				p, err := c.ListItemsWithMetrics(ctx, q)
				if err != nil {
					return 0, nil, nil, err
				}
				n, next, ids := pageIDs(p, func(it Item) any { return it.ID })
				return n, next, ids, nil
			}},
		{"assets", "/api/inventory/assets/", "assets_page1.json", "assets_page2.json", false,
			func(c *Client, q url.Values) (int, *string, []string, error) {
				p, err := c.ListAssets(ctx, q)
				if err != nil {
					return 0, nil, nil, err
				}
				n, next, ids := pageIDs(p, func(a Asset) any { return a.ID })
				return n, next, ids, nil
			}},
		{"work orders", "/api/inventory/work-orders/", "work_orders_page1.json", "work_orders_page2.json", false,
			func(c *Client, q url.Values) (int, *string, []string, error) {
				p, err := c.ListWorkOrders(ctx, q)
				if err != nil {
					return 0, nil, nil, err
				}
				n, next, ids := pageIDs(p, func(w WorkOrder) any { return w.ID })
				return n, next, ids, nil
			}},
		{"purchase orders", "/api/reorders/purchase-orders/", "purchase_orders_page1.json", "purchase_orders_page2.json", true,
			func(c *Client, q url.Values) (int, *string, []string, error) {
				p, err := c.ListPurchaseOrders(ctx, q)
				if err != nil {
					return 0, nil, nil, err
				}
				n, next, ids := pageIDs(p, func(po PurchaseOrder) any { return po.ID })
				return n, next, ids, nil
			}},
	}
}

// rawPage is a recorded page read without the client's types.
func rawPage(t *testing.T, body []byte) (count float64, next any, ids []any) {
	t.Helper()
	raw := rawAsset(t, body)
	results, ok := raw["results"].([]any)
	if !ok {
		t.Fatalf("fixture has no results array")
	}
	for _, r := range results {
		ids = append(ids, r.(map[string]any)["id"])
	}
	count, _ = raw["count"].(float64)
	return count, raw["next"], ids
}

func TestListPages_TheRecordedPagesDecodeAsTheServerSentThem(t *testing.T) {
	for _, tc := range listPageCases() {
		t.Run(tc.name, func(t *testing.T) {
			bodies := map[string][]byte{"": wireBody(t, tc.page1), "2": wireBody(t, tc.page2)}
			var asked []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.path)
				}
				asked = append(asked, r.URL.Query().Get("page"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(bodies[r.URL.Query().Get("page")])
			}))
			defer srv.Close()
			c := New(srv.URL)

			seen := map[string]bool{}
			total := 0
			for _, page := range []string{"", "2"} {
				rawCount, rawNext, rawIDs := rawPage(t, bodies[page])
				var q url.Values
				if page != "" {
					q = url.Values{"page": {page}}
				}
				count, next, ids, err := tc.list(c, q)
				if err != nil {
					t.Fatalf("page %q did not decode: %v", page, err)
				}
				if float64(count) != rawCount || count <= 50 {
					t.Errorf("page %q count = %d, recorded %v; the fixtures must run past PAGE_SIZE 50", page, count, rawCount)
				}
				if (next != nil) != (rawNext != nil) || (page == "") != (next != nil) {
					t.Errorf("page %q next = %v, recorded %v; page 1 names a next page and page 2 does not", page, next, rawNext)
				}
				for i, id := range ids {
					_, isNumber := rawIDs[i].(float64)
					if isNumber != tc.numberID {
						t.Fatalf("recorded id %#v is number=%v, want number=%v — a fixture edited to "+
							"match a struct proves nothing", rawIDs[i], isNumber, tc.numberID)
					}
					if want := strings.TrimSuffix(fmt.Sprint(rawIDs[i]), ".0"); !tc.numberID && id != want {
						t.Errorf("id %q decoded as %q", want, id)
					}
					if tc.numberID && strings.ContainsAny(id, "e.") {
						t.Errorf("a numeric pk decoded as %q, not its digits", id)
					}
					if seen[id] {
						t.Errorf("id %s is on both recorded pages", id)
					}
					seen[id] = true
				}
				total += len(ids)
			}
			if server, _, _ := rawPage(t, bodies[""]); len(seen) != total || float64(total) != server {
				t.Errorf("the two recorded pages hold %d distinct rows, want the server's count", len(seen))
			}
			if strings.Join(asked, ",") != ",2" {
				t.Errorf("pages asked = %v, want the first without ?page= and then page=2", asked)
			}
		})
	}
}

// TestListPages_AnOutOfRangePageIsTheStandardEnvelope pins what a refused page
// looks like, recorded: a 404 in OMS's standardized envelope, which parseError
// reads into its message — the sentence the terminal's header shows for a next
// page that failed.
func TestListPages_AnOutOfRangePageIsTheStandardEnvelope(t *testing.T) {
	body := wireBody(t, "inventory_items_page_out_of_range.json")
	raw := rawAsset(t, body)
	if env, ok := raw["error"].(map[string]any); !ok || env["code"] != "not_found" {
		t.Fatalf("recorded body = %s, want the standardized not_found envelope", body)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	_, err := New(srv.URL).ListItemsWithMetrics(context.Background(), url.Values{"page": {"9"}})
	if err == nil || !strings.Contains(err.Error(), "Invalid page.") {
		t.Fatalf("err = %v, want the server's sentence", err)
	}
}
