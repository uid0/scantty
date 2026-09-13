package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_foot_prompts_test.go — the fixtures for the EIGHTH recipe taken out
// of proseBarUnconverted as a group rather than a screen at a time.
//
// THE RECIPE, and what makes these screens one group. Each is a FLAT list —
// every row written, no window — whose answer to a key is drawn UNDER the rows or
// IN PLACE of them, each with a literal of its own: the checklist run's notes box
// under its steps; the work-order attachments' delete confirm under its rows and
// an upload form in their place; the maker-box directory's service notices under
// its rows, two forms and two confirms in their place. The flat-list recipe's
// frame had no room for anything between the rows and the bar, so the literal
// under the rows was the first thing a long list pushed off the pane: `n` opened
// a notes box nobody could see, and `x` asked a question nobody could read.
//
// THE ANSWER IS THE FOOT (proseFlatListFrameFoot): whatever is drawn under the
// rows is spent BEFORE the window gets a line, and proseBar answers for whichever
// surface is up. Two things stay what they were, for the reasons the earlier
// recipes give: a y/n confirm that names its own keys answers nil, and a write
// while it is out is a working line.
//
// EVERY WINDOW HAS AN OVERSIZED FIXTURE — a row taller than the whole pane —
// because a footer check whose fixtures cannot push the footer off is not
// checking the footer. Each was measured against HEAD before the conversion
// (47 to 49 rows against 18, no `esc back` on the pane).

