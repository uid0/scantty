package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/uid0/scantty/internal/omsapi"
)

// The reorder form's three outcomes, driven through Root against the REAL
// response bodies POST /api/reorders/requests/ sends.
//
// WHY THIS SCREEN NEEDED A TEST AT ALL. It used to report every non-error reply
// as "reorder #N created". OMS now files no second ANONYMOUS request for an item
// while one is still pending and answers the duplicate with a 200 carrying the
// EXISTING request — a success, so the old wording would have told an operator a
// reorder was created when none was, and somebody told "created" twice can
// reasonably believe two requests exist.
//
// SCANTTY LANDS BEFORE THE SERVER CHANGE, so the interesting case is not the new
// shape but the OLD one: reorderCreatePreRule is a recording off remote main,
// where a second scan really did file a second row, and the screen must go on
// saying exactly what it says today against it.
//
// EVERY BODY IS RECORDED, never written: internal/omsapi/testdata/README.md
// carries the provenance and the reason. A fake built from ScanTTY's own struct
// would carry whatever key the struct declares and could not disagree with it.

const (
	reorderCreateFiled     = "reorder_create_filed.json"
	reorderCreateDuplicate = "reorder_create_already_requested.json"
	reorderCreatePreRule   = "reorder_create_pre_rule.json"
	reorderCreateRefused   = "reorder_create_validation_failed.json"
)

// reorderCreateWire reads a recorded create response.
func reorderCreateWire(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "omsapi", "testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

// driveReorderSubmit fills a quantity in, submits, and returns the CLIPPED frame
// at 80 columns — the width the size contract floors at and the one that decides
// whether a result line survives whole.
//
// It goes through Root rather than the screen because clampToBox truncates in
// Root.View: a check reading the screen's own View passes while the terminal
// shows a cut line.
func driveReorderSubmit(t *testing.T, status int, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	item := &omsapi.Item{ID: "650681b1-f9b6-4410-be93-351e76c718d3", Name: "Blue nitrile gloves (M)", SKU: "GLV-NIT-M"}
	screen := NewReorderFormScreen(deps, item, nil)
	r := newTestRoot(screen)
	r.deps = deps
	r = pump(t, r, screen.Init(), 0)
	r = pump(t, r, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }, 0)

	// The quantity box has no default without a supplier pack size, and an empty
	// one is refused before anything reaches the wire.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	// Enter walks the four fields and submits off the last one.
	for i := 0; i < 4; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	}
	return r.View()
}

// FILED reads as filed. The 201 carrying `already_requested: false` is the
// ordinary outcome and its wording is unchanged.
func TestReorderForm_AFiledRequestReportsAsCreated(t *testing.T) {
	view := driveReorderSubmit(t, http.StatusCreated, reorderCreateWire(t, reorderCreateFiled))
	if !strings.Contains(view, "reorder #1200041 created") {
		t.Fatalf("the filed reply did not report as created:\n%s", view)
	}
}

// A DUPLICATE READS AS ALREADY REQUESTED — not as created, and not as a failure.
// This is the whole point of the change: the 200 is a success, so before it the
// screen said a reorder was created when the server had created nothing.
func TestReorderForm_ADuplicateReportsAsAlreadyRequested(t *testing.T) {
	view := driveReorderSubmit(t, http.StatusOK, reorderCreateWire(t, reorderCreateDuplicate))
	if strings.Contains(view, "created") {
		t.Errorf("the duplicate reply says something was created; nothing was:\n%s", view)
	}
	if !strings.Contains(view, "already requested") {
		t.Errorf("the duplicate reply does not say the need is already requested:\n%s", view)
	}
	// The id echoed back is the EXISTING pending request's, and naming it is what
	// makes the answer actionable rather than a shrug.
	if !strings.Contains(view, "#1200041 is still pending") {
		t.Errorf("the duplicate reply does not name the pending request:\n%s", view)
	}
	if strings.Contains(strings.ToLower(view), "failed") {
		t.Errorf("the duplicate reply reads as a failure; the need IS recorded:\n%s", view)
	}
}

