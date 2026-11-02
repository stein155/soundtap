// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// allowRefresh is how long an added address is left alone: the set's 10-minute timeout
// is refreshed well before it runs out, and repeated asks cost no nft run.
const allowRefresh = time.Minute

var (
	allowMu   sync.Mutex
	allowedAt = map[string]time.Time{}
	// nftSlot lets one nft run at a time: each is a process of its own, and a LAN host
	// asking in a loop must not start dozens at once on a speaker with 20 MB free.
	nftSlot = make(chan struct{}, 1)
)

// runNft is replaced in tests.
var runNft = func(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "nft", args...)
	childproc.NetTool(cmd)
	return cmd.Run()
}

// AllowFollower lets a follower's address reach the stream port (1704): the production
// filter (os/board/rootfs/firewall.nft) accepts it only from addresses in snap_followers,
// and an entry expires after 10 minutes unless it is refreshed. Without nft or the set
// (dev images) nothing is filtered and the call does nothing. host may carry a port;
// a name is resolved; anything but IPv4 is ignored.
func AllowFollower(ctx context.Context, host string) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ip := net.ParseIP(host).To4()
	if ip == nil {
		// An invite may name the speaker (speaker.local): it connects from that address.
		addrs, _ := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if len(addrs) == 0 {
			return
		}
		ip = addrs[0].To4()
	}
	key := ip.String()
	now := time.Now()
	allowMu.Lock()
	if now.Sub(allowedAt[key]) < allowRefresh {
		allowMu.Unlock()
		return
	}
	for k, t := range allowedAt {
		if now.Sub(t) >= allowRefresh {
			delete(allowedAt, k)
		}
	}
	allowedAt[key] = now
	allowMu.Unlock()
	select {
	case nftSlot <- struct{}{}:
	case <-ctx.Done():
		forgetAllowed(key)
		return
	}
	defer func() { <-nftSlot }()
	if runNft(ctx, "add", "element", "ip", "filter", "snap_followers", "{", key, "timeout", "10m", "}") != nil {
		forgetAllowed(key)
	}
}

// forgetAllowed lets the next ask for key try nft again.
func forgetAllowed(key string) {
	allowMu.Lock()
	delete(allowedAt, key)
	allowMu.Unlock()
}
