package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Decode and request-shape tests for the asset document library, built from
// RECORDED OMS responses (testdata/README.md carries the rule).

// multipartParts reads an uploaded request back into its text fields and its
// file parts, so a test asserts what the SERVER would see rather than what the
// client meant to send.
func multipartParts(t *testing.T, r *http.Request) (map[string][]string, map[string]string) {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("content type %q: %v", r.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	fields := map[string][]string{}
	files := map[string]string{}
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(p)
		if p.FileName() != "" {
			files[p.FormName()] = p.FileName()
			continue
		}
		fields[p.FormName()] = append(fields[p.FormName()], string(body))
	}
	return fields, files
}

func docServer(t *testing.T, fixture string, capture func(*http.Request)) *Client {
	t.Helper()
	body := wireBody(t, fixture)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture(r)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// THE DECODE, and the version chain with it: the recorded list carries a
// superseded v1 beside its current v2, which is the pair the grid has to mark.
func TestListAssetDocuments_DecodesTheBytesOMSReallySends(t *testing.T) {
	body := wireBody(t, "asset_documents_list.json")
	c := docServer(t, "asset_documents_list.json", nil)
	docs, err := c.ListAssetDocuments(context.Background(), "0289121e-7524-46f8-8dc6-8c3c5337e28a")
	if err != nil {
		t.Fatalf("a recorded OMS reply did not decode: %v", err)
	}

	var raw struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if len(docs) != raw.Count {
		t.Fatalf("decoded %d documents, want %d", len(docs), raw.Count)
	}
	for i, want := range raw.Results {
		got := docs[i]
		if v, _ := want["id"].(string); got.ID != v {
			t.Errorf("doc %d id = %q, want %q", i, got.ID, v)
		}
		if v, _ := want["title"].(string); got.Title != v {
			t.Errorf("doc %d title = %q, want %q", i, got.Title, v)
		}
		if v, _ := want["version"].(float64); got.Version != int(v) {
			t.Errorf("doc %d version = %d, want %d", i, got.Version, int(v))
		}
		if v, _ := want["is_current"].(bool); got.IsCurrent != v {
			t.Errorf("doc %d is_current = %v, want %v", i, got.IsCurrent, v)
		}
		if v, _ := want["category_display"].(string); got.CategoryDisplay != v {
			t.Errorf("doc %d category_display = %q, want %q", i, got.CategoryDisplay, v)
		}
	}

	// SUPERSEDED AND FIRST-VERSION ARE BOTH PRESENT, or "the grid marks a stale
	// manual" is a claim nothing in this fixture exercises.
	var current, superseded, linked bool
	for _, d := range docs {
		current = current || d.IsCurrent
		superseded = superseded || !d.IsCurrent
		linked = linked || (d.Supersedes != nil && *d.Supersedes != "")
	}
	if !current || !superseded || !linked {
		t.Errorf("the recorded library reaches current=%v superseded=%v linked=%v; all three "+
			"must be present or the version chain is unexercised", current, superseded, linked)
	}
}

// `supersedes` is a POINTER because null means "this is version 1" and a plain
// string would flatten that into the empty string a deleted predecessor also
// leaves behind.
func TestAssetDocumentFixture_CarriesTheServersOwnTypes(t *testing.T) {
	var raw struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(wireBody(t, "asset_documents_list.json"), &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if len(raw.Results) == 0 {
		t.Fatal("fixture has no documents — it would prove nothing about a document's types")
	}
	var sawNull, sawString bool
	for i, d := range raw.Results {
		if _, isString := d["id"].(string); !isString {
			t.Errorf("results[%d].id is %T, want a JSON string — AssetDocument's pk is a UUIDField", i, d["id"])
		}
		if _, isNumber := d["version"].(float64); !isNumber {
			t.Errorf("results[%d].version is %T, want a JSON number (PositiveIntegerField)", i, d["version"])
		}
		switch d["supersedes"].(type) {
		case nil:
			sawNull = true
		case string:
			sawString = true
		default:
			t.Errorf("results[%d].supersedes is %T, want a JSON string or null", i, d["supersedes"])
		}
	}
	if !sawNull || !sawString {
		t.Errorf("supersedes reaches null=%v string=%v; both are needed or the pointer is "+
			"holding a distinction nothing tests", sawNull, sawString)
	}
}

// THE UPLOAD IS A MULTIPART REQUEST AND THE FIELD NAMES ARE THE CONTRACT.
func TestUploadAssetDocument_SendsTheWebsOwnMultipart(t *testing.T) {
	var fields map[string][]string
	var files map[string]string
	var path, method string
	c := docServer(t, "asset_document_upload.json", func(r *http.Request) {
		path, method = r.URL.Path, r.Method
		fields, files = multipartParts(t, r)
	})

	doc, err := c.UploadAssetDocument(context.Background(),
		"asset-1", "Soft-jaw fixture plate", "cut_ready_template", "",
		"fixture-plate.nc", strings.NewReader("G0 X0 Y0\n"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if method != http.MethodPost || path != "/api/inventory/asset-documents/" {
		t.Errorf("upload went %s %s, want POST /api/inventory/asset-documents/", method, path)
	}
	if files["file"] != "fixture-plate.nc" {
		t.Errorf("file part = %q under field %v, want the basename under field \"file\"",
			files["file"], files)
	}
	for key, want := range map[string]string{
		"asset": "asset-1", "title": "Soft-jaw fixture plate", "category": "cut_ready_template",
	} {
		if got := fields[key]; len(got) != 1 || got[0] != want {
			t.Errorf("field %q = %v, want [%q]", key, got, want)
		}
	}
	// A blank description is OMITTED, not sent empty.
	if got, present := fields["description"]; present {
		t.Errorf("upload sent description %v with nothing to describe; blank must be omitted", got)
	}
	if doc.Version != 1 || !doc.IsCurrent {
		t.Errorf("a fresh upload decoded as v%d current=%v, want v1 current", doc.Version, doc.IsCurrent)
	}
}

// SUPERSEDE OMITS WHAT IT DOES NOT CHANGE, and that is not tidiness: the server
// does `data.setdefault(...)` from the PRIOR document, so an omitted title
// INHERITS and an empty one would make the new version untitled.
func TestSupersedeAssetDocument_OmitsWhatItDoesNotChange(t *testing.T) {
	var fields map[string][]string
	var files map[string]string
	var path string
	c := docServer(t, "asset_document_supersede.json", func(r *http.Request) {
		path = r.URL.Path
		fields, files = multipartParts(t, r)
	})

	doc, err := c.SupersedeAssetDocument(context.Background(), "doc-1", "", "", "",
		"vf2-manual-revb.txt", strings.NewReader("rev B\n"))
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if want := "/api/inventory/asset-documents/doc-1/supersede/"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if files["file"] != "vf2-manual-revb.txt" {
		t.Errorf("file part = %q, want the replacement's basename", files["file"])
	}
	for _, key := range []string{"title", "category", "description", "asset"} {
		if got, present := fields[key]; present {
			t.Errorf("supersede sent %q = %v with nothing to change; the server inherits it "+
				"from the prior document and an empty value would OVERWRITE instead", key, got)
		}
	}

	// The recorded reply is the version bump and the back-link, which is what
	// tells the operator the chain held.
	if doc.Version < 2 {
		t.Errorf("superseded document decoded as v%d, want the server's bump", doc.Version)
	}
	if doc.Supersedes == nil || *doc.Supersedes == "" {
		t.Error("superseded document carries no back-link, so the chain it belongs to is lost")
	}
	if doc.SupersedesTitle == "" {
		t.Error("supersedes_title was dropped; it is what names the version this one replaced")
	}
}

// A supersede that DOES change the title sends it, so the omission above is a
// property of a blank value rather than of the call.
func TestSupersedeAssetDocument_SendsWhatItDoesChange(t *testing.T) {
	var fields map[string][]string
	c := docServer(t, "asset_document_supersede.json", func(r *http.Request) {
		fields, _ = multipartParts(t, r)
	})
	if _, err := c.SupersedeAssetDocument(context.Background(), "doc-1",
		"VF-2 Operator Manual", "manual", "Rev B", "x.txt", strings.NewReader("x")); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	for key, want := range map[string]string{
		"title": "VF-2 Operator Manual", "category": "manual", "description": "Rev B",
	} {
		if got := fields[key]; len(got) != 1 || got[0] != want {
			t.Errorf("field %q = %v, want [%q]", key, got, want)
		}
	}
}

func TestDeleteAssetDocument_HitsTheDetailRoute(t *testing.T) {
	var path, method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, method = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := New(srv.URL).DeleteAssetDocument(context.Background(), "doc-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if method != http.MethodDelete || path != "/api/inventory/asset-documents/doc-1/" {
		t.Errorf("delete went %s %s, want DELETE /api/inventory/asset-documents/doc-1/", method, path)
	}
}

// AssetDocumentCategories is transcribed from OMS's own TextChoices, and a value
// outside that set is a 400 the operator cannot fix from the picker. This pins
// the VALUES against the categories the recorded bodies actually carry.
func TestAssetDocumentCategories_CoverTheRecordedOnes(t *testing.T) {
	known := map[string]string{}
	for _, c := range AssetDocumentCategories() {
		known[c.Value] = c.Label
		if c.Label == "" {
			t.Errorf("category %q has no label; a picker row has to show something", c.Value)
		}
	}
	var raw struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(wireBody(t, "asset_documents_list.json"), &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	for _, d := range raw.Results {
		v, _ := d["category"].(string)
		if _, ok := known[v]; !ok {
			t.Errorf("the server served category %q, which AssetDocumentCategories does not "+
				"offer — a document uploaded from the terminal could not be given it", v)
		}
	}
}
