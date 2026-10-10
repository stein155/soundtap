// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/safego"
)

// followHostOK is what may reach a command line and a tcp:// URL: an IPv4 address or a
// host name, nothing that could be read as a second argument or a shell word. The wrapper
// script filters the same set again, because a file on a writable partition is not a trust
// boundary this side owns.
var followHostOK = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.-]{0,253}$`)

// multiroomServerPath is the speaker this one follows: empty or missing means its own
// snapserver, which is the silent-anchor shape every speaker boots with.
func (s *Server) multiroomServerPath() string {
	if s.homeDir == "" {
		return ""
	}
	return filepath.Join(s.homeDir, "multiroom-server")
}

// leaderIDOK is what a device id looks like: a serial or a MAC-derived id.
var leaderIDOK = regexp.MustCompile(`^[0-9A-Za-z._-]{1,64}$`)

// follow is whom this speaker follows: the leader's address and device id, "" when unknown.
func (s *Server) follow() (host, id string) { return multiroom.ReadFollow(s.multiroomServerPath()) }

// restartFollowing kicks snapclient and asks the leader it is about to connect to to
// accept this speaker; every way into a follow ends here. The ask runs on the
// side: an unreachable leader would hold the group lock for its 2 s timeout, and snapclient
// retries its connection anyway.
// listenInBackground is listenAsk. A variable so tests can keep the ask from outliving them.
var listenInBackground = listenAsk

func (s *Server) restartFollowing() {
	if host := s.followedHost(); host != "" {
		listen := listenInBackground(host)
		safego.Go("multiroom.listen", func() { listen(context.Background()) })
	}
	if s.restartSnapclient != nil {
		s.restartSnapclient()
	}
}

// groupEnv wires the multiroom Group to this speaker as it is set up at this moment.
func (s *Server) groupEnv() multiroom.Env {
	env := multiroom.Env{
		Log: s.log, Path: s.multiroomServerPath(),
		SelectSource: s.selectSource, SourceChanging: s.sourceChanging, RestartSnapclient: s.restartFollowing,
		LeaderLive: s.leaderLive, LeaderNow: s.leaderNow, DeviceIP: s.deviceIP,
		PeerLeader: peerLeader, PeerNow: peerNow, LeaderReachable: leaderReachable,
		FollowerPeers: s.followerPeers, StepPeerVolume: stepPeerVolume, ReleasePeer: releasePeer,
		PeerTimeout: peerTimeout,
		Snapcast:    s.multiroom, LocalID: s.snapclientID, Active: s.multiroomActive, ActivePath: s.multiroomActivePath(),
		Downstream: fetchDownstream, WantAnchor: s.wantAnchor,
	}
	if s.backend != nil {
		env.NowPlaying = s.backend.NowPlaying
	}
	if s.hub != nil {
		env.Nudge = s.hub.nudge
	}
	if s.snapclientID != "" {
		env.JoinLeaderGroup = s.joinLeaderGroup
	}
	return env
}

// joinLeaderTries bounds joinLeaderGroup: a client the leader has never seen is only
// known to it once the restarted snapclient has connected, a second or two.
const joinLeaderTries = 10

// joinLeaderGroup asks the leader at host to make this speaker's snapclient a member of
// its group, as its membership switch does, which also unmutes it.
func (s *Server) joinLeaderGroup(host string) {
	base := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, downstreamPort)}).String()
	path := "/api/multiroom/clients/" + url.PathEscape(s.snapclientID) + "/membership"
	for try := 1; ; try++ {
		code, err := sendPeer(context.Background(), http.MethodPut, base, path, map[string]bool{"member": true}, false)
		if err == nil && code == http.StatusOK {
			return
		}
		if code != http.StatusNotFound || try == joinLeaderTries {
			s.log.Info("multiroom: leader's group not joined", "leader", host, "status", code, "err", err)
			return
		}
		time.Sleep(time.Second / 2)
	}
}

// postMultiroomFollow points this speaker's snapclient at another speaker's snapserver, or
// with an empty host puts it back on its own.
//
// Every speaker runs a server and a client, and until this existed the client was nailed to
// tcp://127.0.0.1:1704 -- so two SoundTap speakers could be grouped in the interface and
// still never share a stream.
func (s *Server) postMultiroomFollow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host string `json:"host"`
		// ID is the leader's device id, so a follower can find the leader again after
		// it was renumbered. Optional: an older leader does not send it.
		ID string `json:"id"`
	}
	// A mistyped key would read as an empty host, which means leave the group.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	host, id := strings.TrimSpace(body.Host), strings.TrimSpace(body.ID)
	if code, msg := s.followRefusal(r.Context(), host, id); code != 0 {
		apiError(w, code, msg)
		return
	}
	if host == "" {
		id = ""
	}
	prevHost, err := s.group.Join(r.Context(), s.groupEnv(), host, id)
	var failed *multiroom.JoinError
	switch {
	case errors.Is(err, multiroom.ErrChained):
		apiError(w, http.StatusConflict, multiroom.ErrChained.Error())
		return
	case errors.As(err, &failed):
		s.fail(w, failed.Step.Error(), failed.Err)
		return
	}
	if host != "" && host != prevHost {
		s.chimeEvent("grouped.wav")
	}
	writeJSON(w, http.StatusOK, map[string]string{"following": host})
}

// followRefusal says why this speaker may not follow host: a status and a message, or 0.
func (s *Server) followRefusal(ctx context.Context, host, id string) (code int, msg string) {
	if host != "" && (!followHostOK.MatchString(host) || !followTargetAllowed(host)) {
		return http.StatusBadRequest, "invalid host"
	}
	if id != "" && !leaderIDOK.MatchString(id) {
		return http.StatusBadRequest, "invalid id"
	}
	if s.multiroomServerPath() == "" {
		return http.StatusServiceUnavailable, "no settings directory"
	}
	// A speaker others are listening to cannot follow in turn: A following B while B
	// follows A is a loop that plays nothing, and each extra hop adds a snapserver buffer.
	if host != "" && multiroom.Leads(ctx, s.groupEnv()) {
		return http.StatusConflict, "this speaker leads a group; ungroup it first"
	}
	// Nor may it follow a speaker that follows in turn: that is a chain, and with this
	// speaker as the far end of it a loop.
	if host != "" && peerLeader(ctx, host) != "" {
		return http.StatusConflict, "that speaker follows another; group with its leader"
	}
	return 0, ""
}

// followTargetAllowed reports whether host may be the leader this speaker follows: a peer
// address and not one of this speaker's own. Following itself, the speaker would probe
// its own snapserver, which always answers, so the leader-gone watchdog never fires and
// it sits on a silent MULTIROOM source.
func followTargetAllowed(host string) bool {
	if !inviteTargetAllowed(host) {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ownAddress(ip)
}

// KeepFollow reports, once, whether the leave from MULTIROOM now happening is the
// group's own standby; see multiroom.Group.KeepFollow.
func (s *Server) KeepFollow() bool { return s.group.KeepFollow(s.groupEnv()) }

// Follow is whom this speaker follows now, in the form LeaveFollow takes.
func (s *Server) Follow() (host, id string) { return s.follow() }

// LeaveFollow ends the follow of host and id after the source left MULTIROOM; see
// multiroom.Group.LeaveFollow.
func (s *Server) LeaveFollow(host, id string) { s.group.LeaveFollow(s.groupEnv(), host, id) }
