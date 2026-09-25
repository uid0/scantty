package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_fixture_refills_test.go — the bar-honesty fixtures for the fixture
// refill screens (fixture_refills.go), which were written on the record from the
// start rather than converted to it.
//
// Two screens, three surfaces: FixtureRefillScreen as the fixture detail and as
// the cross-fixture queue — whose bars differ by `enter`, `A` and `n` — and
// LocationFixturesScreen. Each is swept in every state its bar changes shape in:
// long and walked, one row, empty, an inactive fixture (where `n` comes off),
// and each of the three prompts; and every window has an OVERSIZED fixture, a
// row taller than the pane, because a footer check whose fixtures cannot push
// the footer off is not checking the footer.

var proseBarFixtureAt = time.Date(2026, 9, 14, 6, 40, 0, 0, time.UTC)

func proseBarFixtureRefillFixtures() []proseBarFixture {
	out := proseBarFlatTriple(proseBarFlatList{
		name: "fixture refill queue", recv: "FixtureRefillScreen", noun: "refill request",
		build: func(n int) proseBarScreen { return proseBarFixtureQueue(n) },
	})
	out = append(out, proseBarFlatTriple(proseBarFlatList{
		name: "fixture detail", recv: "FixtureRefillScreen", noun: "refill request",
		build: func(n int) proseBarScreen { return proseBarFixtureDetail(n, true) },
	})...)
	out = append(out, proseBarFlatTriple(proseBarFlatList{
		name: "location fixtures", recv: "LocationFixturesScreen", noun: "fixture",
		build: func(n int) proseBarScreen { return proseBarLocationFixtures(n) },
	})...)
	const promptImmobile = "the notes box has the focus, so no movement key moves anything"
	out = append(out,
		// An inactive fixture: `n` comes off, because OMS refuses the request.
		proseBarFixture{
			name: "fixture detail/inactive", recv: "FixtureRefillScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarFixtureDetail(proseBarFlatLongRows, false), "j") },
		},
		proseBarFixture{
			name: "fixture detail/inactive, empty", recv: "FixtureRefillScreen",
			build: func() proseBarScreen { return proseBarFixtureDetail(0, false) },
			immobile: "no requests on an inactive fixture — the point of this fixture: " +
				"R, A and n must all be absent, and r and esc named",
		},
		// A landed write: the head carries the server's sentence across the
		// reload, which is the state a key is next pressed in.
		proseBarFixture{
			name: "fixture detail/after a write", recv: "FixtureRefillScreen",
			build: func() proseBarScreen {
				s := proseBarWalk(proseBarFixtureDetail(proseBarFlatLongRows, true), "j").(*FixtureRefillScreen)
				s.note = "Resolved 3 pending refill request(s)"
				return s
			},
		},
		proseBarFixture{
			name: "fixture refill queue/resolve prompt", recv: "FixtureRefillScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarFixtureQueue(3), "R") },
			immobile: promptImmobile,
		},
		proseBarFixture{
			name: "fixture detail/resolve prompt", recv: "FixtureRefillScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarFixtureDetail(3, true), "R") },
			immobile: promptImmobile,
		},
		proseBarFixture{
			name: "fixture detail/resolve all prompt", recv: "FixtureRefillScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarFixtureDetail(3, true), "A") },
			immobile: promptImmobile,
		},
		proseBarFixture{
			name: "fixture detail/request prompt", recv: "FixtureRefillScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarFixtureDetail(0, true), "n") },
			immobile: promptImmobile,
		},
		// A write that failed with no sentence of the server's kept the prompt
		// and what was typed; the failure sits above the bar.
		proseBarFixture{
			name: "fixture detail/resolve all prompt, failed", recv: "FixtureRefillScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				s := proseBarPress(proseBarFixtureDetail(3, true), "A").(*FixtureRefillScreen)
				next, _ := s.Update(fixtureRefillWroteMsg{prompt: fixturePromptResolveAll,
					err: &omsapi.APIError{Status: 502, Message: proseLoadGatewayPage}})
				return next.(proseBarScreen)
			},
			immobile: promptImmobile,
		},
	)
	return append(out, proseBarOversizedFixtureRefills()...)
}

