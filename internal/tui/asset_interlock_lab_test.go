//go:build omslab

package tui

import (
	"context"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Drives the real interlock screen against a REAL OpenMakerSuite, end to end.
//
// Build-tagged `omslab` and skipped without its env vars, so it costs CI nothing;
// internal/omsapi/lab_sweep_test.go is the same shape and AGENTS.md's "Verifying
// against a REAL OMS" section is the recipe for standing one up:
//
//	LAB_URL=http://127.0.0.1:8931 LAB_TOKEN=<access> LAB_ASSET=<uuid> \
//	  go test -tags omslab ./internal/tui/ -run TestLabInterlock -count=1 -v
//
// It is kept because a fake built from ScanTTY's own structs cannot disagree with
// them, and three of this screen's claims are about what the SERVER does rather
// than what this client sends: that locking leaves the asset active, that an
// unlock of a STACKED lockout answers 200 and stays locked, and that the refusal
// sentences arrive in two different envelope shapes. Run against remote `main`
// commit a1f8c6e8 it reported all three, plus the disable-does-not-stop warning.
//
// `-count=1` matters: the cache keys on env vars and will replay a stale PASS.
//
// IT MUTATES THE ASSET IT IS POINTED AT — two locks, two unlocks, a disable and an
// enable — so point it at a scratch asset, never a real one.
func TestLabInterlock(t *testing.T) {
	url, tok, id := os.Getenv("LAB_URL"), os.Getenv("LAB_TOKEN"), os.Getenv("LAB_ASSET")
	if url == "" || tok == "" || id == "" {
		t.Skip("set LAB_URL, LAB_TOKEN, LAB_ASSET")
	}
	deps := Deps{OMS: omsapi.New(url, omsapi.WithToken(tok, "")), Ctx: context.Background()}
	s := NewAssetInterlockScreen(deps, id, "")
	r := newTestRoot(s)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	r = next.(Root)
	r = pump(t, r, s.Init(), 0)
	// WAIT FOR THE LOAD BEFORE PRESSING ANYTHING. The first run of this drive did
	// not, and the screen correctly declined `l` with "the state could not be
	// read" — so the reason text was typed into the STATE frame instead, where the
	// `d` in "lab drive" opened the DISABLE confirm and the trailing ctrl+x wrote
	// it. That is the screen behaving exactly as designed, and a harness racing its
	// own load.
	for i := 0; i < 40 && !s.stateKnown(); i++ {
		r = pump(t, r, s.load(), 0)
	}
	if !s.stateKnown() {
		t.Fatalf("the asset never loaded: %s", s.loadErr)
	}

	show := func(step string) {
		t.Logf("=== %s === locked=%v active=%v note=%q err=%q\n%s",
			step, s.isLocked(), s.isActive(), s.note, s.errMsg,
			stripANSI(clampToBox(r.screen.View(), screenBodyWidth(80), screenBodyHeight(40))))
	}
	show("loaded")

	// LOCK
	r = interlockPress(t, r, "l")
	if s.phase != interlockPhaseReason {
		t.Fatalf("l did not open the reason form (phase %v); typing now would go to the "+
			"state frame and its letters are ACTIONS there", s.phase)
	}
	r = interlockType(t, r, "lab drive: spindle bearing seized")
	r = interlockPress(t, r, "enter")
	if s.phase != interlockPhaseConfirm {
		t.Fatalf("enter did not open the confirm; phase = %v", s.phase)
	}
	show("lock confirm")
	r = interlockPress(t, r, "ctrl+x")
	show("after lock")

	// ADD A SECOND LOCK, then unlock once — the 200 that stays locked.
	r = interlockPress(t, r, "l")
	if s.phase != interlockPhaseReason {
		t.Fatalf("l did not open the reason form for the second lock; phase = %v", s.phase)
	}
	r = interlockType(t, r, "lab drive: my own second lock")
	r = interlockPress(t, r, "enter")
	r = interlockPress(t, r, "ctrl+x")
	show("after second lock")
	r = interlockPress(t, r, "u")
	r = interlockPress(t, r, "ctrl+x")
	show("after ONE unlock (expect STILL LOCKED)")
	r = interlockPress(t, r, "u")
	r = interlockPress(t, r, "ctrl+x")
	show("after second unlock")

	// UNLOCK WITH NOTHING LOCKED — the server refusal path.
	s.asset.IsLocked = true // force the key on offer so the server gets to refuse
	r = interlockPress(t, r, "u")
	r = interlockPress(t, r, "ctrl+x")
	show("unlock with nothing locked (expect the server's refusal)")

	// DISABLE / ENABLE
	r = interlockPress(t, r, "d")
	r = interlockPress(t, r, "ctrl+x")
	show("after disable")
	r = interlockPress(t, r, "e")
	r = interlockPress(t, r, "ctrl+x")
	show("after enable")

}
