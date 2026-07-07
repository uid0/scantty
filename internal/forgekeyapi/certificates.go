package forgekeyapi

import (
	"context"
	"time"
)

// CertificateAuthority mirrors CertificateAuthoritySerializer (the read-only CA
// view backing the web ForgeKeyCertificatesPage). It carries ONLY the public
// certificate metadata the serializer emits — the stored cert PEM and the
// encrypted private key are NOT serializer fields, so no CA key material ever
// crosses this wire. common_name / fingerprint are parsed from the cert by the
// backend and arrive as JSON null when the PEM can't be loaded (decode to "").
// active_cert_count / revoked_cert_count are null for a non-active CA row.
type CertificateAuthority struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	CommonName        string    `json:"common_name"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	IsActive          bool      `json:"is_active"`
	CreatedAt         time.Time `json:"created_at"`
	ActiveCertCount   *int      `json:"active_cert_count"`
	RevokedCertCount  *int      `json:"revoked_cert_count"`
}

// DeviceCertificate mirrors DeviceCertificateSerializer (the read-only issued
// mTLS-cert view). The model stores no private-key bytes at all — only the
// public cert metadata (serial / subject DN / fingerprint / validity window /
// revocation timestamp) — so, like the web page, nothing secret is exposed.
// status is the serializer's computed lifecycle label: active / expired /
// revoked. revoked_at is null unless the cert was revoked (in the enroll flow
// or Django admin — there is no API revoke action).
type DeviceCertificate struct {
	ID                string     `json:"id"`
	Device            string     `json:"device"`
	DeviceChipID      string     `json:"device_chip_id"`
	Serial            string     `json:"serial"`
	Subject           string     `json:"subject"`
	FingerprintSHA256 string     `json:"fingerprint_sha256"`
	NotBefore         time.Time  `json:"not_before"`
	NotAfter          time.Time  `json:"not_after"`
	RevokedAt         *time.Time `json:"revoked_at"`
	IssuedBy          string     `json:"issued_by"`
	CreatedAt         time.Time  `json:"created_at"`
	Status            string     `json:"status"`
}

// ListCertificateAuthorities returns the internal CA(s) — active first in the
// web's usage, but the caller picks the active one by is_active. Staff-gated
// (IsAdminUser) server-side; a non-staff caller gets a 403.
func (c *Client) ListCertificateAuthorities(ctx context.Context) ([]CertificateAuthority, error) {
	var out MaybeList[CertificateAuthority]
	if err := c.Get(ctx, "/api/forgekey/certificate-authorities/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ListDeviceCertificates returns every issued device (mTLS) certificate +
// its lifecycle status. Staff-gated (IsAdminUser) server-side.
func (c *Client) ListDeviceCertificates(ctx context.Context) ([]DeviceCertificate, error) {
	var out MaybeList[DeviceCertificate]
	if err := c.Get(ctx, "/api/forgekey/device-certificates/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// RotateCA mints a fresh self-signed root CA and retires the current one,
// returning the new active CA. It mirrors the web's parameter-less rotateCA():
// no name/common_name/validity_years are sent, so the backend applies its
// defaults (forgekey-root / "ForgeKey Internal Root CA" / 10 years). This is a
// DefaultRouter @action, so the path MUST carry the trailing slash — without it
// DRF's APPEND_SLASH redirects the POST into a GET and the rotation silently
// no-ops (the sc-amjg bug class). Staff-gated (IsAdminUser) server-side.
func (c *Client) RotateCA(ctx context.Context) (*CertificateAuthority, error) {
	var out CertificateAuthority
	// Send an explicit empty JSON object, exactly like the web api.post(url, {}).
	if err := c.Post(ctx, "/api/forgekey/certificate-authorities/rotate/", struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
