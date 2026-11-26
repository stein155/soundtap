// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The input chain as `nft list chain inet filter input` prints it from firewall.nft.
const loadedChain = `table inet filter {
	chain input {
		type filter hook input priority filter; policy drop;
		ct state established,related accept
	}
}
`

// fakeNft records each run and answers with out and err.
// What a fake nft run returns when it fails.
var (
	errExitStatus1 = errors.New("exit status 1")
	errMustNotRun  = errors.New("must not run")
)

type fakeNft struct {
	calls [][]string
	out   string
	err   error
	ctx   func(context.Context) // inspects the context of each run
}

func (f *fakeNft) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.ctx != nil {
		f.ctx(ctx)
	}

	return []byte(f.out), f.err
}

// newCheck is a check over a profile file holding profile ("" writes none) with a clock
// the test moves, logging into the returned buffer.
func newCheck(t *testing.T, profile string, nft *fakeNft) (*FirewallCheck, *time.Time, *bytes.Buffer) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "profile")
	if profile != "" {
		err := os.WriteFile(path, []byte(profile+"\n"), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	var logs bytes.Buffer

	fc := NewFirewallCheck(path, nft.run, slog.New(slog.NewTextHandler(&logs, nil)))
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	fc.now = func() time.Time { return now }

	return fc, &now, &logs
}

func TestFirewallNotExpectedWithoutProductionProfile(t *testing.T) {
	t.Parallel()

	for name, profile := range map[string]string{"alpha image": "alpha", "no profile file": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nft := &fakeNft{err: errMustNotRun, calls: nil, out: "", ctx: nil}

			fc, _, _ := newCheck(t, profile, nft)
			if state, _ := fc.Status(context.Background()); state != FirewallNA {
				t.Errorf("state = %q, want %q", state, FirewallNA)
			}

			if len(nft.calls) != 0 {
				t.Errorf("nft ran %v; an image without the filter must not be asked", nft.calls)
			}
		})
	}
}

func TestFirewallLoadedOnProduction(t *testing.T) {
	t.Parallel()

	nft := &fakeNft{out: loadedChain, calls: nil, err: nil, ctx: nil}
	fc, _, logs := newCheck(t, "production", nft)

	state, reason := fc.Status(context.Background())
	if state != FirewallLoaded || reason != "" {
		t.Errorf("got %q (%q), want %q", state, reason, FirewallLoaded)
	}

	want := []string{"/usr/sbin/nft", "list", "chain", "inet", "filter", "input"}
	if len(nft.calls) != 1 || !slices.Equal(nft.calls[0], want) {
		t.Errorf("nft calls = %v, want one %v", nft.calls, want)
	}

	if logs.Len() != 0 {
		t.Errorf("a loaded filter logged %q", logs)
	}
}

// An image whose profile says something else than alpha is held to production.
func TestFirewallUnknownProfileIsProduction(t *testing.T) {
	t.Parallel()

	nft := &fakeNft{err: errExitStatus1, calls: nil, out: "", ctx: nil}

	fc, _, _ := newCheck(t, "garbled", nft)
	if state, _ := fc.Status(context.Background()); state != FirewallMissing {
		t.Errorf("state = %q, want %q", state, FirewallMissing)
	}
}

func TestFirewallMissing(t *testing.T) {
	t.Parallel()

	for name, nft := range map[string]*fakeNft{
		"nft fails": {err: errExitStatus1, calls: nil, out: "", ctx: nil},
		"no default drop": {
			out:   strings.Replace(loadedChain, "policy drop;", "policy accept;", 1),
			calls: nil, err: nil, ctx: nil,
		},
		"no chain in output":  {out: "", calls: nil, err: nil, ctx: nil},
		"nft is not on image": {err: os.ErrNotExist, calls: nil, out: "", ctx: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fc, _, logs := newCheck(t, "production", nft)

			state, reason := fc.Status(context.Background())
			if state != FirewallMissing || reason == "" {
				t.Errorf("got %q (%q), want %q with a reason", state, reason, FirewallMissing)
			}

			if !strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("a missing filter logged no error: %q", logs)
			}
		})
	}
}

// /api/health is open to the LAN: an answer is reused for a minute, the error is logged
// once per time the filter goes missing, and its return is logged too.
func TestFirewallAnswerIsReusedAndLoggedOnChange(t *testing.T) {
	t.Parallel()

	nft := &fakeNft{err: errExitStatus1, calls: nil, out: "", ctx: nil}

	check, now, logs := newCheck(t, "production", nft)
	for range 5 {
		check.Status(context.Background())
	}

	if len(nft.calls) != 1 {
		t.Fatalf("nft ran %d times within a minute, want 1", len(nft.calls))
	}

	*now = now.Add(firewallRecheck)

	if state, _ := check.Status(context.Background()); state != FirewallMissing || len(nft.calls) != 2 {
		t.Fatalf("after a minute: state %q, %d runs; want missing after a second run", state, len(nft.calls))
	}

	if n := strings.Count(logs.String(), "level=ERROR"); n != 1 {
		t.Errorf("%d error lines for one outage, want 1: %q", n, logs)
	}

	nft.err, nft.out = nil, loadedChain
	*now = now.Add(firewallRecheck)

	if state, _ := check.Status(context.Background()); state != FirewallLoaded {
		t.Fatalf("state = %q after the filter came back, want loaded", state)
	}

	if !strings.Contains(logs.String(), "loaded again") {
		t.Errorf("the filter's return was not logged: %q", logs)
	}
}

// Each run is bounded in time, and a request that hangs up does not cut it short into a
// "missing" that would be kept for a minute.
func TestFirewallRunIsBoundedAndOutlivesTheRequest(t *testing.T) {
	t.Parallel()

	nft := &fakeNft{out: loadedChain, calls: nil, err: nil, ctx: nil}
	nft.ctx = func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > firewallTimeout {
			t.Errorf("nft runs without a deadline of at most %s", firewallTimeout)
		}

		if ctx.Err() != nil {
			t.Errorf("nft ran under a cancelled context: %v", ctx.Err())
		}
	}
	fc, _, _ := newCheck(t, "production", nft)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if state, _ := fc.Status(ctx); state != FirewallLoaded {
		t.Errorf("state = %q, want loaded", state)
	}
}

// The real runner gives up on a command that hangs.
func TestRunCommandTimesOut(t *testing.T) {
	t.Parallel()

	_, err := os.Stat("/bin/sleep")
	if err != nil {
		t.Skip("no /bin/sleep")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err = runCommand(ctx, "/bin/sleep", "5")
	if err == nil {
		t.Error("a killed command reported success")
	}

	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("runCommand returned after %s, want soon after its timeout", d)
	}
}
