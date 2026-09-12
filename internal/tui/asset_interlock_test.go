package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/uid0/scantty/internal/omsapi"
)

// Drives all four interlock actions through the REAL key handler and asserts the
// REQUEST that reached the wire, the confirm standing in front of it, and what
// the screen says about the state afterwards.
//
// Root-level rather than screen-level, for the reason the work-order material
// drive records: the state frame claims the plain letters l / u / d / e, and only
// the routing in Root decides whether they reach the screen at all. A
// screen-level test would pass while the real app did something else with them.

// ---------------------------------------------------------------------------
// The fake — a stateful asset that behaves the way the measured server does
// ---------------------------------------------------------------------------

// interlockFake is one asset plus its lockout STACK, and it reproduces the three
// behaviours the recorded fixtures pin (internal/omsapi/testdata/README.md):
// lock appends rather than replacing, unlock clears ONE, and neither axis touches
// the other.
type interlockFake struct {
	mu sync.Mutex
	// lockouts is the stack, newest last. Each is a reason.
	lockouts []string
	active   bool
	// refuse, when set, is served instead of acting: the status and the raw body,
	// so a test can drive either real refusal shape.
	refuse     map[string]int
	refuseBody map[string]string

	requests []string
	bodies   map[string]string
}

func newInterlockFake() *interlockFake {
	return &interlockFake{
		active:     true,
		refuse:     map[string]int{},
		refuseBody: map[string]string{},
		bodies:     map[string]string{},
	}
}

const interlockFakeID = "233eeb12-775f-44a3-9a6c-8cd28e35acd7"

func (f *interlockFake) asset() map[string]any {
	locked := len(f.lockouts) > 0
	out := map[string]any{
		"id":            interlockFakeID,
		"name":          "Haas VF-2 Vertical Machining Center",
		"asset_tag":     "DMS-26A0011E",
		"location_name": "Lab Bay 1",
		"is_locked":     locked,
		"is_active":     f.active,
		"report_only":   false,
		"can_unlock":    true,
		"can_enable":    true,
		"lockout_info":  nil,
	}
	mode := "available"
	if locked {
		mode = "locked_out"
		// The serializer's lockout_info is `…filter(is_active=True).first()`, so
		// it names ONE of the stack and carries no count. The fake names the
		// OLDEST, as the model's ordering does.
		out["lockout_info"] = map[string]any{
			"locked_by":     "labmaint",
			"locked_at":     "2026-09-12T07:21:29.488098+00:00",
			"lockout_level": "maintainer",
			"reason":        f.lockouts[0],
		}
	}
	out["operational_mode"] = map[string]any{"mode": mode, "classroom_mode_enabled": false}
	return out
}

func (f *interlockFake) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		action := ""
		for _, a := range []string{"lock", "unlock", "disable", "enable"} {
			if strings.HasSuffix(r.URL.Path, "/"+a+"/") {
				action = a
				break
			}
		}
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		if action != "" {
			f.bodies[action] = string(raw)
		}

		w.Header().Set("Content-Type", "application/json")
		if code, ok := f.refuse[action]; ok {
			w.WriteHeader(code)
			_, _ = io.WriteString(w, f.refuseBody[action])
			return
		}

		switch action {
		case "lock":
			var body struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(raw, &body)
			if strings.TrimSpace(body.Reason) == "" {
				// The measured refusal, in OMS's standardized envelope.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w,
					`{"error":{"code":"validation_failed","message":"reason is required"}}`)
				return
			}
			// APPENDS — the measured behaviour.
			f.lockouts = append(f.lockouts, body.Reason)
			w.WriteHeader(http.StatusCreated)
		case "unlock":
			if len(f.lockouts) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w,
					`{"error":{"code":"validation_failed","message":"Asset is not locked"}}`)
				return
			}
			// Clears ONE — the measured behaviour, and the whole reason the
			// screen reports the state that comes back.
			f.lockouts = f.lockouts[1:]
		case "disable":
			f.active = false // leaves the lockouts alone
		case "enable":
			f.active = true // leaves the lockouts alone
		}
		_ = json.NewEncoder(w).Encode(f.asset())
	}
}

func (f *interlockFake) sentTo(action string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[action]
}

func (f *interlockFake) sawPath(action string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, req := range f.requests {
		if req == "POST /api/inventory/assets/"+interlockFakeID+"/"+action+"/" {
			return true
		}
	}
	return false
}

func (f *interlockFake) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, req := range f.requests {
		if strings.HasPrefix(req, "POST ") {
			n++
		}
	}
	return n
}

// interlockDrive stands a real Root in front of the screen, sized to the floor
// the size contract guarantees, and loads it.
func interlockDrive(t *testing.T, fake *interlockFake) (Root, *AssetInterlockScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewAssetInterlockScreen(deps, interlockFakeID, "Haas VF-2 Vertical Machining Center")
	r := newTestRoot(screen)
	r.deps = deps
	// 80x24 is the canonical pane: the floor the size contract guarantees is 80
	// wide, and 24 rows is what every columnar fixture in this package is
	// measured at first.
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)
	return r, screen
}

// interlockType types a string and does NOT settle what each rune returns.
//
// Typing is synchronous — the screen updates its own textinput inside Update, so
// the value and the frame are already correct — and the only command a typed rune
// produces is the cursor's blink tick. `pump` abandons that after a flat 200ms
// budget, so settling every rune cost this file ~20 seconds across six typing
// tests. receiveType is the same helper for the same reason; a key that starts real
// work goes through interlockPress, which does settle.
func interlockType(t *testing.T, r Root, text string) Root {
	t.Helper()
	for _, ch := range text {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		after, ok := next.(Root)
		if !ok {
			t.Fatalf("Root.Update returned %T, want Root", next)
		}
		r = after
	}
	return r
}

