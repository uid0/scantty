// Third-party (VENDOR) maintenance work orders — the `maintenance_orders`
// Django app, served under /api/maintenance-orders/.
//
// NOT to be confused with `maintenance.go`, which is the PM/in-house side
// (MaintenanceItem, and the asset-problem promote that CREATES one of these).
// A ThirdPartyWorkOrder is a purchase: a vendor comes on site, an invoice
// arrives, and money leaves the makerspace. The whole surface is
// authenticated and staff/Logistics/SIG-admin gated server-side
// (`_StaffWriteMixin`, `permission_classes = [IsStaffOrSigAdmin]`) — READS
// included, deliberately, so a volunteer never sees vendor identity or vendor
// pricing. Nothing on this side may widen that.
//
// THE SEVEN-STEP STATE MACHINE IS THE SERVER'S AND IS NEVER RE-DERIVED HERE.
// `backend/maintenance_orders/transitions.py` owns every gate; each action
// below is a thin POST that lets the server refuse. What this client DOES
// carry is the `workflow` block the serializer computes
// (ThirdPartyWorkOrderSerializer.get_workflow) — has_nte, has_required_quotes,
// has_photo_evidence, has_invoice_and_fsr, variance_status,
// keyfob_outstanding — which is the same block the web stepper's gate
// rendering reads. A client showing an operator WHY an advance is blocked
// reads those flags; it does not count quotes itself, because the count is not
// the rule (an emergency authorization or a signed waiver satisfies it with
// zero quotes on file).
//
// EVERY PRIMARY KEY IN THIS APP IS AN EXPLICIT models.UUIDField, so every id
// here is a `string` rather than an `any`: there is no number for a `%v` to
// mangle into "1e+06" (AGENTS.md's conjunction rule — an INTEGER pk AND a
// fmt.Sprint over an `any`). `location` is the ONE exception on the payload
// and it is typed accordingly: inventory.Location declares no pk, so it takes
// settings.DEFAULT_AUTO_FIELD (BigAutoField) and arrives as a JSON NUMBER.
package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const maintenanceOrdersPath = "/api/maintenance-orders/work-orders/"
const maintenanceOrderQuotesPath = "/api/maintenance-orders/quotes/"
const maintenanceOrderAttachmentsPath = "/api/maintenance-orders/attachments/"

// Third-party work-order work types (backend ThirdPartyWorkOrder.WORK_TYPE_*).
// A vendor order defaults to "standard" when the caller sends nothing.
const (
	ThirdPartyWorkTypeStandard          = "standard"
	ThirdPartyWorkTypeMajorRepair       = "major_repair"
	ThirdPartyWorkTypeBuildout          = "buildout"
	ThirdPartyWorkTypeBuildingEmergency = "building_emergency"
)

// ThirdPartyWorkOrderWorkTypes is the ordered code list a work-type picker
// cycles through, in the backend's WORK_TYPE_CHOICES order (default first).
var ThirdPartyWorkOrderWorkTypes = []string{
	ThirdPartyWorkTypeStandard,
	ThirdPartyWorkTypeMajorRepair,
	ThirdPartyWorkTypeBuildout,
	ThirdPartyWorkTypeBuildingEmergency,
}

// The seven live statuses of ThirdPartyWorkOrder plus the two terminal ones
// (backend ThirdPartyWorkOrder.STATUS_*). A client uses these to decide which
// action to OFFER; the server still decides whether it is allowed.
const (
	MaintenanceOrderRequested       = "requested"
	MaintenanceOrderSourcing        = "sourcing"
	MaintenanceOrderScheduled       = "scheduled"
	MaintenanceOrderInProgress      = "in_progress"
	MaintenanceOrderValidated       = "validated"
	MaintenanceOrderFinancialReview = "financial_review"
	MaintenanceOrderClosed          = "closed"
	MaintenanceOrderCancelled       = "cancelled"
)

// MaintenanceOrderVarianceBlocked is the one variance_status that STOPS a
// closure (transitions.close_work_order refuses on it). "auto_approved" and ""
// both close; "" means the reconciliation could not be scored at all, which
// happens on an emergency order with no NTE and is not an error.
const MaintenanceOrderVarianceBlocked = "blocked"

