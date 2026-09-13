package tui

import (
	"context"
	"encoding/json"
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

// The terminal's adoption of the supplier-link version token (OMS #1091): every
// write states the version its copy was loaded at, and a refusal is put in
// front of the operator with a reload rather than retried.
//
// EVERY OMS BODY HERE IS RECORDED (internal/omsapi/testdata/README.md carries
// the session that produced them, in order): the link as created at version 1
// with a lead time of 7, the refusal once somebody else had saved a quote of 12
// over it, the link as it then stood at version 2, and the save that landed at
// version 3. So the drive below is that session replayed from the terminal's
// side, and the versions it asserts are the server's, not numbers written here.

const (
	supLinkCreated      = "supplier_link_version_created.json"
	supLinkDetail       = "supplier_link_version_detail.json"
	supLinkList         = "supplier_link_version_item_suppliers.json"
	supLinkPatched      = "supplier_link_version_patch.json"
	supLinkGone         = "supplier_link_version_detail_gone.json"
	supLinkStalePatch   = "supplier_link_stale_version_patch.json"
	supLinkStaleDeleted = "supplier_link_stale_version_deleted.json"
	supLinkStalePrimary = "supplier_link_stale_version_set_primary.json"
	supLinkStaleDelete  = "supplier_link_stale_version_delete.json"
)

func supLinkWire(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

// supLinkReply is one scripted answer: a recorded body at the status it came
// back with.
type supLinkReply struct {
	status int
	file   string
}

// supLinkFake answers the supplier-link routes from recorded bodies, one
// scripted reply per method in order, and records every request the terminal
// made — which is how a test proves that a key sent NOTHING.
type supLinkFake struct {
	t       *testing.T
	mu      sync.Mutex
	replies map[string][]supLinkReply // by method
	seen    []supLinkRequest
}

type supLinkRequest struct {
	method, path, query string
	body                map[string]any
}

func (f *supLinkFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/inventory/suppliers/" {
			// The supplier PICKER's list, which the form loads to name the link's
			// supplier. It is not the seam under test and carries no version.
			_, _ = w.Write([]byte(`{"count":3,"next":null,"results":[` +
				`{"id":1,"name":"Grainger"},{"id":2,"name":"Fastenal Company"},{"id":3,"name":"McMaster-Carr"}]}`))
			return
		}
		req := supLinkRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req.body)
		}
		f.seen = append(f.seen, req)
		queue := f.replies[r.Method]
		if len(queue) == 0 {
			f.t.Errorf("unscripted %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		next := queue[0]
		f.replies[r.Method] = queue[1:]
		w.WriteHeader(next.status)
		_, _ = w.Write(supLinkWire(f.t, next.file))
	}
}

func (f *supLinkFake) requests() []supLinkRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]supLinkRequest(nil), f.seen...)
}

// supLinkRecorded decodes one recorded link through the real client.
func supLinkRecorded(t *testing.T, name string) *omsapi.ItemSupplier {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(supLinkWire(t, name))
	}))
	defer srv.Close()
	link, err := omsapi.New(srv.URL).GetItemSupplier(context.Background(), 0)
	if err != nil {
		t.Fatalf("%s did not decode: %v", name, err)
	}
	return link
}

// supLinkFormDrive opens the edit form on `loaded` through Root at 80x24 — the
// width the size contract floors at, where every row this work adds is tightest.
func supLinkFormDrive(t *testing.T, fake *supLinkFake, loaded *omsapi.ItemSupplier) (Root, *ItemSupplierFormScreen) {
	t.Helper()
	fake.t = t
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewItemSupplierFormScreen(deps, loaded.Item, loaded.ItemName, loaded)
	r := newTestRoot(screen)
	r.deps = deps
	r = pump(t, r, screen.Init(), 0)
	r = pump(t, r, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }, 0)
	if screen.loading {
		t.Fatalf("the form never finished loading its suppliers")
	}
	return r, screen
}

