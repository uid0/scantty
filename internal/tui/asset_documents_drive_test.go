package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Drives of the asset document library through the real key handlers, asserting
// the multipart requests actually sent.
//
// The two writes that replace a document are the interesting pair: SUPERSEDE
// keeps both rows and DELETE destroys one, and exactly one of them is confirmed.

type docFake struct {
	mu            sync.Mutex
	docs          []map[string]any
	posts         []docPost
	deletes       []string
	nextID        int
	assetTag      string
	blockNextList bool
	listStarted   chan struct{}
	releaseList   chan struct{}
}

type docPost struct {
	Path   string
	Fields map[string][]string
	Files  map[string]string
}

func (f *docFake) seenPosts() []docPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]docPost, len(f.posts))
	copy(out, f.posts)
	return out
}

func (f *docFake) seenDeletes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

func (f *docFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/asset-documents/") {
			f.mu.Lock()
			if f.blockNextList {
				f.blockNextList = false
				docs := append([]map[string]any(nil), f.docs...)
				started, release := f.listStarted, f.releaseList
				f.mu.Unlock()
				close(started)
				<-release
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"count": len(docs), "next": nil, "previous": nil, "results": docs,
				})
				return
			}
			f.mu.Unlock()
		}
		f.mu.Lock()
		defer f.mu.Unlock()

		switch {
		case r.Method == http.MethodDelete:
			f.deletes = append(f.deletes, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPost:
			fields, files := map[string][]string{}, map[string]string{}
			if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil {
				mr := multipart.NewReader(r.Body, params["boundary"])
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
			}
			f.posts = append(f.posts, docPost{r.URL.Path, fields, files})
			f.nextID++
			first := func(k string) string {
				if v := fields[k]; len(v) > 0 {
					return v[0]
				}
				return ""
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			doc := map[string]any{
				"id": fmt.Sprintf("d-%d", f.nextID), "asset": "a1",
				"title": first("title"), "category": first("category"),
				"category_display": "Manual / Documentation",
				"description":      first("description"),
				"version":          2, "is_current": true,
				"supersedes": nil, "uploaded_at": "2026-09-11T14:05:00Z",
			}
			f.docs = append(f.docs, doc)
			_ = json.NewEncoder(w).Encode(doc)

		case strings.Contains(r.URL.Path, "/asset-documents/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": len(f.docs), "next": nil, "previous": nil, "results": f.docs,
			})

		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "a1", "name": assetMeterFixtureAsset, "asset_tag": f.assetTag,
			})
		}
	}
}

func docFakeWithManual() *docFake {
	return &docFake{nextID: 1, docs: []map[string]any{{
		"id": "d-1", "asset": "a1", "title": "VF-2 Operator Manual",
		"category": "manual", "category_display": "Manual / Documentation",
		"description": "Scanned from the binder", "version": 1, "is_current": true,
		"supersedes": nil, "uploaded_at": "2026-09-11T14:05:00Z",
	}}}
}

func docDrive(t *testing.T, fake *docFake) (Root, *AssetDocumentsScreen, func()) {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewAssetDocumentsScreen(deps, "a1", assetMeterFixtureAsset)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)
	return r, screen, srv.Close
}

// docFile writes a file the upload can really read, since the screen stats and
// opens the path the operator types.
func docFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	return path
}

// THE UPLOAD IS A MULTIPART POST with the web's own field names, and the file
// part carries the BASENAME of the path the operator typed.
func TestAssetDocuments_UploadingSendsTheWebsOwnMultipart(t *testing.T) {
	fake := &docFake{}
	r, screen, done := docDrive(t, fake)
	defer done()

	path := docFile(t, "vf2-manual.txt", "rev A\n")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != assetDocPhaseUpload {
		t.Fatalf("Enter on the grid left the phase at %v, want the upload form", screen.phase)
	}
	r = meterType(t, r, path)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = meterType(t, r, "VF-2 Operator Manual")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	posts := fake.seenPosts()
	if len(posts) != 1 {
		t.Fatalf("the drive made %d posts, want 1: %+v", len(posts), posts)
	}
	got := posts[0]
	if got.Path != "/api/inventory/asset-documents/" {
		t.Fatalf("uploaded to %s, want the collection route", got.Path)
	}
	if got.Files["file"] != "vf2-manual.txt" {
		t.Errorf("file part = %v, want the basename under the field %q", got.Files, "file")
	}
	for key, want := range map[string]string{
		"asset": "a1", "title": "VF-2 Operator Manual", "category": "manual",
	} {
		if v := got.Fields[key]; len(v) != 1 || v[0] != want {
			t.Errorf("field %q = %v, want [%q]", key, v, want)
		}
	}
	if _, present := got.Fields["description"]; present {
		t.Errorf("a blank description was sent as %v; it must be omitted", got.Fields["description"])
	}
	if _, present := got.Fields["supersedes"]; present {
		t.Error("a plain upload carried a supersedes link, which would flip another document " +
			"out of the current view")
	}
}