// proseBarOversizedFixtureRefills is every window these screens draw with its
// cursor on a row taller than the pane: a reporter's note of many lines on the
// queue and on a fixture, and a fixture name of many lines on a location.
func proseBarOversizedFixtureRefills() []proseBarFixture {
	return []proseBarFixture{
		{
			name: "fixture refill queue/oversized row", recv: "FixtureRefillScreen",
			build: func() proseBarScreen {
				s := proseBarFixtureQueue(0)
				rows := proseBarRefillRows(1, true)
				rows[0].Notes = proseBarTallName()
				next, _ := s.Update(fixtureRefillLoadedMsg{rows: rows})
				return next.(proseBarScreen)
			},
			immobile: "one refill request, so there is nowhere for the cursor to go",
		},
		{
			name: "fixture detail/oversized row", recv: "FixtureRefillScreen",
			build: func() proseBarScreen {
				s := proseBarFixtureDetail(0, true)
				rows := proseBarRefillRows(1, false)
				rows[0].Notes = proseBarTallName()
				next, _ := s.Update(fixtureRefillLoadedMsg{fixture: s.fixture, rows: rows})
				return next.(proseBarScreen)
			},
			immobile: "one refill request, so there is nowhere for the cursor to go",
		},
		{
			name: "location fixtures/oversized row", recv: "LocationFixturesScreen",
			build: func() proseBarScreen {
				s := NewLocationFixturesScreen(Deps{}, 1200041, "Front bathroom")
				rows := proseBarFixtureRows(1)
				rows[0].Name = proseBarTallName()
				next, _ := s.Update(locationFixturesLoadedMsg{rows: rows})
				return next.(proseBarScreen)
			},
			immobile: "one fixture, so there is nowhere for the cursor to go",
		},
	}
}

// proseBarFixtureLoadScreens is the load-state roster entry for both screens.
func proseBarFixtureLoadScreens() []proseLoadScreen {
	return []proseLoadScreen{
		{name: "fixture detail", recv: "FixtureRefillScreen", loaded: "fixture detail", reload: "r",
			fresh: func(d Deps) proseBarScreen {
				return NewFixtureDetailScreen(d, "352067b3-6d6a-4a9b-ac83-90a0e1958fa3")
			}},
		{name: "fixture refill queue", recv: "FixtureRefillScreen", loaded: "fixture refill queue", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewFixtureRefillQueueScreen(d) }},
		{name: "location fixtures", recv: "LocationFixturesScreen", loaded: "location fixtures", reload: "r",
			fresh: func(d Deps) proseBarScreen {
				return NewLocationFixturesScreen(d, 1200041, "Front bathroom (ground floor, by the laser room)")
			}},
	}
}