func supLinkTypeSKU(t *testing.T, r Root, s *ItemSupplierFormScreen, suffix string) Root {
	t.Helper()
	itemSupplierCursorOn(t, s, isSKU)
	s.syncFocus()
	for _, ch := range suffix {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	return r
}

func supLinkPane(r Root) string { return stripANSI(r.View()) }

// THE DEFECT, CLOSED. The terminal opens the link at version 1; somebody else
// saves a quote of 12 over it; the terminal's edit of the SKU is refused rather
// than writing the 7 it loaded back over the 12. The operator is told, Enter
// sends nothing further, and Ctrl-R — the reload — puts the link as stored in
// front of them, after which their save carries THAT copy's version.
func TestItemSupplierForm_AStaleSaveIsRefusedShownAndReloaded(t *testing.T) {
	loaded := supLinkRecorded(t, supLinkCreated)
	fake := &supLinkFake{replies: map[string][]supLinkReply{
		http.MethodPatch: {{http.StatusConflict, supLinkStalePatch}, {http.StatusOK, supLinkPatched}},
		// The reload, then the supplier list a landed save returns to.
		http.MethodGet: {{http.StatusOK, supLinkDetail}, {http.StatusOK, supLinkList}},
	}}
	r, s := supLinkFormDrive(t, fake, loaded)

	r = supLinkTypeSKU(t, r, s, "-B")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	reqs := fake.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPatch {
		t.Fatalf("enter sent %+v, want one PATCH", reqs)
	}
	if got := reqs[0].body["supplier_sku"]; got != "4NUE9-B" {
		t.Fatalf("the save sent SKU %v, so the typing never reached the form", got)
	}
	if got := reqs[0].body["version"]; got != float64(loaded.Version) || loaded.Version != 1 {
		t.Fatalf("the save stated version %v; the copy was loaded at %d (recorded as 1)", got, loaded.Version)
	}
	if s.stale == nil {
		t.Fatalf("the recorded 409 did not reach the form as a stale refusal (errMsg %q)", s.errMsg)
	}

	pane := supLinkPane(r)
	var refusal struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(supLinkWire(t, supLinkStalePatch), &refusal); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		itemSupplierStaleChangedNote,          // the status row: what must survive, whole at 80 columns
		"Someone else changed this",           // the server's sentence, pinned above the form
		"Ctrl-R=Reload",                       // the way on, on the bar
		"Reloading replaces everything typed", // the cost of the way on, before it is paid
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("the refused form at 80x24 does not show %q:\n%s", want, pane)
		}
	}
	if strings.Contains(jdeBarLine(stripANSI(s.View())), "Enter=Save") {
		t.Errorf("the bar still offers a save that can only be refused again:\n%s", pane)
	}
	// The bottom line carries the server's sentence as ONE line, marked where
	// the 80-column bar cuts it (StatusBar.View).
	bottom := strings.TrimSpace(jdeBarLine(pane))
	head := strings.TrimSuffix(bottom, "…")
	if head == bottom || !strings.HasPrefix(refusal.Error.Message, strings.TrimSpace(head)) {
		t.Errorf("the bottom line should be the server's sentence, cut and marked; got %q", bottom)
	}

	// Enter sends NOTHING, and the press is answered.
	before := supLinkPane(r)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if n := len(fake.requests()); n != 1 {
		t.Fatalf("enter on a refused copy sent another request (%d total)", n)
	}
	if after := supLinkPane(r); after == before || !strings.Contains(after, itemSupplierStaleEnterNote) {
		t.Errorf("enter on the refused copy changed nothing visible:\n%s", after)
	}
	if typed := s.inputs[isSKU].Value(); typed != "4NUE9-B" {
		t.Fatalf("the refusal lost what was typed: SKU box holds %q", typed)
	}

	// Ctrl-R re-reads the link and replaces the copy.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	reqs = fake.requests()
	if len(reqs) != 2 || reqs[1].method != http.MethodGet || !strings.HasSuffix(reqs[1].path, "/item-suppliers/4/") {
		t.Fatalf("ctrl+r sent %+v, want a GET of the link", reqs[len(reqs)-1])
	}
	if s.stale != nil || s.errMsg != "" {
		t.Fatalf("the reload left the refusal standing: stale=%v errMsg=%q", s.stale, s.errMsg)
	}
	if got := s.inputs[isLeadTime].Value(); got != "12" {
		t.Errorf("the reload should show the other writer's quote of 12, lead box holds %q", got)
	}
	if got := s.inputs[isSKU].Value(); got != "4NUE9" {
		t.Errorf("the reload should replace the typed SKU with the stored one, got %q", got)
	}
	pane = supLinkPane(r)
	if !strings.Contains(pane, "Enter=Save") || strings.Contains(pane, "Ctrl-R") ||
		strings.Contains(pane, "Someone else changed") {
		t.Errorf("after the reload the form should be an ordinary edit again:\n%s", pane)
	}

	// The operator makes their change again; the save states the RELOADED copy.
	r = supLinkTypeSKU(t, r, s, "-B")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	reqs = fake.requests()
	if len(reqs) < 3 || reqs[2].method != http.MethodPatch || reqs[2].body["supplier_sku"] != "4NUE9-B" {
		t.Fatalf("the second save did not go out as the third request: %+v", reqs)
	}
	fresh := supLinkRecorded(t, supLinkDetail)
	if got := reqs[2].body["version"]; got != float64(fresh.Version) {
		t.Errorf("the second save stated version %v, the reloaded copy is at %d", got, fresh.Version)
	}
	if _, present := reqs[2].body["average_lead_time"]; present {
		t.Errorf("the second save restated the other writer's lead time: %v", reqs[2].body["average_lead_time"])
	}
	if _, ok := r.screen.(*ItemSuppliersScreen); !ok {
		t.Errorf("a landed save should return to the supplier list, on %T", r.screen)
	}
}

