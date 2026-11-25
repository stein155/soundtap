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

const (
	// peerAskTimeout bounds one question to a peer speaker.
	peerAskTimeout = 2 * time.Second
	// nowPlayingLimit bounds a peer's now-playing answer.
	nowPlayingLimit = 64 << 10
)

// peerLeader asks the speaker at host whom it follows. A speaker that does not answer, or
// is too old to know the question, is taken to follow nobody. A variable for tests.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var peerLeader = func(ctx context.Context, host string) string {
	ctx, cancel := context.WithTimeout(ctx, peerAskTimeout)
	defer cancel()

	target := "http://" + net.JoinHostPort(host, downstreamPort) + "/api/multiroom/leader"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
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
		json.NewDecoder(io.LimitReader(resp.Body, tinyBody)).Decode(&body) != nil {
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
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	fails := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		s.syncLeaderPower(ctx)
	}
}

// syncLeaderPower runs one WatchLeaderPower tick; see multiroom.Group.SyncLeaderPower.
func (s *Server) syncLeaderPower(ctx context.Context) { s.group.SyncLeaderPower(ctx, s.groupEnv()) }

// peerNow asks the speaker at host what it plays; STANDBY means it is off. ok is false
// when it does not answer. A variable for tests.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var peerNow = func(ctx context.Context, host string) (speaker.NowPlaying, bool) {
	var none, playing speaker.NowPlaying

	target := "http://" + net.JoinHostPort(host, downstreamPort) + "/api/now"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return none, false
	}

	resp, err := inviteClient.Do(req)
	if err != nil {
		return none, false
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return none, false
	}

	err = json.NewDecoder(io.LimitReader(resp.Body, nowPlayingLimit)).Decode(&playing)
	if err != nil || playing.Source == "" {
		return none, false
	}

	return playing, true
}

// leaderReachable is multiroom.LeaderReachable, after asking the leader to let this speaker
// reach its stream port: after a leader reboot its follower set is empty. A
// variable for tests.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var leaderReachable = func(ctx context.Context, host string) bool {
	askListen(ctx, host)

	return multiroom.LeaderReachable(ctx, host)
}

// allowFollower is multiroom.AllowFollower. A variable for tests.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var allowFollower = multiroom.AllowFollower

// askListen asks the leader at host to accept this speaker on its stream port, best effort.
func askListen(ctx context.Context, host string) { listenAsk(host)(ctx) }

// listenAsk reads the leader's address now and returns the request to make, so a caller
// can run it on the side without touching package state later.
func listenAsk(host string) func(context.Context) {
	url := "http://" + net.JoinHostPort(host, downstreamPort) + "/api/multiroom/listen"
	client := inviteClient

	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, peerAskTimeout)
		defer cancel()

		// G704: the leader this speaker follows; inviteClient refuses loopback.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody) //nolint:gosec // G704: see above
		if err != nil {
			return
		}

		resp, err := client.Do(req) //nolint:gosec // G704: as above
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

// postMultiroomListen lets the caller reach the stream port: a follower that comes back on
// its own (leader reboot, restart) asks before it dials. It is as open as the rest of the
// LAN API, so what it keeps out is passive listeners.
func (*Server) postMultiroomListen(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		allowFollower(r.Context(), host)
	}

	w.WriteHeader(http.StatusNoContent)
}

// checkLeader runs one WatchLeader tick and returns the failure count for the next; see
// multiroom.Group.CheckLeader.
func (s *Server) checkLeader(ctx context.Context, fails int) int {
	return s.group.CheckLeader(ctx, s.groupEnv(), fails)
}
