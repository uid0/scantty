package forgekeyapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListCertificateAuthorities_Decodes feeds the CertificateAuthoritySerializer
// shape (paginated envelope), including the active-CA cert counts and a retired
// row whose counts arrive as JSON null — both must decode without error.
func TestListCertificateAuthorities_Decodes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"results":[
			{"id":"11111111-1111-1111-1111-111111111111","name":"forgekey-root",
			 "common_name":"ForgeKey Internal Root CA",
			 "fingerprint_sha256":"aa11bb22","not_before":"2026-01-01T00:00:00Z",
			 "not_after":"2036-01-01T00:00:00Z","is_active":true,
			 "created_at":"2026-01-01T00:00:00Z","active_cert_count":7,"revoked_cert_count":2},
			{"id":"22222222-2222-2222-2222-222222222222","name":"forgekey-root",
			 "common_name":null,"fingerprint_sha256":null,
			 "not_before":"2020-01-01T00:00:00Z","not_after":"2030-01-01T00:00:00Z",
			 "is_active":false,"created_at":"2020-01-01T00:00:00Z",
			 "active_cert_count":null,"revoked_cert_count":null}
		]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cas, err := c.ListCertificateAuthorities(context.Background())
	if err != nil {
		t.Fatalf("ListCertificateAuthorities: %v", err)
	}
	if gotPath != "/api/forgekey/certificate-authorities/" {
		t.Errorf("path = %q, want /api/forgekey/certificate-authorities/", gotPath)
	}
	if len(cas) != 2 {
		t.Fatalf("cas = %d, want 2", len(cas))
	}
	active := cas[0]
	if !active.IsActive || active.CommonName != "ForgeKey Internal Root CA" || active.FingerprintSHA256 != "aa11bb22" {
		t.Errorf("active CA decoded wrong: %+v", active)
	}
	if active.ActiveCertCount == nil || *active.ActiveCertCount != 7 {
		t.Errorf("active_cert_count = %v, want 7", active.ActiveCertCount)
	}
	if active.RevokedCertCount == nil || *active.RevokedCertCount != 2 {
		t.Errorf("revoked_cert_count = %v, want 2", active.RevokedCertCount)
	}
	retired := cas[1]
	if retired.IsActive {
		t.Errorf("cas[1].IsActive = true, want false")
	}
	if retired.CommonName != "" || retired.FingerprintSHA256 != "" {
		t.Errorf("null common_name/fingerprint should decode to empty, got %+v", retired)
	}
	if retired.ActiveCertCount != nil || retired.RevokedCertCount != nil {
		t.Errorf("null counts should decode to nil, got %v / %v", retired.ActiveCertCount, retired.RevokedCertCount)
	}
}

// TestListDeviceCertificates_Decodes feeds the DeviceCertificateSerializer shape
// (bare array), covering a valid cert (revoked_at null) and a revoked one, plus
// the computed status label. No key material is present in the payload — the
// model stores none.
func TestListDeviceCertificates_Decodes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"aaaaaaaa-0000-0000-0000-000000000001","device":"dev-uuid-1",
			 "device_chip_id":"ESP-CHIP-001","serial":"0A1B2C","subject":"CN=ESP-CHIP-001",
			 "fingerprint_sha256":"deadbeef","not_before":"2026-01-01T00:00:00Z",
			 "not_after":"2027-01-01T00:00:00Z","revoked_at":null,"issued_by":"forgekey-root",
			 "created_at":"2026-01-01T00:00:00Z","status":"active"},
			{"id":"aaaaaaaa-0000-0000-0000-000000000002","device":null,
			 "device_chip_id":null,"serial":"0A1B2D","subject":"CN=old",
			 "fingerprint_sha256":"cafebabe","not_before":"2025-01-01T00:00:00Z",
			 "not_after":"2026-01-01T00:00:00Z","revoked_at":"2025-06-01T00:00:00Z",
			 "issued_by":"forgekey-root","created_at":"2025-01-01T00:00:00Z","status":"revoked"}
		]`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	certs, err := c.ListDeviceCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListDeviceCertificates: %v", err)
	}
	if gotPath != "/api/forgekey/device-certificates/" {
		t.Errorf("path = %q, want /api/forgekey/device-certificates/", gotPath)
	}
	if len(certs) != 2 {
		t.Fatalf("certs = %d, want 2", len(certs))
	}
	valid := certs[0]
	if valid.DeviceChipID != "ESP-CHIP-001" || valid.Serial != "0A1B2C" || valid.Status != "active" {
		t.Errorf("valid cert decoded wrong: %+v", valid)
	}
	if valid.RevokedAt != nil {
		t.Errorf("valid cert RevokedAt = %v, want nil", valid.RevokedAt)
	}
	revoked := certs[1]
	if revoked.Status != "revoked" || revoked.RevokedAt == nil {
		t.Errorf("revoked cert decoded wrong: %+v", revoked)
	}
	if revoked.DeviceChipID != "" {
		t.Errorf("null device_chip_id should decode to empty, got %q", revoked.DeviceChipID)
	}
}

// TestRotateCA_PostsTrailingSlashPath is the trailing-slash guard for the one
// write action on this surface: the rotate @action MUST be POSTed to the
// slashed path or DRF APPEND_SLASH redirects it to a GET and the rotation
// silently no-ops. Also verifies the returned CA decodes.
func TestRotateCA_PostsTrailingSlashPath(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"33333333-3333-3333-3333-333333333333","name":"forgekey-root",
			"common_name":"ForgeKey Internal Root CA","fingerprint_sha256":"newfp",
			"not_before":"2026-07-06T00:00:00Z","not_after":"2036-07-06T00:00:00Z",
			"is_active":true,"created_at":"2026-07-06T00:00:00Z",
			"active_cert_count":0,"revoked_cert_count":0}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ca, err := c.RotateCA(context.Background())
	if err != nil {
		t.Fatalf("RotateCA: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/forgekey/certificate-authorities/rotate/" {
		t.Errorf("path = %q, want /api/forgekey/certificate-authorities/rotate/ (trailing slash)", gotPath)
	}
	if ca == nil || ca.FingerprintSHA256 != "newfp" || !ca.IsActive {
		t.Errorf("rotated CA decoded wrong: %+v", ca)
	}
}
