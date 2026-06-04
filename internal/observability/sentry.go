// Package observability wraps scantty's Sentry integration so the main
// entry point only needs a single defer to wire crash reporting + flush.
//
// Sentry reads the DSN, environment, and release from the standard
// SENTRY_DSN / SENTRY_ENVIRONMENT / SENTRY_RELEASE env vars by default,
// so we don't add scantty-prefixed wrappers — operators can pass the
// project DSN straight from the Sentry UI. The self-hosted project
// slug is "scantty" on highlighter.openmakersuite.net; the DSN is
// printed in the project settings.
//
// When SENTRY_DSN is unset, Init becomes a no-op so local dev runs
// don't accidentally ship breadcrumbs to prod. The returned flush
// function is safe to defer regardless.
package observability

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/getsentry/sentry-go"
)

// Init configures the Sentry SDK from environment variables. The
// returned function flushes pending events with a short deadline; it
// is always safe to call (no-op when Sentry was not initialized).
//
// Typical usage from main():
//
//	defer observability.Init()()
//
// The double-call shape lets a single line wire init + deferred flush.
func Init() func() {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return func() {}
	}

	environment := os.Getenv("SENTRY_ENVIRONMENT")
	if environment == "" {
		environment = "dev"
	}
	release := os.Getenv("SENTRY_RELEASE")
	if release == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && s.Value != "" {
					if len(s.Value) > 12 {
						release = "scantty@" + s.Value[:12]
					} else {
						release = "scantty@" + s.Value
					}
					break
				}
			}
		}
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:         dsn,
		Environment: environment,
		Release:     release,
		// TUIs are interactive; tracing every render would just inflate
		// the bill. Errors only.
		EnableTracing:    false,
		SendDefaultPII:   false,
		AttachStacktrace: true,
	})
	if err != nil {
		// Sentry init failure is non-fatal — log it once and keep
		// running so a misconfigured DSN doesn't ground the TUI.
		fmt.Fprintf(os.Stderr, "scantty: sentry init failed: %v\n", err)
		return func() {}
	}

	return func() {
		sentry.Flush(2 * time.Second)
	}
}

// CaptureError sends err to Sentry and returns it unchanged so callers
// can chain: `return observability.CaptureError(err)`. Nil-safe.
func CaptureError(err error) error {
	if err == nil {
		return nil
	}
	sentry.CaptureException(err)
	return err
}

// Recover re-raises a panic after capturing it. Wrap with `defer`
// inside any goroutine you want covered — sentry-go's own panic
// integration handles the main goroutine, but the bubbletea program
// runs in goroutines we want to instrument too.
func Recover() {
	if r := recover(); r != nil {
		var err error
		switch v := r.(type) {
		case error:
			err = v
		default:
			err = errors.New(fmt.Sprint(r))
		}
		sentry.CurrentHub().RecoverWithContext(nil, err)
		sentry.Flush(2 * time.Second)
		panic(r)
	}
}