// Attachment kinds (backend ThirdPartyWorkOrderAttachment.KIND_*). Two of them
// are GATES rather than filing categories: KIND_PHOTO is what ops_sign_off
// requires, and KIND_INVOICE plus KIND_FSR together are what close_work_order
// requires. Uploading under the wrong kind leaves the gate shut.
const (
	MaintenanceOrderAttachmentInvoice   = "invoice"
	MaintenanceOrderAttachmentFSR       = "fsr"
	MaintenanceOrderAttachmentPhoto     = "photo"
	MaintenanceOrderAttachmentQuote     = "quote"
	MaintenanceOrderAttachmentPaperForm = "paper_form"
	MaintenanceOrderAttachmentOther     = "other"
)

// MaintenanceOrderAttachmentKinds is the ordered code list a kind picker
// cycles through, in the backend's KIND_CHOICES order.
var MaintenanceOrderAttachmentKinds = []string{
	MaintenanceOrderAttachmentInvoice,
	MaintenanceOrderAttachmentFSR,
	MaintenanceOrderAttachmentPhoto,
	MaintenanceOrderAttachmentQuote,
	MaintenanceOrderAttachmentPaperForm,
	MaintenanceOrderAttachmentOther,
}

// MaintenanceOrderWorkflow is the serializer's per-step gate block
// (ThirdPartyWorkOrderSerializer.get_workflow). It is READ and never
// re-derived: the same facts computed on this side would disagree with the
// server the moment a rule moves, and two of them cannot be computed here at
// all — has_active_emergency_authorization needs the 24-hour window against
// the server's clock, and the attachment gates need the attachment rows.
type MaintenanceOrderWorkflow struct {
	HasNTE                          bool   `json:"has_nte"`
	HasActiveEmergencyAuthorization bool   `json:"has_active_emergency_authorization"`
	HasRequiredQuotes               bool   `json:"has_required_quotes"`
	QuoteCount                      int    `json:"quote_count"`
	HasPhotoEvidence                bool   `json:"has_photo_evidence"`
	HasInvoiceAndFSR                bool   `json:"has_invoice_and_fsr"`
	VarianceStatus                  string `json:"variance_status"`
	KeyfobOutstanding               bool   `json:"keyfob_outstanding"`
}

// MaintenanceOrderQuote mirrors ThirdPartyWorkOrderQuoteSerializer. `amount` is
// a DecimalField, so it arrives as a JSON string; DecimalString takes it either
// way and Empty() means null rather than zero.
type MaintenanceOrderQuote struct {
	ID         string        `json:"id"`
	WorkOrder  string        `json:"work_order"`
	Vendor     string        `json:"vendor"`
	VendorName string        `json:"vendor_name"`
	Amount     DecimalString `json:"amount"`
	Notes      string        `json:"notes"`
	CreatedAt  time.Time     `json:"created_at"`
}

// MaintenanceOrderAttachment mirrors ThirdPartyWorkOrderAttachmentSerializer.
type MaintenanceOrderAttachment struct {
	ID             string    `json:"id"`
	WorkOrder      string    `json:"work_order"`
	File           string    `json:"file"`
	Kind           string    `json:"kind"`
	KindDisplay    string    `json:"kind_display"`
	Caption        string    `json:"caption"`
	UploadedByName string    `json:"uploaded_by_username"`
	UploadedAt     time.Time `json:"uploaded_at"`
}

// MaintenanceOrderAssetLink mirrors ThirdPartyWorkOrderAssetSerializer — the
// per-asset cost share a multi-asset order is reconciled across.
// `allocated_cost` is null until advance_to_financial_review materializes it.
type MaintenanceOrderAssetLink struct {
	ID            string        `json:"id"`
	Asset         string        `json:"asset"`
	AssetName     string        `json:"asset_name"`
	AssetTag      string        `json:"asset_tag"`
	SharePct      DecimalString `json:"share_pct"`
	AllocatedCost DecimalString `json:"allocated_cost"`
	Notes         string        `json:"notes"`
}