func interlockPress(t *testing.T, r Root, k string) Root {
	t.Helper()
	switch k {
	case "enter":
		return key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	case "esc":
		return key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	case "ctrl+x":
		return key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	case "up":
		return key(t, r, tea.KeyMsg{Type: tea.KeyUp})
	case "down":
		return key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	return key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
}

// interlockFlat is the pane with its line breaks and fold indents collapsed, so a
// sentence the layer FOLDED across three rows reads as the one sentence it is.
//
// Every content assertion below goes through it, because folding is the rule on
// these frames (jdeCaveatLines) and a Contains over the raw pane would fail on
// correct behaviour — and would quietly start passing again if somebody replaced a
// fold with a clip, which is the defect rather than the fix.
//
// It is deliberately NOT used for the one-row and per-line claims: a flattened pane
// cannot see a headline lose its second row, which is why the refusal's one-line
// check reads the pane's real lines.
func interlockFlat(r Root) string {
	return interlockFlatAt(r, 80, 24)
}

// interlockFlatAt is the SCREEN's own view, clipped exactly as Root clips it, with
// the line breaks and fold indents collapsed.
//
// It is the screen's pane and NOT Root's whole frame, which is the mistake this
// replaced: Root draws a 24-column nav column beside the content, so flattening the
// whole frame splices "Maintenance" and "SIGs" in between a folded sentence's own
// lines and no content assertion can match. poRemoveFlatPane is the same shape for
// the same reason — clip s.View() to the content box, then flatten.
func interlockFlatAt(r Root, width, height int) string {
	pane := clampToBox(r.screen.View(), screenBodyWidth(width), screenBodyHeight(height))
	return strings.Join(strings.Fields(stripANSI(pane)), " ")
}

// ---------------------------------------------------------------------------
// LOCK — the reason, the confirm, and the request
// ---------------------------------------------------------------------------

// LOCKING TAKES THREE DELIBERATE ACTS AND SENDS THE OPERATOR'S REASON. `l` opens
// the reason form, Enter opens the confirm, Ctrl-X writes — and this asserts that
// nothing reaches the wire until the last of them, because "never a single
// unguarded keypress" is a claim about what a press SENDS and not about how many
// frames it draws.
func TestInterlock_LockSendsTheReasonOnlyAfterTheConfirm(t *testing.T) {
	fake := newInterlockFake()
	r, s := interlockDrive(t, fake)

	const reason = "spindle bearing seized"
	r = interlockPress(t, r, "l")
	if s.phase != interlockPhaseReason {
		t.Fatalf("l did not open the reason form; phase = %v", s.phase)
	}
	if n := fake.postCount(); n != 0 {
		t.Fatalf("l alone sent %d writes; opening a form must send nothing", n)
	}

	r = interlockType(t, r, reason)
	r = interlockPress(t, r, "enter")
	if s.phase != interlockPhaseConfirm {
		t.Fatalf("enter on the reason form did not open the confirm; phase = %v", s.phase)
	}
	if n := fake.postCount(); n != 0 {
		t.Fatalf("enter on the reason form sent %d writes; it must only open the confirm, "+
			"or a reflexive second Enter would be enough to stop a machine", n)
	}

	r = interlockPress(t, r, "ctrl+x")
	if !fake.sawPath("lock") {
		t.Fatalf("ctrl+x did not POST the lock; requests = %v", fake.requests)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(fake.sentTo("lock")), &body); err != nil {
		t.Fatalf("lock body %q is not JSON: %v", fake.sentTo("lock"), err)
	}
	if body["reason"] != reason {
		t.Errorf("reason sent = %v, want %q — the server stores this sentence and refuses "+
			"a blank one", body["reason"], reason)
	}
	if !s.isLocked() {
		t.Error("the screen does not report the asset locked after a successful lock")
	}
	if got := interlockFlat(r); !strings.Contains(got, "LOCKED") {
		t.Errorf("the pane does not say LOCKED after locking:\n%s", got)
	}
}

// ENTER IS NOT THE WRITE ON THE CONFIRM, and this presses it there to prove it.
// Enter is the key that OPENED the confirm, so binding the write to it would make
// a double-tap enough to stop a machine.
func TestInterlock_EnterOnTheConfirmWritesNothing(t *testing.T) {
	fake := newInterlockFake()
	r, s := interlockDrive(t, fake)
	r = interlockPress(t, r, "l")
	r = interlockType(t, r, "guard check")
	r = interlockPress(t, r, "enter")
	if s.phase != interlockPhaseConfirm {
		t.Fatalf("setup: not on the confirm; phase = %v", s.phase)
	}
	_ = interlockPress(t, r, "enter")
	if fake.postCount() != 0 {
		t.Fatalf("enter on the confirm SENT a write; only ctrl+x may. requests = %v",
			fake.requests)
	}
}

// A BLANK REASON IS DECLINED WITHOUT A ROUND TRIP, and it says so. The server
// refuses it, so sending it would spend a request to be told what the form
// already knows — and losing the frame in the process.
func TestInterlock_ABlankReasonIsDeclinedOnTheForm(t *testing.T) {
	fake := newInterlockFake()
	r, s := interlockDrive(t, fake)
	r = interlockPress(t, r, "l")
	r = interlockPress(t, r, "enter")

	if s.phase != interlockPhaseReason {
		t.Errorf("a blank reason left the form; phase = %v — the operator has nowhere "+
			"to type the reason the server requires", s.phase)
	}
	if fake.postCount() != 0 {
		t.Errorf("a blank reason was SENT; the server refuses it and the form knows that")
	}
	if got := interlockFlat(r); !strings.Contains(got, "reason is required") {
		t.Errorf("the decline does not say a reason is required:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// UNLOCK — the dangerous direction
// ---------------------------------------------------------------------------

// UNLOCKING IS AS DELIBERATE AS LOCKING AND SHOWS WHAT IS BEING RE-ENABLED. The
// confirm must carry the lockout's own reason — the operator reads why the
// machine was stopped before they let it start — and Ctrl-X alone may write.
func TestInterlock_UnlockConfirmShowsWhatIsBeingReEnabled(t *testing.T) {
	fake := newInterlockFake()
	fake.lockouts = []string{"coolant pump rebuild in progress"}
	r, s := interlockDrive(t, fake)

	if !s.isLocked() {
		t.Fatal("setup: the fake asset is not locked")
	}
	r = interlockPress(t, r, "u")
	if s.phase != interlockPhaseConfirm {
		t.Fatalf("u did not open a confirm; phase = %v — unlocking must not be one "+
			"unguarded keypress", s.phase)
	}
	if n := fake.postCount(); n != 0 {
		t.Fatalf("u alone sent %d writes", n)
	}
	pane := interlockFlat(r)
	if !strings.Contains(pane, "coolant pump rebuild in progress") {
		t.Errorf("the unlock confirm does not show the lockout's reason, so the operator "+
			"cannot see WHAT they are re-enabling:\n%s", pane)
	}
	if !strings.Contains(pane, "labmaint") {
		t.Errorf("the unlock confirm does not name who set the lockout:\n%s", pane)
	}

	r = interlockPress(t, r, "ctrl+x")
	if !fake.sawPath("unlock") {
		t.Fatalf("ctrl+x did not POST the unlock; requests = %v", fake.requests)
	}
	if body := strings.TrimSpace(fake.sentTo("unlock")); body != "" && body != "null" {
		t.Errorf("unlock sent a body %q; the endpoint reads none", body)
	}
	if s.isLocked() {
		t.Error("the screen still reports the asset locked after the only lockout was cleared")
	}
}

// AN UNLOCK THAT SUCCEEDS AND LEAVES THE MACHINE LOCKED SAYS SO, AT WARNING
// LEVEL. This is the measured trap (testdata/asset_unlock_still_locked.json): the
// view clears ONE lockout of a stack and answers 200. Reporting "unlocked" here
// would tell somebody a machine was safe to start while the server still denies
// it.
func TestInterlock_AnUnlockThatLeavesItLockedSaysSo(t *testing.T) {
	fake := newInterlockFake()
	fake.lockouts = []string{"bearing replacement", "my own lock while I change tooling"}
	r, s := interlockDrive(t, fake)

	r = interlockPress(t, r, "u")
	r = interlockPress(t, r, "ctrl+x")

	if !fake.sawPath("unlock") {
		t.Fatalf("the unlock was not sent; requests = %v", fake.requests)
	}
	if !s.isLocked() {
		t.Fatal("the fake cleared the whole stack; this test needs one lockout to remain " +
			"or it is not about the state it names")
	}
	if s.noteLevel != StatusWarn {
		t.Errorf("a still-locked unlock reported at level %v, want StatusWarn — a success "+
			"that left the machine denied is not an OK", s.noteLevel)
	}
	pane := stripANSI(r.View())
	if !strings.Contains(pane, "STILL LOCKED") {
		t.Errorf("the pane does not say the machine is STILL LOCKED after an unlock that "+
			"cleared one of two lockouts:\n%s", pane)
	}
	// And the state rows must agree with the note rather than contradicting it.
	if !strings.Contains(pane, "LOCKED — the machine is denied") {
		t.Errorf("the interlock row does not still report the machine denied:\n%s", pane)
	}
}

// ---------------------------------------------------------------------------
// DISABLE / ENABLE — the other axis, and the distinction that matters
// ---------------------------------------------------------------------------

// DISABLE IS CONFIRMED AND SAYS IT DOES NOT STOP THE MACHINE. That sentence is the
// point of the whole screen: ForgeKey never reads is_active, so somebody who
// disables a dangerous machine and walks away has stopped nothing.
func TestInterlock_DisableConfirmSaysItDoesNotStopTheMachine(t *testing.T) {
	fake := newInterlockFake()
	r, s := interlockDrive(t, fake)

	r = interlockPress(t, r, "d")
	if s.phase != interlockPhaseConfirm {
		t.Fatalf("d did not open a confirm; phase = %v", s.phase)
	}
	pane := interlockFlat(r)
	if !strings.Contains(pane, "DOES NOT STOP THE MACHINE") {
		t.Errorf("the disable confirm does not say it fails to stop the machine:\n%s", pane)
	}
	if !strings.Contains(pane, "lock it instead") {
		t.Errorf("the disable confirm does not point at locking, which is the thing the "+
			"operator probably meant:\n%s", pane)
	}

	r = interlockPress(t, r, "ctrl+x")
	if !fake.sawPath("disable") {
		t.Fatalf("ctrl+x did not POST the disable; requests = %v", fake.requests)
	}
	if body := strings.TrimSpace(fake.sentTo("disable")); body != "" && body != "null" {
		t.Errorf("disable sent a body %q; the endpoint reads none", body)
	}
	if s.isActive() {
		t.Error("the screen still reports the record active after a successful disable")
	}
	// Disabling an UNLOCKED asset is the dangerous outcome, so the report is a
	// warning rather than a tick.
	if s.noteLevel != StatusWarn {
		t.Errorf("disabling an unlocked machine reported at level %v, want StatusWarn — "+
			"the machine can still be used", s.noteLevel)
	}
	if got := interlockFlat(r); !strings.Contains(got, "NOT stopped") {
		t.Errorf("the pane does not say the machine was not stopped:\n%s", got)
	}
}

// ENABLE IS CONFIRMED TOO, which is where this screen parts company with the web:
// the web confirms disable and NOT enable, guarding the safe direction and leaving
// the restoring one to a single click.
func TestInterlock_EnableIsConfirmedAndSent(t *testing.T) {
	fake := newInterlockFake()
	fake.active = false
	r, s := interlockDrive(t, fake)

	if s.isActive() {
		t.Fatal("setup: the fake asset is already active")
	}
	r = interlockPress(t, r, "e")
	if s.phase != interlockPhaseConfirm {
		t.Fatalf("e did not open a confirm; phase = %v — restoring must not be one "+
			"unguarded keypress either", s.phase)
	}
	if n := fake.postCount(); n != 0 {
		t.Fatalf("e alone sent %d writes", n)
	}
	r = interlockPress(t, r, "ctrl+x")
	if !fake.sawPath("enable") {
		t.Fatalf("ctrl+x did not POST the enable; requests = %v", fake.requests)
	}
	if !s.isActive() {
		t.Error("the screen does not report the record active after a successful enable")
	}
}

// ENABLING A LOCKED ASSET SAYS IT IS STILL LOCKED. The two axes are independent,
// so an enable leaves a lockout in place, and a bare "enabled" would read as
// "usable".
func TestInterlock_EnablingALockedAssetSaysItStaysLocked(t *testing.T) {
	fake := newInterlockFake()
	fake.active = false
	fake.lockouts = []string{"guard interlock bypassed — do not run"}
	r, s := interlockDrive(t, fake)

	r = interlockPress(t, r, "e")
	pane := interlockFlat(r)
	if !strings.Contains(pane, "stays LOCKED") {
		t.Errorf("the enable confirm does not warn that the asset stays locked:\n%s", pane)
	}
	r = interlockPress(t, r, "ctrl+x")
	if !s.isActive() || !s.isLocked() {
		t.Fatalf("after enabling a locked asset: active=%v locked=%v, want both true",
			s.isActive(), s.isLocked())
	}
	if got := interlockFlat(r); !strings.Contains(got, "still LOCKED") {
		t.Errorf("the report after enabling a locked asset does not say it is still "+
			"locked:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// The refusals — relayed, in the server's own words, on one marked line
// ---------------------------------------------------------------------------

// BOTH REFUSAL SHAPES REACH THE OPERATOR AS THE SERVER'S OWN SENTENCE, on ONE
// line, marked, and never as raw JSON.
//
// These are the two shapes the four endpoints really answer in (measured; see
// internal/omsapi/asset_interlock.go): the coded envelope for validation and a
// hand-built flat body for permission. A client that read only one would put the
// other's raw JSON on the status row.
func TestInterlock_TheServersRefusalsReachTheOperatorVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name     string
		action   string
		keys     []string
		typed    string
		status   int
		body     string
		want     string
		unwanted string
	}{
		{
			name:   "the flat permission shape on unlock",
			action: "unlock",
			keys:   []string{"u", "ctrl+x"},
			status: http.StatusForbidden,
			body:   `{"error":"You do not have permission to unlock this asset"}`,
			want:   "You do not have permission to unlock this asset",
			// The raw envelope must not be what the operator reads.
			unwanted: `{"error"`,
		},
		{
			name:     "the coded validation shape on unlock",
			action:   "unlock",
			keys:     []string{"u", "ctrl+x"},
			status:   http.StatusBadRequest,
			body:     `{"error":{"code":"validation_failed","message":"Asset is not locked"}}`,
			want:     "Asset is not locked",
			unwanted: `"code"`,
		},
		{
			name:     "the flat permission shape on enable",
			action:   "enable",
			keys:     []string{"e", "ctrl+x"},
			status:   http.StatusForbidden,
			body:     `{"error":"You do not have permission to enable this asset"}`,
			want:     "You do not have permission to enable this asset",
			unwanted: `{"error"`,
		},
		{
			name:     "the coded validation shape on lock",
			action:   "lock",
			keys:     []string{"l"},
			typed:    "bearing seized",
			status:   http.StatusBadRequest,
			body:     `{"error":{"code":"validation_failed","message":"reason is required"}}`,
			want:     "reason is required",
			unwanted: `"code"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newInterlockFake()
			// Locked so `u` is on offer; inactive so `e` is.
			fake.lockouts = []string{"a lockout somebody else set"}
			if tc.action == "enable" {
				fake.active = false
			}
			fake.refuse[tc.action] = tc.status
			fake.refuseBody[tc.action] = tc.body

			r, s := interlockDrive(t, fake)
			for _, k := range tc.keys {
				r = interlockPress(t, r, k)
			}
			if tc.typed != "" {
				r = interlockType(t, r, tc.typed)
				r = interlockPress(t, r, "enter")
				r = interlockPress(t, r, "ctrl+x")
			}

			if s.errMsg != tc.want {
				t.Errorf("errMsg = %q, want the server's own sentence %q", s.errMsg, tc.want)
			}
			pane := interlockFlat(r)
			if !strings.Contains(pane, tc.want) {
				t.Errorf("the refusal is not on the pane:\n%s", pane)
			}
			if strings.Contains(pane, tc.unwanted) {
				t.Errorf("the pane carries the raw envelope %q rather than the sentence "+
					"inside it:\n%s", tc.unwanted, pane)
			}
			// ONE MARKED LINE, asked of the pane's REAL lines rather than the
			// flattened one — a flattened pane is one line by construction and could
			// not fail this. The layer's status row is what guarantees it, and
			// asserting it here is what stops a later rewrite drawing a multi-line
			// failure into the body instead.
			raw := clampToBox(s.View(), screenBodyWidth(80), screenBodyHeight(24))
			line, ok := interlockRefusalLine(raw, tc.want)
			if !ok {
				t.Errorf("the refusal is not on a single line of the pane:\n%s", raw)
			} else if !strings.HasPrefix(strings.TrimLeft(line, " "), jdeStatusErrMark) {
				t.Errorf("the refusal line %q does not lead with the error mark %q",
					line, jdeStatusErrMark)
			}
		})
	}
}

// A REFUSAL IS FLATTENED TO ONE LINE even when the server's body is several.
// parseError hands a whole raw payload over when the envelope has no code, and a
// gateway's 502 page is seven lines — drawn unflattened it would run the frame
// over and clampToBox would take the action bar off the bottom.
func TestInterlock_AMultiLineRefusalIsFlattenedToOneLine(t *testing.T) {
	fake := newInterlockFake()
	fake.lockouts = []string{"a lockout"}
	fake.refuse["unlock"] = http.StatusBadGateway
	fake.refuseBody["unlock"] = "<html>\n<head><title>502 Bad Gateway</title></head>\n" +
		"<body>\n<center><h1>502 Bad Gateway</h1></center>\n<hr>\n" +
		"<center>nginx/1.24.0</center>\n</body>\n</html>\n"

	r, s := interlockDrive(t, fake)
	r = interlockPress(t, r, "u")
	// The Root is not read again: everything below is measured on the SCREEN's own
	// view, for the reason the next comment gives.
	interlockPress(t, r, "ctrl+x")

	// MEASURED ON THE SCREEN'S OWN PANE, clipped the way Root clips it. Root's
	// frame is legitimately 80 cells wide (it carries the nav column), so
	// measuring its lines would be measuring the wrong thing — the rule is about
	// what the SCREEN hands over for the content pane.
	pane := stripANSI(s.View())
	for i, line := range strings.Split(pane, "\n") {
		if visibleCells(line) > screenBodyCells(80) {
			t.Fatalf("line %d is %d cells against a %d-cell pane; a gateway page drawn "+
				"unflattened is what runs the frame over and takes the action bar off the "+
				"bottom:\n%q", i, visibleCells(line), screenBodyCells(80), line)
		}
	}
	// The bar must have survived it — that is what the flattening buys.
	clipped := clampToBox(s.View(), screenBodyWidth(80), screenBodyHeight(24))
	if !strings.Contains(stripANSI(clipped), "Esc") {
		t.Errorf("the action bar is gone after a multi-line refusal:\n%s", stripANSI(clipped))
	}
}

// interlockRefusalLine finds the single pane line carrying `want`, and reports
// false when the sentence is split across lines.
func interlockRefusalLine(pane, want string) (string, bool) {
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, want) {
			return line, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// The state the operator reads, and the keys they are offered
// ---------------------------------------------------------------------------

// THE BAR NAMES EXACTLY THE KEYS THAT APPLY, in every one of the four states the
// two axes make. This is the honesty rule asked of the one screen whose keys stop
// and start machines, and it presses each of the four letters in each state.
func TestInterlock_TheBarNamesExactlyTheActionsThatApply(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lockouts []string
		active   bool
		// want maps each letter to whether it should be named AND act.
		want map[string]bool
	}{
		{"unlocked and active", nil, true,
			map[string]bool{"l": true, "u": false, "d": true, "e": false}},
		{"locked and active", []string{"seized"}, true,
			map[string]bool{"l": true, "u": true, "d": true, "e": false}},
		{"unlocked and disabled", nil, false,
			map[string]bool{"l": true, "u": false, "d": false, "e": true}},
		{"locked and disabled", []string{"seized"}, false,
			map[string]bool{"l": true, "u": true, "d": false, "e": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for letter, want := range tc.want {
				fake := newInterlockFake()
				fake.lockouts = tc.lockouts
				fake.active = tc.active
				r, s := interlockDrive(t, fake)

				named := false
				for _, item := range s.stateBar() {
					if item.Key == letter {
						named = true
					}
				}
				if named != want {
					t.Errorf("%q named on the bar = %v, want %v", letter, named, want)
				}

				before := s.phase
				r = interlockPress(t, r, letter)
				acted := s.phase != before
				if acted != want {
					t.Errorf("%q moved off the state frame = %v, want %v — a key the bar "+
						"names must act and one it does not name must not", letter, acted, want)
				}
				if !want {
					// A key that declines must SAY SO: this frame holds no cursor
					// and no caret, so a silent return redraws an identical pane.
					if s.note == "" {
						t.Errorf("%q declined in silence; on a frame with nothing else "+
							"moving that reads as a wedged program", letter)
					}
					if n := fake.postCount(); n != 0 {
						t.Errorf("%q sent %d writes while not applying", letter, n)
					}
					if got := interlockFlat(r); !strings.Contains(got, letter) {
						t.Errorf("the decline for %q does not name the key that was "+
							"pressed:\n%s", letter, got)
					}
				}
			}
		})
	}
}

// `l` IS OFFERED ON AN ALREADY-LOCKED ASSET, and the bar and the confirm both say
// it ADDS a lockout rather than creating the first.
//
// Lock stacks (measured), and stacking is how lockout/tagout is meant to work, so
// hiding the key would refuse something the server supports and that carries
// safety meaning. What must not happen is a bar reading plain "Lock" there.
func TestInterlock_LockOnALockedAssetSaysItAdds(t *testing.T) {
	fake := newInterlockFake()
	fake.lockouts = []string{"first lockout"}
	r, s := interlockDrive(t, fake)

	label := ""
	for _, item := range s.stateBar() {
		if item.Key == "l" {
			label = item.Label
		}
	}
	if label != "Add lock" {
		t.Errorf("the bar labels l %q on a locked asset, want \"Add lock\" — plain "+
			"\"Lock\" would name an operation the operator did not mean", label)
	}

	r = interlockPress(t, r, "l")
	r = interlockType(t, r, "my own lock")
	r = interlockPress(t, r, "enter")
	pane := interlockFlat(r)
	if !strings.Contains(pane, "ALREADY LOCKED") {
		t.Errorf("the confirm does not say the asset is already locked:\n%s", pane)
	}
	if !strings.Contains(pane, "SECOND lockout") {
		t.Errorf("the confirm does not say a second lockout is being added:\n%s", pane)
	}

	r = interlockPress(t, r, "ctrl+x")
	fake.mu.Lock()
	n := len(fake.lockouts)
	fake.mu.Unlock()
	if n != 2 {
		t.Errorf("the stack holds %d lockouts after adding one to a locked asset, want 2", n)
	}
}

// THE STATE IS ON THE PANE AT EVERY PANE THE FRAME IS DRAWN AT. "An operator must
// never be unsure whether the machine they are standing at is locked", so the
// interlock state is the header's ESSENTIAL row and survives every drawable pane —
// asserted on the CLIPPED frame, because clampToBox cuts in Root.View rather than
// in the screen.
//
// SCOPED BY WHAT THE FRAME REALLY DRAWS, and counting BOTH sides of the boundary.
// Below a certain height the layer REFUSES the frame outright (jdeTooShort) and
// draws a bounded notice instead, which is its designed answer and not a missing
// state row — so the claim is asked of the drawn panes, and the sweep fails if it
// never reached either side, since a scoping that only ever visits one side is a
// way of asserting nothing.
//
// On the refused panes the claim is different and is also checked: the notice must
// name a height that works and a way out, so the pane is never a dead end.
func TestInterlock_TheLockStateSurvivesEveryPaneTheFrameIsDrawnAt(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	drawn, refused := 0, 0
	for _, locked := range []bool{false, true} {
		want := "not locked"
		if locked {
			want = "LOCKED"
		}
		for _, w := range jdeDrawableWidths() {
			for _, h := range jdePaneHeights() {
				fake := newInterlockFake()
				if locked {
					fake.lockouts = []string{"spindle bearing seized"}
				}
				r, s := interlockDrive(t, fake)
				next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
				r = next.(Root)
				flat := interlockFlatAt(r, w, h)

				if !s.frameDrawn(len(s.stateHeader()), s.stateBar()) {
					refused++
					// WHAT A REFUSED PANE OWES THE OPERATOR IS THE LAYER'S DECISION,
					// not this screen's, and only the part this screen can be wrong
					// about is asserted here: that the notice really is jdeTooShort's
					// — it names the HEIGHT to resize to, which is the one thing an
					// operator acts on.
					//
					// The way-out clause is DELIBERATELY not claimed. jdeTooShort
					// gives ground from the tail and Esc rides its third sentence, so
					// the height at which it survives is a property of that wording's
					// own fold; re-deriving the boundary here would be a second
					// implementation of it, and the claim already belongs to
					// jde_pane_fit_test.go's sweeps over every columnar screen.
					if !strings.Contains(flat, "rows") {
						t.Fatalf("at %dx%d the refused pane does not name the height it "+
							"needs, so the operator cannot act on it:\n%s", w, h, flat)
					}
					continue
				}
				drawn++
				if !strings.Contains(flat, want) {
					t.Fatalf("at %dx%d a locked=%v asset's drawn pane does not say %q:\n%s",
						w, h, locked, want, flat)
				}
			}
		}
	}
	if drawn == 0 {
		t.Fatal("no pane drew the frame, so the claim was never tested")
	}
	if refused == 0 {
		t.Fatal("no pane refused the frame, so the scoping is untested and could be " +
			"hiding every failure behind it")
	}
	t.Logf("interlock state asserted on %d drawn panes; %d refused panes checked for a "+
		"way out", drawn, refused)
}

// THE WHOLE UUID IS ON THE SHEET. A truncated id is not an identifier, and this is
// the screen somebody quotes from when they phone about a machine they cannot
// start — so it is drawn as a folded line rather than in a labelled row, which at
// the 51-cell pane an 80-column terminal gives leaves a value only 33 cells and
// clipped 36 characters of UUID.
//
// Checked at every drawable WIDTH, because the width is what decides whether the
// digits fit; the height decides only whether they are above the fold, and the
// pane is opened tall enough that the sheet does not scroll.
func TestInterlock_TheAssetIdIsShownWhole(t *testing.T) {
	for _, w := range jdeDrawableWidths() {
		fake := newInterlockFake()
		r, s := interlockDrive(t, fake)
		next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: 44})
		r = next.(Root)
		if s.stateScrolls() {
			t.Fatalf("at %dx44 the sheet still scrolls, so this check cannot tell a "+
				"clipped id from one below the fold", w)
		}
		if got := interlockFlatAt(r, w, 44); !strings.Contains(got, interlockFakeID) {
			t.Errorf("at width %d the pane does not carry the whole asset id %q:\n%s",
				w, interlockFakeID, got)
		}
	}
}

// AND IT IS REACHABLE BY SCROLLING when the sheet is longer than the pane.
//
// A value only a key can fetch is fine; a value NO key can fetch is the
// unreachable-body defect. On a LOCKED asset the lockout block leads the sheet —
// deliberately, because on a stopped machine the reason it was stopped is what
// matters most — so the id is below the fold at an ordinary pane and this walks the
// scroll to prove a key gets there.
//
// It presses the real keys rather than setting the offset, and it counts the
// offsets it visited, so a sheet that stopped scrolling would fail rather than pass
// on the first frame.
func TestInterlock_TheAssetIdIsReachableByScrolling(t *testing.T) {
	fake := newInterlockFake()
	fake.lockouts = []string{
		"spindle bearing seized — do not run until the bearing has been replaced " +
			"and the head re-trammed",
	}
	r, s := interlockDrive(t, fake)
	if !s.stateScrolls() {
		t.Fatal("at 80x24 the sheet does not scroll, so this check is not about the " +
			"state it names")
	}
	if strings.Contains(interlockFlat(r), interlockFakeID) {
		t.Skip("the id is already on the first screenful, so there is nothing to reach")
	}

	seen := map[int]bool{}
	for i := 0; i < 40; i++ {
		if strings.Contains(interlockFlat(r), interlockFakeID) {
			if len(seen) < 2 {
				t.Fatalf("the id appeared without the sheet ever moving (offsets %v), so "+
					"this check proved nothing about scrolling", seen)
			}
			return
		}
		seen[s.scroll] = true
		r = key(t, r, tea.KeyMsg{Type: tea.KeyPgDown})
		if seen[s.scroll] && len(seen) > 1 {
			break // clamped at the end
		}
	}
	t.Errorf("the whole asset id is on no screenful of the sheet; offsets visited = %v, "+
		"body = %d lines:\n%s", len(seen), s.stateBody().Len(), interlockFlat(r))
}

// NOTHING IS PRE-EMPTED ON THE SERVER'S ADVISORY FLAGS. can_unlock and can_enable
// are false on a report_only asset while the endpoints accept the write anyway
// (measured), so a client that gated on them would refuse what the server allows.
// Every key still acts, and the flags are REPORTED instead.
func TestInterlock_TheAdvisoryFlagsAreShownAndNeverGate(t *testing.T) {
	fake := newInterlockFake()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/lock/") {
			raw, _ := io.ReadAll(r.Body)
			fake.requests = append(fake.requests, "POST "+r.URL.Path)
			fake.bodies["lock"] = string(raw)
			var body struct{ Reason string }
			_ = json.Unmarshal(raw, &body)
			fake.lockouts = append(fake.lockouts, "locked anyway")
		}
		a := fake.asset()
		// The report_only shape: the server says no to both and accepts anyway.
		a["report_only"] = true
		a["can_unlock"] = false
		a["can_enable"] = false
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a)
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewAssetInterlockScreen(deps, interlockFakeID, "Report-only bench grinder")
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)

	pane := interlockFlatAt(r, 80, 40)
	if !strings.Contains(pane, "PREDICTION and not a gate") {
		t.Errorf("the sheet does not say the server's flags are advisory:\n%s", pane)
	}
	if !strings.Contains(pane, "report-only") {
		t.Errorf("the sheet does not report the report-only flag:\n%s", pane)
	}

	// And the key still works: `l` is named and reaches the wire.
	named := false
	for _, item := range screen.stateBar() {
		if item.Key == "l" {
			named = true
		}
	}
	if !named {
		t.Fatal("l is not named on a report_only asset; gating on can_unlock would refuse " +
			"what the server accepts")
	}
	r = interlockPress(t, r, "l")
	r = interlockType(t, r, "locked despite the flag")
	r = interlockPress(t, r, "enter")
	_ = interlockPress(t, r, "ctrl+x")
	if !fake.sawPath("lock") {
		t.Errorf("the lock was not sent on a report_only asset; requests = %v", fake.requests)
	}
}

// A STALE CONFIRM RELAYS THE SERVER'S REFUSAL RATHER THAN GUESSING.
//
// ScanTTY has no push, so an operator can open the unlock confirm and stand there
// while somebody else clears the lockout. This is the reachable version of "the
// state changed underneath": Ctrl-X sends the unlock, the server answers
// "Asset is not locked", and that sentence is what reaches the operator.
//
// It is driven rather than faked, which is why there is no reseat guard on this
// screen — see the note in asset_interlock.go. The screen must also come back to a
// frame the operator can act on, with the state re-read.
func TestInterlock_AStaleConfirmRelaysTheServersRefusal(t *testing.T) {
	fake := newInterlockFake()
	fake.lockouts = []string{"somebody else's lockout"}
	r, s := interlockDrive(t, fake)

	r = interlockPress(t, r, "u")
	if s.phase != interlockPhaseConfirm {
		t.Fatalf("setup: not on the unlock confirm; phase = %v", s.phase)
	}
	// Somebody else clears it while the confirm is up.
	fake.mu.Lock()
	fake.lockouts = nil
	fake.mu.Unlock()

	r = interlockPress(t, r, "ctrl+x")

	if !fake.sawPath("unlock") {
		t.Fatalf("ctrl+x sent nothing; the client must ask rather than deciding for "+
			"itself. requests = %v", fake.requests)
	}
	if s.errMsg != "Asset is not locked" {
		t.Errorf("errMsg = %q, want the server's own %q", s.errMsg, "Asset is not locked")
	}
	if got := interlockFlat(r); !strings.Contains(got, "Asset is not locked") {
		t.Errorf("the server's refusal is not on the pane:\n%s", got)
	}
	// Back on a frame that can be acted on, with the state re-read: the refusal
	// fires a load, so `u` must now be gone and `l` still there.
	if s.phase != interlockPhaseState {
		t.Errorf("phase = %v after a refused write, want the state frame", s.phase)
	}
	if s.isLocked() {
		t.Errorf("the screen still reports the asset locked; the refusal's re-read is " +
			"what corrects a state somebody else moved")
	}
	for _, item := range s.stateBar() {
		if item.Key == "u" {
			t.Error("the bar still names u on an asset that is no longer locked")
		}
	}
}

// ESC ON THE LOCK CONFIRM KEEPS THE OPERATOR'S SENTENCE. Throwing away typed
// input in answer to "are you sure" is discarding work the operator did.
func TestInterlock_EscFromTheLockConfirmKeepsTheReason(t *testing.T) {
	fake := newInterlockFake()
	r, s := interlockDrive(t, fake)
	const reason = "guard interlock bypassed"
	r = interlockPress(t, r, "l")
	r = interlockType(t, r, reason)
	r = interlockPress(t, r, "enter")
	interlockPress(t, r, "esc")

	if s.phase != interlockPhaseReason {
		t.Errorf("esc from the lock confirm went to phase %v, want back to the reason form",
			s.phase)
	}
	if got := s.reason.Value(); got != reason {
		t.Errorf("the reason box holds %q, want the operator's own %q", got, reason)
	}
}

// A SECOND WRITE IS NOT SENT WHILE ONE IS OUT, and the decline says so. Two
// interlock writes racing would leave the operator reading whichever answered
// last.
func TestInterlock_NoSecondWriteWhileOneIsOut(t *testing.T) {
	fake := newInterlockFake()
	r, s := interlockDrive(t, fake)
	s.saving = true // a write in flight, reached without needing a slow server

	for _, letter := range []string{"l", "u", "d", "e"} {
		before := s.phase
		r = interlockPress(t, r, letter)
		if s.phase != before {
			t.Errorf("%q opened a frame while a write was out; phase %v -> %v",
				letter, before, s.phase)
		}
	}
	if n := fake.postCount(); n != 0 {
		t.Errorf("%d writes were sent while one was already out", n)
	}
	if got := interlockFlat(r); !strings.Contains(got, "a write is already out") {
		t.Errorf("the decline does not say a write is in flight:\n%s", got)
	}
}

// A REFRESH DOES NOT DESTROY WHAT THE OPERATOR WAS READING. `r` puts a read in
// flight, and while it is out the state rows go on showing the last-known state and
// the actions stay on the bar — the working line is what says a read is
// outstanding.
//
// This is pinned because the first version got it wrong in a way an operator would
// have felt: `stateKnown` included `!loading`, so pressing `r` blanked all three
// rows to "not known" and took every action off the bar, and `r` then `l` answered
// "the asset's state could not be read" about a state that had just been read
// perfectly well.
func TestInterlock_ARefreshKeepsTheStateOnScreen(t *testing.T) {
	fake := newInterlockFake()
	fake.lockouts = []string{"spindle bearing seized"}
	r, s := interlockDrive(t, fake)
	if !s.isLocked() {
		t.Fatal("setup: the fixture is not locked")
	}

	// Press r WITHOUT settling, so the read is genuinely still in flight. The
	// command is deliberately dropped rather than pumped: pumping it would complete
	// the read and there would be nothing in flight to check.
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	r = next.(Root)
	if !s.loading {
		t.Fatal("r did not put a read in flight, so this check is not about the state " +
			"it names")
	}

	if !s.stateKnown() {
		t.Error("a read in flight made the state unknown; the last read still stands and " +
			"blanking it destroys what the operator was looking at")
	}
	pane := interlockFlat(r)
	if !strings.Contains(pane, "LOCKED") {
		t.Errorf("the pane stopped saying LOCKED while a refresh was out:\n%s", pane)
	}
	if !strings.Contains(pane, "Reading") {
		t.Errorf("the pane does not say a read is out:\n%s", pane)
	}
	named := map[string]bool{}
	for _, item := range s.stateBar() {
		named[item.Key] = true
	}
	for _, k := range []string{"l", "u"} {
		if !named[k] {
			t.Errorf("the bar dropped %q while a refresh was out; the action still "+
				"applies to the state we last read", k)
		}
	}
	// And the letter still opens its frame rather than declining.
	interlockPress(t, r, "u")
	if s.phase != interlockPhaseConfirm {
		t.Errorf("u declined while a refresh was out; phase = %v", s.phase)
	}
}

func TestInterlock_AStaleRefreshCannotOverwriteAWriteReply(t *testing.T) {
	fake := newInterlockFake()
	r, s := interlockDrive(t, fake)

	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	r = next.(Root)
	r = interlockType(t, r, "spindle bearing seized")
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	next, writeCmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	r = next.(Root)
	if writeCmd == nil || !s.saving {
		t.Fatal("ctrl+x did not start the interlock write")
	}

	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	r = next.(Root)
	next, refreshCmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	r = next.(Root)
	if refreshCmd == nil {
		t.Fatal("r did not start the refresh")
	}

	staleRead := refreshCmd()
	writeReply := writeCmd()
	next, _ = r.Update(writeReply)
	r = next.(Root)
	if !s.isLocked() {
		t.Fatal("the write reply did not install the server's locked state")
	}
	next, _ = r.Update(staleRead)
	r = next.(Root)

	if !s.isLocked() {
		t.Error("a pre-write refresh overwrote the write reply with an unlocked state")
	}
	if pane := interlockFlat(r); !strings.Contains(pane, "LOCKED — the machine is denied") {
		t.Errorf("the pane does not retain the state returned by the write:\n%s", pane)
	}
}

// NOTHING ACTS ON A GUESS. A failed load leaves COULD NOT TELL rather than NOT
// LOCKED, and on this screen that difference is whether somebody starts a machine.
func TestInterlock_AFailedLoadOffersNoActionAndSaysWhy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"detail":"upstream is down"}`)
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewAssetInterlockScreen(deps, interlockFakeID, "Haas VF-2")
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)

	if screen.stateKnown() {
		t.Fatal("setup: the load did not fail")
	}
	for _, item := range screen.stateBar() {
		switch item.Key {
		case "l", "u", "d", "e":
			t.Errorf("the bar names %q after a failed load; nothing here may act on a "+
				"state it could not read", item.Key)
		}
	}
	pane := interlockFlat(r)
	if !strings.Contains(pane, "not known") {
		t.Errorf("the pane asserts a lock state it could not read; COULD NOT TELL and "+
			"NOT LOCKED are different facts here:\n%s", pane)
	}
	r = interlockPress(t, r, "l")
	if screen.phase != interlockPhaseState {
		t.Error("l opened a frame after a failed load")
	}
	if got := interlockFlat(r); !strings.Contains(got, "could not be read") {
		t.Errorf("the decline does not say the state could not be read:\n%s", got)
	}
}

// THE DETAIL SCREEN REACHES IT WITH `L`, and its footer names the key. A sibling
// surface nothing opens is a surface nobody finds.
func TestInterlock_TheAssetDetailOpensItWithL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/assets/"+interlockFakeID+"/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": interlockFakeID, "name": "Haas VF-2", "is_active": true,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewAssetDetailScreen(deps, interlockFakeID)
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	r = next.(Root)
	r = pump(t, r, detail.Init(), 0)

	if got := interlockFlatAt(r, 80, 40); !strings.Contains(got, "L interlock") {
		t.Errorf("the asset detail footer does not name L:\n%s", got)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	if _, ok := r.screen.(*AssetInterlockScreen); !ok {
		t.Fatalf("L opened %T, want *AssetInterlockScreen", r.screen)
	}
}

// A REPORT THAT NAMES A KEY NAMES ONE THE BAR IS OFFERING. Two of the post-write
// reports give advice — "Press u again to clear the next one" after an unlock that
// left the machine locked, and "Press l to lock it" after disabling an unlocked
// one — and a sentence naming a key the frame does not offer is the rule broken on
// the surface an operator is most likely to act on.
//
// Driven through the real writes rather than by calling wroteNote, so the state the
// advice is given in is the state the server really left behind.
func TestInterlock_TheAdviceAfterAWriteNamesAnOfferedKey(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lockouts []string
		active   bool
		keys     []string
		wantWord string
		wantKey  string
	}{
		{
			// Two lockouts, unlock once: still locked, "press u again".
			name: "a still-locked unlock points at u", lockouts: []string{"first", "second"},
			active: true, keys: []string{"u", "ctrl+x"},
			wantWord: "Press u again", wantKey: "u",
		},
		{
			// Disable an unlocked asset: "press l to lock it".
			name: "a disable of an unlocked asset points at l", active: true,
			keys: []string{"d", "ctrl+x"}, wantWord: "Press l to lock it", wantKey: "l",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newInterlockFake()
			fake.lockouts, fake.active = tc.lockouts, tc.active
			r, s := interlockDrive(t, fake)
			for _, k := range tc.keys {
				r = interlockPress(t, r, k)
			}
			if !strings.Contains(s.note, tc.wantWord) {
				t.Fatalf("the report is %q, want it to carry %q — otherwise this check is "+
					"not about the sentence it names", s.note, tc.wantWord)
			}
			offered := false
			for _, item := range s.stateBar() {
				if item.Key == tc.wantKey {
					offered = true
				}
			}
			if !offered {
				t.Errorf("the report says %q but the bar does not offer %q; bar = %v",
					tc.wantWord, tc.wantKey, s.stateBar())
			}
			// And the key really acts, so the advice is not just spelled on the bar.
			before := s.phase
			interlockPress(t, r, tc.wantKey)
			if s.phase == before {
				t.Errorf("%q did nothing, so the advice sends the operator at a dead key",
					tc.wantKey)
			}
		})
	}
}

// ESC REALLY LEAVES, through a real Root, and lands back on the asset.
//
// The state frame is deliberately NOT raw-input — so workspace switching and the
// global back step keep working while an operator reads it — which means Root
// claims `esc` BEFORE updateState's own arm ever sees it. The bar names Esc on
// every frame here, so the claim has to be checked where it is really answered
// rather than against the screen's handler in isolation.
func TestInterlock_EscLeavesForTheAssetThroughARealRoot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/assets/"+interlockFakeID+"/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": interlockFakeID, "name": "Haas VF-2", "is_active": true,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewAssetDetailScreen(deps, interlockFakeID)
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	r = next.(Root)
	r = pump(t, r, detail.Init(), 0)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	s, ok := r.screen.(*AssetInterlockScreen)
	if !ok {
		t.Fatalf("L opened %T, want the interlock", r.screen)
	}
	// Esc is named on every frame this screen draws, including the refused ones.
	named := false
	for _, item := range s.stateBar() {
		if item.Key == "Esc" {
			named = true
		}
	}
	if !named {
		t.Fatal("the state bar does not name Esc")
	}
	r = interlockPress(t, r, "esc")
	if _, ok := r.screen.(*AssetInterlockScreen); ok {
		t.Fatal("esc did not leave the interlock screen — the bar names a way out that " +
			"does not work, which is the dead end a refusal must never be")
	}
	if _, ok := r.screen.(*AssetDetailScreen); !ok {
		t.Errorf("esc landed on %T, want back on the asset detail", r.screen)
	}
}

// THE DETAIL SCREEN SAYS LOCKED PLAINLY, with the reason, because it is the screen
// a scanned asset lands on. A bare "locked" chip does not tell a tech whether the
// machine will start or why it was stopped.
func TestInterlock_TheAssetDetailBannerNamesTheLockout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/assets/"+interlockFakeID+"/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": interlockFakeID, "name": "Haas VF-2",
				"is_active": false, "is_locked": true,
				"lockout_info": map[string]any{
					"locked_by": "labmaint", "lockout_level": "maintainer",
					"locked_at": "2026-09-12T07:21:29.488098+00:00",
					"reason":    "spindle bearing seized",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewAssetDetailScreen(deps, interlockFakeID)
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	r = next.(Root)
	r = pump(t, r, detail.Init(), 0)

	pane := interlockFlatAt(r, 80, 40)
	for _, want := range []string{
		"LOCKED — the machine is denied",
		"spindle bearing seized",
		"labmaint",
		// The weaker fact, stated as the weaker fact.
		"does NOT stop the machine",
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("the asset detail does not carry %q:\n%s", want, pane)
		}
	}
}
