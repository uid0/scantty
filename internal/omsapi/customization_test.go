package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestGetSiteSettings decodes the read record, including the
// pm_auto_bundle_due_within_days integer and the nullable *logo_url.
func TestGetSiteSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/customization/settings/" {
			t.Fatalf("method/path = %q %q", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"site_name":"Acme Makerspace",
			"site_tagline":"build things",
			"logo_url":"http://x/logo.png",
			"favicon_url":null,
			"primary_color":"#112233",
			"secondary_color":"#445566",
			"footer_text":"© Acme",
			"contact_email":"hi@acme.test",
			"show_logo_on_dashboard":true,
			"pm_auto_bundle_due_within_days":14
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	st, err := c.GetSiteSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSiteSettings: %v", err)
	}
	if st.SiteName != "Acme Makerspace" {
		t.Errorf("site_name = %q", st.SiteName)
	}
	if st.PmAutoBundleDueWithinDays != 14 {
		t.Errorf("pm_auto_bundle_due_within_days = %d, want 14", st.PmAutoBundleDueWithinDays)
	}
	if st.LogoURL == nil || *st.LogoURL != "http://x/logo.png" {
		t.Errorf("logo_url = %v", st.LogoURL)
	}
	if st.FaviconURL != nil {
		t.Errorf("favicon_url = %v, want nil", st.FaviconURL)
	}
	if !st.ShowLogoOnDashboard {
		t.Errorf("show_logo_on_dashboard = false, want true")
	}
}

// TestUpdateSiteSettings_JSON asserts the no-file path is a JSON PATCH that
// carries the FULL editable set: a false bool is present, the int rides as a
// JSON number, and a cleared optional is sent as "" (not omitted) so it clears.
func TestUpdateSiteSettings_JSON(t *testing.T) {
	var captured struct {
		method      string
		path        string
		contentType string
		body        map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.contentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"site_name":"Acme"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	st, err := c.UpdateSiteSettings(context.Background(), SiteSettingsWrite{
		SiteName:                  "Acme",
		PrimaryColor:              "#007cba",
		SecondaryColor:            "#417690",
		ContactEmail:              "", // cleared
		ShowLogoOnDashboard:       false,
		PmAutoBundleDueWithinDays: 7,
	})
	if err != nil {
		t.Fatalf("UpdateSiteSettings: %v", err)
	}
	if st == nil || st.SiteName != "Acme" {
		t.Fatalf("returned settings = %+v", st)
	}
	if captured.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", captured.method)
	}
	if captured.path != "/api/customization/settings/" {
		t.Errorf("path = %q", captured.path)
	}
	if !strings.HasPrefix(captured.contentType, "application/json") {
		t.Errorf("content-type = %q, want json", captured.contentType)
	}
	// The int is a JSON number.
	if v, ok := captured.body["pm_auto_bundle_due_within_days"].(float64); !ok || v != 7 {
		t.Errorf("pm_auto_bundle_due_within_days = %v", captured.body["pm_auto_bundle_due_within_days"])
	}
	// A false bool must be present (no omitempty).
	if v, ok := captured.body["show_logo_on_dashboard"]; !ok || v != false {
		t.Errorf("show_logo_on_dashboard = %v (present=%v), want false present", v, ok)
	}
	// A cleared optional is sent as "" so the backend clears it.
	if v, ok := captured.body["contact_email"]; !ok || v != "" {
		t.Errorf("contact_email = %v (present=%v), want \"\" present", v, ok)
	}
	// Files never appear in the JSON body.
	for _, k := range []string{"logo", "favicon"} {
		if _, ok := captured.body[k]; ok {
			t.Errorf("%s must not appear in JSON body", k)
		}
	}
}

