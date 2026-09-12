package tui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The two DERIVED sweeps over the asset interlock screen: every phase is swept,
// and on each one the bar names exactly the keys that work.
//
// They are derived rather than listed for the reason AGENTS.md records three
// times over: every key that ever reached an operator's terminal doing nothing
// while the bar named it got there past a sweep reading a hand-kept roster the
// key was not in. So the PHASES come from the iota walked to its sentinel, and the
// KEYS are the whole SPACE — every printable ASCII rune plus the named specials —
// because no authority can be derived for them and a curated vocabulary presses a
// missing key in NEITHER direction.
//
// This screen earns the strict treatment more than most: its keys stop and start
// machines, so a bar naming a dead key is a legend an operator cannot trust about
// the one thing they cannot check by looking.

// interlockPhaseCases builds one screen per (phase, state) the operator can reach.
//
// A phase's cases must span every state its BAR CHANGES SHAPE IN, which is the
// axis a derived phase roster says nothing about (AGENTS.md: "a derived roster is
// one axis, and a sweep has two"). On the state frame that is the four
// combinations the two interlock axes make, the could-not-tell of a failed load,
// and a write in flight; on the confirm it is one per action, the second-lock
// branch whose wording differs, and again a write in flight.
func interlockPhaseCases() map[interlockPhase]map[string]func() *AssetInterlockScreen {
	return map[interlockPhase]map[string]func() *AssetInterlockScreen{
		interlockPhaseState: {
			"locked and active":     func() *AssetInterlockScreen { return assetInterlockFixture(true, true) },
			"unlocked and active":   func() *AssetInterlockScreen { return assetInterlockFixture(false, true) },
			"locked and disabled":   func() *AssetInterlockScreen { return assetInterlockFixture(true, false) },
			"unlocked and disabled": func() *AssetInterlockScreen { return assetInterlockFixture(false, false) },
			"load failed": func() *AssetInterlockScreen {
				s := assetInterlockFixture(false, true)
				s.asset, s.loadErr = nil, "oms: http 502: upstream is down"
				return s
			},
			// A WRITE IN FLIGHT is a state of its own and it is the half a
			// freshly-opened fixture never reaches — the trap AGENTS.md records
			// for the destructive confirms, whose sweeps all measured the frame
			// before anybody pressed the destroy key. It is REACHED BY SETTING
			// `saving` rather than by pressing Ctrl-X because the drive helpers
			// settle the command they get, so a pressed write always completes;
			// the state itself is ordinary (any server slower than instant), and
			// `saving` is exactly what write() sets on the way out.
			"write in flight": func() *AssetInterlockScreen {
				s := assetInterlockFixture(true, true)
				s.saving = true
				return s
			},
		},
		interlockPhaseReason: {
			"fresh": func() *AssetInterlockScreen {
				s := assetInterlockFixture(false, true)
				s.openAction(interlockLock, "l")
				return s
			},
			"reason typed": func() *AssetInterlockScreen {
				s := assetInterlockFixture(false, true)
				s.openAction(interlockLock, "l")
				s.reason.SetValue("spindle bearing seized")
				return s
			},
		},
		interlockPhaseConfirm: {
			"lock":        func() *AssetInterlockScreen { return assetInterlockConfirmFixture(interlockLock, false, true) },
			"second lock": func() *AssetInterlockScreen { return assetInterlockConfirmFixture(interlockLock, true, true) },
			"unlock":      func() *AssetInterlockScreen { return assetInterlockConfirmFixture(interlockUnlock, true, true) },
			"disable":     func() *AssetInterlockScreen { return assetInterlockConfirmFixture(interlockDisable, false, true) },
			"enable":      func() *AssetInterlockScreen { return assetInterlockConfirmFixture(interlockEnable, true, false) },
			// The confirm with its own write OUT: Esc still leaves and the caveat
			// still scrolls, but Ctrl-X must be off the bar AND inert.
			"write in flight": func() *AssetInterlockScreen {
				s := assetInterlockConfirmFixture(interlockUnlock, true, true)
				s.saving = true
				return s
			},
		},
	}
}

// EVERY PHASE OF THE IOTA IS SWEPT. Walked to the sentinel, so a phase added later
// fails this until somebody says what its states are — rather than joining the app
// in no sweep at all.
func TestInterlockSweep_EveryPhaseIsSwept(t *testing.T) {
	cases := interlockPhaseCases()
	for p := interlockPhase(0); p < interlockPhaseCount; p++ {
		states, ok := cases[p]
		if !ok {
			t.Errorf("phase %d is in the interlockPhase iota and has no cases in "+
				"interlockPhaseCases, so no sweep in this file ever reaches it", p)
			continue
		}
		if len(states) == 0 {
			t.Errorf("phase %d has an EMPTY case set; absent and empty are different "+
				"states and an empty one asserts nothing", p)
		}
		for name, mk := range states {
			if got := mk().phase; got != p {
				t.Errorf("phase %d case %q builds a screen in phase %d instead, so it is "+
					"sweeping the wrong frame", p, name, got)
			}
		}
	}
	if len(cases) != int(interlockPhaseCount) {
		t.Errorf("interlockPhaseCases has %d phases and the iota has %d; a stale entry "+
			"passes in silence", len(cases), interlockPhaseCount)
	}
}

