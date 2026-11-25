// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// firewallWaitDelay is how long a firewall command's output may outlive it.
const firewallWaitDelay = 500 * time.Millisecond

// What /api/health says about the production input filter (ADR 0066, ADR 0113).
const (
	FirewallLoaded  = "loaded"  // the filter's input chain is there with policy drop
	FirewallMissing = "missing" // a production image without it: every port is open
	FirewallNA      = "n/a"     // an alpha image, or not a SoundTap image: no filter is meant to be loaded
)

// ProfileFile names the image's build profile: post-build.sh writes "alpha" or
// "production" there, and rootfs_check.py --profile holds the image to it.
const ProfileFile = "/etc/soundtap/profile"

const (
	// firewallTimeout bounds one nft run; listing a chain takes milliseconds.
	firewallTimeout = 2 * time.Second
	// firewallRecheck is how long an answer is reused: /api/health is open to the LAN,
	// and a client asking in a loop must not start an nft process per request.
	firewallRecheck = time.Minute
)

// CommandRunner runs name with args and returns what it wrote to standard output.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// runCommand is the real CommandRunner. WaitDelay keeps a killed command from holding
// the call open on its pipes after the timeout.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	childproc.NetTool(cmd) // nft needs CAP_NET_ADMIN even to list
	cmd.WaitDelay = firewallWaitDelay

	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s: %w", name, err)
	}

	return out, nil
}

// FirewallCheck says whether a production image's inbound filter is loaded. The filter
// is the first sysinit line of a production inittab (os/board/rootfs/post-build.sh), and a
// failing `nft -f` lets the boot go on unfiltered; this check makes that visible instead
// of silent. It only reads: it lists the input chain and never loads or changes rules.
type FirewallCheck struct {
	profileFile string
	run         CommandRunner
	log         *slog.Logger
	now         func() time.Time

	mu      sync.Mutex // one nft run at a time; waiters take its answer
	checked time.Time
	state   string
	reason  string
}

// NewFirewallCheck checks the profile named by profileFile; run nil is the real command.
func NewFirewallCheck(profileFile string, run CommandRunner, log *slog.Logger) *FirewallCheck {
	if run == nil {
		run = runCommand
	}

	if log == nil {
		log = slog.Default()
	}

	return &FirewallCheck{
		profileFile: profileFile, run: run, log: log, now: time.Now,
		mu: sync.Mutex{}, checked: time.Time{}, state: "", reason: "",
	}
}

// Status returns FirewallLoaded, FirewallMissing or FirewallNA, and for a missing filter
// why. It logs an error each time the filter is found missing after it was not.
// It returns the state, then the reason.
func (f *FirewallCheck) Status(ctx context.Context) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.state != "" && f.now().Sub(f.checked) < firewallRecheck {
		return f.state, f.reason
	}

	state, reason := f.check(ctx)
	switch {
	case state == FirewallMissing && f.state != FirewallMissing:
		f.log.Error("production inbound filter is not loaded: every listening port is open to the network",
			"reason", reason)
	case state == FirewallLoaded && f.state == FirewallMissing:
		f.log.Info("production inbound filter is loaded again")
	default:
		// Anything else needs nothing here.
	}

	f.state, f.reason, f.checked = state, reason, f.now()

	return state, reason
}

func (f *FirewallCheck) check(ctx context.Context) (string, string) {
	profile, err := os.ReadFile(f.profileFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Not a SoundTap image (a development host): there is no filter to expect.
		return FirewallNA, ""
	case err != nil:
		// An image that cannot say it is alpha is held to production, as the update
		// signature check does.
	case strings.TrimSpace(string(profile)) == "alpha":
		return FirewallNA, ""
	default:
		// Anything else needs nothing here.
	}
	// A client that hangs up must not turn into a "missing" kept for a minute.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), firewallTimeout)
	defer cancel()
	// The chain that firewall.nft defines; its default verdict is what makes it a filter.
	out, err := f.run(ctx, "/usr/sbin/nft", "list", "chain", "ip", "filter", "input")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(bytes.TrimSpace(exit.Stderr)) > 0 {
			return FirewallMissing, "nft: " + firstLine(exit.Stderr)
		}

		return FirewallMissing, fmt.Sprintf("nft: %v", err)
	}

	if !bytes.Contains(out, []byte("policy drop;")) {
		return FirewallMissing, "the input chain does not drop by default"
	}

	return FirewallLoaded, ""
}

func firstLine(b []byte) string {
	line, _, _ := bytes.Cut(bytes.TrimSpace(b), []byte("\n"))

	return string(line)
}

// firewallStatus is the check's answer, or n/a when none is wired.
func (s *Server) firewallStatus(ctx context.Context) (string, string) {
	if s.firewall == nil {
		return FirewallNA, ""
	}

	return s.firewall.Status(ctx)
}
