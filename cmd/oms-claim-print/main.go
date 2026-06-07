// Command oms-claim-print is the Raspberry Pi side of the project-storage
// claim-tag print pipeline. It polls the OMS backend's print queue, fetches
// each pending stint's rendered PNG, hands it to CUPS for the Epson TM
// receipt printer, and posts mark-printed so the queue drains.
//
// Configuration (env vars — same names as the legacy Python daemon at
// backend/project_storage/scripts/pi/print_daemon.py, so a systemd
// ExecStart swap is the only change to migrate a Pi):
//
//	OMS_API_BASE          required, e.g. https://oms.example.com (no /api suffix)
//	OMS_API_TOKEN         optional bearer; default empty (endpoints AllowAny)
//	OMS_POLL_INTERVAL_S   optional, default 10
//	OMS_EPSON_CUPS_QUEUE  optional; empty = system default CUPS queue
//
// Design notes:
//   - Single goroutine. The whole queue/print/ack cycle is sequential per
//     stint so a wedged printer can't interleave half a job behind another.
//   - HTTP errors backoff exponentially up to 60s before re-polling;
//     printer errors do NOT backoff because they usually want a human
//     (paper out, cover open) and re-trying every 10s makes the log
//     useful for that.
//   - Stateless. No on-disk queue. If the daemon dies mid-print, the OMS
//     side never saw mark-printed and the stint surfaces next poll. The
//     warden can reprint a crushed label via the OMS admin (printed_at toggle).
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
	"github.com/uid0/scantty/internal/printer"
)

const (
	defaultPollSeconds = 10
	maxHTTPBackoff     = 60 * time.Second
)

type config struct {
	apiBase  string
	apiToken string
	pollIvl  time.Duration
	queue    string
}

func loadConfig() (config, error) {
	cfg := config{
		apiBase:  os.Getenv("OMS_API_BASE"),
		apiToken: os.Getenv("OMS_API_TOKEN"),
		queue:    os.Getenv("OMS_EPSON_CUPS_QUEUE"),
		pollIvl:  defaultPollSeconds * time.Second,
	}
	if cfg.apiBase == "" {
		return cfg, errEnvMissing("OMS_API_BASE")
	}
	if raw := os.Getenv("OMS_POLL_INTERVAL_S"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			cfg.pollIvl = time.Duration(v * float64(time.Second))
		}
	}
	return cfg, nil
}

type errEnvMissing string

func (e errEnvMissing) Error() string { return "missing required env var: " + string(e) }

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var opts []omsapi.ClientOption
	if cfg.apiToken != "" {
		opts = append(opts, omsapi.WithToken(cfg.apiToken, ""))
	}
	client := omsapi.New(cfg.apiBase, opts...)
	prn := printer.CUPS{Queue: cfg.queue}

	log.Printf("oms-claim-print: polling %s every %s", cfg.apiBase, cfg.pollIvl)
	if cfg.queue != "" {
		log.Printf("oms-claim-print: lp queue %q", cfg.queue)
	} else {
		log.Printf("oms-claim-print: using system default CUPS queue")
	}

	if err := run(ctx, client, prn, cfg.pollIvl); err != nil {
		log.Fatalf("loop: %v", err)
	}
	log.Printf("oms-claim-print: shutdown clean")
}

// run is the main loop. It returns only when ctx is cancelled (clean
// shutdown via SIGTERM). Errors inside the loop are logged but never
// abort the daemon — systemd restarts only on crash, not on stuck printer.
func run(ctx context.Context, client *omsapi.Client, prn printer.CUPS, pollIvl time.Duration) error {
	backoff := 1 * time.Second
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		entries, err := client.ListProjectStoragePrintQueue(ctx)
		if err != nil {
			log.Printf("queue poll failed (%v); backing off %s", err, backoff)
			if sleepOrDone(ctx, backoff) {
				return nil
			}
			backoff *= 2
			if backoff > maxHTTPBackoff {
				backoff = maxHTTPBackoff
			}
			continue
		}
		backoff = 1 * time.Second

		for _, e := range entries {
			if ctx.Err() != nil {
				return nil
			}
			if err := processOne(ctx, client, prn, e); err != nil {
				log.Printf("stint %s: %v — will retry next poll", e.StintID, err)
			}
		}

		if sleepOrDone(ctx, pollIvl) {
			return nil
		}
	}
}

func processOne(ctx context.Context, client *omsapi.Client, prn printer.CUPS, e omsapi.ProjectStoragePrintQueueEntry) error {
	log.Printf("stint %s: fetching label", e.StintID)
	png, err := client.GetProjectStorageLabelBytes(ctx, e.LabelURL)
	if err != nil {
		return err
	}

	log.Printf("stint %s: printing (%d bytes)", e.StintID, len(png))
	if err := prn.Print(png); err != nil {
		return err
	}

	log.Printf("stint %s: confirming print to OMS", e.StintID)
	return client.MarkProjectStorageStintPrinted(ctx, e.StintID, "printed via oms-claim-print (epson_tm)")
}

// sleepOrDone returns true if ctx fired during the sleep, signalling the
// caller to exit cleanly.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}