func TestAssetDocuments_PreUploadRefreshCannotHideTheUploadedRow(t *testing.T) {
	fake := docFakeWithManual()
	r, screen, done := docDrive(t, fake)
	defer done()

	fake.mu.Lock()
	fake.blockNextList = true
	fake.listStarted = make(chan struct{})
	fake.releaseList = make(chan struct{})
	started, release := fake.listStarted, fake.releaseList
	fake.mu.Unlock()
	next, oldCmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	r = next.(Root)
	if oldCmd == nil {
		t.Fatal("refresh did not produce a load command")
	}
	oldReply := make(chan tea.Msg, 1)
	go func() { oldReply <- oldCmd() }()
	<-started
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	path := docFile(t, "new-manual.txt", "rev B\n")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, path)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = meterType(t, r, "New Operator Manual")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.loading || len(screen.docs) != 2 {
		t.Fatalf("post-upload reload left loading=%v with %d documents, want loaded with 2",
			screen.loading, len(screen.docs))
	}

	close(release)
	released = true
	next, _ = r.Update(<-oldReply)
	r = next.(Root)
	if screen.loading || len(screen.docs) != 2 {
		t.Fatalf("superseded pre-upload reply left loading=%v with %d documents",
			screen.loading, len(screen.docs))
	}
	if pane := assetFlatPane(screen, 80, 30); !strings.Contains(pane, "New Operator Manual") {
		t.Fatalf("the pre-upload snapshot hid the uploaded row:\n%s", stripANSI(pane))
	}
}