// MaintenanceOrder mirrors ThirdPartyWorkOrderSerializer — a work order issued
// to a third-party vendor.
//
// Money is DecimalString throughout because the serializer emits DecimalField
// as a JSON string, and because Empty() ("null/unset") and "0.00" are
// different facts here: a null nte_amount is an emergency "blank cheque", and
// a 0.00 one is a ceiling of nothing.
//
// TotalDowntime is a STRING: DRF renders a DurationField with
// django.utils.duration_string, which is "[D ]HH:MM:SS[.ffffff]" and not a
// number. It is displayed verbatim rather than parsed — there is nothing this
// client does with it arithmetically, and re-deriving it from
// downtime_end - downtime_start would disagree with the persisted figure the
// server stamped at sign-off.
type MaintenanceOrder struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id"`
	Title   string `json:"title"`

	Asset     *string `json:"asset"`
	AssetName string  `json:"asset_name"`
	// Location is an INT id, not a UUID: inventory.Location declares no
	// primary key, so it is settings.DEFAULT_AUTO_FIELD (BigAutoField) and
	// arrives as a JSON number. Everything else on this payload is a UUID.
	Location     *int   `json:"location"`
	LocationName string `json:"location_name"`
	Vendor       string `json:"vendor"`
	VendorName   string `json:"vendor_name"`

	WorkType        string `json:"work_type"`
	WorkTypeDisplay string `json:"work_type_display"`
	IsEmergency     bool   `json:"is_emergency"`
	Status          string `json:"status"`
	StatusDisplay   string `json:"status_display"`

	NTEAmount          DecimalString `json:"nte_amount"`
	ParCostBuffer      DecimalString `json:"par_cost_buffer"`
	ActualInvoiceTotal DecimalString `json:"actual_invoice_total"`
	DispatchFee        DecimalString `json:"dispatch_fee"`
	VarianceStatus     string        `json:"variance_status"`

	DowntimeStart *time.Time `json:"downtime_start"`
	DowntimeEnd   *time.Time `json:"downtime_end"`
	TotalDowntime string     `json:"total_downtime"`

	KeyfobID         string     `json:"keyfob_id"`
	KeyfobReturnedAt *time.Time `json:"keyfob_returned_at"`

	WarrantyRecovery bool `json:"warranty_recovery"`

	NTESetAt              *time.Time `json:"nte_set_at"`
	EmergencyAuthorizedAt *time.Time `json:"emergency_authorized_at"`
	QuoteWaiverSignedAt   *time.Time `json:"quote_waiver_signed_at"`
	QuoteWaiverReason     string     `json:"quote_waiver_reason"`

	OpenedAt time.Time  `json:"opened_at"`
	ClosedAt *time.Time `json:"closed_at"`

	Notes         string `json:"notes"`
	InternalNotes string `json:"internal_notes"`

	AssetLinks  []MaintenanceOrderAssetLink  `json:"asset_links"`
	Attachments []MaintenanceOrderAttachment `json:"attachments"`
	Quotes      []MaintenanceOrderQuote      `json:"quotes"`

	Workflow MaintenanceOrderWorkflow `json:"workflow"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListMaintenanceOrders lists vendor work orders. The viewset's
// filterset_fields are status / work_type / is_emergency / vendor / asset, so
// a status view is a SERVER-side ?status= rather than a local narrowing of
// page one.
func (c *Client) ListMaintenanceOrders(ctx context.Context, q url.Values) (*Page[MaintenanceOrder], error) {
	return GetPage[MaintenanceOrder](ctx, c, maintenanceOrdersPath, q)
}

// GetMaintenanceOrder fetches one vendor work order whole — the workflow gate
// block, the quotes, the attachments and the per-asset cost split all ride on
// the detail payload, so the stepper needs exactly one round trip.
func (c *Client) GetMaintenanceOrder(ctx context.Context, id string) (*MaintenanceOrder, error) {
	var out MaintenanceOrder
	if err := c.Get(ctx, maintenanceOrdersPath+id+"/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// maintenanceOrderAction POSTs one of the state-machine @actions and decodes
// the refreshed order the view answers with.
//
// The body is an explicit object even where the action reads nothing from it:
// a bodyless POST goes out with no Content-Type at all, which is the same
// reason PromoteAssetProblemStandard sends one.
func (c *Client) maintenanceOrderAction(ctx context.Context, id, action string, body any) (*MaintenanceOrder, error) {
	if body == nil {
		body = map[string]any{}
	}
	var out MaintenanceOrder
	if err := c.Post(ctx, maintenanceOrdersPath+id+"/"+action+"/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetMaintenanceOrderNTE sets the Not-To-Exceed ceiling (POST .../set-nte/).
//
// amount is sent as the operator TYPED it, as a string: the server parses it
// with Decimal(str(value)) and a float round-trip through this client would be
// this project's own money rule broken at the wire (AGENTS.md: do money
// arithmetic in big.Rat, never float64). A blank or unparseable amount is the
// server's 400 to give, not ours to guess at.
//
// Allowed only while the order is `requested` or `sourcing`
// (transitions.set_nte); re-setting it within that window is legitimate and
// overwrites the previous ceiling.
func (c *Client) SetMaintenanceOrderNTE(ctx context.Context, id, amount string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "set-nte",
		map[string]string{"nte_amount": strings.TrimSpace(amount)})
}

// MaintenanceEmergencyAuthorization mirrors EmergencyAuthorizationSerializer.
// authorize-emergency is the ONE action that does NOT answer with the work
// order: it returns the 24-hour authorization row it just created, HTTP 201.
type MaintenanceEmergencyAuthorization struct {
	ID           string     `json:"id"`
	WorkOrder    string     `json:"work_order"`
	AuthorizedAt time.Time  `json:"authorized_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	Reason       string     `json:"reason"`
	RevokedAt    *time.Time `json:"revoked_at"`
	IsValid      bool       `json:"is_currently_valid"`
}

