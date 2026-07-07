package omsapi

import (
	"context"
	"net/url"
	"time"
)

// Donation mirrors backend DonationSerializer / DonationListSerializer
// (backend/donations/). ListDonations uses the lighter list serializer; this
// struct is the superset so it decodes either shape. Per
// [[scantty-api-field-drift]] the earlier struct crashed the screen: the PK is
// a UUID string (`donations/models.py` `id = UUIDField`) not an int,
// `date_received` is a date-only DateField, and the money fields are DRF
// DecimalFields that arrive as JSON strings.
type Donation struct {
	ID                     string        `json:"id"`
	DonationNumber         string        `json:"donation_number,omitempty"`
	DonorName              string        `json:"donor_name,omitempty"`
	DonorEmail             string        `json:"donor_email,omitempty"`
	DonorPhone             string        `json:"donor_phone,omitempty"`
	DonorAddress           string        `json:"donor_address,omitempty"`
	DateReceived           DateOnly      `json:"date_received"`
	Status                 string        `json:"status,omitempty"`
	ReceivedNotes          string        `json:"received_notes,omitempty"`
	ReviewNotes            string        `json:"review_notes,omitempty"`
	EstimatedNumberOfItems int           `json:"estimated_number_of_items,omitempty"`
	EstimatedValue         DecimalString `json:"estimated_value,omitempty"`
	AssociatedCosts        DecimalString `json:"associated_costs,omitempty"`
	NetValue               DecimalString `json:"net_value,omitempty"`
	TaxReceiptIssued       bool          `json:"tax_receipt_issued,omitempty"`
	TaxReceiptNumber       string        `json:"tax_receipt_number,omitempty"`
	TotalItems             int           `json:"total_items,omitempty"`
	TotalQuantity          int           `json:"total_quantity,omitempty"`
	CreatedAt              time.Time     `json:"created_at,omitempty"`
	UpdatedAt              time.Time     `json:"updated_at,omitempty"`
}

func (c *Client) ListDonations(ctx context.Context, q url.Values) (*Page[Donation], error) {
	return GetPage[Donation](ctx, c, "/api/donations/donations/", q)
}

func (c *Client) CreateDonation(ctx context.Context, d Donation) (*Donation, error) {
	var out Donation
	if err := c.Post(ctx, "/api/donations/donations/", d, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) LookupDonationCode(ctx context.Context, code string) (*Donation, error) {
	var out Donation
	q := url.Values{"code": []string{code}}
	if err := c.Get(ctx, "/api/donations/lookup-code/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TaxReceipt mirrors backend TaxReceiptSerializer (backend/donations/). Both id
// and serial_number are UUIDs, `donation` is the FK to a UUID-PK Donation (a
// string, not an int), and issued_date is a date-only DateField — the earlier
// int/int/datetime typing here was the same drift class as Donation.
type TaxReceipt struct {
	ID               string    `json:"id"`
	SerialNumber     string    `json:"serial_number,omitempty"`
	Donation         string    `json:"donation,omitempty"`
	DonationNumber   string    `json:"donation_number,omitempty"`
	DonorName        string    `json:"donor_name,omitempty"`
	DonorEmail       string    `json:"donor_email,omitempty"`
	IssuedDate       DateOnly  `json:"issued_date"`
	IssuedBy         int       `json:"issued_by,omitempty"`
	IssuedByUsername string    `json:"issued_by_username,omitempty"`
	PDFFile          string    `json:"pdf_file,omitempty"`
	IsCopy           bool      `json:"is_copy,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

func (c *Client) ListTaxReceipts(ctx context.Context, q url.Values) (*Page[TaxReceipt], error) {
	return GetPage[TaxReceipt](ctx, c, "/api/donations/tax-receipts/", q)
}

// LookupTaxReceipt mirrors the web donationsAPI.lookupTaxReceipt — the public
// (AllowAny) lookup that the Tax Receipt Lookup page is built on. It GETs
// `/api/donations/tax-receipts/lookup/?serial_number=<serial>`, which returns a
// single TaxReceipt object (not a page) or a 404 when the serial doesn't match.
// A 404 surfaces as an *APIError whose IsNotFound() reports true, so callers can
// distinguish "no such receipt" from a transport/auth failure.
func (c *Client) LookupTaxReceipt(ctx context.Context, serialNumber string) (*TaxReceipt, error) {
	var out TaxReceipt
	q := url.Values{"serial_number": []string{serialNumber}}
	if err := c.Get(ctx, "/api/donations/tax-receipts/lookup/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