// A PATH THAT IS NOT A FILE IS REFUSED HERE and nothing is sent: the operator is
// standing on the frame with the box in front of them.
func TestAssetDocuments_ABadPathUploadsNothing(t *testing.T) {
	fake := &docFake{}
	r, screen, done := docDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, filepath.Join(t.TempDir(), "there-is-no-such-file"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = meterType(t, r, "Ghost")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if posts := fake.seenPosts(); len(posts) != 0 {
		t.Fatalf("an unreadable path was uploaded anyway: %+v", posts)
	}
	if !strings.HasPrefix(screen.errMsg, "nothing uploaded") {
		t.Errorf("the refusal reads %q; it must lead with what was NOT done", screen.errMsg)
	}
	if screen.phase != assetDocPhaseUpload {
		t.Errorf("phase = %v, want to stay on the form it can be fixed from", screen.phase)
	}
}

// SUPERSEDE POSTS TO THE OTHER ROUTE and omits what it is not changing, because
// the server inherits an omitted title from the prior document and would read an
// empty one as "this version is untitled".
func TestAssetDocuments_SupersedingPostsToItsOwnRouteAndOmitsWhatItKeeps(t *testing.T) {
	fake := docFakeWithManual()
	r, screen, done := docDrive(t, fake)
	defer done()

	if len(screen.docs) != 1 {
		t.Fatalf("the grid loaded %d documents, want 1", len(screen.docs))
	}
	path := docFile(t, "vf2-manual-revb.txt", "rev B\n")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if screen.phase != assetDocPhaseUpload || screen.supersedeOf != "d-1" {
		t.Fatalf("Ctrl-E left phase=%v supersedeOf=%q, want the form replacing d-1",
			screen.phase, screen.supersedeOf)
	}
	// The TITLE is prefilled from the document being replaced, so a replacement
	// that changes nothing keeps the identity of the version it replaces.
	if got := screen.inputs[assetDocFieldTitle].Value(); got != "VF-2 Operator Manual" {
		t.Errorf("the title row holds %q, want the prior document's", got)
	}
	r = meterType(t, r, path)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	posts := fake.seenPosts()
	if len(posts) != 1 {
		t.Fatalf("the drive made %d posts, want 1: %+v", len(posts), posts)
	}
	got := posts[0]
	if got.Path != "/api/inventory/asset-documents/d-1/supersede/" {
		t.Fatalf("superseded at %s, want .../d-1/supersede/", got.Path)
	}
	if got.Files["file"] != "vf2-manual-revb.txt" {
		t.Errorf("file part = %v", got.Files)
	}
	// The ASSET is never sent on this route: the server takes it from the prior
	// document, and sending one is how a replacement lands on another asset.
	if _, present := got.Fields["asset"]; present {
		t.Errorf("supersede sent an asset field %v; the server inherits it", got.Fields["asset"])
	}
	// A blank description is OMITTED, so the prior document's survives.
	if _, present := got.Fields["description"]; present {
		t.Errorf("supersede sent description %v with nothing to change; an empty value would "+
			"OVERWRITE the one it inherits", got.Fields["description"])
	}
	// Nothing was destroyed.
	if d := fake.seenDeletes(); len(d) != 0 {
		t.Errorf("superseding deleted something: %v — it keeps both versions", d)
	}
}

// DELETE IS CONFIRMED AND THE DESTROY IS NOT A SINGLE UNGUARDED KEYPRESS.
func TestAssetDocuments_DeletingAsksFirstAndThenDeletesTheRowItNamed(t *testing.T) {
	fake := docFakeWithManual()
	r, screen, done := docDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	if !screen.confirmingDelete {
		t.Fatal("Ctrl-X did not open a confirm")
	}
	if d := fake.seenDeletes(); len(d) != 0 {
		t.Fatalf("Ctrl-X destroyed the document outright: %v", d)
	}
	// The frame NAMES what is about to go, with its version.
	pane := assetFlatPane(screen, 80, 30)
	for _, want := range []string{"VF-2 Operator Manual", "v1", "cannot be undone"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the confirm does not say %q:\n%s", want, pane)
		}
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	deletes := fake.seenDeletes()
	if len(deletes) != 1 {
		t.Fatalf("the confirm made %d deletes, want 1: %v", len(deletes), deletes)
	}
	if deletes[0] != "/api/inventory/asset-documents/d-1/" {
		t.Errorf("deleted %s, want the highlighted document's detail route", deletes[0])
	}
}

// ESC ON THE CONFIRM DESTROYS NOTHING.
func TestAssetDocuments_BackingOutOfTheDeleteDestroysNothing(t *testing.T) {
	fake := docFakeWithManual()
	r, screen, done := docDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if screen.confirmingDelete {
		t.Error("Esc left the confirm up")
	}
	if d := fake.seenDeletes(); len(d) != 0 {
		t.Fatalf("backing out deleted something: %v", d)
	}
}

// DELETING A DOCUMENT ANOTHER VERSION REPLACES SAYS SO. The server's FK is
// SET_NULL, so the later version keeps its row and silently loses the link, and
// the gap is recorded nowhere — which an operator cannot know from the row.
func TestAssetDocuments_TheDeleteConfirmWarnsWhenItBreaksAChain(t *testing.T) {
	screen := assetDocumentsFixture()
	screen.setSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	// The LAST fixture row is the superseded v1 that the current v2 points at.
	screen.cursor = len(screen.docs) - 1
	if !screen.deleteBreaksChain() {
		t.Fatalf("the fixture's row %d is not superseded by anything, so this test measures "+
			"nothing about a broken chain", screen.cursor)
	}
	screen.confirmingDelete = true
	pane := assetFlatPane(screen, 80, 30)
	if !strings.Contains(pane, "nothing to point back at") {
		t.Errorf("the confirm does not warn that the chain breaks:\n%s", pane)
	}

	// And a row NOTHING supersedes does not carry that warning: a caveat said
	// where it is untrue is the same defect as silence where it is true.
	screen.cursor = 0
	if screen.deleteBreaksChain() {
		t.Fatalf("the fixture's row 0 IS superseded by something; pick a different row")
	}
	pane = assetFlatPane(screen, 80, 30)
	if strings.Contains(pane, "nothing to point back at") {
		t.Errorf("a document nothing replaces claimed a broken chain:\n%s", pane)
	}
}

// A SUPERSEDED VERSION IS SHOWN AND MARKED rather than filtered out, because a
// row that says "superseded" stops somebody following a stale manual where a
// filtered-out row states nothing at all.
func TestAssetDocuments_ASupersededVersionIsMarkedOnThePane(t *testing.T) {
	screen := assetDocumentsFixture()
	screen.setSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	pane := assetFlatPane(screen, 80, 30)
	if !strings.Contains(pane, "superseded") {
		t.Errorf("the library does not mark its superseded version:\n%s", pane)
	}
	if !strings.Contains(pane, "v2") || !strings.Contains(pane, "v1") {
		t.Errorf("the library does not show both versions:\n%s", pane)
	}
	// The list asks for EVERY document, superseded ones included — the whole
	// point of showing them.
	var superseded bool
	for _, d := range screen.docs {
		superseded = superseded || !d.IsCurrent
	}
	if !superseded {
		t.Fatal("the fixture carries no superseded row, so this test measures nothing")
	}
}
