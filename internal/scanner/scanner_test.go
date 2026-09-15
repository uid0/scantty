package scanner

import "testing"

// The barcode formats OMS's dispatcher actually resolves, and the badge format
// its own resolver documents. Both rosters are the SERVER's, transcribed rather
// than invented, because this whole defect came of a rule somebody reasoned out
// locally about codes only the server holds the data for:
//
//   - backend/scanner/resolvers.py, `_UPC_LENGTHS = {8, 12, 13, 14}` and the
//     module docstring naming UPC-A (12), UPC-E (8), EAN-13 (13), ITF-14 (14),
//     plus the 6-char location access code and the asset tag beside them;
//   - backend/maker_boxes/services/identity_resolver.py, `_looks_like_badge`:
//     "Real RFID badge numbers in the DMS deploy are 8-10 digits".
//
// The values are real-world check-digit-valid codes, not `111111111111`: a
// fixture of one repeated digit cannot tell a length rule from a content rule.
var (
	// Retail and MRO barcodes. Every one of these is 8-20 characters of
	// [0-9a-fA-F], which is exactly what the retired badge rule claimed.
	upcA   = "036000291452"   // UPC-A, 12 digits
	upcE   = "04252614"       // UPC-E, 8 digits
	ean13  = "4006381333931"  // EAN-13, 13 digits
	ean8   = "96385074"       // EAN-8, 8 digits
	itf14  = "10036000291459" // ITF-14, 14 digits
	badges = []string{
		"12345678",   // 8 digits — the Common API's own fixture shape
		"123456789",  // 9 digits
		"1234567890", // 10 digits
	}
)

// TestClassify_ABarcodeIsNotClaimedLocally is the regression this file exists
// for. Every one of these used to classify as a ForgeKey badge and be answered
// with "badge lookup not yet wired", so no UPC ever reached
// /api/scanner/dispatch/ — the one endpoint that can resolve it.
//
// KindUnknown is the PASS here, not a failure: the scan screen sends both it
// and KindOMSCode to the dispatcher, and a barcode is precisely a code this
// side cannot name a local destination for.
func TestClassify_ABarcodeIsNotClaimedLocally(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"UPC-A", upcA},
		{"UPC-E", upcE},
		{"EAN-13", ean13},
		{"EAN-8", ean8},
		{"ITF-14", itf14},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.code); got != KindUnknown {
				t.Fatalf("Classify(%q) = %v, want KindUnknown so it reaches the dispatcher", tc.code, got)
			}
		})
	}
}

// TestClassify_ABadgeAlsoReachesTheDispatcher is the other half, and it is the
// one that looks wrong until you read why.
//
// A badge is 8-10 digits and a UPC-E is 8 digits: the SAME string. No
// classifier can separate them, and only one side of the collision has a
// resolver — ScanTTY has never looked a badge up and OMS exposes no
// member-by-badge read endpoint. So a badge-shaped code is sent to the
// dispatcher like anything else, where it either turns out to be a barcode
// after all or comes back unmatched with the badge caveat on it.
//
// Claiming it locally is what the retired rule did, and it bought nothing: the
// branch it routed to could only ever return an error.
func TestClassify_ABadgeAlsoReachesTheDispatcher(t *testing.T) {
	for _, code := range badges {
		if got := Classify(code); got != KindUnknown {
			t.Errorf("Classify(%q) = %v, want KindUnknown — nothing may claim a badge "+
				"for a path that cannot resolve one", code, got)
		}
	}
}

// TestClassify_TheCodesWithALocalDestinationKeepIt guards the two shapes that
// really do name where they go, so the fix above cannot be read as "send
// everything to the server".
func TestClassify_TheCodesWithALocalDestinationKeepIt(t *testing.T) {
	// A 6-char OMS code was never affected — it is shorter than the retired
	// rule's floor of 8 — and it must stay unaffected.
	for _, code := range []string{"ABC123", "123456", "XYZ789"} {
		if got := Classify(code); got != KindOMSCode {
			t.Errorf("Classify(%q) = %v, want KindOMSCode", code, got)
		}
	}
	for _, code := range []string{
		"https://oms.example.org/inventory/items/42",
		"http://oms.example.org/assets/7",
	} {
		if got := Classify(code); got != KindOMSURL {
			t.Errorf("Classify(%q) = %v, want KindOMSURL", code, got)
		}
	}
	if got := Classify(""); got != KindUnknown {
		t.Errorf("Classify(\"\") = %v, want KindUnknown", got)
	}
}

// TestMightBeForgeKeyBadge_IsTheFormatOMSDocuments pins the ADVISORY predicate
// to the server's own figures. It decides a sentence, never a destination, so
// what matters is that it recognises a real badge and does not stretch to
// lengths no reader emits.
func TestMightBeForgeKeyBadge_IsTheFormatOMSDocuments(t *testing.T) {
	for _, code := range badges {
		if !MightBeForgeKeyBadge(code) {
			t.Errorf("MightBeForgeKeyBadge(%q) = false, want true — OMS documents 8-10 digits", code)
		}
	}
	for _, tc := range []struct{ name, code string }{
		// Below and above the documented window.
		{"7 digits", "1234567"},
		{"11 digits", "12345678901"},
		// Hex letters. The retired rule accepted these AS badges; the real
		// column has no validator, but no reader emits them as a UID and
		// OMS's own heuristic is digits-only.
		{"hex letters", "DEADBEEF"},
		{"mixed hex", "A1B2C3D4E5F6"},
		// Longer barcodes are outside the window outright.
		{"UPC-A", upcA},
		{"EAN-13", ean13},
		{"ITF-14", itf14},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if MightBeForgeKeyBadge(tc.code) {
				t.Fatalf("MightBeForgeKeyBadge(%q) = true, want false", tc.code)
			}
		})
	}
}

