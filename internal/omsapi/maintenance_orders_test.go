package omsapi

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// thirdPartyWOBody is the detail payload of a vendor work order.
//
// IT IS TRANSCRIBED FROM THE SERIALIZER, NOT RECORDED FROM A SERVER, and that
// is worth saying plainly because internal/omsapi/testdata/README.md's rule is
// that a fixture written from the Go struct cannot contradict it. The authority
// this one was written from is the OTHER side —
// `backend/maintenance_orders/serializers.py`'s ThirdPartyWorkOrderSerializer
// (Meta.fields, plus get_workflow's dict) and the model field declarations in
// `backend/maintenance_orders/models.py` — so it can still disagree with the
// struct, which is the property that matters. Where it is at its most useful is
// exactly where a struct-first author would have guessed wrong:
//
//   - `location` is a NUMBER while every other id here is a UUID string.
//     inventory.Location declares no primary key, so it is
//     settings.DEFAULT_AUTO_FIELD (BigAutoField); every model in the
//     maintenance_orders app declares an explicit models.UUIDField.
//   - every money field is a JSON STRING (DRF DecimalField), and `dispatch_fee`
//     is null rather than absent.
//   - `total_downtime` is django.utils.duration_string's "HH:MM:SS", not
//     seconds.
const thirdPartyWOBody = `{
  "id":"c0ffee00-1111-2222-3333-444455556666",
  "short_id":"TPWO-C0FFEE00",
  "title":"Replace compressor belt",
  "asset":"a1b2c3d4-0000-4444-8888-abcdef012345","asset_name":"Bridgeport Mill",
  "location":17,"location_name":"Machine Shop",
  "vendor":"3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f","vendor_name":"Acme Electric",
  "work_type":"major_repair","work_type_display":"Major Repair",
  "is_emergency":false,
  "status":"financial_review","status_display":"Financial Review",
  "nte_amount":"1200.00","par_cost_buffer":"50.00",
  "actual_invoice_total":"1425.50","dispatch_fee":null,
  "downtime_start":"2026-03-02T14:00:00Z","downtime_end":"2026-03-02T18:30:00Z",
  "total_downtime":"04:30:00",
  "keyfob_id":"KF-0042","keyfob_returned_at":null,"keyfob_returned_by":null,
  "warranty_recovery":true,
  "nte_set_by":7,"nte_set_at":"2026-03-01T09:00:00Z",
  "emergency_authorized_by":null,"emergency_authorized_at":null,
  "quote_waiver_signed_by":9,"quote_waiver_signed_at":"2026-03-01T11:00:00Z",
  "quote_waiver_reason":"Sole authorized service provider for this model.",
  "variance_status":"blocked",
  "shadow_user":null,"opened_by":7,
  "opened_at":"2026-03-01T08:00:00Z","closed_at":null,
  "notes":"Belt slipping under load.","internal_notes":"Third call this year.",
  "asset_links":[{"id":"11111111-2222-3333-4444-555555555555",
    "work_order":"c0ffee00-1111-2222-3333-444455556666",
    "asset":"a1b2c3d4-0000-4444-8888-abcdef012345",
    "asset_name":"Bridgeport Mill","asset_tag":"AST-0031",
    "share_pct":"100.00","allocated_cost":"1425.50","notes":"",
    "created_at":"2026-03-01T08:00:00Z"}],
  "attachments":[{"id":"22222222-3333-4444-5555-666666666666",
    "work_order":"c0ffee00-1111-2222-3333-444455556666",
    "file":"/media/third_party_work_orders/c0ffee00/invoice.pdf",
    "kind":"invoice","kind_display":"Invoice","caption":"Acme invoice 8812",
    "uploaded_by":7,"uploaded_by_username":"shop.lead",
    "uploaded_at":"2026-03-02T19:00:00Z"}],
  "quotes":[{"id":"33333333-4444-5555-6666-777777777777",
    "work_order":"c0ffee00-1111-2222-3333-444455556666",
    "vendor":"3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f","vendor_name":"Acme Electric",
    "amount":"1180.00","notes":"Parts included","submitted_by":7,
    "created_at":"2026-03-01T10:00:00Z"}],
  "active_warranty":null,
  "workflow":{"has_nte":true,"has_active_emergency_authorization":false,
    "has_required_quotes":true,"quote_count":1,"has_photo_evidence":true,
    "has_invoice_and_fsr":false,"variance_status":"blocked",
    "keyfob_outstanding":true},
  "created_at":"2026-03-01T08:00:00Z","updated_at":"2026-03-02T19:00:00Z"
}`

