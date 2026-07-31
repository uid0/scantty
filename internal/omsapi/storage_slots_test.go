package omsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newSlotTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL), srv
}

// TestStorageSlot_Decode pins the read shape against StorageSlotSerializer,
// including the two nullables that a naive struct would swallow: a slot with no
// AprilTag (the family ran dry) and a slot with no owning SIG.
func TestStorageSlot_Decode(t *testing.T) {
	body := `{
	  "count": 2, "next": null, "previous": null,
	  "results": [
	    {"id": 7, "code": "1A1", "rack": 1, "level": "A", "position": 1,
	     "requires_pallet_jack": false, "is_active": true,
	     "owning_group": 3, "owning_group_name": "Woodshop",
	     "notes": "by the door", "april_tag_id": 41,
	     "current_stint": {"id": 12, "stint_id": "PS-AB23CDFG", "username": "alice",
	                       "display_name": "Alice Smith", "project_title": "CNC jig",
	                       "started_at": "2026-07-01T10:00:00Z",
	                       "expires_at": "2026-07-31T10:00:00Z", "status": "active"},
	     "is_occupied": true,
	     "created_at": "2026-06-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
	    {"id": 8, "code": "1Y2", "rack": 1, "level": "Y", "position": 2,
	     "requires_pallet_jack": true, "is_active": false,
	     "owning_group": null, "owning_group_name": "",
	     "notes": "", "april_tag_id": null, "current_stint": null, "is_occupied": false,
	     "created_at": "2026-06-01T00:00:00Z", "updated_at": "2026-06-01T00:00:00Z"}
	  ]}`
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/project-storage/slots/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	page, err := c.ListStorageSlots(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListStorageSlots: %v", err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("got %d rows, want 2", len(page.Results))
	}

	occupied := page.Results[0]
	if occupied.Code != "1A1" || occupied.Rack != 1 || occupied.Level != "A" || occupied.Position != 1 {
		t.Errorf("components decoded wrong: %+v", occupied)
	}
	if occupied.AprilTagID == nil || *occupied.AprilTagID != 41 {
		t.Errorf("april_tag_id = %v, want 41", occupied.AprilTagID)
	}
	if occupied.OwningGroup == nil || *occupied.OwningGroup != 3 || occupied.OwningGroupName != "Woodshop" {
		t.Errorf("owning group decoded wrong: %+v", occupied)
	}
	if occupied.CurrentStint == nil {
		t.Fatalf("current_stint should decode for an occupied slot")
	}
	if occupied.CurrentStint.StintID != "PS-AB23CDFG" || occupied.CurrentStint.DisplayName != "Alice Smith" {
		t.Errorf("occupant decoded wrong: %+v", occupied.CurrentStint)
	}
	if occupied.CurrentStint.ExpiresAt == nil {
		t.Errorf("occupant expires_at should decode")
	}
	if !occupied.IsOccupied {
		t.Errorf("is_occupied should be true")
	}

	free := page.Results[1]
	if free.AprilTagID != nil {
		t.Errorf("a tagless slot must decode april_tag_id as nil, got %v", *free.AprilTagID)
	}
	if free.OwningGroup != nil {
		t.Errorf("an unowned slot must decode owning_group as nil")
	}
	if free.CurrentStint != nil || free.IsOccupied {
		t.Errorf("a free slot must decode current_stint nil / is_occupied false")
	}
	if !free.RequiresPalletJack || free.IsActive {
		t.Errorf("flags decoded wrong: %+v", free)
	}
}

// TestStorageSlotWrite_Wire pins the writable field set: `code` is read-only
// and must never be sent, while owning_group and notes carry no omitempty so a
// PATCH can CLEAR them (JSON null / "").
func TestStorageSlotWrite_Wire(t *testing.T) {
	buf, err := json.Marshal(StorageSlotWrite{Rack: 1, Level: "A", Position: 2})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	if strings.Contains(got, `"code"`) {
		t.Errorf("code is read-only and must not be sent: %s", got)
	}
	if !strings.Contains(got, `"owning_group":null`) {
		t.Errorf("a nil owning_group must serialize as null so a PATCH clears it: %s", got)
	}
	if !strings.Contains(got, `"notes":""`) {
		t.Errorf("a blank notes must be sent so a PATCH clears it: %s", got)
	}
	if !strings.Contains(got, `"is_active":false`) || !strings.Contains(got, `"requires_pallet_jack":false`) {
		t.Errorf("booleans must always be sent: %s", got)
	}
}