// interlockBarKeys is the set of keystrokes a drawn bar NAMES, read through the
// package's own transcription table.
//
// poBarKeyNames is that table and a token it does not know FAILS rather than being
// skipped — which is what keeps the convention true, since a bar displaying `L` for
// a key that is really `l` would be spelling a keystroke nobody presses.
func interlockBarKeys(t *testing.T, items []actionBarItem) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, item := range items {
		keys, ok := poBarKeyNames[item.Key]
		if !ok {
			t.Fatalf("bar token %q is in no transcription table, so the sweep cannot tell "+
				"which keystroke it claims. Add it to poBarKeyNames", item.Key)
		}
		for _, k := range keys {
			out[k] = true
		}
	}
	return out
}

// interlockBarFor is the bar the phase really draws.
func interlockBarFor(s *AssetInterlockScreen) []actionBarItem {
	switch s.phase {
	case interlockPhaseReason:
		return interlockReasonBar
	case interlockPhaseConfirm:
		return s.confirmBar()
	}
	return s.stateBar()
}

// interlockEffect is what a key DID, deliberately excluding what it SAID.
//
// THIS DISTINCTION IS THE WHOLE FORWARD CLAIM, and getting it wrong made the first
// version of this sweep vacuous. It compared a fingerprint that carried the NOTE
// and the rendered PANE, so a key that DECLINED and said why changed it and read as
// working — verified by naming a dead key `v` on the state bar and watching the
// sweep stay green over it.
//
// A key that declines and says why has not ACTED. That is the answer rule this
// screen obeys on purpose, since its frames hold no cursor and no caret and a
// silent return would redraw an identical pane. So the EFFECT is the frame it
// leaves you on, where you are in it, and whether a write went out: phase, the two
// scroll offsets, the pending action, saving, the state the asset came back in, and
// the operator's typed reason. note, noteLevel and errMsg are excluded — and
// excluded by being LEFT OUT of a hand-written list rather than filtered out of a
// reflected one, so the list is short enough to read and a field added later is
// absent until somebody decides which side of the line it is on.

func interlockEffect(s *AssetInterlockScreen) string {
	return fmt.Sprintf("phase=%d;scroll=%d;confirmScroll=%d;pending=%d;saving=%t;"+
		"locked=%t;active=%t;loading=%t;reason=%q;focus=%t",
		s.phase, s.scroll, s.confirmScroll, s.pending, s.saving,
		s.isLocked(), s.isActive(), s.loading, s.reason.Value(), s.reason.Focused())
}

