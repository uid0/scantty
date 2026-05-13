package omsapi

import (
	"context"
	"net/url"
	"time"
)

type Donation struct {
	ID          int       `json:"id"`
	DonorName   string    `json:"donor_name,omitempty"`
	DonorEmail  string    `json:"donor_email,omitempty"`
	Description string    `json:"description,omitempty"`
	Value       float64   `json:"value,omitempty"`
	ReceivedAt  time.Time `json:"received_at,omitempty"`
	ReceiptID   string    `json:"receipt_id,omitempty"`
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

type TaxReceipt struct {
	ID         int       `json:"id"`
	Donation   int       `json:"donation"`
	Number     string    `json:"number,omitempty"`
	IssuedAt   time.Time `json:"issued_at,omitempty"`
	URL        string    `json:"url,omitempty"`
}

func (c *Client) ListTaxReceipts(ctx context.Context, q url.Values) (*Page[TaxReceipt], error) {
	return GetPage[TaxReceipt](ctx, c, "/api/donations/tax-receipts/", q)
}
