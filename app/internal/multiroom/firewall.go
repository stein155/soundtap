// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"fmt"
	"net"
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
// filter (os/board/rootfs/firewall.nft) accepts it only from addresses in snap_followers,
// and an entry expires after 10 minutes unless it is refreshed. Without nft or the set
// (dev images) nothing is filtered and the call does nothing. host may carry a port;
// a name is resolved; anything but IPv4 is ignored.
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

	addr := net.ParseIP(host).To4()
	if addr == nil {
		// An invite may name the speaker (speaker.local): it connects from that address.
		addrs, _ := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if len(addrs) == 0 {
			return
		}

		addr = addrs[0].To4()
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

	if l.run(ctx, "add", "element", "ip", "filter", "snap_followers", "{", key, "timeout", "10m", "}") != nil {
		l.forget(key)
	}
}

// forget lets the next ask for key try nft again.
func (l *allowList) forget(key string) {
	l.mu.Lock()
	delete(l.at, key)
	l.mu.Unlock()
}
