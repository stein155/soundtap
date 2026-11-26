// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package lanfilter keeps the production input filter's set lan_nets6 equal to the IPv6
// prefixes of the speaker's own links (ADR 0066, ADR 0134). It is the IPv6 half of what
// /opt/soundtap/lan-allow.sh does for IPv4: there a DHCP lease, here an address the kernel
// configures itself (link-local, SLAAC) with no hook to call, so soundtapd looks.
package lanfilter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
)

const (
	// Interval is how often the addresses are looked at: a new SLAAC prefix is answered
	// within this long.
	Interval = 5 * time.Second
	// runTimeout bounds one nft run.
	runTimeout = 2 * time.Second
	// nftWaitDelay is how long a killed nft's pipes may hold the run open.
	nftWaitDelay = 500 * time.Millisecond
	// retryAfter spaces out tries of a script nft refused: a set that is not there stays
	// away, and starting nft every few seconds for it only costs the speaker.
	retryAfter = time.Minute
)

// interfaces are the speaker's own links, the ones lan-allow.sh is called for: the Wi-Fi
// station, wired Ethernet (ST20, ST300) and the setup AP. Not lo, the USB gadget (usb0) or
// the ST20's internal link to its BCO.
//
//nolint:gochecknoglobals // a fixed list, read only
var interfaces = []string{"eth0", "wlan0", "wlan1"}

// linkLocal is every link's own prefix; it is answered on each listed interface that has
// an IPv6 address.
//
//nolint:gochecknoglobals // a constant prefix
var linkLocal = netip.MustParsePrefix("fe80::/10")

// Wanted reports whether profileFile names an image that loads the production filter. As
// web.FirewallCheck: no file is a development host, "alpha" loads no ruleset, and a file
// that cannot be read is held to production.
func Wanted(profileFile string) bool {
	b, err := os.ReadFile(profileFile)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}

	return err != nil || strings.TrimSpace(string(b)) != "alpha"
}

// Sync puts each change of the prefixes into the set.
type Sync struct {
	// addrs lists one interface's addresses; an interface that is not there has none.
	addrs func(name string) ([]net.Addr, error)
	// run hands script to nft -f -.
	run func(ctx context.Context, script string) error
	log *slog.Logger
	now func() time.Time

	mu        sync.Mutex
	applied   string    // the last script nft took
	failed    string    // the last failure logged, so a repeating one is logged once
	refused   string    // the last script nft refused
	refusedAt time.Time // when it did
}

// New returns a Sync on the real interfaces and nft.
func New(log *slog.Logger) *Sync {
	if log == nil {
		log = slog.Default()
	}

	return &Sync{
		addrs: interfaceAddrs, run: runNft, log: log, now: time.Now,
		mu: sync.Mutex{}, applied: "", failed: "", refused: "", refusedAt: time.Time{},
	}
}

// Run syncs at once and then every interval until ctx ends.
func (s *Sync) Run(ctx context.Context, interval time.Duration) {
	for {
		s.Once(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Once compares the interfaces' prefixes with what nft last took and, when they differ,
// replaces the set's elements in one nft transaction. A script nft refused is tried again
// after retryAfter, or at once when the prefixes change.
func (s *Sync) Once(ctx context.Context) {
	byIface := map[string][]net.Addr{}

	for _, name := range interfaces {
		addrs, err := s.addrs(name)
		if err == nil {
			byIface[name] = addrs
		}
	}

	script := Script(Elements(byIface))

	s.mu.Lock()
	defer s.mu.Unlock()

	if script == s.applied {
		return
	}

	if script == s.refused && s.now().Sub(s.refusedAt) < retryAfter {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	err := s.run(ctx, script)
	if err != nil {
		s.refused, s.refusedAt = script, s.now()

		if msg := err.Error(); msg != s.failed {
			s.failed = msg
			s.log.Warn("IPv6 LAN prefixes not in the input filter", "err", err)
		}

		return
	}

	s.applied, s.failed, s.refused = script, "", ""
	s.log.Info("IPv6 LAN prefixes", "elements", strings.TrimSpace(script))
}

// Elements returns lan_nets6's elements for these addresses, sorted: per interface
// fe80::/10 once it has any IPv6 address, and the prefix of every other unicast address
// with a prefix length below 128. A prefix inside another one of the same interface is
// left out, since an interval set refuses overlaps.
func Elements(byIface map[string][]net.Addr) []string {
	out := make([]string, 0, len(byIface))

	for name, addrs := range byIface {
		var prefixes []netip.Prefix

		for _, a := range addrs {
			if p, ok := onLink(a); ok {
				prefixes = append(prefixes, p)
			}
		}

		for _, p := range widest(prefixes) {
			out = append(out, fmt.Sprintf("%q . %s", name, p))
		}
	}

	slices.Sort(out)

	return out
}

// onLink is the on-link prefix of one interface address, when it is IPv6 unicast.
func onLink(a net.Addr) (netip.Prefix, bool) {
	ipNet, ok := a.(*net.IPNet)
	if !ok || ipNet.IP.To4() != nil {
		return netip.Prefix{}, false
	}

	addr, ok := netip.AddrFromSlice(ipNet.IP)
	if !ok || !addr.Is6() {
		return netip.Prefix{}, false
	}

	if addr.IsLinkLocalUnicast() {
		return linkLocal, true
	}

	ones, bits := ipNet.Mask.Size()
	if !addr.IsGlobalUnicast() || bits != net.IPv6len*8 || ones == 0 || ones == bits {
		// Not unicast, or a /128 (DHCPv6) that says nothing about who else is on the link.
		return netip.Prefix{}, false
	}

	return netip.PrefixFrom(addr, ones).Masked(), true
}

// widest drops duplicates and every prefix that lies inside another one of the list.
func widest(prefixes []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix

	for i, prefix := range prefixes {
		inside := false

		for j, other := range prefixes {
			if i != j && other.Bits() <= prefix.Bits() && other.Contains(prefix.Addr()) && (other != prefix || j < i) {
				inside = true

				break
			}
		}

		if !inside {
			out = append(out, prefix)
		}
	}

	return out
}

// Script is the nft transaction that makes lan_nets6 hold exactly elements.
func Script(elements []string) string {
	var script strings.Builder

	script.WriteString("flush set inet filter lan_nets6\n")

	if len(elements) > 0 {
		script.WriteString("add element inet filter lan_nets6 { " + strings.Join(elements, ", ") + " }\n")
	}

	return script.String()
}

// interfaceAddrs lists the addresses of the interface called name.
func interfaceAddrs(name string) ([]net.Addr, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", name, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", name, err)
	}

	return addrs, nil
}

// runNft runs `nft -f -` with script on its standard input, as a network tool.
func runNft(ctx context.Context, script string) error {
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	childproc.NetTool(cmd)
	cmd.Stdin = strings.NewReader(script)
	cmd.WaitDelay = nftWaitDelay

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); line != "" {
			return fmt.Errorf("nft: %w: %s", err, line)
		}

		return fmt.Errorf("nft: %w", err)
	}

	return nil
}
