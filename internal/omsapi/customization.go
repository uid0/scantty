package omsapi

import "context"

// SiteSettings mirrors backend SiteSettingsSerializer — the per-deploy
// branding + contact-info bag the web frontend reads on every page
// load. All fields are read-only on this serializer; the PUT/PATCH
// path uses SiteSettingsUpdateSerializer and is superuser-only on the
// backend, so scantty doesn't offer a write path here.
type SiteSettings struct {
	SiteName            string  `json:"site_name"`
	SiteTagline         string  `json:"site_tagline,omitempty"`
	LogoURL             *string `json:"logo_url"`
	LogoAltText         string  `json:"logo_alt_text,omitempty"`
	FaviconURL          *string `json:"favicon_url"`
	PrimaryColor        string  `json:"primary_color,omitempty"`
	SecondaryColor      string  `json:"secondary_color,omitempty"`
	FooterText          string  `json:"footer_text,omitempty"`
	ContactEmail        string  `json:"contact_email,omitempty"`
	ContactPhone        string  `json:"contact_phone,omitempty"`
	WebsiteURL          string  `json:"website_url,omitempty"`
	DashboardTitle      string  `json:"dashboard_title,omitempty"`
	DashboardSubtitle   string  `json:"dashboard_subtitle,omitempty"`
	ShowLogoOnDashboard bool    `json:"show_logo_on_dashboard"`
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