// TestUpdateSiteSettings_LogoUploadMultipart asserts a logo path switches the
// call to multipart, uploads the file as the "logo" part, and still sends every
// scalar field (bools/int coerced to strings).
func TestUpdateSiteSettings_LogoUploadMultipart(t *testing.T) {
	logo := t.TempDir() + "/logo.png"
	if err := os.WriteFile(logo, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatalf("write logo fixture: %v", err)
	}

	var captured struct {
		method      string
		contentType string
		fields      map[string][]string
		fileName    string
		fileBody    string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.contentType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		captured.fields = r.MultipartForm.Value
		file, header, err := r.FormFile("logo")
		if err != nil {
			t.Fatalf("logo file: %v", err)
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		captured.fileName = header.Filename
		captured.fileBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"site_name":"Acme"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.UpdateSiteSettings(context.Background(), SiteSettingsWrite{
		SiteName:                  "Acme",
		PrimaryColor:              "#007cba",
		SecondaryColor:            "#417690",
		ShowLogoOnDashboard:       true,
		PmAutoBundleDueWithinDays: 3,
		LogoPath:                  logo,
	})
	if err != nil {
		t.Fatalf("UpdateSiteSettings multipart: %v", err)
	}
	if captured.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", captured.method)
	}
	if !strings.HasPrefix(captured.contentType, "multipart/form-data;") {
		t.Errorf("content-type = %q, want multipart", captured.contentType)
	}
	if captured.fileName != "logo.png" || captured.fileBody != "PNGDATA" {
		t.Errorf("logo file = %q %q", captured.fileName, captured.fileBody)
	}
	if got := captured.fields["site_name"]; len(got) != 1 || got[0] != "Acme" {
		t.Errorf("site_name field = %v", got)
	}
	if got := captured.fields["show_logo_on_dashboard"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("show_logo_on_dashboard field = %v, want [true]", got)
	}
	if got := captured.fields["pm_auto_bundle_due_within_days"]; len(got) != 1 || got[0] != "3" {
		t.Errorf("pm_auto_bundle_due_within_days field = %v, want [3]", got)
	}
	// No removal marker when uploading.
	if _, ok := captured.fields["logo"]; ok {
		t.Errorf("logo text field must be absent when uploading a file")
	}
}

// TestUpdateSiteSettings_RemoveLogo asserts a remove (no upload) goes multipart
// with an empty "logo" value and no file part — the shape the backend deletes on.
func TestUpdateSiteSettings_RemoveLogo(t *testing.T) {
	var captured struct {
		fields  map[string][]string
		hasFile bool
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		captured.fields = r.MultipartForm.Value
		if r.MultipartForm.File != nil && len(r.MultipartForm.File["logo"]) > 0 {
			captured.hasFile = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"site_name":"Acme"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.UpdateSiteSettings(context.Background(), SiteSettingsWrite{
		SiteName:       "Acme",
		PrimaryColor:   "#007cba",
		SecondaryColor: "#417690",
		RemoveLogo:     true,
	})
	if err != nil {
		t.Fatalf("UpdateSiteSettings remove: %v", err)
	}
	got, ok := captured.fields["logo"]
	if !ok || len(got) != 1 || got[0] != "" {
		t.Errorf("logo field = %v (present=%v), want [\"\"]", got, ok)
	}
	if captured.hasFile {
		t.Errorf("remove must not attach a logo file")
	}
}

// TestSiteSettingsWrite_needsMultipart covers the JSON-vs-multipart decision.
func TestSiteSettingsWrite_needsMultipart(t *testing.T) {
	cases := []struct {
		name string
		w    SiteSettingsWrite
		want bool
	}{
		{"scalars only", SiteSettingsWrite{SiteName: "A"}, false},
		{"logo upload", SiteSettingsWrite{LogoPath: "/x/l.png"}, true},
		{"favicon upload", SiteSettingsWrite{FaviconPath: "/x/f.png"}, true},
		{"remove logo", SiteSettingsWrite{RemoveLogo: true}, true},
		{"blank logo path stays json", SiteSettingsWrite{LogoPath: "   "}, false},
	}
	for _, tc := range cases {
		if got := tc.w.needsMultipart(); got != tc.want {
			t.Errorf("%s: needsMultipart = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSiteSettingsWrite_multipartFields_uploadDoesNotRemove asserts that when a
// logo upload AND a remove flag are both set, no empty "logo" value is emitted
// (the file part carries the change; an empty value would fight it).
func TestSiteSettingsWrite_multipartFields_uploadDoesNotRemove(t *testing.T) {
	w := SiteSettingsWrite{SiteName: "A", LogoPath: "/x/l.png", RemoveLogo: true}
	fields := w.multipartFields()
	if _, ok := fields["logo"]; ok {
		t.Errorf("logo removal must be suppressed when an upload path is present")
	}
}