// AuthorizeMaintenanceOrderEmergency opens a 24-hour emergency bypass of the
// NTE and 3-quote gates (POST .../authorize-emergency/, HTTP 201).
//
// IT IS NOT REVERSIBLE FROM THIS API. The window expires on its own, and
// `revoked_at` is writable only through the Django admin — there is no revoke
// endpoint. It also sets is_emergency on the order permanently, so a later
// advance still counts as emergency work after the window lapses. A caller
// must confirm before sending.
//
// Logistics or staff only (transitions.authorize_emergency has its own,
// narrower permission check than the SIG-admin gate the other actions take).
func (c *Client) AuthorizeMaintenanceOrderEmergency(ctx context.Context, id, reason string) (*MaintenanceEmergencyAuthorization, error) {
	var out MaintenanceEmergencyAuthorization
	if err := c.Post(ctx, maintenanceOrdersPath+id+"/authorize-emergency/",
		map[string]string{"reason": strings.TrimSpace(reason)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdvanceMaintenanceOrderToSourcing is step 1 → 2 (POST
// .../advance-to-sourcing/). Refused without an NTE or an active emergency
// authorization, and refused from any status but `requested`.
func (c *Client) AdvanceMaintenanceOrderToSourcing(ctx context.Context, id string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "advance-to-sourcing", nil)
}

// WaiveMaintenanceOrderQuoteRequirement signs off on bypassing the three-quote
// sourcing rule (POST .../waive-quote-requirement/).
//
// The reason is REQUIRED — transitions.waive_quote_requirement 400s on a blank
// one — and it is persisted with the signer and a timestamp as the audit
// record of why the makerspace stopped shopping around. There is no unsign
// endpoint.
func (c *Client) WaiveMaintenanceOrderQuoteRequirement(ctx context.Context, id, reason string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "waive-quote-requirement",
		map[string]string{"reason": strings.TrimSpace(reason)})
}

// AdvanceMaintenanceOrderToScheduled is step 2 → 3 (POST
// .../advance-to-scheduled/). Refused unless has_required_quotes: three quotes
// on file, OR a signed waiver, OR an active emergency authorization.
func (c *Client) AdvanceMaintenanceOrderToScheduled(ctx context.Context, id string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "advance-to-scheduled", nil)
}

// MarkMaintenanceOrderVendorArrived is step 3 → 4 (POST .../vendor-arrived/):
// the vendor is on site, and this STARTS THE DOWNTIME CLOCK.
//
// keyfobID is optional and is the site-access fob handed over. Recording one
// arms a CLOSURE GATE — close_work_order refuses while keyfob_id is set and
// keyfob_returned_at is null — so a caller that offers this field must also
// offer RecordMaintenanceOrderKeyfobReturn, or it builds a dead end of its
// own. A blank keyfobID leaves whatever was already on the order alone: the
// transition only assigns `if keyfob_id`.
func (c *Client) MarkMaintenanceOrderVendorArrived(ctx context.Context, id, keyfobID string) (*MaintenanceOrder, error) {
	body := map[string]string{}
	if k := strings.TrimSpace(keyfobID); k != "" {
		body["keyfob_id"] = k
	}
	return c.maintenanceOrderAction(ctx, id, "vendor-arrived", body)
}

// SignOffMaintenanceOrder is step 4 → 5 (POST .../sign-off/): Ops validates the
// work, which STOPS THE DOWNTIME CLOCK and persists total_downtime. Refused
// without at least one `photo` attachment.
func (c *Client) SignOffMaintenanceOrder(ctx context.Context, id string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "sign-off", nil)
}

