// Package observability wraps scantty's Sentry integration so the main
// entry point only needs a single defer to wire crash reporting + flush.
//
// The DSN for the self-hosted `scantty` project on
// highlighter.openmakersuite.net is baked into the binary so a freshly-
// installed scantty reports crashes out of the box — same convention
// the mobile / desktop Sentry SDKs use, where the DSN is a public
// identifier and embedding it doesn't grant write access beyond
// "send events to this project". Overrides:
//
//   - SENTRY_DSN=https://… → use a different project (staging mirror,
//     a personal sandbox, etc.).
//   - SENTRY_DISABLED=1     → no-op init, no events sent.
//   - SENTRY_ENVIRONMENT    → environment tag, defaults to "dev".
//   - SENTRY_RELEASE        → release tag, defaults to
//     scantty@<vcs.revision[:12]> via debug.ReadBuildInfo.
package observability

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/getsentry/sentry-go"
)

// defaultDSN points at the `scantty` project on the self-hosted
// highlighter.openmakersuite.net instance. Public DSNs are safe to
// commit — see the package doc. To rotate, regenerate the Client Key
// in the Sentry UI (Settings → Client Keys) and replace this string.
const defaultDSN = "https://99755afd9a18364d2c89d96bdac2d0fa@highlighter.openmakersuite.net/4"

// Init configures the Sentry SDK using the baked-in DSN by default;
// SENTRY_DSN overrides, SENTRY_DISABLED=1 disables. The returned
// function flushes pending events with a short deadline; it is always
// safe to call (no-op when Sentry was not initialized).
//
// Typical usage from main():
//
//	defer observability.Init()()
//
// The double-call shape lets a single line wire init + deferred flush.
func Init() func() {
	if disabled := os.Getenv("SENTRY_DISABLED"); disabled == "1" || disabled == "true" {
		return func() {}
	}

	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		dsn = defaultDSN
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
