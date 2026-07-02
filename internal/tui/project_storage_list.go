package tui

import (
	"context"
	"net/url"
)

// NewProjectStorageListScreen is the browsable OVERVIEW of project/personal
// storage stints, opened from the Facilities menu. It reuses the generic
// ListScreen; enter opens a per-stint DETAIL screen that includes a text
// label preview. Mirrors the OMS web "items overview" surface.
func NewProjectStorageListScreen(deps Deps) *ListScreen {
	return NewListScreen(deps, "Project Storage", listScreenSpec{
		kind:   "project_storage",
		loader: loadProjectStorageStints,
		detail: func(id string, d Deps) Screen { return NewProjectStorageDetailScreen(d, id) },
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
