package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// NewProjectStorageListScreen is the browsable OVERVIEW of project/personal
// storage stints, opened from the Facilities menu. It reuses the generic
// ListScreen; enter opens a per-stint DETAIL screen that includes a text
// label preview. Mirrors the OMS web "items overview" surface.
func NewProjectStorageListScreen(deps Deps) *ListScreen {
	return NewListScreen(deps, "Project Storage", listScreenSpec{
		kind:      "project_storage",
		loader:    loadProjectStorageStints,
		detail:    func(id string, d Deps) Screen { return NewProjectStorageDetailScreen(d, id) },
		newScreen: func(d Deps) Screen { return NewProjectStorageFormScreen(d) },
	})
}

// loadProjectStorageStints defaults to the active stints — the day-to-day
// "who has storage right now" view. Expired / purgatory / removed stints
// stay queryable on the backend via ?status= but aren't surfaced here (the
// generic ListScreen has no per-screen filter state to cycle them yet).
func loadProjectStorageStints(ctx context.Context, deps Deps) ([]listRow, error) {
	q := url.Values{}
	q.Set("status", "active")
	page, err := deps.OMS.ListProjectStorageStints(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, st := range page.Results {
		title := st.DisplayName
		if title == "" {
			title = st.Username
		}
		subtitle := st.ProjectTitle
		if subtitle == "" {
			subtitle = "(personal)"
		}
		row := listRow{
			ID:       st.StintID,
			Title:    title,
			Subtitle: subtitle,
			Tag:      st.Status,
		}
		// ExpiresAt is nullable; only feed the date column when present so
		// the "due soonest" sort has something to order on.
		if st.ExpiresAt != nil {
			row.FallbackDate = *st.ExpiresAt
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// NewProjectStorageMemberStintsScreen is every stint one member has held, most
// recent first — the web's "Member stint history" on FacilitiesProjectStoragePage,
// where it is the warden's answer to "is this a serial offender?". It is reached
// from a stint (`m` on ProjectStorageDetailScreen) rather than from the nav tree,
// because the question is always asked about the member in front of you, and
// enter opens any of the rows as a stint of its own.
//
// It rides ListScreen rather than drawing its own rows, so the window, the
// markers that say rows are cut off, the folded footer and the row bounds are
// the ones every other list keeps; projectStorageMemberRows is the part that is
// this list's to decide.
func NewProjectStorageMemberStintsScreen(deps Deps, username string) *ListScreen {
	return NewListScreen(deps, "Stints held by @"+username, listScreenSpec{
		kind: "project_storage",
		loader: func(ctx context.Context, deps Deps) ([]listRow, error) {
			return loadProjectStorageMemberStints(ctx, deps, username)
		},
		detail: func(id string, d Deps) Screen { return NewProjectStorageDetailScreen(d, id) },
	})
}

// loadProjectStorageMemberStints fetches the member's stints and shapes them.
//
// A 404 FOR A USERNAME THE ROUTE CANNOT MATCH IS SAID, not relayed. OMS's
// by-member url_path excludes `.` and `/`, so `bob.jones` gets the router's HTML
// page — "http 404: <!doctype html>…" — which reads as "no such member" when the
// truth is that OMS cannot be asked about this one from any client, the web
// included (omsapi.ListProjectStorageStintsByMember).
func loadProjectStorageMemberStints(ctx context.Context, deps Deps, username string) ([]listRow, error) {
	stints, err := deps.OMS.ListProjectStorageStintsByMember(ctx, username)
	if err != nil {
		var api *omsapi.APIError
		if errors.As(err, &api) && api.IsNotFound() && omsapi.ProjectStorageByMemberUnroutable(username) {
			return nil, fmt.Errorf("OMS's by-member route cannot match a username containing "+
				"%q, so @%s's stints cannot be listed from any client (http 404)", ".", username)
		}
		return nil, err
	}
	return projectStorageMemberRows(stints), nil
}

// projectStorageMemberRows is one row per stint.
//
// THE STINT ID LEADS THE TITLE, and that order is the decision. A title is a
// bounded identifier that abbreviates from the RIGHT, and a project title is
// free text a member typed at the kiosk, so a title led by the project loses
// the stint id — the one thing on the row a warden can act on or read off a
// tag — exactly where the project is long. The status rides the row's tag, the
// start date the list's date column, and the rest of the lifecycle is a FACT
// line whose tokens are whole dates or absent (listFitFacts).
func projectStorageMemberRows(stints []omsapi.ProjectStorageStint) []listRow {
	rows := make([]listRow, 0, len(stints))
	for i := range stints {
		st := &stints[i]
		facts := []string{}
		if st.ExpiresAt != nil {
			facts = append(facts, "expires "+st.ExpiresAt.Format("2006-01-02"))
		}
		if st.NoticeSentAt != nil {
			facts = append(facts, "notice "+st.NoticeSentAt.Format("2006-01-02"))
		}
		if st.MovedToPurgatoryAt != nil {
			facts = append(facts, "purgatory "+st.MovedToPurgatoryAt.Format("2006-01-02"))
		}
		if st.RemovedAt != nil {
			facts = append(facts, "removed "+st.RemovedAt.Format("2006-01-02"))
		}
		if loc := st.LocationDisplay; loc != "" {
			facts = append(facts, loc)
		}
		rows = append(rows, listRow{
			ID:        st.StintID,
			Title:     st.StintID + " · " + projectTitleOrPersonal(st),
			Tag:       stintStatusName(st),
			CreatedAt: st.StartedAt,
			Subtitle:  strings.Join(facts, " · "),
		})
	}
	return rows
}