// A link DELETED since the form opened has no current copy to reload into it,
// so Ctrl-R is the item's supplier list — which is what the server's own
// sentence asks for — and it is reached without a read of the link.
func TestItemSupplierForm_AStaleSaveOnADeletedLinkReloadsTheList(t *testing.T) {
	loaded := supLinkRecorded(t, supLinkCreated)
	fake := &supLinkFake{replies: map[string][]supLinkReply{
		http.MethodPatch: {{http.StatusConflict, supLinkStaleDeleted}},
		http.MethodGet:   {{http.StatusOK, supLinkList}},
	}}
	r, s := supLinkFormDrive(t, fake, loaded)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.stale == nil || !s.stale.Deleted() {
		t.Fatalf("the recorded deleted-link refusal did not reach the form: %+v", s.stale)
	}
	pane := supLinkPane(r)
	for _, want := range []string{itemSupplierStaleDeletedNote, "This supplier link was deleted", "Ctrl-R=Reload"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the refused form does not show %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "Reloading replaces everything typed") {
		t.Errorf("there is no copy to reload into the form, so no caveat about replacing it:\n%s", pane)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	list, ok := r.screen.(*ItemSuppliersScreen)
	if !ok {
		t.Fatalf("ctrl+r on a deleted link should open the supplier list, on %T", r.screen)
	}
	reqs := fake.requests()
	if last := reqs[len(reqs)-1]; last.method != http.MethodGet || !strings.HasSuffix(last.path, "/item-suppliers/") {
		t.Errorf("the reload should be the item's supplier list, last request %+v", last)
	}
	if list.loading || len(list.rows) == 0 {
		t.Errorf("the list did not load (loading=%v rows=%d)", list.loading, len(list.rows))
	}
}

// The link can also go between the refusal and the reload. The read then says
// so, and the list is the only current copy left.
func TestItemSupplierForm_AReloadThatFindsTheLinkGoneOpensTheList(t *testing.T) {
	loaded := supLinkRecorded(t, supLinkCreated)
	fake := &supLinkFake{replies: map[string][]supLinkReply{
		http.MethodPatch: {{http.StatusConflict, supLinkStalePatch}},
		http.MethodGet:   {{http.StatusNotFound, supLinkGone}, {http.StatusOK, supLinkList}},
	}}
	r, _ := supLinkFormDrive(t, fake, loaded)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	if _, ok := r.screen.(*ItemSuppliersScreen); !ok {
		t.Fatalf("a reload answered 404 should open the supplier list, on %T", r.screen)
	}
	if bottom := supLinkPane(r); !strings.Contains(bottom, "that supplier link was deleted") {
		t.Errorf("the bottom line should say the link went:\n%s", bottom)
	}
}

// Ctrl-R is the refusal's key and nobody else's: on an ordinary form it is
// neither named nor sends anything.
func TestItemSupplierForm_CtrlRIsOnlyTheRefusalsKey(t *testing.T) {
	loaded := supLinkRecorded(t, supLinkCreated)
	fake := &supLinkFake{replies: map[string][]supLinkReply{}}
	r, s := supLinkFormDrive(t, fake, loaded)
	before := supLinkPane(r)
	if strings.Contains(before, "Ctrl-R") {
		t.Errorf("an ordinary form names Ctrl-R:\n%s", before)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	if n := len(fake.requests()); n != 0 || s.reloading {
		t.Errorf("ctrl+r on an ordinary form sent %d request(s), reloading=%v", n, s.reloading)
	}
	_ = r
}

// supLinkListDrive opens the supplier list on the recorded rows at 80x24.
func supLinkListDrive(t *testing.T, fake *supLinkFake) (Root, *ItemSuppliersScreen) {
	t.Helper()
	fake.t = t
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewItemSuppliersScreen(deps, "7db5e37f-443d-4297-b678-f9746ac74d66", "Hex nut M8 zinc")
	r := newTestRoot(screen)
	r.deps = deps
	r = pump(t, r, screen.Init(), 0)
	r = pump(t, r, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }, 0)
	if screen.loading || len(screen.rows) == 0 {
		t.Fatalf("the list did not load")
	}
	return r, screen
}

// The list's two writes state the version of the row they act on, and a
// refusal of either stands on the pane — the bottom line is a flash — until a
// reload lands.
func TestItemSuppliers_EveryWriteStatesItsRowsVersionAndARefusalStandsUntilReloaded(t *testing.T) {
	cases := []struct {
		name   string
		refuse string
		press  []tea.KeyMsg
		sent   func(t *testing.T, req supLinkRequest) float64
	}{
		{
			name: "set primary", refuse: supLinkStalePrimary,
			press: []tea.KeyMsg{rune1("p")},
			sent: func(t *testing.T, req supLinkRequest) float64 {
				t.Helper()
				if req.method != http.MethodPatch || req.body["is_primary"] != true {
					t.Errorf("p sent %+v, want a PATCH of is_primary", req)
				}
				v, _ := req.body["version"].(float64)
				return v
			},
		},
		{
			name: "remove", refuse: supLinkStaleDelete,
			press: []tea.KeyMsg{rune1("x"), rune1("y")},
			sent: func(t *testing.T, req supLinkRequest) float64 {
				t.Helper()
				if req.method != http.MethodDelete {
					t.Errorf("x/y sent %s, want DELETE", req.method)
				}
				var v float64
				if q := strings.TrimPrefix(req.query, "version="); q != req.query {
					_ = json.Unmarshal([]byte(q), &v)
				}
				return v
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &supLinkFake{replies: map[string][]supLinkReply{
				http.MethodGet:    {{http.StatusOK, supLinkList}, {http.StatusOK, supLinkList}},
				http.MethodPatch:  {{http.StatusConflict, tc.refuse}},
				http.MethodDelete: {{http.StatusConflict, tc.refuse}},
			}}
			r, s := supLinkListDrive(t, fake)
			// Stand on a link that is not the primary one, so `p` writes.
			for s.cursor < len(s.rows) && s.rows[s.cursor].IsPreferred {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
			}
			row := s.rows[s.cursor]
			if row.Version < 1 {
				t.Fatalf("the recorded row carries no version, so nothing is proved")
			}
			for _, k := range tc.press {
				r = key(t, r, k)
			}
			reqs := fake.requests()
			write := reqs[len(reqs)-1]
			if got := tc.sent(t, write); got != float64(row.Version) {
				t.Errorf("the write stated version %v, the row was loaded at %d", got, row.Version)
			}
			if s.stale == nil {
				t.Fatalf("the recorded refusal did not reach the list")
			}
			pane := supLinkPane(r)
			if !strings.Contains(pane, suppliersStaleNote) {
				t.Errorf("the refused list does not keep the fact on the pane:\n%s", pane)
			}
			if !strings.Contains(pane, "r refresh") {
				t.Errorf("the key the note names is not on the bar:\n%s", pane)
			}
			if n := len(fake.requests()); n != len(reqs) {
				t.Errorf("the refusal was retried")
			}
			r = key(t, r, rune1("r"))
			if s.stale != nil || strings.Contains(supLinkPane(r), suppliersStaleNote) {
				t.Errorf("a landed reload left the refusal standing:\n%s", supLinkPane(r))
			}
		})
	}
}

// itemSupplierStaleFixture is the edit form standing on a stale refusal, for
// the derived pane sweeps (jdeScreenStates), which build their states without a
// *testing.T. Both inputs are RECORDED: the link as loaded, and the refusal —
// decoded into the envelope parseError produces from the same bytes.
func itemSupplierStaleFixture(refusal string) *ItemSupplierFormScreen {
	var link omsapi.ItemSupplier
	if err := json.Unmarshal(supLinkWireOrPanic(supLinkCreated), &link); err != nil {
		panic(err)
	}
	s := NewItemSupplierFormScreen(Deps{}, link.Item, link.ItemName, &link)
	s.Update(itemSupplierFormSuppliersMsg{suppliers: []omsapi.Supplier{{ID: 1, Name: "Grainger"}}})
	s.saving = true
	s.Update(itemSupplierSavedMsg{err: supLinkStaleErr(refusal)})
	if s.stale == nil {
		panic("the recorded refusal did not reach the form as a stale refusal")
	}
	return s
}

// supLinkStaleErr is a recorded 409 body as the error the client returns for
// it: the envelope parseError decodes, at the status it was recorded with.
func supLinkStaleErr(refusal string) error {
	var envelope struct {
		Error omsapi.APIError `json:"error"`
	}
	if err := json.Unmarshal(supLinkWireOrPanic(refusal), &envelope); err != nil {
		panic(err)
	}
	envelope.Error.Status = http.StatusConflict
	if _, ok := omsapi.AsStaleSupplierLink(&envelope.Error); !ok {
		panic(refusal + " is not a stale_version refusal")
	}
	return &envelope.Error
}

func supLinkWireOrPanic(name string) []byte {
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		panic(err)
	}
	return b
}
