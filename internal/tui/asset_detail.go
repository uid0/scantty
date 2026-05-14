package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type AssetDetailScreen struct {
	deps    Deps
	assetID int
	asset   *omsapi.Asset
	loading bool
	loadErr string
}

type assetDetailLoadedMsg struct {
	asset *omsapi.Asset
	err   error
}

func NewAssetDetailScreen(deps Deps, id string) *AssetDetailScreen {
	assetID, _ := strconv.Atoi(id)
	return &AssetDetailScreen{deps: deps, assetID: assetID, loading: true}
}

func (s *AssetDetailScreen) Title() string {
	if s.asset != nil {
		return fmt.Sprintf("Asset: %s", s.asset.Name)
	}
	return "Asset"
}

func (s *AssetDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.assetID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		a, err := deps.OMS.GetAsset(ctx, id)
		return assetDetailLoadedMsg{asset: a, err: err}
	}
}

func (s *AssetDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case assetDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.asset = m.asset
		return s, nil
	case tea.KeyMsg:
		if m.String() == "r" {
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		}
	}
	return s, nil
}

func (s *AssetDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading asset…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	if s.asset == nil {
		return StyleMuted.Render("Asset not found.")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(s.asset.Name) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %d", s.asset.ID)) + "\n\n")
	if s.asset.Description != "" {
		b.WriteString(s.asset.Description + "\n\n")
	}
	if s.asset.Status != "" {
		b.WriteString(StyleMuted.Render("Status: ") + s.asset.Status + "\n")
	}
	if s.asset.Location != nil {
		b.WriteString(StyleMuted.Render("Location: ") + fmt.Sprintf("#%d", *s.asset.Location) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("(full parity — maintenance items, work orders, problems, power chain — coming next slice)") + "\n\n")
	b.WriteString(StyleMuted.Render("r refresh · esc back"))
	return b.String()
}