// RecordMaintenanceOrderKeyfobReturn records that the vendor handed the fob
// back (POST .../record-keyfob-return/), clearing the closure gate
// MarkMaintenanceOrderVendorArrived can arm.
//
// Idempotent server-side: a second call keeps the original timestamp and only
// writes a fresh audit row. Refused when no fob is checked out at all.
func (c *Client) RecordMaintenanceOrderKeyfobReturn(ctx context.Context, id string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "record-keyfob-return", nil)
}

// AdvanceMaintenanceOrderToFinancialReview is step 5 → 6 (POST
// .../advance-to-financial-review/): capture the invoice and score the
// variance against the NTE.
//
// Both figures go as TYPED STRINGS for the reason SetMaintenanceOrderNTE's do.
// dispatchFee is optional; sending a blank one omits the key, which leaves any
// fee already on the order untouched (the transition only assigns
// `if dispatch_fee is not None`). actualInvoiceTotal is required — the
// transition 400s when the order still has none.
//
// This is where the money lands: the server evaluates the variance, writes
// variance_status, splits the dispatch fee across the asset links, and — on a
// blocked variance — alerts Finance. It cannot be undone from this API.
func (c *Client) AdvanceMaintenanceOrderToFinancialReview(ctx context.Context, id, actualInvoiceTotal, dispatchFee string) (*MaintenanceOrder, error) {
	body := map[string]string{
		"actual_invoice_total": strings.TrimSpace(actualInvoiceTotal),
	}
	if f := strings.TrimSpace(dispatchFee); f != "" {
		body["dispatch_fee"] = f
	}
	return c.maintenanceOrderAction(ctx, id, "advance-to-financial-review", body)
}

// OverrideMaintenanceOrderVariance clears a `blocked` variance so the order can
// close (POST .../override-variance/).
//
// STAFF ONLY — narrower than every other action here — and refused unless the
// variance really is blocked. The reason is required and is the audit record of
// who agreed to absorb the overage. Without this a blocked variance is a dead
// end: close_work_order refuses on it and nothing else clears it.
func (c *Client) OverrideMaintenanceOrderVariance(ctx context.Context, id, reason string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "override-variance",
		map[string]string{"reason": strings.TrimSpace(reason)})
}

// CloseMaintenanceOrder is step 6 → 7 (POST .../close/).
//
// Irreversible: there is no reopen endpoint. It also resolves every asset
// problem that was promoted into this order and, on a warranty-recovery order,
// opens a Logistics recovery task. Refused on a blocked variance, without both
// an `invoice` and an `fsr` attachment, and while a keyfob is outstanding.
func (c *Client) CloseMaintenanceOrder(ctx context.Context, id string) (*MaintenanceOrder, error) {
	return c.maintenanceOrderAction(ctx, id, "close", nil)
}