// proseBarFootPromptFixtures is every screen on that recipe, in every state its
// bar changes shape in.
func proseBarFootPromptFixtures() []proseBarFixture {
	const formImmobile = "a form whose boxes hold the focus: no movement key moves anything there"
	out := proseBarFlatTriple(proseBarFlatList{
		name: "work order attachments", recv: "WorkOrderAttachmentsScreen", noun: "attachment",
		build: func(rows int) proseBarScreen { return proseBarWOAttachments(rows) },
	})
	out = append(out,
		// --- checklist run -----------------------------------------------------
		// Finalizable, so the one state that names `f` beside the scan keys.
		proseBarFixture{
			name: "checklist run", recv: "ChecklistRunScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarChecklistRun(proseBarFlatLongRows, true), "j") },
		},
		proseBarFixture{
			name: "checklist run/not finalizable", recv: "ChecklistRunScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarChecklistRun(proseBarFlatLongRows, false), "j") },
		},
		// A scan out: `enter`, `n` and `f` come off, and the pane says why.
		proseBarFixture{
			name: "checklist run/sending", recv: "ChecklistRunScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarChecklistRun(proseBarFlatLongRows, true), "j"), "enter")
			},
		},
		proseBarFixture{
			name: "checklist run/one step", recv: "ChecklistRunScreen",
			build:    func() proseBarScreen { return proseBarChecklistRun(1, false) },
			immobile: "one step, so there is nowhere for the cursor to go",
		},
		proseBarFixture{
			name: "checklist run/no steps", recv: "ChecklistRunScreen",
			build: func() proseBarScreen { return proseBarChecklistRun(0, false) },
			immobile: "no steps — the point of this fixture: the movement and scan segments " +
				"must be absent, and `r` and `esc` named",
		},
		proseBarFixture{
			name: "checklist run/not found", recv: "ChecklistRunScreen",
			build: func() proseBarScreen {
				next, _ := NewChecklistRunScreen(Deps{}, "cmpl-1").Update(checklistRunLoadedMsg{})
				return next.(proseBarScreen)
			},
			immobile: "no run to draw, so nothing to move and nothing to scan",
		},
		proseBarFixture{
			name: "checklist run/notes", recv: "ChecklistRunScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarChecklistRun(proseBarFlatLongRows, true), "j"), "n")
			},
			immobile: "the notes box has the focus, so the movement keys go into it",
		},

		// --- work order attachments: the upload form ---------------------------
		proseBarFixture{
			name: "work order attachments/upload", recv: "WorkOrderAttachmentsScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarPress(proseBarWOAttachments(3), "u") },
		},
		// An upload out: `enter` comes off and the working line sits above the bar.
		proseBarFixture{
			name: "work order attachments/upload, uploading", recv: "WorkOrderAttachmentsScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarPress(proseBarWOUploadTyped(), "enter") },
		},
		proseBarFixture{
			name: "work order attachments/upload, refused with a gateway page", recv: "WorkOrderAttachmentsScreen",
			typing: proseBarFormBox,
			build: func() proseBarScreen {
				s := proseBarPress(proseBarWOUploadTyped(), "enter")
				next, _ := s.Update(woAttachUploadedMsg{err: errors.New(proseLoadGatewayPage)})
				return next.(proseBarScreen)
			},
		},

		// --- maker boxes -------------------------------------------------------
		// The cursor on a QUEUED row: the one state that names `c`.
		proseBarFixture{
			name: "maker boxes", recv: "MakerBoxesScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarMakerBoxes(Deps{}, proseBarFlatLongRows), "j") },
		},
		// The cursor on a row that is not queued: `c` comes off, and pressed it
		// still answers why with a toast, which changes no pane.
		proseBarFixture{
			name: "maker boxes/not queued", recv: "MakerBoxesScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarMakerBoxes(Deps{}, proseBarFlatLongRows), "j"), "j")
			},
		},
		// Billing down: `s` and `p` come off, and the notice saying why is part of
		// the foot the window is budgeted around.
		proseBarFixture{
			name: "maker boxes/billing down", recv: "MakerBoxesScreen",
			build: func() proseBarScreen {
				return proseBarWalk(proseBarMakerBoxes(proseBarMakerBoxesDown(), proseBarFlatLongRows), "j")
			},
		},
		// The three result blocks the list opens with, which are the head the
		// window is budgeted under.
		proseBarFixture{
			name: "maker boxes/last results", recv: "MakerBoxesScreen",
			build: func() proseBarScreen {
				loaded := proseBarMakerBoxes(Deps{}, proseBarFlatLongRows)
				rows := loaded.rows
				s := proseBarWalk(loaded, "j")
				days := 12
				// A landed pre-conversion reloads the directory, so the reload it
				// asked for is delivered too: the fixture is the list the operator
				// sees afterwards, not the load frame in between.
				for _, msg := range []any{
					makerBoxScanMsg{result: &omsapi.MakerBoxScanResult{
						Status: "grace", BinID: "MBX-014", Username: "welder07", DaysRemaining: &days,
					}},
					makerBoxPreConvertMsg{result: &omsapi.MakerBox{
						AssignedUsername: "welder08", DisplayName: "Alex Hernandez-Whitfield",
						IdentitySource: "common_api",
					}},
					makerBoxesLoadedMsg{rows: rows},
				} {
					next, _ := s.Update(msg)
					s = next.(proseBarScreen)
				}
				return s
			},
		},
		proseBarFixture{
			name: "maker boxes/one row", recv: "MakerBoxesScreen",
			build:    func() proseBarScreen { return proseBarMakerBoxes(Deps{}, 1) },
			immobile: "one maker box, so there is nowhere for the cursor to go",
		},
		proseBarFixture{
			name: "maker boxes/empty", recv: "MakerBoxesScreen",
			build: func() proseBarScreen { return proseBarMakerBoxes(Deps{}, 0) },
			immobile: "no maker boxes — the point of this fixture: the movement segment and " +
				"every row action must be absent, and `n`, `s`, `p`, `r` and `esc` named",
		},
		proseBarFixture{
			name: "maker boxes/pre-convert", recv: "MakerBoxesScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarMakerBoxes(Deps{}, 3), "p") },
			immobile: formImmobile,
		},
		proseBarFixture{
			name: "maker boxes/pre-convert, badge lookups down", recv: "MakerBoxesScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				deps := Deps{Health: ssHealth(map[string]string{omsapi.ServiceKeyCommonAPI: omsapi.ServiceStateOpen})}
				return proseBarPress(proseBarMakerBoxes(deps, 3), "p")
			},
			immobile: formImmobile,
		},
		proseBarFixture{
			name: "maker boxes/scan", recv: "MakerBoxesScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarMakerBoxes(Deps{}, 3), "s") },
			immobile: formImmobile,
		},
		proseBarFixture{
			name: "maker boxes/scan, username field", recv: "MakerBoxesScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarMakerBoxes(Deps{}, 3), "s", "tab") },
			immobile: formImmobile,
		},
	)
	return append(out, proseBarOversizedFootPrompts()...)
}

