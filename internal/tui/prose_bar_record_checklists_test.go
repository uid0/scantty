package tui

import (
	"fmt"
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_record_checklists_test.go — the per-record checklist list
// (record_checklists.go) in every state its bar changes shape in.
//
// It is a FLAT cursor list, so it takes the flat recipe's three states (a long
// list that outruns the pane, one row, none) and adds the two its `enter` makes:
// a START IN FLIGHT, where enter comes off the bar and the head says why, and a
// REFUSED START, where the server's sentence rides the head the window is
// budgeted under and enter is back. Both of those are built from the LONG list,
// so the refusal's two rows are measured against a window that really has to
// give them up.

func proseBarRecordChecklistFixtures() []proseBarFixture {
	out := proseBarFlatTriple(proseBarFlatList{
		name: "record checklists", recv: "RecordChecklistsScreen", noun: "checklist",
		build: func(rows int) proseBarScreen { return proseBarRecordChecklists(rows) },
	})
	return append(out,
		proseBarFixture{
			name: "record checklists/starting", recv: "RecordChecklistsScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarRecordChecklists(proseBarFlatLongRows), "j"), "enter")
			},
		},
		proseBarFixture{
			name: "record checklists/refused", recv: "RecordChecklistsScreen",
			build: func() proseBarScreen {
				s := proseBarWalk(proseBarRecordChecklists(proseBarFlatLongRows), "j")
				next, _ := s.Update(recordChecklistStartedMsg{err: &omsapi.APIError{
					Status:  403,
					Message: `{"detail":"You do not have permission to start this checklist."}`,
				}})
				return next.(proseBarScreen)
			},
		},
	)
}

// proseBarRecordChecklists is an asset's checklist list holding `n` rows, with
// the lengths and the second lines OMS really serves: a full checklist name, a
// SIG, and a two-line description on most rows.
func proseBarRecordChecklists(n int) *RecordChecklistsScreen {
	rows := make([]omsapi.ChecklistSummary, 0, n)
	for i := 0; i < n; i++ {
		r := omsapi.ChecklistSummary{
			ID:        fmt.Sprintf("cl-%d", i+1),
			Name:      fmt.Sprintf("%d. Opening walkthrough — wood shop, south wall", i+1),
			SIGName:   "Woodshop SIG",
			IsActive:  true,
			IsPublic:  i%3 != 1,
			StepCount: 3 + i%4,
		}
		if i%4 != 3 {
			r.Description = strings.Join([]string{
				"Walk the shop before members arrive.",
				"Scan each station as you check it.",
			}, "\n")
		}
		rows = append(rows, r)
	}
	s := NewAssetChecklistsScreen(Deps{}, "a-1", "SawStop PCS 3HP cabinet saw")
	next, _ := s.Update(recordChecklistsLoadedMsg{rows: rows})
	return next.(*RecordChecklistsScreen)
}