// TestGetMaintenanceOrder_DecodesTheWholeStepper walks every field the vendor
// stepper reads. It is one test rather than a dozen because the failure it
// guards against is a SINGLE mistyped field silently zeroing itself: a decode
// that half-worked is what put int ids on this struct in the first place.
func TestGetMaintenanceOrder_DecodesTheWholeStepper(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, thirdPartyWOBody, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetMaintenanceOrder(context.Background(),
		"c0ffee00-1111-2222-3333-444455556666")
	if err != nil {
		t.Fatalf("GetMaintenanceOrder: %v", err)
	}
	if cap.method != http.MethodGet {
		t.Errorf("method = %s, want GET", cap.method)
	}
	if want := "/api/maintenance-orders/work-orders/c0ffee00-1111-2222-3333-444455556666/"; cap.path != want {
		t.Errorf("path = %s, want %s", cap.path, want)
	}

	if wo.ID != "c0ffee00-1111-2222-3333-444455556666" {
		t.Errorf("id = %q", wo.ID)
	}
	if wo.ShortID != "TPWO-C0FFEE00" {
		t.Errorf("short_id = %q", wo.ShortID)
	}
	if wo.Vendor != "3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f" || wo.VendorName != "Acme Electric" {
		t.Errorf("vendor = %q / %q", wo.Vendor, wo.VendorName)
	}
	if wo.Asset == nil || *wo.Asset != "a1b2c3d4-0000-4444-8888-abcdef012345" {
		t.Errorf("asset = %v", wo.Asset)
	}
	// The one numeric id on the payload. A *string here decodes nothing and
	// the machine-shop location silently becomes "no location".
	if wo.Location == nil || *wo.Location != 17 || wo.LocationName != "Machine Shop" {
		t.Errorf("location = %v / %q, want 17 / Machine Shop", wo.Location, wo.LocationName)
	}
	if wo.Status != "financial_review" || wo.StatusDisplay != "Financial Review" {
		t.Errorf("status = %q / %q", wo.Status, wo.StatusDisplay)
	}
	if wo.WorkTypeDisplay != "Major Repair" {
		t.Errorf("work_type_display = %q", wo.WorkTypeDisplay)
	}
	if wo.NTEAmount != "1200.00" || wo.ActualInvoiceTotal != "1425.50" || wo.ParCostBuffer != "50.00" {
		t.Errorf("money = %q / %q / %q", wo.NTEAmount, wo.ActualInvoiceTotal, wo.ParCostBuffer)
	}
	// null is "no figure recorded" and is NOT 0.00 — the fee is unset here, and
	// a reader that showed $0.00 would be stating a fact the server did not.
	if !wo.DispatchFee.Empty() {
		t.Errorf("dispatch_fee = %q, want empty for null", wo.DispatchFee)
	}
	if wo.TotalDowntime != "04:30:00" {
		t.Errorf("total_downtime = %q, want the duration string verbatim", wo.TotalDowntime)
	}
	if wo.DowntimeStart == nil || wo.DowntimeEnd == nil {
		t.Errorf("downtime window = %v..%v", wo.DowntimeStart, wo.DowntimeEnd)
	}
	if wo.KeyfobID != "KF-0042" || wo.KeyfobReturnedAt != nil {
		t.Errorf("keyfob = %q / %v", wo.KeyfobID, wo.KeyfobReturnedAt)
	}
	if !wo.WarrantyRecovery {
		t.Error("warranty_recovery = false, want true")
	}
	if wo.VarianceStatus != MaintenanceOrderVarianceBlocked {
		t.Errorf("variance_status = %q", wo.VarianceStatus)
	}
	if wo.QuoteWaiverReason == "" || wo.QuoteWaiverSignedAt == nil {
		t.Errorf("waiver = %q / %v", wo.QuoteWaiverReason, wo.QuoteWaiverSignedAt)
	}
	if wo.ClosedAt != nil {
		t.Errorf("closed_at = %v, want nil", wo.ClosedAt)
	}

	wf := wo.Workflow
	if !wf.HasNTE || !wf.HasRequiredQuotes || !wf.HasPhotoEvidence {
		t.Errorf("workflow gates = %+v", wf)
	}
	if wf.HasInvoiceAndFSR {
		t.Error("has_invoice_and_fsr = true, want false")
	}
	if wf.QuoteCount != 1 || !wf.KeyfobOutstanding || wf.VarianceStatus != "blocked" {
		t.Errorf("workflow = %+v", wf)
	}

	if len(wo.Quotes) != 1 || wo.Quotes[0].Amount != "1180.00" ||
		wo.Quotes[0].VendorName != "Acme Electric" {
		t.Errorf("quotes = %+v", wo.Quotes)
	}
	if len(wo.Attachments) != 1 || wo.Attachments[0].Kind != MaintenanceOrderAttachmentInvoice ||
		wo.Attachments[0].UploadedByName != "shop.lead" {
		t.Errorf("attachments = %+v", wo.Attachments)
	}
	if len(wo.AssetLinks) != 1 || wo.AssetLinks[0].AllocatedCost != "1425.50" ||
		wo.AssetLinks[0].AssetTag != "AST-0031" {
		t.Errorf("asset_links = %+v", wo.AssetLinks)
	}
}

