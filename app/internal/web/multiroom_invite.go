// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/multiroom"
)

// inviteTimeout bounds asking a peer to join this speaker's group.
const inviteTimeout = 10 * time.Second

// inviteHostOK is followHostOK plus an optional port.
var inviteHostOK = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.-]{0,253}(:\d{1,5})?$`)

// inviteClient follows no redirects: http.DefaultClient re-sent the POST, body and all,
// wherever a 307/308 pointed.
//
//nolint:gochecknoglobals // one connection pool shared by every request to a peer speaker
var inviteClient = &http.Client{Transport: peerTransport, CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

// inviteTargetAllowed refuses this speaker's own loopback: the invite is a POST made by
// this process, so it would pass every loopbackOnly check here.
func inviteTargetAllowed(target string) bool {
	host := target

	h, _, err := net.SplitHostPort(target)
	if err == nil {
		host = h
	}

	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return false
	}

	ip := literalIP(host)

	return ip == nil || peerIPAllowed(ip)
}

// ownAddress reports whether ip is one of this host's non-loopback interface addresses.
// Loopback is peerIPAllowed's to refuse, so tests whose fake peers listen there can allow it.
func ownAddress(addr net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}

	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.Equal(addr) {
			return true
		}
	}

	return false
}

// postMultiroomInvite tells another speaker to follow this one.
//
// The browser cannot do this itself: a page served by this speaker asking another speaker's
// API is cross-origin, and both ends refuse that on purpose (see sameSpeakerOrigin). So the
// speaker the user is looking at makes the call, which is also the speaker whose address the
// other one needs.
// inviteLeader is the leader an invite names, its address and id; "" when this speaker
// does not know its own address. A speaker that follows another passes on its own leader,
// so the group stays one hop deep instead of chaining through this speaker. An unknown
// leader id is left out rather than filled with this speaker's.
func (s *Server) inviteLeader(ctx context.Context) (string, string) {
	self := ""
	if s.selfAddress != nil {
		self = s.selfAddress(ctx)
	}

	leader, leaderID := s.Follow()
	if leader != "" {
		return leader, leaderID
	}

	if self == "" {
		return "", ""
	}

	if s.backend != nil {
		info, err := s.backend.Info(ctx)
		if err == nil {
			leaderID = s.Identity(info.DeviceID)
		}
	}
	// This speaker is about to lead: its anchor starts now, while the invited speaker
	// switches over, so the membership call that follows rarely has to wait for it.
	if s.wantAnchor != nil {
		s.wantAnchor()
	}

	return self, leaderID
}

func (s *Server) postMultiroomInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IP string `json:"ip"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body")

		return
	}

	target := strings.TrimSpace(body.IP)
	// A port is allowed here and nowhere else: this address is dialled over HTTP by this
	// process, not pasted onto a command line, and a test speaker does not listen on 80.
	if !inviteHostOK.MatchString(target) || !inviteTargetAllowed(target) {
		apiError(w, http.StatusBadRequest, "invalid ip")

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inviteTimeout)
	defer cancel()

	self, leaderID := s.inviteLeader(ctx)
	if self == "" {
		apiError(w, http.StatusServiceUnavailable, "this speaker does not know its own address")

		return
	}

	invite := map[string]string{"host": self}
	if leaderID != "" {
		invite["id"] = leaderID
	}

	payload, _ := json.Marshal(invite) //nolint:errchkjson // a map of strings always marshals

	multiroom.AllowFollower(ctx, target)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+target+"/api/multiroom/follow", strings.NewReader(string(payload)))
	if err != nil {
		s.fail(w, "invite speaker failed", err)

		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := inviteClient.Do(req)
	if err != nil {
		s.fail(w, "invite speaker failed", err)

		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		apiError(w, http.StatusBadGateway, fmt.Sprintf("%s answered %s", target, resp.Status))

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"invited": target, "following": self})
}