// AddMaintenanceOrderQuote records a vendor quote against the order (POST
// /api/maintenance-orders/quotes/ — a FLAT collection, so the work order rides
// in the body rather than the path).
//
// amount goes as the operator typed it, for the reason the other money fields
// do. vendorID is a Vendor UUID; the web quote form offers the order's OWN
// vendor and nothing else, which is what this mirrors.
func (c *Client) AddMaintenanceOrderQuote(ctx context.Context, woID, vendorID, amount, notes string) (*MaintenanceOrderQuote, error) {
	body := map[string]string{
		"work_order": woID,
		"vendor":     strings.TrimSpace(vendorID),
		"amount":     strings.TrimSpace(amount),
	}
	if n := strings.TrimSpace(notes); n != "" {
		body["notes"] = n
	}
	var out MaintenanceOrderQuote
	if err := c.Post(ctx, maintenanceOrderQuotesPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadMaintenanceOrderAttachment attaches a file to a vendor work order
// (multipart POST to the FLAT /attachments/ collection, so the order id rides
// as the `work_order` form field).
//
// `kind` is not filing metadata here, it is a GATE: `photo` is what sign-off
// requires and `invoice` + `fsr` together are what closure requires, so an
// operator who files an invoice as `other` still cannot close. Caller passes
// one of MaintenanceOrderAttachmentKinds; a blank one lets the server apply
// its `other` default.
func (c *Client) UploadMaintenanceOrderAttachment(
	ctx context.Context, woID, fileName string, file io.Reader, kind, caption string,
) (*MaintenanceOrderAttachment, error) {
	fields := map[string][]string{"work_order": {woID}}
	if k := strings.TrimSpace(kind); k != "" {
		fields["kind"] = []string{k}
	}
	if c := strings.TrimSpace(caption); c != "" {
		fields["caption"] = []string{c}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("oms: read attachment %s: %w", fileName, err)
	}
	var out MaintenanceOrderAttachment
	if err := c.PostMultipart(ctx, maintenanceOrderAttachmentsPath, fields,
		[]MultipartFile{{Field: "file", Filename: fileName, Data: data}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AsMaintenanceOrderRefusal recovers the human sentence out of a
// maintenance_orders refusal.
//
// THE REFUSAL BODY IS NOT THE STANDARD ENVELOPE. `views._err` writes
// `{"detail": <prose>}` by hand and returns it as a plain Response, so it never
// reaches OMS's DRF exception handler and parseError — which only understands
// `{"error": {"code": ...}}` — puts the ENTIRE RAW BODY into APIError.Message.
// Without this the operator at the bench reads
// `oms: http 400: {"detail": "Work order is in 'requested' state; expected 'sourcing'."}`
// on a row that cannot fold.
//
// BOTH SHAPES ARE RECOVERED, because _err produces both: a ValidationError
// carrying one message answers `{"detail": "<string>"}` and one carrying
// several answers `{"detail": ["<string>", ...]}` (`detail[0] if len(detail)
// == 1 else detail`). A recogniser that handled only the string form would
// fall back to the raw blob on exactly the refusals that have the most to say.
//
// It is deliberately NARROW — an object whose `detail` is a non-blank string,
// or a list of them — so a gateway page, a DRF field-validation envelope and
// anything else keep the shape they arrived in and are reported as what they
// are. DRF's OWN 403/401 bodies are `{"detail": "..."}` too and are recovered
// here on purpose: "You do not have permission to perform this action." is the
// sentence an operator needs, not the JSON around it.
func AsMaintenanceOrderRefusal(err error) (string, bool) {
	var api *APIError
	if !errors.As(err, &api) {
		return "", false
	}
	body := strings.TrimSpace(api.Message)
	if !strings.HasPrefix(body, "{") {
		return "", false
	}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Detail) == 0 {
		return "", false
	}
	var one string
	if json.Unmarshal(envelope.Detail, &one) == nil {
		if strings.TrimSpace(one) == "" {
			return "", false
		}
		return one, true
	}
	var many []string
	if json.Unmarshal(envelope.Detail, &many) != nil {
		return "", false
	}
	kept := make([]string, 0, len(many))
	for _, s := range many {
		if strings.TrimSpace(s) != "" {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return "", false
	}
	return strings.Join(kept, " "), true
}