// TestListMaintenanceOrders_DecodesAndFiltersServerSide pins the list: the
// status view is a SERVER-side ?status= (the viewset's filterset_fields), not a
// local narrowing of page one, which could only ever filter rows already in
// hand.
func TestListMaintenanceOrders_DecodesAndFiltersServerSide(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"count":1,"results":[`+thirdPartyWOBody+`]}`, &cap)
	defer srv.Close()

	page, err := New(srv.URL).ListMaintenanceOrders(context.Background(),
		map[string][]string{"status": {"sourcing"}})
	if err != nil {
		t.Fatalf("ListMaintenanceOrders: %v", err)
	}
	if cap.path != "/api/maintenance-orders/work-orders/" {
		t.Errorf("path = %s", cap.path)
	}
	if cap.query != "status=sourcing" {
		t.Errorf("query = %q, want status=sourcing", cap.query)
	}
	if len(page.Results) != 1 || page.Results[0].ShortID != "TPWO-C0FFEE00" {
		t.Fatalf("results = %+v", page.Results)
	}
	if page.Results[0].VendorName != "Acme Electric" {
		t.Errorf("a list row must carry the vendor name: %+v", page.Results[0])
	}
}

// TestMaintenanceOrderActions_PathAndBody drives every state-machine action
// this client offers and asserts the REQUEST that went out — the method, the
// exact @action url_path, and the body.
//
// The url_path strings are the half a reader cannot check by eye against the Go
// method name: `sign-off` is ops_sign_off, `record-keyfob-return` is
// record_keyfob_return, and a typo in any of them is a 404 the operator reads
// as "the server refused my sign-off".
func TestMaintenanceOrderActions_PathAndBody(t *testing.T) {
	const id = "c0ffee00-1111-2222-3333-444455556666"
	base := "/api/maintenance-orders/work-orders/" + id + "/"

	cases := []struct {
		name string
		call func(c *Client) error
		path string
		body map[string]any
	}{
		{
			name: "set-nte carries the amount as typed",
			call: func(c *Client) error {
				_, err := c.SetMaintenanceOrderNTE(context.Background(), id, " 1200.00 ")
				return err
			},
			path: base + "set-nte/",
			// Trimmed, but NOT reformatted: a float round-trip is how a decimal
			// stops being the number the operator typed.
			body: map[string]any{"nte_amount": "1200.00"},
		},
		{
			name: "advance-to-sourcing sends an explicit empty object",
			call: func(c *Client) error {
				_, err := c.AdvanceMaintenanceOrderToSourcing(context.Background(), id)
				return err
			},
			path: base + "advance-to-sourcing/",
			body: map[string]any{},
		},
		{
			name: "waive-quote-requirement carries the audit reason",
			call: func(c *Client) error {
				_, err := c.WaiveMaintenanceOrderQuoteRequirement(context.Background(), id, "Sole provider")
				return err
			},
			path: base + "waive-quote-requirement/",
			body: map[string]any{"reason": "Sole provider"},
		},
		{
			name: "advance-to-scheduled sends an explicit empty object",
			call: func(c *Client) error {
				_, err := c.AdvanceMaintenanceOrderToScheduled(context.Background(), id)
				return err
			},
			path: base + "advance-to-scheduled/",
			body: map[string]any{},
		},
		{
			name: "vendor-arrived carries the keyfob",
			call: func(c *Client) error {
				_, err := c.MarkMaintenanceOrderVendorArrived(context.Background(), id, "KF-0042")
				return err
			},
			path: base + "vendor-arrived/",
			body: map[string]any{"keyfob_id": "KF-0042"},
		},
		{
			// The transition assigns only `if keyfob_id`, so an empty string
			// would be a no-op server-side — but sending the key at all is a
			// claim about a fob nobody handed over.
			name: "vendor-arrived omits a blank keyfob rather than sending an empty one",
			call: func(c *Client) error {
				_, err := c.MarkMaintenanceOrderVendorArrived(context.Background(), id, "   ")
				return err
			},
			path: base + "vendor-arrived/",
			body: map[string]any{},
		},
		{
			name: "sign-off sends an explicit empty object",
			call: func(c *Client) error {
				_, err := c.SignOffMaintenanceOrder(context.Background(), id)
				return err
			},
			path: base + "sign-off/",
			body: map[string]any{},
		},
		{
			name: "record-keyfob-return sends an explicit empty object",
			call: func(c *Client) error {
				_, err := c.RecordMaintenanceOrderKeyfobReturn(context.Background(), id)
				return err
			},
			path: base + "record-keyfob-return/",
			body: map[string]any{},
		},
		{
			name: "advance-to-financial-review carries both figures as strings",
			call: func(c *Client) error {
				_, err := c.AdvanceMaintenanceOrderToFinancialReview(context.Background(), id, "1425.50", "95.00")
				return err
			},
			path: base + "advance-to-financial-review/",
			body: map[string]any{"actual_invoice_total": "1425.50", "dispatch_fee": "95.00"},
		},
		{
			// An omitted dispatch_fee leaves whatever is on the order alone
			// (`if dispatch_fee is not None`). An empty string would 400 on
			// Decimal(""), and a "0" would OVERWRITE a real fee with zero.
			name: "advance-to-financial-review omits a blank dispatch fee",
			call: func(c *Client) error {
				_, err := c.AdvanceMaintenanceOrderToFinancialReview(context.Background(), id, "1425.50", "")
				return err
			},
			path: base + "advance-to-financial-review/",
			body: map[string]any{"actual_invoice_total": "1425.50"},
		},
		{
			name: "override-variance carries the audit reason",
			call: func(c *Client) error {
				_, err := c.OverrideMaintenanceOrderVariance(context.Background(), id, "SIG absorbing overage")
				return err
			},
			path: base + "override-variance/",
			body: map[string]any{"reason": "SIG absorbing overage"},
		},
		{
			name: "close sends an explicit empty object",
			call: func(c *Client) error {
				_, err := c.CloseMaintenanceOrder(context.Background(), id)
				return err
			},
			path: base + "close/",
			body: map[string]any{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cap capture
			srv := captureServer(t, http.StatusOK, thirdPartyWOBody, &cap)
			defer srv.Close()
			if err := tc.call(New(srv.URL)); err != nil {
				t.Fatalf("call: %v", err)
			}
			if cap.method != http.MethodPost {
				t.Errorf("method = %s, want POST", cap.method)
			}
			if cap.path != tc.path {
				t.Errorf("path = %s, want %s", cap.path, tc.path)
			}
			if len(cap.body) != len(tc.body) {
				t.Fatalf("body = %v, want %v", cap.body, tc.body)
			}
			for k, want := range tc.body {
				if got, ok := cap.body[k]; !ok || got != want {
					t.Errorf("body[%q] = %v (present=%v), want %v", k, got, ok, want)
				}
			}
		})
	}
}

// TestAuthorizeMaintenanceOrderEmergency_AnswersWithTheAuthorization pins the
// ONE action that does not answer with the work order: authorize_emergency
// returns the 24-hour EmergencyAuthorization row it created, at HTTP 201.
// Decoding it as a MaintenanceOrder would leave a caller holding a
// zero-valued order it might then display.
func TestAuthorizeMaintenanceOrderEmergency_AnswersWithTheAuthorization(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{
		"id":"44444444-5555-6666-7777-888888888888",
		"work_order":"c0ffee00-1111-2222-3333-444455556666",
		"authorized_by":7,"authorized_at":"2026-03-01T09:00:00Z",
		"expires_at":"2026-03-02T09:00:00Z","reason":"weekend leak",
		"revoked_at":null,"is_currently_valid":true}`, &cap)
	defer srv.Close()

	auth, err := New(srv.URL).AuthorizeMaintenanceOrderEmergency(context.Background(),
		"c0ffee00-1111-2222-3333-444455556666", "weekend leak")
	if err != nil {
		t.Fatalf("AuthorizeMaintenanceOrderEmergency: %v", err)
	}
	if want := "/api/maintenance-orders/work-orders/c0ffee00-1111-2222-3333-444455556666/authorize-emergency/"; cap.path != want {
		t.Errorf("path = %s, want %s", cap.path, want)
	}
	if cap.body["reason"] != "weekend leak" {
		t.Errorf("body = %v", cap.body)
	}
	if auth.ID != "44444444-5555-6666-7777-888888888888" || !auth.IsValid {
		t.Errorf("authorization = %+v", auth)
	}
	if auth.ExpiresAt.IsZero() {
		t.Error("expires_at did not decode — the 24-hour window is the whole point of the row")
	}
}

