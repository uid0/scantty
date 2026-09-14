package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// scanLandingCase is one scan and where it must leave the operator: on a
// screen of type `screen` addressed by `id`, or — where ScanTTY has no screen
// for what was scanned — still on the scan screen with a WARNING naming that.
type scanLandingCase struct {
	name     string
	screen   string // "%T" of the destination; "" means no screen exists
	id       string
	noScreen string // the noScreenNote fragment a no-screen scan must say
}

// scanDriveRoot is a Root on the scan screen against a fake OMS whose
// dispatcher answers `dispatch` and whose every other route answers `{}` — the
// destination screens fetch on Init, and what they fetch is not under test.
func scanDriveRoot(t *testing.T, dispatch map[string]any) Root {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/scanner/dispatch/" && dispatch != nil {
			_ = json.NewEncoder(w).Encode(dispatch)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	root := newTestRoot(NewScanScreen(deps))
	root.deps = deps
	return root
}

func scanDrive(t *testing.T, root Root, code string) Root {
	t.Helper()
	kitDriveType(t, &root, code)
	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return pump(t, next.(Root), cmd, 0)
}

// scanLandedID reads the record id off whichever detail screen a scan opened.
func scanLandedID(s Screen) string {
	switch d := s.(type) {
	case *InventoryDetailScreen:
		return d.itemID
	case *AssetDetailScreen:
		return d.assetID
	case *WorkOrderDetailScreen:
		return d.woID
	case *LocationDetailScreen:
		return d.id
	case *ProjectStorageDetailScreen:
		return d.stintID
	case *VendorWorkOrderDetailScreen:
		return d.id
	}
	return ""
}

func assertScanLanding(t *testing.T, root Root, tc scanLandingCase) {
	t.Helper()
	got := scanScreenName(root.screen)
	if tc.screen == "" {
		if _, ok := root.screen.(*ScanScreen); !ok {
			t.Fatalf("a scan ScanTTY has no screen for left the operator on %s", got)
		}
		if root.status.msgLevel != StatusWarn || !strings.Contains(root.status.message, tc.noScreen) {
			t.Fatalf("status = %q (level %v), want a WARNING containing %q — a green line "+
				"over a scan that opened nothing reads as done", root.status.message,
				root.status.msgLevel, tc.noScreen)
		}
		view := root.screen.View()
		if strings.Contains(view, "✓") || !strings.Contains(view, tc.noScreen) {
			t.Fatalf("the history row must say %q and carry no tick:\n%s", tc.noScreen, view)
		}
		return
	}
	if got != tc.screen {
		t.Fatalf("the scan left the operator on %s, want %s (status %q)", got, tc.screen,
			root.status.message)
	}
	if id := scanLandedID(root.screen); id != tc.id {
		t.Fatalf("%s opened on id %q, want %q", got, id, tc.id)
	}
}

func scanScreenName(s Screen) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", s), "*tui.")
}

// TestScan_EveryPrintedOMSLabelLandsWhereItNames drives each URL shape OMS's QR
// generators print (the scanner package's table carries their provenance)
// through the real scan screen. Every one of these used to parse as "unknown"
// and end at "(no detail screen yet)"; the third-party work order parsed and
// still opened nothing, because navigateToURL had no arm for a kind the parser
// had always produced.
func TestScan_EveryPrintedOMSLabelLandsWhereItNames(t *testing.T) {
	const base = "https://oms.example.org"
	const itemUUID = "0fa1fe96-f11c-4886-b6ff-4ba87870acb3"
	for _, tc := range []struct {
		url string
		scanLandingCase
	}{
		{base + "/scan/" + itemUUID, scanLandingCase{name: "item", screen: "InventoryDetailScreen", id: itemUUID}},
		{base + "/scan/asset/12", scanLandingCase{name: "asset", screen: "AssetDetailScreen", id: "12"}},
		{base + "/scan/location/7", scanLandingCase{name: "location", screen: "LocationDetailScreen", id: "7"}},
		{base + "/scan/project-storage/PS-0042", scanLandingCase{name: "project storage", screen: "ProjectStorageDetailScreen", id: "PS-0042"}},
		{base + "/maintenance/third-party/9", scanLandingCase{name: "third-party work order", screen: "VendorWorkOrderDetailScreen", id: "9"}},
		{base + "/scan/fixture/5", scanLandingCase{name: "fixture", noScreen: "no ScanTTY screen for fixture labels yet"}},
		{base + "/inventory/scan/fixture/5", scanLandingCase{name: "fixture long form", noScreen: "no ScanTTY screen for fixture labels yet"}},
		{base + "/scan/donation-item/88", scanLandingCase{name: "donation item", noScreen: "no ScanTTY screen for donation items yet"}},
		{base + "/scan/makerbox/BIN-04/ada/", scanLandingCase{name: "maker box", noScreen: "no ScanTTY screen for maker box labels yet"}},
		{base + "/nothing/oms/prints", scanLandingCase{name: "unrecognised url", noScreen: "not an OMS label ScanTTY can read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil dispatch answer: a URL is read locally and must never
			// need the round trip.
			root := scanDrive(t, scanDriveRoot(t, nil), tc.url)
			assertScanLanding(t, root, tc.scanLandingCase)
		})
	}
}

// TestScan_EveryDispatcherResultKindLandsOrSaysItCannot drives each
// target_type OMS's dispatcher returns (`scanner/resolvers.py`) through the
// scan screen. location, project_storage_stint, fixture and donation_item used
// to end at a green `scan X → location 7` with nothing opened.
func TestScan_EveryDispatcherResultKindLandsOrSaysItCannot(t *testing.T) {
	for _, tc := range []struct {
		targetType string
		targetID   any
		scanLandingCase
	}{
		{"inventory_item", "item-9", scanLandingCase{name: "inventory item", screen: "InventoryDetailScreen", id: "item-9"}},
		{"asset", "12", scanLandingCase{name: "asset", screen: "AssetDetailScreen", id: "12"}},
		// A location's pk is an integer on the wire; it must reach the path
		// as its digits at any magnitude.
		{"location", 1000000, scanLandingCase{name: "location", screen: "LocationDetailScreen", id: "1000000"}},
		{"project_storage_stint", "PS-0042", scanLandingCase{name: "project storage stint", screen: "ProjectStorageDetailScreen", id: "PS-0042"}},
		{"fixture", 5, scanLandingCase{name: "fixture", noScreen: "no ScanTTY screen for fixture labels yet"}},
		{"donation_item", 88, scanLandingCase{name: "donation item", noScreen: "no ScanTTY screen for donation items yet"}},
		{"something_new", 1, scanLandingCase{name: "a kind OMS adds later", noScreen: "no ScanTTY screen for this yet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scanDriveRoot(t, map[string]any{
				"action":      "view",
				"target_type": tc.targetType,
				"target_id":   tc.targetID,
				"target_name": "Scanned record",
			})
			root = scanDrive(t, root, "LOC123")
			assertScanLanding(t, root, tc.scanLandingCase)
		})
	}
}