// AGAINST A SERVER THAT DOES NOT SEND THE MARKER THE SCREEN IS UNCHANGED, which
// is what lets ScanTTY land first. The body here is the SECOND anonymous scan
// against remote main: it really did file a second row, and reporting it as
// created is the truth.
func TestReorderForm_AServerWithoutTheMarkerBehavesExactlyAsBefore(t *testing.T) {
	body := reorderCreateWire(t, reorderCreatePreRule)
	if strings.Contains(string(body), "already_requested") {
		t.Fatal("the pre-rule recording carries the marker; it cannot then prove " +
			"anything about a server that does not send one")
	}
	view := driveReorderSubmit(t, http.StatusCreated, body)
	if !strings.Contains(view, "reorder #1200042 created") {
		t.Fatalf("an older OMS's filed request stopped reporting as created:\n%s", view)
	}
	if strings.Contains(view, "already requested") {
		t.Errorf("an absent marker was read as a duplicate:\n%s", view)
	}
}

// COULD NOT TELL stays its own outcome. A refusal must not be softened into
// either success — the operator has to know the need is NOT on file.
func TestReorderForm_ARefusalIsNeitherSuccess(t *testing.T) {
	view := driveReorderSubmit(t, http.StatusBadRequest, reorderCreateWire(t, reorderCreateRefused))
	if !strings.Contains(view, "submit failed") {
		t.Fatalf("a 400 did not report as a failure:\n%s", view)
	}
	if strings.Contains(view, "created") || strings.Contains(view, "already requested") {
		t.Errorf("a refusal was reported as one of the two successes:\n%s", view)
	}
}

// THE THREE OUTCOMES ARE DISTINGUISHABLE BY COLOUR AS WELL AS BY WORDS, and an
// operator reads the colour first.
//
// lipgloss strips every escape when stdout is not a TTY, which it never is in a
// test binary, so a lost highlight and a present one are byte-identical unless
// the profile is forced — the rule AGENTS.md records. Forced, each level's own
// rendering of the line is what the pane must contain, which pins the LEVEL
// without this test keeping a second copy of the palette.
func TestReorderForm_TheThreeOutcomesAreToldApartByColour(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	cases := []struct {
		name   string
		status int
		body   string
		line   string
		level  StatusLevel
	}{
		{"filed", http.StatusCreated, reorderCreateFiled, "reorder #1200041 created", StatusOK},
		{"already requested", http.StatusOK, reorderCreateDuplicate, "already requested — #1200041 is still pending", StatusWarn},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		view := driveReorderSubmit(t, tc.status, reorderCreateWire(t, tc.body))
		want := RenderStatus(tc.line, tc.level)
		if !strings.Contains(view, want) {
			t.Errorf("%s: the pane does not draw %q at its own level; a duplicate drawn "+
				"in the created colour is the report this work exists to fix:\n%s",
				tc.name, tc.line, view)
		}
		seen[tc.name] = want
	}
	if seen["filed"] == seen["already requested"] {
		t.Error("the two successes render identically, so nothing on the pane tells " +
			"an operator whether a request was filed")
	}
	// StatusWarn is not StatusInfo on purpose: StatusInfo is the MUTED colour
	// every hint on this pane already uses, so the answer to a submit would be
	// drawn as though it were decoration.
	if RenderStatus("x", StatusWarn) == RenderStatus("x", StatusInfo) {
		t.Error("StatusWarn and StatusInfo render alike, so the middle outcome has " +
			"no colour of its own")
	}
}

// THE RESULT LINE FITS THE PANE, and it is the new wording that made this worth
// asserting: this screen writes its result straight out with no fold and no cut
// mark, so a line past the pane is silently truncated by clampToBox — and a
// truncated "already requested — #N is still pending" must still be unreadable
// as a filing. The load-bearing clause therefore LEADS.
func TestReorderForm_TheResultLineFitsTheNarrowestPane(t *testing.T) {
	// A seven-digit pk is the widest an id gets in practice and the recorded
	// fixtures carry one, so this measures the real worst case rather than a
	// convenient short one.
	for _, tc := range []struct {
		name string
		line string
	}{
		{"filed", "reorder #1200041 created"},
		{"already requested", "already requested — #1200041 is still pending"},
	} {
		if got := lipgloss.Width(tc.line); got > screenBodyWidth(minTerminalWidth) {
			t.Errorf("%s: the result line is %d cells against the %d an %d-column "+
				"terminal gives; it would be cut with no mark", tc.name, got,
				screenBodyWidth(minTerminalWidth), minTerminalWidth)
		}
	}
	// Whatever a cut does take, what stands cannot be read as a filed request.
	const duplicate = "already requested — #1200041 is still pending"
	for n := 1; n <= len(duplicate); n++ {
		if strings.Contains(duplicate[:n], "created") {
			t.Fatalf("a prefix of the duplicate line reads as a filing: %q", duplicate[:n])
		}
	}
}