// THE BAR NAMES EXACTLY THE KEYS THAT WORK, on every phase and in every state its
// bar changes shape in, pressed over the WHOLE KEY SPACE.
//
// ASKED AT TWO GRANULARITIES, which is the layer's own rule and not a convenience:
//
//   - FORWARD ("a named token must act") per TOKEN, pressing every key the token
//     spells in sequence on one screen. A token names a PAIR, and `up` on a sheet
//     already at the top legitimately moves nothing — so the claim is that SOME key
//     the token spells does something, which is exactly what an operator reads a
//     token as promising.
//   - REVERSE ("a key that acts must be named") per KEY, because "is this key
//     named" cannot be answered about a pair.
//
// AND THE REVERSE HALF IS ASKED OF THE WRITE, not of the pane. A key that declines
// and SAYS WHY has not acted — that is the answer rule this screen obeys
// deliberately, since its frames hold no cursor and no caret and a silent return
// would redraw an identical pane. So the thing an unnamed key may never do is reach
// the WIRE, which on this screen means stopping or starting a machine.
func TestInterlockSweep_TheBarNamesExactlyTheKeysThatWork(t *testing.T) {
	space := poKeySpace()
	// A typed frame routes every key its switch does not name to its BOX on
	// purpose: a rune is the operator's input, and declining it would discard what
	// they typed — including a scanner burst. So only the write claim applies there.
	typedPhases := map[interlockPhase]bool{interlockPhaseReason: true}

	pressed, tokens := 0, 0
	for p, states := range interlockPhaseCases() {
		for name, mk := range states {
			// ---- FORWARD, per token ----
			for _, item := range interlockBarFor(mk()) {
				keys, ok := poBarKeyNames[item.Key]
				if !ok {
					t.Fatalf("bar token %q is in no transcription table, so the sweep cannot "+
						"tell which keystroke it claims. Add it to poBarKeyNames", item.Key)
				}
				if typedPhases[p] {
					continue // its keys are the box's; the write claim below covers it
				}
				fresh := mk()
				jdeRootAt(t, fresh, 80, 24)
				before := interlockEffect(fresh)
				acted := false
				for _, k := range keys {
					next, cmd := fresh.Update(poPhaseKeyMsg(k))
					if after, ok := next.(*AssetInterlockScreen); ok {
						fresh = after
					}
					// A COMMAND counts as acting, and it has to: Esc LEAVES this
					// screen and `r` fetches, so their whole product is a command the
					// screen's own state cannot show. Judged on state alone, the one
					// key every frame here names would read as dead.
					if interlockCmdActs(cmd) || interlockEffect(fresh) != before {
						acted = true
						break
					}
				}
				tokens++
				if !acted {
					t.Errorf("%v/%s: the bar names %q and no key it spells (%v) does "+
						"anything", p, name, item.Key, keys)
				}
			}

			// ---- REVERSE, per key ----
			//
			// THE WRITE IS DETECTED SYNCHRONOUSLY, off `saving`, and that is both the
			// exact answer and the cheap one. AssetInterlockScreen.write sets saving
			// BEFORE it returns its command, so a key that started a write has
			// already flipped the flag by the time Update returns — no command has to
			// be run to find out.
			//
			// Running them was the first shape and it cost 100 seconds: a tea.Cmd
			// that is a TIMER blocks until it fires, and bubbles hands one back from
			// textinput.Update for nearly every key (the cursor tick, 530ms), so the
			// two typed REASON states alone spent ~220 presses waiting one out. This
			// reads a flag instead, and is more precise as well as ~40× faster — a
			// clock-based probe can only ever infer what a synchronous flag states.
			for _, k := range space {
				fresh := mk()
				jdeRootAt(t, fresh, 80, 24)
				named := interlockBarKeys(t, interlockBarFor(fresh))[k]
				wasSaving := fresh.saving
				next, _ := fresh.Update(poPhaseKeyMsg(k))
				after, ok := next.(*AssetInterlockScreen)
				if !ok {
					t.Fatalf("%v/%s: Update returned %T", p, name, next)
				}
				pressed++
				if !named && !wasSaving && after.saving {
					t.Errorf("%v/%s: %q started a WRITE and the bar does not name it — on "+
						"this screen an unnamed key that reaches the wire stops or starts "+
						"a machine", p, name, k)
				}
			}
		}
	}
	if pressed == 0 || tokens == 0 {
		t.Fatalf("pressed %d keys against %d tokens, so this sweep asserted nothing",
			pressed, tokens)
	}
	t.Logf("pressed %d (phase, state, key) pairs and judged %d drawn bar tokens",
		pressed, tokens)
}

// interlockCmdActs reports a command that DOES something, discounting the cursor
// blink.
//
// bubbles falls through to Cursor.Update for any key its own switch does not
// handle and that returns a tick unconditionally, so without the blink filter every
// key pressed inside a focused box reads as acting — the poCmdActs / poIsBlink
// lesson, one screen over.
func interlockCmdActs(cmd tea.Cmd) bool {
	msg, panicked := interlockRunCmd(cmd)
	if panicked {
		// Reaching the wire is the most emphatic way a key can act.
		return true
	}
	if msg == nil || driveIsBlink(msg) {
		return false
	}
	return true
}

// interlockRunCmd runs one command and reports its message, whether it panicked,
// and NOTHING at all when it does not answer promptly.
//
// THE BUDGET IS WHY THIS EXISTS, and it cost 100 seconds to learn. A tea.Cmd that
// is a TIMER blocks until the timer fires, and bubbles hands one back from
// textinput.Update for nearly every key: the cursor tick, at 530ms. The reverse
// half of the sweep runs a command per unnamed key per state, so on the two typed
// REASON states alone that was ~220 presses × 530ms — 116s of a 103s test, in a
// package that has hit `go test`'s 600s per-package timeout twice already
// (AGENTS.md records both).
//
// A cmd that does not answer inside the budget is a TIMER, and a timer is not a
// write: a write against these fixtures has no OMS client and panics instantly, so
// the two are told apart by the clock without having to recognise either. The
// budget is generous for the same reason pump's is — it is a backstop, not a
// measurement.
func interlockRunCmd(cmd tea.Cmd) (msg tea.Msg, panicked bool) {
	if cmd == nil {
		return nil, false
	}
	done := make(chan tea.Msg, 1)
	fail := make(chan struct{}, 1)
	go func() {
		defer func() {
			if recover() != nil {
				fail <- struct{}{}
			}
		}()
		done <- cmd()
	}()
	select {
	case m := <-done:
		return m, false
	case <-fail:
		return nil, true
	case <-time.After(50 * time.Millisecond):
		return nil, false
	}
}