// TestMightBeForgeKeyBadge_SaysNothingAboutRouting is the guard that keeps the
// advisory predicate advisory. The 8-digit overlap is REAL — a UPC-E and a
// badge are the same string — so if this ever stops holding, somebody has
// narrowed one of the two in a way the formats do not support and a barcode is
// about to be claimed again.
func TestMightBeForgeKeyBadge_SaysNothingAboutRouting(t *testing.T) {
	for _, code := range []string{upcE, ean8} {
		if !MightBeForgeKeyBadge(code) {
			t.Fatalf("MightBeForgeKeyBadge(%q) = false: the 8-digit overlap between a "+
				"badge and a UPC-E is the reason nothing routes on this predicate. "+
				"If the formats really have been separated, this test is what should change.", code)
		}
		if got := Classify(code); got != KindUnknown {
			t.Fatalf("Classify(%q) = %v: badge SHAPE must not become a destination", code, got)
		}
	}
}

// TestParseOMSURL_EveryPrintedScanLabelNamesItsTarget holds the parser to the
// URLs OMS really PRINTS on a label, transcribed from the generators at
// uid0/openmakersuite main with the line that builds each one. Every OMS QR
// generator writes the short `/scan/...` form, and ParseOMSURL had no case for
// `scan` at all — so every printed item, asset, location, project-storage and
// donation label parsed as "unknown" and the scan screen opened nothing.
//
// The generator rows are the contract; the route rows are the web's own
// `frontend/src/App.tsx` table for the same prefix, which is what a phone
// camera opens, and they are here because `/scan/fixture/<id>` is routed and a
// label reaching it must not be read as a bare item id called "fixture".
//
// The kinds are the dispatcher's target_type spellings, so a printed label and
// a dispatcher-resolved code for the same record reach the same arm.
func TestParseOMSURL_EveryPrintedScanLabelNamesItsTarget(t *testing.T) {
	const base = "https://oms.example.org"
	const itemUUID = "0fa1fe96-f11c-4886-b6ff-4ba87870acb3"
	for _, tc := range []struct {
		source   string
		url      string
		kind, id string
	}{
		// backend/inventory/utils/qr_generator.py
		{"qr_generator.py:29 item", base + "/scan/" + itemUUID, "code", itemUUID},
		{"qr_generator.py:81 asset", base + "/scan/asset/12", "asset", "12"},
		{"qr_generator.py:119 location", base + "/scan/location/7", "location", "7"},
		// backend/inventory/services/qr_code_service.py
		{"qr_code_service.py:223 asset", base + "/scan/asset/1000000", "asset", "1000000"},
		{"qr_code_service.py:255 project storage", base + "/scan/project-storage/PS-0042", "project_storage_stint", "PS-0042"},
		{"qr_code_service.py:281 item", base + "/scan/" + itemUUID, "code", itemUUID},
		{"qr_code_service.py:313 location", base + "/scan/location/31", "location", "31"},
		// backend/donations/services/qr_code_service.py
		{"donations qr_code_service.py:155 donation item", base + "/scan/donation-item/88", "donation_item", "88"},
		// backend/maker_boxes/services/label_service.py:94 — trailing slash,
		// and a path-escaped bin id and username.
		{"label_service.py:94 maker box", base + "/scan/makerbox/BIN%2004/ada/", "maker_box", "BIN 04"},
		// frontend/src/App.tsx route with no generator today.
		{"App.tsx /scan/fixture/:fixtureId", base + "/scan/fixture/5", "fixture", "5"},
		// The long forms the web redirects to, which were already parsed; the
		// donation spelling now matches the dispatcher's.
		{"App.tsx /inventory/scan/:itemId", base + "/inventory/scan/" + itemUUID, "code", itemUUID},
		{"App.tsx /inventory/scan/asset/:assetId", base + "/inventory/scan/asset/12", "asset", "12"},
		{"App.tsx /inventory/scan/fixture/:fixtureId", base + "/inventory/scan/fixture/5", "fixture", "5"},
		{"App.tsx /inventory/scan/donation-item/:itemId", base + "/inventory/scan/donation-item/88", "donation_item", "88"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			if got := Classify(tc.url); got != KindOMSURL {
				t.Fatalf("Classify(%q) = %v, want KindOMSURL", tc.url, got)
			}
			target, err := ParseOMSURL(tc.url)
			if err != nil {
				t.Fatalf("ParseOMSURL(%q): %v", tc.url, err)
			}
			if target.Kind != tc.kind || target.ResourceID != tc.id {
				t.Fatalf("ParseOMSURL(%q) = kind %q id %q, want kind %q id %q",
					tc.url, target.Kind, target.ResourceID, tc.kind, tc.id)
			}
		})
	}
}

// TestParseOMSURL_AScanTypeOMSDoesNotPrintIsUnknown keeps a guess from
// becoming a route: an unrecognised <type> segment is "unknown", never its raw
// spelling, so no arm downstream can match a word nobody prints.
func TestParseOMSURL_AScanTypeOMSDoesNotPrintIsUnknown(t *testing.T) {
	for _, raw := range []string{
		"https://oms.example.org/scan/widget/9",
		"https://oms.example.org/inventory/scan/widget/9",
	} {
		target, err := ParseOMSURL(raw)
		if err != nil {
			t.Fatalf("ParseOMSURL(%q): %v", raw, err)
		}
		if target.Kind != "unknown" {
			t.Errorf("ParseOMSURL(%q).Kind = %q, want unknown", raw, target.Kind)
		}
	}
}