// TestAddMaintenanceOrderQuote_PostsToTheFlatCollection pins that the quote
// endpoint is a COLLECTION with the work order in the body, not a sub-resource
// under the order's path.
func TestAddMaintenanceOrderQuote_PostsToTheFlatCollection(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{
		"id":"33333333-4444-5555-6666-777777777777",
		"work_order":"c0ffee00-1111-2222-3333-444455556666",
		"vendor":"3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f","vendor_name":"Acme Electric",
		"amount":"1180.00","notes":"","created_at":"2026-03-01T10:00:00Z"}`, &cap)
	defer srv.Close()

	q, err := New(srv.URL).AddMaintenanceOrderQuote(context.Background(),
		"c0ffee00-1111-2222-3333-444455556666",
		"3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f", "1180.00", "")
	if err != nil {
		t.Fatalf("AddMaintenanceOrderQuote: %v", err)
	}
	if cap.path != "/api/maintenance-orders/quotes/" {
		t.Errorf("path = %s", cap.path)
	}
	if cap.body["work_order"] != "c0ffee00-1111-2222-3333-444455556666" ||
		cap.body["vendor"] != "3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f" ||
		cap.body["amount"] != "1180.00" {
		t.Errorf("body = %v", cap.body)
	}
	if _, ok := cap.body["notes"]; ok {
		t.Errorf("a blank note is omitted rather than sent empty: %v", cap.body)
	}
	if q.Amount != "1180.00" {
		t.Errorf("amount = %q", q.Amount)
	}
}

// TestUploadMaintenanceOrderAttachment_CarriesTheGateKind pins the multipart
// upload. `kind` is the half that matters: photo is what sign-off requires and
// invoice+fsr are what closure requires, so a file filed under the wrong kind
// leaves the gate shut with nothing on screen to say why.
func TestUploadMaintenanceOrderAttachment_CarriesTheGateKind(t *testing.T) {
	var gotPath, gotWO, gotKind, gotCaption, gotFile, gotName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			raw, _ := io.ReadAll(part)
			switch part.FormName() {
			case "work_order":
				gotWO = string(raw)
			case "kind":
				gotKind = string(raw)
			case "caption":
				gotCaption = string(raw)
			case "file":
				gotFile, gotName = string(raw), part.FileName()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"22222222-3333-4444-5555-666666666666",
			"kind":"fsr","kind_display":"Field Service Report","caption":"Acme FSR"}`))
	}))
	defer srv.Close()

	att, err := New(srv.URL).UploadMaintenanceOrderAttachment(context.Background(),
		"c0ffee00-1111-2222-3333-444455556666", "fsr.pdf",
		strings.NewReader("%PDF-1.4 field service report"),
		MaintenanceOrderAttachmentFSR, "Acme FSR")
	if err != nil {
		t.Fatalf("UploadMaintenanceOrderAttachment: %v", err)
	}
	if gotPath != "/api/maintenance-orders/attachments/" {
		t.Errorf("path = %s — the attachment collection is FLAT", gotPath)
	}
	if gotWO != "c0ffee00-1111-2222-3333-444455556666" {
		t.Errorf("work_order field = %q", gotWO)
	}
	if gotKind != "fsr" {
		t.Errorf("kind = %q, want fsr — closure gates on it", gotKind)
	}
	if gotCaption != "Acme FSR" {
		t.Errorf("caption = %q", gotCaption)
	}
	if gotName != "fsr.pdf" || !strings.HasPrefix(gotFile, "%PDF") {
		t.Errorf("file part = %q / %q", gotName, gotFile)
	}
	if att.KindDisplay != "Field Service Report" {
		t.Errorf("kind_display = %q", att.KindDisplay)
	}
}

