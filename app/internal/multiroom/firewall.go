// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// allowRefresh is how long an added address is left alone: the set's 10-minute timeout
// is refreshed well before it runs out, and repeated asks cost no nft run.
const allowRefresh = time.Minute

// allowTimeout bounds one AllowFollower: the name lookup, the wait for the nft slot and
// the nft run.
const allowTimeout = 2 * time.Second

// allowList remembers which follower addresses were added to snap_followers, and when.
type allowList struct {
	mu sync.Mutex
	at map[string]time.Time
	// slot lets one nft run at a time: each is a process of its own, and a LAN host
	// asking in a loop must not start dozens at once on a speaker with 20 MB free.
	slot chan struct{}
	// run runs nft; a test passes its own.
	run func(ctx context.Context, args ...string) error
}

func newAllowList(run func(ctx context.Context, args ...string) error) *allowList {
	return &allowList{mu: sync.Mutex{}, at: map[string]time.Time{}, slot: make(chan struct{}, 1), run: run}
}

// runNft runs nft as a network tool.
func runNft(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "nft", args...)
	childproc.NetTool(cmd)

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("nft: %w", err)
	}

	return nil
}

// followerAllow is the process's view of the nft set, which is one per speaker as well.
var followerAllow = newAllowList(runNft) //nolint:gochecknoglobals // mirrors the one nft set

// AllowFollower lets a follower's address reach the stream port (1704): the production
// filter (os/board/rootfs/firewall.nft) accepts it only from addresses in snap_followers
// (IPv4) or snap_followers6 (IPv6), and an entry expires after 10 minutes unless it is
// refreshed. Without nft or the sets (dev images) nothing is filtered and the call does
// nothing. host may carry a port and a zone; a name is resolved, and its first IPv4 and
// first IPv6 address are both let in.
func AllowFollower(ctx context.Context, host string) {
	followerAllow.allow(ctx, host)
}

// allow is AllowFollower on this list.
func (l *allowList) allow(ctx context.Context, host string) {
	hostOnly, _, err := net.SplitHostPort(host)
	if err == nil {
		host = hostOnly
	}

	ctx, cancel := context.WithTimeout(ctx, allowTimeout)
	defer cancel()

	for _, addr := range followerAddrs(ctx, host) {
		l.add(ctx, addr)
	}
}

// followerAddrs is host as the addresses to let in: the literal itself, or a name's first
// IPv4 and first IPv6 address. An invite may name the speaker (speaker.local): it connects
// from that address.
func followerAddrs(ctx context.Context, host string) []netip.Addr {
	literal, err := netip.ParseAddr(host)
	if err == nil {
		return []netip.Addr{literal.WithZone("").Unmap()}
	}

	addrs, _ := net.DefaultResolver.LookupNetIP(ctx, "ip", host)

	var first4, first6 netip.Addr

	for _, addr := range addrs {
		addr = addr.WithZone("").Unmap()
		switch {
		case addr.Is4() && !first4.IsValid():
			first4 = addr
		case addr.Is6() && !first6.IsValid():
			first6 = addr
		default:
			// Only the first of each family.
		}
	}

	out := make([]netip.Addr, 0, 2) //nolint:mnd // one per address family

	for _, addr := range []netip.Addr{first4, first6} {
		if addr.IsValid() {
			out = append(out, addr)
		}
	}

	return out
}

// add puts one address in its family's set, unless it went in less than allowRefresh ago.
func (l *allowList) add(ctx context.Context, addr netip.Addr) {
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() {
		return
	}

	set := "snap_followers"
	if addr.Is6() {
		set = "snap_followers6"
	}

	key := addr.String()
	now := time.Now()

	l.mu.Lock()
	if now.Sub(l.at[key]) < allowRefresh {
		l.mu.Unlock()

		return
	}

	for k, t := range l.at {
		if now.Sub(t) >= allowRefresh {
			delete(l.at, k)
		}
	}

	l.at[key] = now
	l.mu.Unlock()

	select {
	case l.slot <- struct{}{}:
	case <-ctx.Done():
		l.forget(key)

		return
	}

	defer func() { <-l.slot }()

	if l.run(ctx, "add", "element", "inet", "filter", set, "{", key, "timeout", "10m", "}") != nil {
		l.forget(key)
	}
}

// forget lets the next ask for key try nft again.
func (l *allowList) forget(key string) {
	l.mu.Lock()
	delete(l.at, key)
	l.mu.Unlock()
}