// proseBarOversizedFootPrompts is every window this recipe introduced, each with
// its cursor on a row taller than the pane — and the two feet that are more than
// a bar, the notes box and the delete confirm, drawn under such a row.
func proseBarOversizedFootPrompts() []proseBarFixture {
	return []proseBarFixture{
		{
			name: "checklist run/oversized row", recv: "ChecklistRunScreen",
			build: func() proseBarScreen {
				s := proseBarChecklistRun(1, false)
				s.checklist.Steps[0].Name = proseBarTallName()
				return s
			},
			immobile: "one step, so there is nowhere for the cursor to go",
		},
		{
			name: "checklist run/notes, oversized row", recv: "ChecklistRunScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				s := proseBarChecklistRun(1, false)
				s.checklist.Steps[0].Name = proseBarTallName()
				return proseBarPress(s, "n")
			},
			immobile: "the notes box has the focus, so the movement keys go into it",
		},
		{
			name: "work order attachments/oversized row", recv: "WorkOrderAttachmentsScreen",
			build: func() proseBarScreen {
				s := proseBarWOAttachments(1)
				s.attachments[0].Description = proseBarTallName()
				return s
			},
			immobile: "one attachment, so there is nowhere for the cursor to go",
		},
		{
			name: "maker boxes/oversized row", recv: "MakerBoxesScreen",
			build: func() proseBarScreen {
				s := proseBarMakerBoxes(Deps{}, 1)
				s.rows[0].DisplayName = proseBarTallName()
				return s
			},
			immobile: "one maker box, so there is nowhere for the cursor to go",
		},
	}
}

// TestProseBarFootPrompts_AnOversizedRowFitsAndMarksItsCut holds every window
// this recipe introduced to the bar the flat lists were held to: a row taller
// than the pane is clipped to what the foot leaves, the cut is MARKED, and the
// frame still fits — so the bar, and whatever the foot carries above it, survive.
//
// WATCHED FAILING against HEAD before the conversion: 49, 47 and 48 rows against
// 18 on the run, the attachments and the directory, with no `esc back` on the
// clipped pane.
func TestProseBarFootPrompts_AnOversizedRowFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	for _, f := range proseBarOversizedFootPrompts() {
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

// TestProseBarFootPrompts_TheFootSurvivesTheRowsAtEveryDrawablePane: what is
// drawn UNDER the rows — the notes box, the delete confirm, the service notices —
// reaches the operator, and the frame carrying it fits, at every pane Root draws
// that is tall enough for the foot and the least window the list will draw.
//
// The confirm and the working line under it answer nil from proseBar, so the
// footer sweeps never read them; this is where they are held.
//
// THE BOUNDARY IS DERIVED, NOT "WHEREVER THE FRAME FITS", and that is the lesson
// of this test's first version. Scoped by proseBarFrameFits it skipped every pane
// a foot left out of the budget had pushed the frame past — which is the defect
// itself — so with the notes box's and the notices' rows taken out of the budget
// it went on passing. The least the screen needs is measured off the SAME state
// built with one ordinary row (`least`), plus both scroll markers and the rows the
// window's floor can add past one row; at or above that the frame must fit, and
// below it the claim is not made. Both sides must be reached.
//
// WATCHED FAILING with each foot's rows left out of the budget: all three
// screens, at every width, from the height the boundary names.
func TestProseBarFootPrompts_TheFootSurvivesTheRowsAtEveryDrawablePane(t *testing.T) {
	cases := []struct {
		name  string
		build func() proseBarScreen
		least func() proseBarScreen
		want  []string
	}{
		{
			name:  "checklist run/notes",
			build: func() proseBarScreen { return proseBarPress(proseBarChecklistRun(proseBarFlatLongRows, true), "n") },
			least: func() proseBarScreen { return proseBarPress(proseBarChecklistRun(1, true), "n") },
			want:  []string{"Notes for this step", "enter scan with notes", "esc cancel"},
		},
		{
			name: "work order attachments/delete confirm",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWOAttachments(proseBarFlatLongRows), "j", "j", "x")
			},
			least: func() proseBarScreen { return proseBarPress(proseBarWOAttachments(1), "x") },
			want:  []string{"Delete ", "y delete · n/esc cancel"},
		},
		{
			name: "work order attachments/delete confirm, oversized row",
			build: func() proseBarScreen {
				s := proseBarWOAttachments(1)
				s.attachments[0].Description = proseBarTallName()
				return proseBarPress(s, "x")
			},
			least: func() proseBarScreen { return proseBarPress(proseBarWOAttachments(1), "x") },
			want:  []string{"Delete ", "y delete · n/esc cancel"},
		},
		{
			name: "work order attachments/deleting",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWOAttachments(proseBarFlatLongRows), "x", "y")
			},
			least: func() proseBarScreen { return proseBarPress(proseBarWOAttachments(1), "x", "y") },
			want:  []string{"Deleting…"},
		},
		{
			name: "maker boxes/billing down",
			build: func() proseBarScreen {
				return proseBarMakerBoxes(proseBarMakerBoxesDown(), proseBarFlatLongRows)
			},
			least: func() proseBarScreen { return proseBarMakerBoxes(proseBarMakerBoxesDown(), 1) },
			want:  []string{"Membership lookups are", "esc back"},
		},
	}
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			held, short := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					// Both markers, and the window floor's rows past the one row
					// the least frame drew.
					need := lipgloss.Height(proseBarSize(c.least(), w, h).View()) + 2 + (proseListWindowFloor - 1)
					if screenBodyHeight(h) < need || screenBodyRows(h) < need {
						short++
						continue
					}
					held++
					s := proseBarSize(c.build(), w, h)
					if !proseBarFrameFits(s, h) {
						t.Errorf("at %dx%d the frame needs %d rows and the pane has %d, so the foot "+
							"is what clampToBox takes:\n%s", w, h, lipgloss.Height(s.View()), screenBodyRows(h),
							stripANSI(s.View()))
						continue
					}
					pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
					for _, want := range c.want {
						if !strings.Contains(pane, want) {
							t.Errorf("at %dx%d the pane does not carry %q:\n%s", w, h, want, pane)
						}
					}
				}
			}
			if held == 0 || short == 0 {
				t.Errorf("%s reached %d panes past the boundary and %d short of it, so one side "+
					"was never tested", c.name, held, short)
			}
		})
	}
}

