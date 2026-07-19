// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
)

// bootConfirmAfter is how long a boot has to stay up before it can count as good.
var bootConfirmAfter = time.Minute

// bootConfirmPoll is how often a boot that is not good yet is looked at again.
var bootConfirmPoll = 10 * time.Second

// bootCheck is one thing a boot must show before it is confirmed. It returns "" when the
// boot shows it, or what it still waits for. None of them is the network: a speaker on a
// broken LAN, or without a router at all, still confirms a good image (ADR 0010).
type bootCheck func(ctx context.Context) string

// bootGate carries what the boot confirmation waits for besides the API, and tells the
// rest of soundtapd when it has made its attempt.
type bootGate struct {
	// bco is closed once this boot's BCO install step is over (wireBCOFlash): the OS's
	// image installed, not needed, or given up on while our own app still runs. nil on
	// every model but the ST20.
	bco     chan struct{}
	bcoOnce sync.Once
	// attempted is closed once confirmBoot has made its one attempt.
	attempted     chan struct{}
	attemptedOnce sync.Once
	// netDir is where the kernel lists network devices; a test points it elsewhere.
	netDir string
}

func newBootGate() *bootGate {
	return &bootGate{attempted: make(chan struct{}), netDir: "/sys/class/net"}
}

// waitForBCO makes the confirmation wait for the BCO install step and its link (the ST20).
func (g *bootGate) waitForBCO() { g.bco = make(chan struct{}) }

// bcoDone marks this boot's BCO install step as over.
func (g *bootGate) bcoDone() {
	if g.bco != nil {
		g.bcoOnce.Do(func() { close(g.bco) })
	}
}

// awaitAttempt waits up to d for confirmBoot's attempt, and says whether it came.
func (g *bootGate) awaitAttempt(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-g.attempted:
		return true
	case <-ctx.Done():
	case <-t.C:
	}
	return false
}

// checks are the conditions confirmBoot waits for, the API's answer on probe first.
func (g *bootGate) checks(probe string) []bootCheck {
	checks := []bootCheck{func(ctx context.Context) string {
		if err := answers(ctx, probe); err != nil {
			return "the API to answer on loopback: " + err.Error()
		}
		return ""
	}}
	if g.bco == nil {
		return checks
	}
	// The ST20 has no network but through the BCO, and only our own BCO app gives it one
	// (ADR 0031). A slot whose BCO image does not come up has to roll back, so that the
	// other slot puts its own image back; the link is bcolink0, which exists without any
	// network behind it.
	return append(checks,
		func(context.Context) string {
			select {
			case <-g.bco:
				return ""
			default:
				return "the BCO image install"
			}
		},
		func(context.Context) string {
			if _, err := os.Stat(filepath.Join(g.netDir, "bcolink0")); err != nil {
				return "the BCO link (bcolink0)"
			}
			return ""
		})
}

// confirmBoot tells the bootloader this boot worked (ADR 0010): bootConfirmAfter up, and
// every check met. Until then the boot stays unconfirmed, and U-Boot counts it towards a
// rollback to the other slot. A boot that does not show it yet is looked at again every
// bootConfirmPoll for as long as soundtapd runs, so a slow start, a busy moment or a BCO
// flash only delays the confirmation. The confirmation itself, which writes the
// environment, is tried once.
func confirmBoot(ctx context.Context, srv interface{ ConfirmBoot() error }, g *bootGate, checks []bootCheck, log *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(bootConfirmAfter):
	}
	waiting := ""
	for {
		why := pendingCheck(ctx, checks)
		if why == "" {
			break
		}
		counters.BootConfirmRetries.Add(1) // /api/health (ADR 0127)
		if why != waiting {
			// Once per change, not every poll: a speaker whose BCO never comes up would
			// otherwise fill the log for its whole uptime.
			log.Warn("boot not confirmed yet", "waiting for", why)
			waiting = why
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(bootConfirmPoll):
		}
	}
	defer g.attemptedOnce.Do(func() { close(g.attempted) })
	if err := srv.ConfirmBoot(); err != nil {
		log.Warn("boot confirm", "err", err)
		return
	}
	log.Info("boot confirmed")
}

// pendingCheck returns what the first unmet check waits for, or "" when all are met.
func pendingCheck(ctx context.Context, checks []bootCheck) string {
	for _, check := range checks {
		if why := check(ctx); why != "" {
			return why
		}
	}
	return ""
}

func answers(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Probes our own loopback listener, which SafeTransport refuses.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return nil
}