// TestUpdateStorageSlot_PathAndMethod pins that the viewset is addressed by
// CODE (lookup_field="code", not the pk) with a PATCH, and that a lower-case
// code is normalized before it becomes a URL.
func TestUpdateStorageSlot_PathAndMethod(t *testing.T) {
	var gotMethod, gotPath string
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"code":"1A1"}`))
	}))

	if _, err := c.UpdateStorageSlot(context.Background(), "1a1", StorageSlotWrite{Rack: 1, Level: "A", Position: 1}); err != nil {
		t.Fatalf("UpdateStorageSlot: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/api/project-storage/slots/1A1/" {
		t.Errorf("path = %q, want the normalized code with a trailing slash", gotPath)
	}
}

// TestGetStorageSlot_RejectsMalformedCode keeps a typo local instead of
// spending a round trip on a URL the router can't match.
func TestGetStorageSlot_RejectsMalformedCode(t *testing.T) {
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a malformed code must not reach the server (%s)", r.URL.Path)
	}))
	if _, err := c.GetStorageSlot(context.Background(), "not-a-code"); err == nil {
		t.Errorf("expected an error for a malformed code")
	}
}

// TestParseStorageSlotCode mirrors StorageSlot.parse_code, case fold included.
func TestParseStorageSlotCode(t *testing.T) {
	rack, level, position, err := ParseStorageSlotCode(" 12b7 ")
	if err != nil {
		t.Fatalf("ParseStorageSlotCode: %v", err)
	}
	if rack != 12 || level != "B" || position != 7 {
		t.Errorf("got (%d,%q,%d), want (12,\"B\",7)", rack, level, position)
	}
	for _, bad := range []string{"", "1A", "A1", "1A1B", "11", "1-A-1"} {
		if _, _, _, err := ParseStorageSlotCode(bad); err == nil {
			t.Errorf("ParseStorageSlotCode(%q) should fail", bad)
		}
	}
	if code, err := NormalizeStorageSlotCode("1a1"); err != nil || code != "1A1" {
		t.Errorf("NormalizeStorageSlotCode(1a1) = (%q,%v), want (1A1,nil)", code, err)
	}
}

// TestListAllStorageSlots_PagesAndKeepsFilters walks every page (a rack easily
// exceeds the 50-row page size) and must not mutate the caller's url.Values
// while doing it.
func TestListAllStorageSlots_PagesAndKeepsFilters(t *testing.T) {
	var seenOccupied []string
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenOccupied = append(seenOccupied, r.URL.Query().Get("occupied"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"count":2,"next":"http://x/?page=2","previous":null,"results":[{"id":1,"code":"1A1"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[{"id":2,"code":"1A2"}]}`))
	}))

	q := url.Values{"occupied": {"false"}}
	slots, err := c.ListAllStorageSlots(context.Background(), q)
	if err != nil {
		t.Fatalf("ListAllStorageSlots: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("got %d slots, want both pages", len(slots))
	}
	for i, v := range seenOccupied {
		if v != "false" {
			t.Errorf("page %d dropped the occupied filter (%q)", i+1, v)
		}
	}
	if q.Has("page") {
		t.Errorf("the caller's url.Values must not gain a page key: %v", q)
	}
}

