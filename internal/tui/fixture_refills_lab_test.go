//go:build omslab

package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// A live drive of the fixture refill screens through Root against a REAL
// OpenMakerSuite, run with -tags omslab and SCANTTY_OMS_URL, SCANTTY_OMS_TOKEN
// and SCANTTY_LAB_FIXTURE (an ACTIVE fixture id) set. It files requests, so run
// it against a lab database, never a shared one.
//
// What it re-measures, because the screens' prompts state it as fact: a resolve
// with a blank notes box KEEPS the reporter's note, and resolve-all with typed
// notes REPLACES them.
func TestLab_FixtureRefillsAgainstARealBackend(t *testing.T) {
	url, token, fixture := os.Getenv("SCANTTY_OMS_URL"), os.Getenv("SCANTTY_OMS_TOKEN"), os.Getenv("SCANTTY_LAB_FIXTURE")
	if url == "" || token == "" || fixture == "" {
		t.Skip("set SCANTTY_OMS_URL, SCANTTY_OMS_TOKEN and SCANTTY_LAB_FIXTURE")
	}
	c := omsapi.New(url, omsapi.WithToken(token, ""))
	ctx := context.Background()
	deps := Deps{OMS: c, Ctx: ctx}
	enter := tea.KeyMsg{Type: tea.KeyEnter}

	// Clear the fixture so the drive's rows are its own.
	if _, err := c.ResolveAllFixtureRefillRequests(ctx, fixture, ""); err != nil {
		t.Fatalf("clearing the fixture: %v", err)
	}

	r := fixtureDrive(t, deps, NewFixtureDetailScreen(deps, fixture))
	r = key(t, r, woRuneKey("n"))
	r = fixtureType(t, r, "Lab: reporter's own note")
	r = key(t, r, enter)
	if pane := fixturePane(r); !strings.Contains(pane, "✓ Refill request filed") || !strings.Contains(pane, "Pending refill requests (1)") {
		t.Fatalf("filing through n did not land on a reloaded list of one:\n%s", pane)
	}

	queue := fixtureDrive(t, deps, NewFixtureRefillQueueScreen(deps))
	if pane := fixturePane(queue); !strings.Contains(pane, "Lab: reporter's own note") {
		t.Errorf("the queue does not carry the request just filed:\n%s", pane)
	}

	r = key(t, r, woRuneKey("R"))
	r = key(t, r, enter)
	rows, err := c.ListFixtureRefillRequests(ctx, omsapi.FixtureRefillRequestFilter{Fixture: fixture, Status: omsapi.FixtureRefillCompleted})
	if err != nil || len(rows) == 0 {
		t.Fatalf("reading the completed requests: %v (%d rows)", err, len(rows))
	}
	if rows[0].Notes != "Lab: reporter's own note" {
		t.Errorf("a blank-notes resolve left notes %q; the prompt promises the reporter's note is kept", rows[0].Notes)
	}

	for _, note := range []string{"Lab: first reporter", "Lab: second reporter"} {
		if _, err := c.ScanFixture(ctx, fixture, note); err != nil {
			t.Fatalf("filing %q: %v", note, err)
		}
	}
	r = key(t, r, woRuneKey("r"))
	r = key(t, r, woRuneKey("A"))
	r = fixtureType(t, r, "Lab: refilled")
	r = key(t, r, enter)
	if pane := fixturePane(r); !strings.Contains(pane, "✓ Resolved 2 pending refill request(s)") {
		t.Errorf("resolve all did not relay the server's sentence:\n%s", pane)
	}
	rows, err = c.ListFixtureRefillRequests(ctx, omsapi.FixtureRefillRequestFilter{Fixture: fixture, Status: omsapi.FixtureRefillCompleted})
	if err != nil {
		t.Fatal(err)
	}
	replaced := 0
	for _, row := range rows {
		if row.Notes == "Lab: refilled" {
			replaced++
		}
	}
	if replaced != 2 {
		t.Errorf("%d requests carry the resolve-all notes, want 2; the prompt promises typed notes replace each one", replaced)
	}
}