// TestFixtureRefills_AnOversizedRowFitsAndMarksItsCut: a row taller than the
// pane is clipped to what the head and the bar leave, the cut is MARKED, and the
// bar is the last thing on a frame that fits.
//
// WATCHED FAILING with proseFlatListFrame swapped for writing every row whole
// under the head: all three reported the frame pushed past 80x24.
func TestFixtureRefills_AnOversizedRowFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	for _, f := range proseBarOversizedFixtureRefills() {
		t.Run(f.name, func(t *testing.T) {
			s := proseBarSize(f.build(), width, height)
			if !proseBarFrameFits(s, height) {
				t.Fatalf("an oversized row pushed the frame past %dx%d, taking the bar with it:\n%s",
					width, height, s.View())
			}
			got := stripANSI(s.View())
			if !strings.Contains(got, " more lines") {
				t.Fatalf("an oversized row was clipped with no mark saying so:\n%s", got)
			}
			if want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width))); !strings.HasSuffix(got, want) {
				t.Fatalf("the bar is not the last thing on the pane:\n%s", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

// proseBarRefillRows is `n` pending requests. Rows differ at the FRONT, for the
// reason AGENTS.md gives about a fixture whose rows differ only past the clip;
// every other one is anonymous and every third carries a note.
func proseBarRefillRows(n int, acrossFixtures bool) []omsapi.FixtureRefillRequest {
	rows := make([]omsapi.FixtureRefillRequest, 0, n)
	for i := 0; i < n; i++ {
		r := omsapi.FixtureRefillRequest{
			ID:              fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1),
			Fixture:         "352067b3-6d6a-4a9b-ac83-90a0e1958fa3",
			FixtureName:     "Bathroom 1 soap dispenser",
			FixtureLocation: "Front bathroom (ground floor, by the laser room)",
			RefillItemName:  "Foaming hand soap refill, 1000 mL cartridge",
			Status:          omsapi.FixtureRefillPending,
			RequestedAt:     proseBarFixtureAt.Add(-time.Duration(i) * time.Hour),
			RequestedActor:  "Anonymous",
		}
		if acrossFixtures {
			r.Fixture = fmt.Sprintf("f0000000-0000-4000-8000-%012d", i+1)
			r.FixtureName = fmt.Sprintf("%02d Bathroom soap dispenser by the laser room door", i+1)
		}
		if i%2 == 1 {
			r.RequestedBy, r.RequestedActor = "maker", "maker"
		}
		if i%3 == 0 {
			r.Notes = "Almost empty, the pump sputters and leaves foam on the counter"
		}
		rows = append(rows, r)
	}
	return rows
}

func proseBarFixtureQueue(n int) *FixtureRefillScreen {
	s := NewFixtureRefillQueueScreen(Deps{})
	next, _ := s.Update(fixtureRefillLoadedMsg{rows: proseBarRefillRows(n, true)})
	return next.(*FixtureRefillScreen)
}

func proseBarFixtureDetail(n int, active bool) *FixtureRefillScreen {
	tag := "FIX-0001"
	fixture := &omsapi.Fixture{
		ID: "352067b3-6d6a-4a9b-ac83-90a0e1958fa3", Name: "Bathroom 1 soap dispenser",
		Description: "Left of the sink, wall mounted", Location: 1200041,
		LocationName:   "Front bathroom (ground floor, by the laser room)",
		RefillItemName: "Foaming hand soap refill, 1000 mL cartridge", RefillItemSKU: "SOAP-1000",
		AssetTag: &tag, IsActive: active, PendingRequestsCount: n,
		RefillItemDetails: &omsapi.FixtureRefillItem{Name: "Foaming hand soap refill", CurrentStock: 4, MinimumStock: 6, NeedsReorder: true},
	}
	s := NewFixtureDetailScreen(Deps{}, fixture.ID)
	next, _ := s.Update(fixtureRefillLoadedMsg{fixture: fixture, rows: proseBarRefillRows(n, false)})
	return next.(*FixtureRefillScreen)
}

func proseBarFixtureRows(n int) []omsapi.Fixture {
	rows := make([]omsapi.Fixture, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, omsapi.Fixture{
			ID:             fmt.Sprintf("f0000000-0000-4000-8000-%012d", i+1),
			Name:           fmt.Sprintf("%02d Bathroom soap dispenser by the laser room door", i+1),
			Location:       1200041,
			RefillItemName: "Foaming hand soap refill, 1000 mL cartridge", RefillItemSKU: "SOAP-1000",
			IsActive: true, PendingRequestsCount: i % 3,
		})
	}
	return rows
}

func proseBarLocationFixtures(n int) *LocationFixturesScreen {
	s := NewLocationFixturesScreen(Deps{}, 1200041, "Front bathroom (ground floor, by the laser room)")
	next, _ := s.Update(locationFixturesLoadedMsg{rows: proseBarFixtureRows(n)})
	return next.(*LocationFixturesScreen)
}

// Compile-time: both screens state the record contract.
var (
	_ proseBarScreen = (*FixtureRefillScreen)(nil)
	_ proseBarScreen = (*LocationFixturesScreen)(nil)
)
