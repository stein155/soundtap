// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/snapcast"
)

// SyncListeners runs one WatchMultiroom tick: it keeps the anchor muted, turns the tap on
// and keeps the anchor running while another speaker listens, and then keeps the
// followers' latencies in step.
func SyncListeners(ctx context.Context, env Env) {
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	status, err := env.Snapcast.GetStatus(statusCtx)
	cancel()
	if err != nil {
		return
	}
	MuteAnchor(ctx, env, status)
	for _, group := range status.Groups {
		for _, c := range group.Clients {
			if c.Connected && c.ID != env.LocalID {
				AllowFollower(ctx, c.Host.IP)
			}
		}
	}
	listening := HasListener(status, env.LocalID)
	env.Active(listening)
	// A leader keeps its anchor while it has listeners, and for AnchorHold after the last.
	if listening && env.WantAnchor != nil {
		env.WantAnchor()
	}
	if listening {
		SyncLatencies(ctx, env, status)
	}
}

// MuteAnchor keeps this speaker's own snapclient muted in its own snapserver. That client
// renders into /dev/null and is only there to give the group an identity (snapclientArgs);
// snapserver sends no audio to a muted client (send_to_muted defaults to false), so a
// leader stops streaming the whole mix to itself over loopback. Measured on an ST20
// leader with two followers: 5 points of CPU (anchor 4.6 % -> 1 %, snapserver -1.6 %).
func MuteAnchor(ctx context.Context, env Env, status snapcast.Status) {
	for _, group := range status.Groups {
		for _, c := range group.Clients {
			if c.ID != env.LocalID || !c.Connected || c.Config.Volume.Muted {
				continue
			}
			muteCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := env.Snapcast.SetVolume(muteCtx, c.ID, snapcast.Volume{Percent: c.Config.Volume.Percent, Muted: true})
			cancel()
			if err != nil {
				env.Log.Warn("mute multiroom anchor", "err", err)
			}
		}
	}
}

// LatencyStep is the smallest correction worth sending: each one makes snapclient re-sync.
const LatencyStep = 10

// SyncLatencies tells snapserver how far ahead each follower must play. A follower's
// snapclient writes PCM into the source bus at the moment it should be heard, but the bus,
// the Mixer's jitter buffer, aplay's pipe and the DAC ring still lie ahead of it: 300 to
// 450 ms, different per model and per start. Without this the followers trailed the
// leader audibly. A follower that does not answer keeps the latency it has.
func SyncLatencies(ctx context.Context, env Env, status snapcast.Status) {
	for _, group := range status.Groups {
		for _, c := range group.Clients {
			if !c.Connected || c.ID == env.LocalID {
				continue
			}
			ms, err := env.Downstream(ctx, c.Host.IP)
			if err != nil {
				continue
			}
			if diff := ms - c.Config.Latency; diff > -LatencyStep && diff < LatencyStep {
				continue
			}
			setCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err = env.Snapcast.SetLatency(setCtx, c.ID, ms)
			cancel()
			if err != nil {
				env.Log.Info("set follower latency", "client", c.ID, "err", err)
			}
		}
	}
}

// Leads reports whether another speaker is connected to this one's snapserver. When that
// cannot be read, nobody is known to listen.
func Leads(ctx context.Context, env Env) bool {
	if env.Snapcast == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, err := env.Snapcast.GetStatus(ctx)
	if err != nil {
		env.Log.Info("listeners unknown", "err", err)
		return false
	}
	return HasListener(status, env.LocalID)
}

// LeaderReachable reports whether a leader's snapserver answers.
//
// While this speaker's snapclient holds a stream connection to the leader, that is the
// answer and nothing is sent. Only without one is the stream port dialled: every such
// probe is a stream session that snapserver logs as an error when it closes, which was
// two log lines a minute per follower on the leader (2026-10-09).
func LeaderReachable(ctx context.Context, host string) bool {
	if streamingFrom(procNetTCP, host) {
		return true
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "1704"))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

const procNetTCP = "/proc/net/tcp"

// streamingFrom reports whether procNetTCP lists an established connection to host's
// snapserver stream port. The kernel prints an IPv4 address as the 32-bit word in host
// byte order, which on these little-endian CPUs is the address bytes reversed.
func streamingFrom(procNetTCP, host string) bool {
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return false
	}
	b, err := os.ReadFile(procNetTCP)
	if err != nil {
		return false
	}
	want := fmt.Sprintf("%02X%02X%02X%02X:06A8", ip[3], ip[2], ip[1], ip[0])
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 3 && f[2] == want && f[3] == "01" {
			return true
		}
	}
	return false
}
