package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// loadAssetDetail builds an asset-detail screen with the asset already loaded
// so a test can drive the report-problem key handlers directly.
func loadAssetDetail(t *testing.T, deps Deps, asset *omsapi.Asset) *AssetDetailScreen {
	t.Helper()
	id, _ := asset.ID.(string)
	s := NewAssetDetailScreen(deps, id)
	next, _ := s.Update(assetDetailLoadedMsg{asset: asset})
	return next.(*AssetDetailScreen)
}

// TestReportProblem_PartsChecklistFlow drives the full report-problem flow on
// an asset that has parts: describe → the "which components?" checklist opens →
// toggle two parts → submit posts to report_problem with the flagged part_ids.
// Part ids are float64 here to mirror the JSON-number shape they arrive in.
func TestReportProblem_PartsChecklistFlow(t *testing.T) {
	var captured struct {
		method, path string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"prob-1","asset":"asset-9","description":"belt frayed","status":"reported"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	asset := &omsapi.Asset{ID: "asset-9", Name: "Lathe", Parts: []omsapi.AssetPart{
		{ID: float64(12), Part: "p-1", PartName: "Drive belt", PartSKU: "BELT-1"},
		{ID: float64(15), Part: "p-2", PartName: "Chuck", PartSKU: "CHK-1"},
	}}
	s := loadAssetDetail(t, deps, asset)

	// 'p' opens the log-problem form.
	next, _ := s.Update(runeKey('p'))
	s = next.(*AssetDetailScreen)
	if s.activeForm != formLogProblem {
		t.Fatalf("after 'p', activeForm = %v, want formLogProblem", s.activeForm)
	}
	s.input.SetValue("belt frayed")

	// enter on the description advances to the components checklist (the asset
	// has parts) rather than submitting immediately.
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*AssetDetailScreen)
	if s.activeForm != formProblemParts {
		t.Fatalf("after description enter, activeForm = %v, want formProblemParts", s.activeForm)
	}
	if s.problemDesc != "belt frayed" {
		t.Errorf("problemDesc = %q", s.problemDesc)
	}
	if len(s.problemParts) != 2 {
		t.Fatalf("problemParts len = %d, want 2", len(s.problemParts))
	}
	if !s.WantsRawInput() {
		t.Errorf("expected raw input while the checklist is open")
	}

	// space toggles the highlighted part (cursor 0 → id 12), j moves down, space
	// toggles the second (id 15).
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeySpace})
	s = next.(*AssetDetailScreen)
	next, _ = s.Update(runeKey('j'))
	s = next.(*AssetDetailScreen)
	if s.partCursor != 1 {
		t.Fatalf("partCursor = %d, want 1", s.partCursor)
	}
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeySpace})
	s = next.(*AssetDetailScreen)
	if !s.partSelected["12"] || !s.partSelected["15"] {
		t.Fatalf("partSelected = %v, want 12 & 15 set", s.partSelected)
	}

	// enter submits; run the returned cmd and confirm the wire request.
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*AssetDetailScreen)
	if cmd == nil {
		t.Fatalf("expected a submit cmd")
	}
	msg := cmd()
	logged, ok := msg.(problemLoggedMsg)
	if !ok {
		t.Fatalf("msg = %T, want problemLoggedMsg", msg)
	}
	if logged.err != nil {
		t.Fatalf("submit failed: %v", logged.err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/assets/asset-9/report_problem/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["description"] != "belt frayed" {
		t.Errorf("description = %v", captured.body["description"])
	}
	ids, ok := captured.body["part_ids"].([]any)
	if !ok || len(ids) != 2 || ids[0] != "12" || ids[1] != "15" {
		t.Errorf("part_ids = %v (%T), want [\"12\",\"15\"]", captured.body["part_ids"], captured.body["part_ids"])
	}
}

// TestReportProblem_DescriptionOnlyWhenNoParts confirms that an asset with no
// parts skips the checklist entirely and submits description-only.
func TestReportProblem_DescriptionOnlyWhenNoParts(t *testing.T) {
	var captured struct {
		path string
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"prob-2","asset":"asset-3","description":"won't power on","status":"reported"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	asset := &omsapi.Asset{ID: "asset-3", Name: "Drill"} // no parts
	s := loadAssetDetail(t, deps, asset)

	next, _ := s.Update(runeKey('p'))
	s = next.(*AssetDetailScreen)
	s.input.SetValue("won't power on")

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*AssetDetailScreen)
	// No parts → no checklist; the description enter submits straight away.
	if s.activeForm == formProblemParts {
		t.Fatalf("no-parts asset should skip the components checklist")
	}
	if cmd == nil {
		t.Fatalf("expected a submit cmd")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("submit cmd returned nil msg")
	}
	if captured.path != "/api/inventory/assets/asset-3/report_problem/" {
		t.Errorf("path = %q", captured.path)
	}
	if _, present := captured.body["part_ids"]; present {
		t.Errorf("part_ids should be omitted, got %v", captured.body["part_ids"])
	}
}