// TestUploadMaintenanceOrderAttachment_OmitsABlankKind lets the server apply
// its own `other` default rather than sending an empty field it would have to
// reject or coerce.
func TestUploadMaintenanceOrderAttachment_OmitsABlankKind(t *testing.T) {
	sawKind := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "kind" || part.FormName() == "caption" {
				sawKind = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).UploadMaintenanceOrderAttachment(context.Background(),
		"wo-1", "x.pdf", strings.NewReader("x"), "  ", ""); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if sawKind {
		t.Error("a blank kind/caption was sent as an empty field instead of being omitted")
	}
}

// TestAsMaintenanceOrderRefusal_RecoversTheSentenceAndNothingElse.
//
// views._err writes `{"detail": ...}` by hand and returns it as a plain
// Response, so it never reaches DRF's exception handler and parseError — which
// understands only `{"error": {"code": ...}}` — hands the WHOLE RAW BODY over.
// Without the recogniser the operator reads the JSON on a status row that
// cannot fold.
//
// BOTH SHAPES ARE HERE because _err produces both: `detail[0] if len(detail)
// == 1 else detail`, so a ValidationError carrying several messages answers
// with a LIST. A recogniser that handled only the string form would fall back
// to the raw blob on exactly the refusals with the most to say.
func TestAsMaintenanceOrderRefusal_RecoversTheSentenceAndNothingElse(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{
			name: "the single-message ValidationError _err writes",
			body: `{"detail": "Work order is in 'requested' state; expected 'sourcing'."}`,
			want: "Work order is in 'requested' state; expected 'sourcing'.",
			ok:   true,
		},
		{
			name: "the multi-message form, which the string-only reader would miss",
			body: `{"detail": ["Waiver requires a written reason for audit.", "Nothing was signed."]}`,
			want: "Waiver requires a written reason for audit. Nothing was signed.",
			ok:   true,
		},
		{
			name: "DRF's own permission body, which is the sentence an operator needs",
			body: `{"detail": "Only Logistics or staff may authorize emergency bypass."}`,
			want: "Only Logistics or staff may authorize emergency bypass.",
			ok:   true,
		},
		// Everything below keeps the shape it arrived in: a recogniser that
		// claimed these would be inventing a sentence nobody wrote.
		{name: "a gateway page is not JSON at all", body: `<!DOCTYPE html><html>502`},
		{name: "the standard envelope belongs to parseError", body: `{"error":{"code":"x","message":"y"}}`},
		{name: "a field-validation envelope names fields, not a detail", body: `{"nte_amount":["This field is required."]}`},
		{name: "a blank detail says nothing", body: `{"detail": "   "}`},
		{name: "an empty list says nothing", body: `{"detail": []}`},
		{name: "a numeric detail is not a sentence", body: `{"detail": 42}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := AsMaintenanceOrderRefusal(&APIError{Status: 400, Message: tc.body})
			if ok != tc.ok {
				t.Fatalf("recognised = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Errorf("sentence = %q, want %q", got, tc.want)
			}
		})
	}
	if _, ok := AsMaintenanceOrderRefusal(context.Canceled); ok {
		t.Error("a non-API error was recognised as a refusal")
	}
}
