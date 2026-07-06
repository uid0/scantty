package omsapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SiteSettings mirrors backend SiteSettingsSerializer — the per-deploy
// branding + contact-info bag the web frontend reads on every page
// load. All fields here are read-only on that serializer; the write path
// (PUT/PATCH on the same URL) uses SiteSettingsUpdateSerializer and is
// superuser-only on the backend. See SiteSettingsWrite / UpdateSiteSettings
// for the edit path scantty exposes on its Settings screen.
type SiteSettings struct {
	SiteName                  string  `json:"site_name"`
	SiteTagline               string  `json:"site_tagline,omitempty"`
	LogoURL                   *string `json:"logo_url"`
	LogoAltText               string  `json:"logo_alt_text,omitempty"`
	FaviconURL                *string `json:"favicon_url"`
	PrimaryColor              string  `json:"primary_color,omitempty"`
	SecondaryColor            string  `json:"secondary_color,omitempty"`
	FooterText                string  `json:"footer_text,omitempty"`
	ContactEmail              string  `json:"contact_email,omitempty"`
	ContactPhone              string  `json:"contact_phone,omitempty"`
	WebsiteURL                string  `json:"website_url,omitempty"`
	DashboardTitle            string  `json:"dashboard_title,omitempty"`
	DashboardSubtitle         string  `json:"dashboard_subtitle,omitempty"`
	ShowLogoOnDashboard       bool    `json:"show_logo_on_dashboard"`
	PmAutoBundleDueWithinDays int     `json:"pm_auto_bundle_due_within_days"`
}

// GetSiteSettings fetches the public site-settings record. AllowAny
// on the backend — works pre-login. Useful for surfacing the deploy's
// own name + contact info in the scantty settings screen.
func (c *Client) GetSiteSettings(ctx context.Context) (*SiteSettings, error) {
	var out SiteSettings
	if err := c.Get(ctx, "/api/customization/settings/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SiteSettingsWrite is the editable field set of SiteSettingsUpdateSerializer
// (the read-only logo_url / favicon_url are excluded). The singleton is edited
// in place via PATCH — every field is sent on each save (the form hydrates from
// the read record and round-trips untouched fields), so blank-able strings carry
// NO omitempty and a cleared optional persists as "".
//
// logo and favicon are ImageField uploads, so they never ride in the JSON body:
// LogoPath / FaviconPath name a local file to upload and RemoveLogo asks the
// backend to delete the current logo (it accepts an empty "logo" form value —
// see SiteSettingsUpdateSerializer.validate_logo + the view's empty-string
// handling). favicon has no such delete hook, so there is no RemoveFavicon.
// Any of the three flips the call to multipart/form-data, mirroring the web
// Settings page which always posts FormData.
type SiteSettingsWrite struct {
	SiteName                  string `json:"site_name"`
	SiteTagline               string `json:"site_tagline"`
	LogoAltText               string `json:"logo_alt_text"`
	PrimaryColor              string `json:"primary_color"`
	SecondaryColor            string `json:"secondary_color"`
	FooterText                string `json:"footer_text"`
	ContactEmail              string `json:"contact_email"`
	ContactPhone              string `json:"contact_phone"`
	WebsiteURL                string `json:"website_url"`
	DashboardTitle            string `json:"dashboard_title"`
	DashboardSubtitle         string `json:"dashboard_subtitle"`
	ShowLogoOnDashboard       bool   `json:"show_logo_on_dashboard"`
	PmAutoBundleDueWithinDays int    `json:"pm_auto_bundle_due_within_days"`

	// File actions — never serialized as JSON (`json:"-"`); they drive the
	// multipart path instead.
	LogoPath    string `json:"-"` // absolute path to a new logo image to upload
	FaviconPath string `json:"-"` // absolute path to a new favicon image to upload
	RemoveLogo  bool   `json:"-"` // delete the current logo (sends logo="")
}

// UpdateSiteSettings PATCHes the singleton site-settings record. PATCH mirrors
// the web (the backend treats PUT and PATCH identically — both partial), and the
// backend re-serializes with the read serializer on success, so the response
// decodes back into SiteSettings. Requires superuser auth; a non-superuser gets
// a 403 surfaced as an *APIError. When a logo/favicon upload or logo removal is
// requested the call switches to multipart/form-data, otherwise it sends JSON.
func (c *Client) UpdateSiteSettings(ctx context.Context, body SiteSettingsWrite) (*SiteSettings, error) {
	var out SiteSettings
	const path = "/api/customization/settings/"
	if body.needsMultipart() {
		files, err := body.multipartFiles()
		if err != nil {
			return nil, err
		}
		if err := c.PatchMultipart(ctx, path, body.multipartFields(), files, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
	if err := c.Patch(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// needsMultipart reports whether this write touches a file field (logo/favicon
// upload) or asks to remove the logo — any of which requires multipart/form-data.
func (w SiteSettingsWrite) needsMultipart() bool {
	return strings.TrimSpace(w.LogoPath) != "" ||
		strings.TrimSpace(w.FaviconPath) != "" ||
		w.RemoveLogo
}

// multipartFiles reads the picked logo/favicon images off disk into in-memory
// file parts. Returns an empty slice when neither is being uploaded.
func (w SiteSettingsWrite) multipartFiles() ([]MultipartFile, error) {
	var files []MultipartFile
	for _, f := range []struct {
		field string
		path  string
	}{
		{"logo", strings.TrimSpace(w.LogoPath)},
		{"favicon", strings.TrimSpace(w.FaviconPath)},
	} {
		if f.path == "" {
			continue
		}
		data, err := os.ReadFile(f.path)
		if err != nil {
			return nil, fmt.Errorf("oms: read %s: %w", f.path, err)
		}
		files = append(files, MultipartFile{Field: f.field, Filename: filepath.Base(f.path), Data: data})
	}
	return files, nil
}

// multipartFields renders every scalar field as a form value (bools as
// "true"/"false", the int as a decimal string — DRF coerces both from
// multipart text). A logo removal is expressed as the empty "logo" value the
// backend special-cases; it is only emitted when no upload path is set, so an
// upload never doubles as a delete.
func (w SiteSettingsWrite) multipartFields() map[string][]string {
	fields := map[string][]string{
		"site_name":                      {w.SiteName},
		"site_tagline":                   {w.SiteTagline},
		"logo_alt_text":                  {w.LogoAltText},
		"primary_color":                  {w.PrimaryColor},
		"secondary_color":                {w.SecondaryColor},
		"footer_text":                    {w.FooterText},
		"contact_email":                  {w.ContactEmail},
		"contact_phone":                  {w.ContactPhone},
		"website_url":                    {w.WebsiteURL},
		"dashboard_title":                {w.DashboardTitle},
		"dashboard_subtitle":             {w.DashboardSubtitle},
		"show_logo_on_dashboard":         {strconv.FormatBool(w.ShowLogoOnDashboard)},
		"pm_auto_bundle_due_within_days": {strconv.Itoa(w.PmAutoBundleDueWithinDays)},
	}
	if w.RemoveLogo && strings.TrimSpace(w.LogoPath) == "" {
		fields["logo"] = []string{""}
	}
	return fields
}