// TestGenerateRackSlots pins the bulk payload and the idempotent report — the
// skipped/without_tag halves are the whole point of a re-runnable generator.
func TestGenerateRackSlots(t *testing.T) {
	var gotPath string
	var got GenerateRackRequest
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"rack":1,"created":["1A1","1A2"],"skipped":["1B1"],
		  "created_count":2,"skipped_count":1,"without_tag":["1A2"],
		  "slots":[{"id":1,"code":"1A1"}]}`))
	}))

	group := 3
	res, err := c.GenerateRackSlots(context.Background(), GenerateRackRequest{
		Rack:        1,
		Levels:      []RackLevelSpec{{Level: "A", Positions: 2}, {Level: "Y", Positions: 1, RequiresPalletJack: true}},
		OwningGroup: &group,
	})
	if err != nil {
		t.Fatalf("GenerateRackSlots: %v", err)
	}
	if gotPath != "/api/project-storage/slots/generate/" {
		t.Errorf("path = %q", gotPath)
	}
	if got.Rack != 1 || len(got.Levels) != 2 || got.Levels[1].Level != "Y" || !got.Levels[1].RequiresPalletJack {
		t.Errorf("request body wrong: %+v", got)
	}
	if got.OwningGroup == nil || *got.OwningGroup != 3 {
		t.Errorf("owning_group not carried: %+v", got.OwningGroup)
	}
	if res.CreatedCount != 2 || res.SkippedCount != 1 {
		t.Errorf("counts = %d/%d, want 2/1", res.CreatedCount, res.SkippedCount)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "1B1" {
		t.Errorf("skipped codes wrong: %v", res.Skipped)
	}
	if len(res.WithoutTag) != 1 || res.WithoutTag[0] != "1A2" {
		t.Errorf("without_tag wrong: %v", res.WithoutTag)
	}
}

// TestGenerateRackRequest_OmitsAbsentOptionals keeps a create clean: nothing to
// clear yet, so an unset SIG/note must not be sent at all.
func TestGenerateRackRequest_OmitsAbsentOptionals(t *testing.T) {
	buf, _ := json.Marshal(GenerateRackRequest{Rack: 1, Levels: []RackLevelSpec{{Level: "A", Positions: 1}}})
	got := string(buf)
	if strings.Contains(got, "owning_group") || strings.Contains(got, "notes") {
		t.Errorf("unset optionals must be omitted on a create: %s", got)
	}
}

// TestRenderStorageSlotCards_RackMode pins the print request and the PDF
// round trip: the response is the file itself (never a stored URL) and the
// filename comes off Content-Disposition.
func TestRenderStorageSlotCards_RackMode(t *testing.T) {
	var got SlotCardBatchRequest
	var gotPath string
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="storage_slot_cards_rack1A.pdf"`)
		_, _ = w.Write([]byte("%PDF-1.4 fake"))
	}))

	pdf, err := c.RenderStorageSlotCards(context.Background(), SlotCardsForRack(1, "a", true))
	if err != nil {
		t.Fatalf("RenderStorageSlotCards: %v", err)
	}
	if gotPath != "/api/project-storage/slots/cards/" {
		t.Errorf("path = %q", gotPath)
	}
	if got.Rack == nil || *got.Rack != 1 || got.Level != "A" || !got.IncludeInactive {
		t.Errorf("rack request wrong: %+v", got)
	}
	if len(got.SlotIDs) != 0 {
		t.Errorf("a rack print must not also send slot_ids: %+v", got.SlotIDs)
	}
	if pdf.Filename != "storage_slot_cards_rack1A.pdf" {
		t.Errorf("filename = %q", pdf.Filename)
	}
	if string(pdf.Data) != "%PDF-1.4 fake" {
		t.Errorf("pdf bytes = %q", pdf.Data)
	}
}

// TestRenderStorageSlotCards_IDsMode keeps the caller's order (a reprint of a
// handful of scuffed cards comes off the printer the way it was listed).
func TestRenderStorageSlotCards_IDsMode(t *testing.T) {
	var got SlotCardBatchRequest
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte("pdf"))
	}))
	if _, err := c.RenderStorageSlotCards(context.Background(), SlotCardsForIDs([]int{9, 3, 7})); err != nil {
		t.Fatalf("RenderStorageSlotCards: %v", err)
	}
	if len(got.SlotIDs) != 3 || got.SlotIDs[0] != 9 || got.SlotIDs[2] != 7 {
		t.Errorf("slot_ids order not preserved: %v", got.SlotIDs)
	}
	if got.Rack != nil {
		t.Errorf("an ids print must not also send a rack: %v", *got.Rack)
	}
}

// TestRenderStorageSlotCards_ModeGuard mirrors SlotCardBatchSerializer.validate
// locally — both modes or neither is a 400, so it never leaves the client.
func TestRenderStorageSlotCards_ModeGuard(t *testing.T) {
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an invalid mode must not reach the server")
	}))
	rack := 1
	if _, err := c.RenderStorageSlotCards(context.Background(), SlotCardBatchRequest{}); err == nil {
		t.Errorf("expected an error when neither mode is set")
	}
	if _, err := c.RenderStorageSlotCards(context.Background(), SlotCardBatchRequest{SlotIDs: []int{1}, Rack: &rack}); err == nil {
		t.Errorf("expected an error when both modes are set")
	}
}

