// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/settings"
)

// followerPeersTimeout bounds asking the group for its followers.
const followerPeersTimeout = 3 * time.Second

// What a leader does for the speakers following it lives in multiroom (followers.go);
// here are the calls to the followers' own APIs that it makes through Env.

// volumeSyncHeader marks a volume change that a leader forwarded. The follower applies
// it and goes no further. Without it, two speakers that both believe they lead -- a
// moment that exists during every regrouping -- would bounce one knob turn between
// them for as long as both stay up.
const volumeSyncHeader = "X-SoundTap-Volume-Sync"

// peerClient carries the leader-to-follower calls; peerTransport bounds the dial. It follows
// no redirects: a follower answers itself or not at all.
//
// peerAnswerError is a peer speaker that answered a request with an error status.
type peerAnswerError struct {
	request, status string
}

func (e *peerAnswerError) Error() string { return e.request + " answered " + e.status }

//nolint:gochecknoglobals // one connection pool shared by every request to a peer speaker
var peerClient = &http.Client{
	Transport: peerTransport, Timeout: peerTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// multiroomPrefsView is the API shape of the group preferences. The stored field is the
// inverse of keepGroups (see settings.Multiroom); the UI never sees that.
func multiroomPrefsView(m settings.Multiroom) map[string]bool {
	return map[string]bool{"keepGroups": m.KeepGroups(), "syncVolume": m.SyncVolume}
}

// volumeSyncWanted reports whether a volume request on this speaker should be passed on
// to its followers: the request is the group control (its body says "group": true; a
// per-room slider never does), the user asked for volume sync (or this speaker is half of a
// stereo pair), the request is not itself a forwarded one, and this speaker has a
// snapserver to have followers on at all.
// Whether anyone actually follows is asked later, off the request path.
func (s *Server) volumeSyncWanted(r *http.Request, group bool) bool {
	return group && s.settings != nil && (s.settings.Get().Multiroom.SyncVolume || s.paired()) &&
		r.Header.Get(volumeSyncHeader) == "" && s.multiroom != nil
}

// following reports whether this speaker plays another speaker's stream. A follower has
// no followers of its own worth acting for: its own snapserver is idle.
func (s *Server) following() bool { return s.followedHost() != "" }

// followedHost is the address of the speaker this one follows, or "".
func (s *Server) followedHost() string {
	host, _ := s.Follow()

	return host
}

// followerPeers returns the API base URL of every speaker connected to this speaker's
// snapserver. A client that is not a SoundTap speaker (no device id) and this speaker's own
// client are left out. A speaker that itself follows another reports none.
func (s *Server) followerPeers(ctx context.Context) []string {
	if s.multiroom == nil || s.following() {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, followerPeersTimeout)
	defer cancel()

	status, err := s.multiroom.GetStatus(ctx)
	if err != nil {
		s.log.Info("followers unknown", "err", err)

		return nil
	}

	var peers []string

	seen := map[string]bool{}

	for _, group := range status.Groups {
		for _, client := range group.Clients {
			if !client.Connected || client.ID == s.snapclientID {
				continue
			}

			id := multiroom.DeviceIDFor(client.ID)
			if id == "" || (s.serial != "" && id == s.serial) {
				continue
			}

			if base := s.followerAddress(id, client.Host.IP); base != "" && !seen[base] {
				seen[base] = true
				peers = append(peers, base)
			}
		}
	}

	return peers
}

// followerAddress is the API base URL of follower id, whose snapclient connected from ip:
// discovery's address when it has one, else ip on the port every speaker answers on. A
// follower missing from discovery (a stale or departed entry) used to be skipped without
// a word, so a leader's standby left it following.
func (s *Server) followerAddress(id, ip string) string {
	if base := s.peerAddress(id); base != "" {
		return base
	}

	addr := net.ParseIP(ip)
	if addr == nil || !peerIPAllowed(addr) {
		return ""
	}

	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(addr.String(), downstreamPort)}).String()
}

// stepPeerVolume sets one follower delta further than from, clamped 0..100, and returns
// the unclamped level. Without a known from it reads the follower's volume first.
//
//nolint:revive // flag-parameter: the signature is multiroom.Env's StepPeerVolume
func stepPeerVolume(ctx context.Context, base string, delta, from int, known bool) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, peerTimeout)
	defer cancel()

	if !known {
		cur, err := peerVolume(ctx, base)
		if err != nil {
			return 0, err
		}

		from = cur
	}

	want := from + delta

	next := min(max(want, 0), maxVolume)
	if next == min(max(from, 0), maxVolume) {
		return want, nil
	}

	return want, postPeer(ctx, base, "/api/volume", map[string]int{"volume": next}, forwardedVolume())
}

// peerVolume reads one follower's volume.
func peerVolume(ctx context.Context, base string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/volume", http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("GET volume request: %w", err)
	}

	resp, err := peerClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET volume: %w", err)
	}

	var cur struct {
		Volume *int `json:"volume"`
	}

	err = json.NewDecoder(io.LimitReader(resp.Body, smallBody)).Decode(&cur)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, &peerAnswerError{request: "GET volume", status: resp.Status}
	}

	if err != nil || cur.Volume == nil {
		return 0, fmt.Errorf("GET volume: unreadable answer: %w", err)
	}

	return *cur.Volume, nil
}

// releasePeer tells one follower to stop following: it goes back to its own snapserver
// and to standby (postMultiroomFollow with an empty host).
func releasePeer(ctx context.Context, base string) error {
	return postPeer(ctx, base, "/api/multiroom/follow", map[string]string{"host": ""}, nil)
}

// forwardedVolume marks a volume step the group sends on, so the follower does not send it
// back.
func forwardedVolume() http.Header {
	header := make(http.Header)
	header.Set(volumeSyncHeader, "1")

	return header
}

// postPeer POSTs a small JSON body to one follower and wants a 200 back.
// header is added to the request; nil adds nothing.
func postPeer(ctx context.Context, base, path string, body any, header http.Header) error {
	code, err := sendPeer(ctx, http.MethodPost, base, path, body, header)
	if err == nil && code != http.StatusOK {
		return &peerAnswerError{request: "POST " + path, status: fmt.Sprintf("%d %s", code, http.StatusText(code))}
	}

	return err
}

// sendPeer sends a small JSON body to another speaker and returns the status it answered.
// header is added to the request; nil adds nothing.
func sendPeer(ctx context.Context, method, base, path string, body any, header http.Header) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, peerTimeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("%s %s body: %w", method, path, err)
	}

	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("%s %s request: %w", method, path, err)
	}

	req.Header.Set("Content-Type", "application/json")

	maps.Copy(req.Header, header)

	resp, err := peerClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, path, err)
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, smallBody))
	_ = resp.Body.Close()

	return resp.StatusCode, nil
}
