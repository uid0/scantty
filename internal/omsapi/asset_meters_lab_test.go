//go:build omslab

package omsapi

import (
	"context"
	"os"
	"strings"
	"testing"
)

// A live drive of all eight endpoints against a REAL OpenMakerSuite, run with
// -tags omslab and SCANTTY_OMS_URL / SCANTTY_OMS_TOKEN set. It is the check a
// fake cannot make: a fake built from these structs cannot disagree with them.
func TestLab_AssetMetersAndDocumentsAgainstARealBackend(t *testing.T) {
	url, token := os.Getenv("SCANTTY_OMS_URL"), os.Getenv("SCANTTY_OMS_TOKEN")
	if url == "" || token == "" {
		t.Skip("set SCANTTY_OMS_URL and SCANTTY_OMS_TOKEN")
	}
	asset := os.Getenv("SCANTTY_LAB_ASSET")
	if asset == "" {
		t.Skip("set SCANTTY_LAB_ASSET to an asset id")
	}
	c := New(url, WithToken(token, ""))
	ctx := context.Background()

	meter, err := c.CreateAssetMeter(ctx, CreateAssetMeterRequest{
		Asset: asset, Name: "Lab drive meter", MeterType: MeterTypeRuntimeHours,
		Unit: "hours", Source: MeterSourceManual,
	})
	if err != nil {
		t.Fatalf("CreateAssetMeter: %v", err)
	}
	t.Logf("created meter %s current=%q", meter.ID, meter.CurrentValue)

	rec, err := c.RecordMeterReading(ctx, meter.ID, RecordMeterReadingRequest{
		Value: "1250.5", IsAbsolute: true,
	})
	if err != nil {
		t.Fatalf("RecordMeterReading: %v", err)
	}
	if rec.Meter.CurrentValue.String() != "1250.5000" {
		t.Errorf("after an absolute 1250.5 the meter reads %q", rec.Meter.CurrentValue)
	}
	if rec.Reading.Source != MeterSourceManual {
		t.Errorf("reading source = %q", rec.Reading.Source)
	}

	del, err := c.RecordMeterReading(ctx, meter.ID, RecordMeterReadingRequest{
		Value: "8.25", IsAbsolute: false, IsEstimated: true,
	})
	if err != nil {
		t.Fatalf("RecordMeterReading (delta): %v", err)
	}
	if del.Meter.CurrentValue.String() != "1258.7500" {
		t.Errorf("after a delta of 8.25 the meter reads %q, want 1258.7500 — is_absolute "+
			"false must reach the server", del.Meter.CurrentValue)
	}
	if !del.Reading.IsEstimated {
		t.Error("is_estimated did not reach the server")
	}

	adj, err := c.AdjustMeter(ctx, meter.ID, AdjustMeterRequest{
		Target: "1200", Reason: "lab drive correction",
	})
	if err != nil {
		t.Fatalf("AdjustMeter: %v", err)
	}
	if adj.Reading.Source != MeterReadingSourceManualAdjust {
		t.Errorf("adjust wrote source %q", adj.Reading.Source)
	}
	if adj.Reading.Notes != "lab drive correction" {
		t.Errorf("the reason did not reach the record: %q", adj.Reading.Notes)
	}
	if !strings.HasPrefix(adj.Reading.Delta.String(), "-") {
		t.Errorf("a correction DOWN produced delta %q, want a negative", adj.Reading.Delta)
	}

	// THE SERVER TAKES A BACKWARDS READING AND AN ORDER-OF-MAGNITUDE ONE. This
	// is the measurement the terminal's confirm rests on; if OMS ever starts
	// refusing them, this fails and the confirm's premise has changed.
	back, err := c.RecordMeterReading(ctx, meter.ID, RecordMeterReadingRequest{
		Value: "120", IsAbsolute: true,
	})
	if err != nil {
		t.Fatalf("a BACKWARDS reading was refused: %v — the confirm in "+
			"internal/tui/asset_meters.go exists because it is not", err)
	}
	t.Logf("backwards reading accepted: meter now %q", back.Meter.CurrentValue)

	big, err := c.RecordMeterReading(ctx, meter.ID, RecordMeterReadingRequest{
		Value: "12000000", IsAbsolute: true,
	})
	if err != nil {
		t.Fatalf("an ORDER-OF-MAGNITUDE reading was refused: %v", err)
	}
	t.Logf("magnitude reading accepted: meter now %q", big.Meter.CurrentValue)

	readings, err := c.ListMeterReadings(ctx, meter.ID)
	if err != nil {
		t.Fatalf("ListMeterReadings: %v", err)
	}
	if len(readings) < 5 {
		t.Errorf("the ledger holds %d rows, want every write above", len(readings))
	}
	meters, err := c.ListAssetMeters(ctx, asset)
	if err != nil {
		t.Fatalf("ListAssetMeters: %v", err)
	}
	var found bool
	for _, m := range meters {
		found = found || m.ID == meter.ID
	}
	if !found {
		t.Errorf("the meter just created is not in the asset's list of %d", len(meters))
	}

	// Documents.
	path := t.TempDir() + "/lab-manual.txt"
	if err := os.WriteFile(path, []byte("rev A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.UploadAssetDocument(ctx, asset, "Lab drive manual", "manual", "", "lab-manual.txt", f)
	f.Close()
	if err != nil {
		t.Fatalf("UploadAssetDocument: %v", err)
	}
	if doc.Version != 1 || !doc.IsCurrent {
		t.Errorf("a fresh upload came back v%d current=%v", doc.Version, doc.IsCurrent)
	}

	path2 := t.TempDir() + "/lab-manual-b.txt"
	if err := os.WriteFile(path2, []byte("rev B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f2, err := os.Open(path2)
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.SupersedeAssetDocument(ctx, doc.ID, "", "", "", "lab-manual-b.txt", f2)
	f2.Close()
	if err != nil {
		t.Fatalf("SupersedeAssetDocument: %v", err)
	}
	if next.Version != 2 {
		t.Errorf("supersede produced v%d, want the server's bump", next.Version)
	}
	// The OMITTED title INHERITED the prior one — the whole reason blanks are
	// left out rather than sent empty.
	if next.Title != "Lab drive manual" {
		t.Errorf("supersede with no title produced %q, want the inherited one", next.Title)
	}
	if next.Supersedes == nil || *next.Supersedes != doc.ID {
		t.Errorf("supersede did not link back to %s", doc.ID)
	}

	docs, err := c.ListAssetDocuments(ctx, asset)
	if err != nil {
		t.Fatalf("ListAssetDocuments: %v", err)
	}
	var sawSuperseded bool
	for _, d := range docs {
		if d.ID == doc.ID && !d.IsCurrent {
			sawSuperseded = true
		}
	}
	if !sawSuperseded {
		t.Error("the superseded v1 is not in the list, or is still marked current")
	}
	if err := c.DeleteAssetDocument(ctx, next.ID); err != nil {
		t.Fatalf("DeleteAssetDocument: %v", err)
	}
	if err := c.DeleteAssetDocument(ctx, doc.ID); err != nil {
		t.Fatalf("DeleteAssetDocument (v1): %v", err)
	}
}
