// Command oms-claim-print is the Raspberry Pi side of the project-storage
// claim-tag print pipeline. It polls the OMS backend's print queue, fetches
// each pending stint's rendered PNG, encodes it as ESC/POS and writes it
// straight to the Epson TM receipt printer's USB device, then posts
// mark-printed so the queue drains.
//
// Configuration (env vars). The OMS_API_* vars match the legacy Python
// daemon; the printer now speaks ESC/POS directly to the usblp device
// (no cupsd, no per-host queue), so the OMS_ESCPOS_* vars replace the old
// OMS_EPSON_CUPS_QUEUE:
//
//	OMS_API_BASE              required, e.g. https://oms.example.com (no /api suffix)
//	OMS_API_TOKEN             optional bearer; default empty (endpoints AllowAny)
//	OMS_POLL_INTERVAL_S       optional, default 10
//	OMS_ESCPOS_DEVICE         usblp character device; default /dev/usb/lp0
//	OMS_ESCPOS_WIDTH_DOTS     printhead width in dots; default 576 (80mm; 512 = 58mm)
//	OMS_ESCPOS_CUT            partial-cut after each label; default true
//	OMS_EPSON_CUPS_QUEUE      deprecated no-op (CUPS backend removed); ignored with a warning
//
// Optional Common-API proxy (opt-in: leave LISTEN empty to disable).
// The proxy lets OMS resolve badges → identity via the Pi, since the
// Common API lives on the LAN behind the firewall:
//
//	COMMON_API_PROXY_LISTEN   e.g. ":8083"; empty = proxy disabled
//	COMMON_API_URL            upstream Common API endpoint, e.g.
//	                          http://192.168.200.32:8080/api/v1/lookupByRfid
//	COMMON_API_PROXY_TOKEN    optional bearer; if set, OMS must send it
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
	defaultPollSeconds     = 10
	maxHTTPBackoff         = 60 * time.Second
	defaultESCPOSDevice    = "/dev/usb/lp0"
	defaultESCPOSWidthDots = 576 // 80mm TM-T20III; 512 for 58mm paper
)

type config struct {
	apiBase   string
	apiToken  string
	pollIvl   time.Duration
	device    string
	widthDots int
	cut       bool
}

func loadConfig() (config, error) {
	cfg := config{
		apiBase:   os.Getenv("OMS_API_BASE"),
		apiToken:  os.Getenv("OMS_API_TOKEN"),
		pollIvl:   defaultPollSeconds * time.Second,
		device:    defaultESCPOSDevice,
		widthDots: defaultESCPOSWidthDots,
		cut:       true,
	}
	if cfg.apiBase == "" {
		return cfg, errEnvMissing("OMS_API_BASE")
	}
	if raw := os.Getenv("OMS_POLL_INTERVAL_S"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			cfg.pollIvl = time.Duration(v * float64(time.Second))
		}
	}
	if raw := os.Getenv("OMS_ESCPOS_DEVICE"); raw != "" {
		cfg.device = raw
	}
	if raw := os.Getenv("OMS_ESCPOS_WIDTH_DOTS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			cfg.widthDots = v
		}
	}
	if raw := os.Getenv("OMS_ESCPOS_CUT"); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			cfg.cut = v
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
	prn := printer.ESCPOS{Device: cfg.device, WidthDots: cfg.widthDots, Cut: cfg.cut}

	log.Printf("oms-claim-print: polling %s every %s", cfg.apiBase, cfg.pollIvl)
	log.Printf("oms-claim-print: ESC/POS -> %s (%d dots wide, cut=%t)", cfg.device, cfg.widthDots, cfg.cut)
	if os.Getenv("OMS_EPSON_CUPS_QUEUE") != "" {
		log.Printf("oms-claim-print: OMS_EPSON_CUPS_QUEUE is deprecated and ignored — the CUPS backend was removed in favour of ESC/POS-direct (set OMS_ESCPOS_DEVICE)")
	}

	// Optional Common-API proxy. Disabled when COMMON_API_PROXY_LISTEN
	// is empty — preserves the daemon's prior (pure poll) behavior so
	// existing deployments don't open a port without opting in.
	proxyCfg := proxyConfigFromEnv(os.Getenv)
	shutdownProxy, err := startProxy(ctx, proxyCfg)
	if err != nil {
		log.Fatalf("common-api proxy: %v", err)
	}
	if proxyCfg.listen != "" {
		log.Printf("oms-claim-print: common-api proxy %s", describeProxyConfig(proxyCfg))
	}

	if err := run(ctx, client, prn, cfg.pollIvl); err != nil {
		log.Fatalf("loop: %v", err)
	}

	// Give the proxy a short grace period to drain in-flight requests
	// before systemd hard-stops us. 5s is well under the default
	// TimeoutStopSec=90s but long enough to finish a typical lookup.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := shutdownProxy(shutdownCtx); err != nil {
		log.Printf("common-api proxy shutdown: %v", err)
	}
	log.Printf("oms-claim-print: shutdown clean")
}

// run is the main loop. It returns only when ctx is cancelled (clean
// shutdown via SIGTERM). Errors inside the loop are logged but never
// abort the daemon — systemd restarts only on crash, not on stuck printer.
func run(ctx context.Context, client *omsapi.Client, prn printer.ESCPOS, pollIvl time.Duration) error {
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

func processOne(ctx context.Context, client *omsapi.Client, prn printer.ESCPOS, e omsapi.ProjectStoragePrintQueueEntry) error {
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
