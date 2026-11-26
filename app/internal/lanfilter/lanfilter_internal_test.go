// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package lanfilter

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// errNoSet is nft on an image without the production ruleset.
var errNoSet = errors.New("exit status 1: No such file or directory")

func ipNet(t *testing.T, cidr string) net.Addr {
	t.Helper()

	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}

	network.IP = ip

	return network
}

func TestElementsArePerInterfaceOnLinkPrefixes(t *testing.T) {
	t.Parallel()

	got := Elements(map[string][]net.Addr{
		"wlan0": {
			ipNet(t, "192.0.2.10/24"),                 // IPv4: lan-allow.sh's
			ipNet(t, "fe80::200:ff:fe00:1/64"),        // link-local
			ipNet(t, "2001:db8:1:2:200:ff:fe00:1/64"), // SLAAC
			ipNet(t, "2001:db8:1:2:a1b2::3/64"),       // its temporary twin: one element
			ipNet(t, "fd00:1:2:3::7/64"),              // ULA
			ipNet(t, "2001:db8:9::5/128"),             // DHCPv6: no prefix to answer
		},
		"eth0":  {ipNet(t, "fe80::200:ff:fe00:2/64"), ipNet(t, "2001:db8:1::1/48"), ipNet(t, "2001:db8:1:7::1/64")},
		"wlan1": {ipNet(t, "192.168.42.1/24")},
	})
	want := []string{
		`"eth0" . 2001:db8:1::/48`,
		`"eth0" . fe80::/10`,
		`"wlan0" . 2001:db8:1:2::/64`,
		`"wlan0" . fd00:1:2:3::/64`,
		`"wlan0" . fe80::/10`,
	}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Elements = %q, want %q", got, want)
	}
}

func TestScriptReplacesTheSetInOneTransaction(t *testing.T) {
	t.Parallel()

	if got := Script(nil); got != "flush set inet filter lan_nets6\n" {
		t.Errorf("Script(nil) = %q", got)
	}

	want := "flush set inet filter lan_nets6\n" +
		`add element inet filter lan_nets6 { "wlan0" . fe80::/10, "wlan0" . 2001:db8::/64 }` + "\n"
	if got := Script([]string{`"wlan0" . fe80::/10`, `"wlan0" . 2001:db8::/64`}); got != want {
		t.Errorf("Script = %q, want %q", got, want)
	}
}

// A change runs nft once, an unchanged set none, and a failure is retried and logged once.
func TestOnceRunsNftOnlyForAChange(t *testing.T) {
	t.Parallel()

	addrs := map[string][]net.Addr{"wlan0": {ipNet(t, "fe80::1/64")}}

	var (
		scripts []string
		fail    error
		logs    bytes.Buffer
		clock   = time.Unix(1_800_000_000, 0)
	)

	sync6 := &Sync{
		addrs: func(name string) ([]net.Addr, error) {
			if a, ok := addrs[name]; ok {
				return a, nil
			}

			return nil, errors.New("no such interface") //nolint:err113 // a test's one-off error
		},
		run: func(_ context.Context, script string) error {
			scripts = append(scripts, script)

			return fail
		},
		log: slog.New(slog.NewTextHandler(&logs, nil)),
		now: func() time.Time { return clock },
		mu:  sync.Mutex{}, applied: "", failed: "", refused: "", refusedAt: time.Time{},
	}

	fail = errNoSet

	sync6.Once(context.Background())
	sync6.Once(context.Background())

	if len(scripts) != 1 {
		t.Fatalf("a refused script ran again at once: %d runs, want 1", len(scripts))
	}

	clock = clock.Add(retryAfter)

	sync6.Once(context.Background())

	if len(scripts) != 2 || strings.Count(logs.String(), "level=WARN") != 1 {
		t.Fatalf("after two failures: %d runs, logs %q; want 2 runs and one warning", len(scripts), logs.String())
	}

	fail = nil
	clock = clock.Add(retryAfter)

	sync6.Once(context.Background())
	sync6.Once(context.Background())

	if len(scripts) != 3 {
		t.Fatalf("an unchanged set ran nft again: %d runs", len(scripts))
	}

	addrs["wlan0"] = append(addrs["wlan0"], ipNet(t, "2001:db8::9/64"))

	sync6.Once(context.Background())

	want := "flush set inet filter lan_nets6\n" +
		`add element inet filter lan_nets6 { "wlan0" . 2001:db8::/64, "wlan0" . fe80::/10 }` + "\n"
	if len(scripts) != 4 || scripts[3] != want {
		t.Fatalf("after a new prefix: runs %q, want the last to be %q", scripts, want)
	}
}

func TestWantedOnlyWhereTheFilterIsLoaded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)

		err := os.WriteFile(path, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		return path
	}

	cases := map[string]bool{
		filepath.Join(dir, "missing"):       false, // a development host
		write("alpha", "alpha\n"):           false, // no ruleset
		write("production", "production\n"): true,
		write("odd", "beta\n"):              true, // held to production
	}
	for path, want := range cases {
		if got := Wanted(path); got != want {
			t.Errorf("Wanted(%s) = %v, want %v", filepath.Base(path), got, want)
		}
	}
}