// TestMakerBoxes_TheConvertKeyFollowsTheRowUnderTheCursor: `c` is named on a
// queued row and nowhere else, and off a queued row it still answers why.
func TestMakerBoxes_TheConvertKeyFollowsTheRowUnderTheCursor(t *testing.T) {
	queued := proseBarWalk(proseBarMakerBoxes(Deps{}, 9), "j").(*MakerBoxesScreen)
	if !queued.queuedSelected() || !queued.proseBar().names("c") {
		t.Fatalf("on a queued row the bar must name c: %s", queued.proseBar().hint())
	}
	valid := proseBarPress(queued, "j").(*MakerBoxesScreen)
	if valid.queuedSelected() || valid.proseBar().names("c") {
		t.Fatalf("off a queued row the bar must not name c: %s", valid.proseBar().hint())
	}
	if _, cmd := valid.Update(listRuneKey("c")); cmd == nil {
		t.Error("c off a queued row said nothing — a decline must say why")
	} else if msg, ok := cmd().(StatusMsg); !ok || !strings.Contains(msg.Text, "not on a queued row") {
		t.Errorf("c off a queued row answered %#v, want the toast saying why", cmd())
	}
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

var proseBarFootAt = time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

// proseBarChecklistRun is a run of `n` steps, every one with a scan target, a
// description on every other one and a photo step on every fourth — never the
// one the long fixture's cursor is walked to, so `enter` there scans.
func proseBarChecklistRun(n int, finalizable bool) *ChecklistRunScreen {
	steps := make([]omsapi.ChecklistStep, 0, n)
	for i := 0; i < n; i++ {
		loc := 700 + i
		step := omsapi.ChecklistStep{
			ID: fmt.Sprintf("step-%d", i+1), StepNumber: i + 1,
			Name:     fmt.Sprintf("Wipe the laser lens and log the tube hours, bay %d", i+1),
			Location: &loc, Required: i%3 == 0, RequiresPhoto: i%4 == 1,
		}
		if i%2 == 0 {
			step.Notes = "Use the lens tissue in the drawer under the controller, never the shop rags"
		}
		steps = append(steps, step)
	}
	completion := &omsapi.ChecklistCompletion{
		ID: "cmpl-1", Checklist: "cl-1", Status: "in_progress",
		TotalStepsCount: n, RequiredStepsTotal: 4, RequiredStepsCompleted: 1,
	}
	if finalizable {
		completion.RequiredStepsCompleted = completion.RequiredStepsTotal
	}
	s := NewChecklistRunScreen(Deps{}, "cmpl-1")
	next, _ := s.Update(checklistRunLoadedMsg{
		checklist:  &omsapi.Checklist{ChecklistSummary: omsapi.ChecklistSummary{ID: "cl-1", Name: "Laser cutter end-of-day shutdown"}, Steps: steps},
		completion: completion,
	})
	return next.(*ChecklistRunScreen)
}

func proseBarWOAttachments(n int) *WorkOrderAttachmentsScreen {
	atts := make([]omsapi.WorkOrderAttachment, 0, n)
	kinds := []string{omsapi.WorkOrderAttachmentDocument, omsapi.WorkOrderAttachmentPhoto, ""}
	for i := 0; i < n; i++ {
		att := omsapi.WorkOrderAttachment{
			ID:   fmt.Sprint(i + 1),
			File: fmt.Sprintf("work_orders/42/haas-vf2-spindle-chiller-service-report-%02d.pdf", i+1),
			Kind: kinds[i%len(kinds)],
		}
		if i%2 == 0 {
			att.Description = "As-built wiring for the spindle chiller after the pump swap"
			att.UploadedAt = proseBarFootAt
		}
		atts = append(atts, att)
	}
	s := NewWorkOrderAttachmentsScreen(Deps{}, "42")
	next, _ := s.Update(woAttachLoadedMsg{atts: atts})
	return next.(*WorkOrderAttachmentsScreen)
}

// proseBarWOUploadTyped opens the upload form and TYPES the path of a file that
// exists — this one — so `enter` passes the form's own checks and starts the
// upload, as an operator's would.
func proseBarWOUploadTyped() proseBarScreen {
	s := proseBarPress(proseBarWOAttachments(3), "u")
	for _, r := range "prose_bar_foot_prompts_test.go" {
		s = proseBarPress(s, string(r))
	}
	return s
}

// proseBarMakerBoxes is a directory of `n` boxes whose every third row, starting
// with the one the long fixture's cursor is walked to, is QUEUED — so a walk of
// one more row lands on a row that is not.
func proseBarMakerBoxes(deps Deps, n int) *MakerBoxesScreen {
	statuses := []string{"pre_conversion", "valid", "grace"}
	rows := make([]omsapi.MakerBox, 0, n)
	for i := 0; i < n; i++ {
		expires := proseBarFootAt.AddDate(0, 3, 0)
		row := omsapi.MakerBox{
			ID: i + 1, BinID: fmt.Sprintf("MBX-%03d", i+1),
			AssignedUsername: fmt.Sprintf("welder%02d", i+1),
			DisplayName:      fmt.Sprintf("Alex Hernandez-Whitfield of the metal shop %d", i+1),
			Status:           statuses[i%len(statuses)],
		}
		if i%2 == 0 {
			row.ExpiresAt, row.LastVerifiedAt, row.PaidAt = &expires, &expires, &expires
		}
		rows = append(rows, row)
	}
	s := NewMakerBoxesScreen(deps)
	next, _ := s.Update(makerBoxesLoadedMsg{rows: rows})
	return next.(*MakerBoxesScreen)
}

// proseBarMakerBoxesDown is a Deps whose billing breaker is open.
func proseBarMakerBoxesDown() Deps {
	return Deps{Health: ssHealth(map[string]string{omsapi.ServiceKeyWHMCS: omsapi.ServiceStateOpen})}
}

// Compile-time: every screen this recipe converts states the record contract.
var (
	_ proseBarScreen = (*ChecklistRunScreen)(nil)
	_ proseBarScreen = (*WorkOrderAttachmentsScreen)(nil)
	_ proseBarScreen = (*MakerBoxesScreen)(nil)
)