// TestPreviewStorageSlotCard decodes the single-card preview envelope.
func TestPreviewStorageSlotCard(t *testing.T) {
	var gotPath string
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slot_id":7,"code":"1A1","filename":"storage_slot_1A1_card.pdf",
		  "content_type":"application/pdf","preview":"JVBERi0=",
		  "kiosk_url":"https://oms.example/project-storage/kiosk?slot=1A1","april_tag_id":41}`))
	}))

	pv, err := c.PreviewStorageSlotCard(context.Background(), "1A1")
	if err != nil {
		t.Fatalf("PreviewStorageSlotCard: %v", err)
	}
	if gotPath != "/api/project-storage/slots/1A1/card-preview/" {
		t.Errorf("path = %q", gotPath)
	}
	if pv.KioskURL == "" || pv.AprilTagID == nil || *pv.AprilTagID != 41 || pv.Preview == "" {
		t.Errorf("preview decoded wrong: %+v", pv)
	}
}

// TestStorageSlotErrorDetail pulls the human sentence out of the hand-rolled
// occupied-slot 409. Those bodies are plain Response(...) returns, so the
// backend's exception envelope never wraps them and the operator would
// otherwise be shown raw JSON.
func TestStorageSlotErrorDetail(t *testing.T) {
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"Slot 1A1 still holds stint PS-AB23CDFG (Alice Smith).",
		  "code":"slot_occupied","slot_code":"1A1","occupied_by":"PS-AB23CDFG"}`))
	}))

	err := c.DeleteStorageSlot(context.Background(), "1A1")
	if err == nil {
		t.Fatalf("expected the occupied-slot conflict to surface as an error")
	}
	detail, code := StorageSlotErrorDetail(err)
	if code != "slot_occupied" {
		t.Errorf("code = %q, want slot_occupied", code)
	}
	if !strings.Contains(detail, "PS-AB23CDFG") {
		t.Errorf("detail should name the occupant, got %q", detail)
	}

	// Anything that isn't one of those bodies falls back to nothing, so callers
	// keep using err.Error().
	if d, c2 := StorageSlotErrorDetail(nil); d != "" || c2 != "" {
		t.Errorf("nil error should yield empty detail/code, got (%q,%q)", d, c2)
	}
}

// TestStartStint_SlotCode pins the claim spelling: ScanTTY always has the code,
// so it sends `slot_code` and never the ambiguous pk-or-code `slot` (naming
// both is a 400 when they disagree). A slot-less intake must omit the key.
func TestStartStint_SlotCode(t *testing.T) {
	var got map[string]any
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"stint_id":"PS-AB23CDFG"}`))
	}))

	if _, err := c.StartProjectStorageStint(context.Background(), ProjectStorageStintStart{
		Username: "alice", SlotCode: "1A1",
	}); err != nil {
		t.Fatalf("StartProjectStorageStint: %v", err)
	}
	if got["slot_code"] != "1A1" {
		t.Errorf("slot_code = %v, want 1A1", got["slot_code"])
	}
	if _, ok := got["slot"]; ok {
		t.Errorf("the ambiguous `slot` key must not be sent alongside slot_code: %v", got)
	}

	got = nil
	if _, err := c.StartProjectStorageStint(context.Background(), ProjectStorageStintStart{Username: "bob"}); err != nil {
		t.Fatalf("StartProjectStorageStint: %v", err)
	}
	if _, ok := got["slot_code"]; ok {
		t.Errorf("an ad-hoc intake must omit slot_code entirely: %v", got)
	}
}

// TestProjectStorageStint_SlotFields decodes the three read-only slot fields
// op-hfw5 added to the stint serializer.
func TestProjectStorageStint_SlotFields(t *testing.T) {
	c, _ := newSlotTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"stint_id":"PS-AB23CDFG","username":"alice",
		  "slot":7,"slot_code":"1A1","location_display":"1A1","storage_location_name":""}`))
	}))

	st, err := c.GetProjectStorageStint(context.Background(), "PS-AB23CDFG")
	if err != nil {
		t.Fatalf("GetProjectStorageStint: %v", err)
	}
	if st.Slot == nil || *st.Slot != 7 {
		t.Errorf("slot = %v, want 7", st.Slot)
	}
	if st.SlotCode != "1A1" || st.LocationDisplay != "1A1" {
		t.Errorf("slot_code/location_display decoded wrong: %+v", st)
	}
}

// TestFilenameFromContentDisposition covers the header shapes PostBytes has to
// survive, including the ones that must NOT become a local path.
func TestFilenameFromContentDisposition(t *testing.T) {
	cases := map[string]string{
		`attachment; filename="cards.pdf"`:     "cards.pdf",
		`attachment; filename=cards.pdf`:       "cards.pdf",
		`attachment`:                           "",
		``:                                     "",
		`attachment; filename="../../etc/pwd"`: "pwd",
		`attachment; filename="/etc/passwd"`:   "passwd",
		`attachment; filename="."`:             "",
		`nonsense ;;;`:                         "",
	}
	for header, want := range cases {
		if got := filenameFromContentDisposition(header); got != want {
			t.Errorf("filenameFromContentDisposition(%q) = %q, want %q", header, got, want)
		}
	}
}
