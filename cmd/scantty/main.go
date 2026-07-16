package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/cache"
	"github.com/uid0/scantty/internal/config"
	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/observability"
	"github.com/uid0/scantty/internal/omsapi"
	"github.com/uid0/scantty/internal/tui"
)

func main() {
	// Wire Sentry early so config/cache failures + panics during init
	// are still reported. No-op when SENTRY_DSN isn't set, so dev runs
	// stay quiet.
	defer observability.Init()()
	defer observability.Recover()

	if err := run(); err != nil {
		observability.CaptureError(err)
		fmt.Fprintf(os.Stderr, "scantty: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	tui.ApplyTheme(cfg.Theme)

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

	initialStaff := false
	if cfg.OMS.AuthToken == "" {
		if sess, err := c.LoadSession(cfg.OMS.BaseURL); err == nil && sess != nil {
			oms.SetTokens(sess.AccessToken, sess.RefreshToken)
			initialStaff = sess.IsStaff || sess.IsSuperuser
			if claims, err := omsapi.DecodeJWTClaims(sess.AccessToken); err == nil &&
				claims.IsExpired(30*time.Second) && sess.RefreshToken != "" {
				if rerr := oms.Refresh(ctx); rerr != nil {
					fmt.Fprintf(os.Stderr, "scantty: cached token refresh failed: %v\n", rerr)
					oms.SetTokens("", "")
					_ = c.ClearSession(cfg.OMS.BaseURL)
					initialStaff = false
				}
			}
		}
	} else {
		if claims, err := omsapi.DecodeJWTClaims(cfg.OMS.AuthToken); err == nil {
			initialStaff = claims.IsStaff || claims.IsSuperuser
		}
	}

	// Borrow the OMS JWT at request time when no explicit FK token is set,
	// so signing in via the login screen unlocks FK endpoints too (both
	// APIs share an auth realm on the same Django backend).
	var fkTokenFn func() string
	if cfg.ForgeKey.AuthToken == "" {
		fkTokenFn = oms.AccessToken
	}
	fk, err := forgekeyapi.New(forgekeyapi.Options{
		BaseURL:       cfg.ForgeKey.BaseURL,
		AuthToken:     cfg.ForgeKey.AuthToken,
		AuthTokenFunc: fkTokenFn,
		ClientCert:    cfg.ForgeKey.ClientCert,
		ClientKey:     cfg.ForgeKey.ClientKey,
		CACert:        cfg.ForgeKey.CACert,
		Timeout:       cfg.ForgeKey.Timeout,
	})
	if err != nil {
		return fmt.Errorf("forgekey: %w", err)
	}

	deps := tui.Deps{
		OMS:          oms,
		ForgeKey:     fk,
		Cache:        c,
		Ctx:          ctx,
		InitialStaff: initialStaff,
		SaveThemePreference: func(name string) error {
			return config.SavePrefs(config.Prefs{Theme: name})
		},
	}

	p := tea.NewProgram(tui.NewRoot(deps), tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}
