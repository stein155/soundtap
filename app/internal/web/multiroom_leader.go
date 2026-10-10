// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// peerLeader asks the speaker at host whom it follows. A speaker that does not answer, or
// is too old to know the question, is taken to follow nobody. A variable for tests.
var peerLeader = func(ctx context.Context, host string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+net.JoinHostPort(host, downstreamPort)+"/api/multiroom/leader", nil)
	if err != nil {
		return ""
	}
	resp, err := inviteClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var body struct {
		Host string `json:"host"`
	}
	if resp.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&body) != nil {
		return ""
	}
	return body.Host
}

func (s *Server) getMultiroomLeader(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"host": s.followedHost()})
}

// WatchLeader keeps a follower attached to its leader. The leader's snapserver is
// dialled every tick; when it does not answer, the registry is asked where the leader's
// device id lives now, and a renumbered leader is followed at its new address. A leader
// that stays gone for leaderGiveUp ticks is let go: this speaker goes to STANDBY instead
// of sitting on MULTIROOM, silent, retrying forever.
func (s *Server) WatchLeader(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fails = s.checkLeader(ctx, fails)
	}
}

// WatchLeaderPower keeps a follower's power in step with its leader's. A
// follower on MULTIROOM whose leader went to standby goes to standby too, but keeps
// following: "keep groups" keeps the group, not a white LED and an open amp over a silent
// stream. A follower in standby whose leader plays joins in again, which is also how a
// follower that rebooted, and so came up in standby, finds its way back.
func (s *Server) WatchLeaderPower(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.syncLeaderPower(ctx)
	}
}

// syncLeaderPower runs one WatchLeaderPower tick; see multiroom.Group.SyncLeaderPower.
func (s *Server) syncLeaderPower(ctx context.Context) { s.group.SyncLeaderPower(ctx, s.groupEnv()) }

// peerNow asks the speaker at host what it plays; STANDBY means it is off. ok is false
// when it does not answer. A variable for tests.
var peerNow = func(ctx context.Context, host string) (np speaker.NowPlaying, ok bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+net.JoinHostPort(host, downstreamPort)+"/api/now", nil)
	if err != nil {
		return speaker.NowPlaying{}, false
	}
	resp, err := inviteClient.Do(req)
	if err != nil {
		return speaker.NowPlaying{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&np) != nil || np.Source == "" {
		return speaker.NowPlaying{}, false
	}
	return np, true
}

// leaderReachable is multiroom.LeaderReachable, after asking the leader to let this speaker
// reach its stream port: after a leader reboot its follower set is empty. A
// variable for tests.
var leaderReachable = func(ctx context.Context, host string) bool {
	askListen(ctx, host)
	return multiroom.LeaderReachable(ctx, host)
}

// allowFollower is multiroom.AllowFollower. A variable for tests.
var allowFollower = multiroom.AllowFollower

// askListen asks the leader at host to accept this speaker on its stream port, best effort.
// A variable for tests.
var askListen = func(ctx context.Context, host string) { listenAsk(host)(ctx) }

// listenAsk reads the leader's address now and returns the request to make, so a caller
// can run it on the side without touching package state later.
func listenAsk(host string) func(context.Context) {
	url := "http://" + net.JoinHostPort(host, downstreamPort) + "/api/multiroom/listen"
	client := inviteClient
	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil) //nolint:gosec // G704: the leader this speaker follows; inviteClient refuses loopback
		if err != nil {
			return
		}
		if resp, err := client.Do(req); err == nil { //nolint:gosec // G704: as above
			_ = resp.Body.Close()
		}
	}
}

// postMultiroomListen lets the caller reach the stream port: a follower that comes back on
// its own (leader reboot, restart) asks before it dials. It is as open as the rest of the
// LAN API, so what it keeps out is passive listeners.
func (s *Server) postMultiroomListen(w http.ResponseWriter, r *http.Request) {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		allowFollower(r.Context(), host)
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkLeader runs one WatchLeader tick and returns the failure count for the next; see
// multiroom.Group.CheckLeader.
func (s *Server) checkLeader(ctx context.Context, fails int) int {
	return s.group.CheckLeader(ctx, s.groupEnv(), fails)
}
