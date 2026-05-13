package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/cache"
	"github.com/uid0/scantty/internal/config"
	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
	"github.com/uid0/scantty/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "scantty: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	c, err := cache.Open(cfg.Cache.Path, cfg.Cache.TTL)
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	defer c.Close()

	oms := omsapi.New(cfg.OMS.BaseURL)
	if cfg.OMS.AuthToken != "" {
		oms.SetTokens(cfg.OMS.AuthToken, "")
	}

	fk, err := forgekeyapi.New(forgekeyapi.Options{
		BaseURL:    cfg.ForgeKey.BaseURL,
		AuthToken:  cfg.ForgeKey.AuthToken,
		ClientCert: cfg.ForgeKey.ClientCert,
		ClientKey:  cfg.ForgeKey.ClientKey,
		CACert:     cfg.ForgeKey.CACert,
		Timeout:    cfg.ForgeKey.Timeout,
	})
	if err != nil {
		return fmt.Errorf("forgekey: %w", err)
	}

	deps := tui.Deps{
		OMS:      oms,
		ForgeKey: fk,
		Cache:    c,
		Ctx:      ctx,
	}

	p := tea.NewProgram(tui.NewRoot(deps), tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}
