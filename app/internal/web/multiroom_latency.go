// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

func (s *Server) getMultiroomDownstream(w http.ResponseWriter, _ *http.Request) {
	if s.downstream == nil {
		apiError(w, http.StatusServiceUnavailable, "no audio chain")

		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"ms": int(s.downstream() / time.Millisecond)})
}

// downstreamPort is where a follower's API listens; tests point it at their own server.
//
// Why a follower's downstream latency could not be read.
var (
	errFollowerAddress = errors.New("follower address")
	errDownstream      = errors.New("downstream")
)

// maxDownstreamMS is the longest downstream latency a follower can report.
const maxDownstreamMS = 5000

//nolint:gochecknoglobals // a variable so tests can replace it
var downstreamPort = "80"

func fetchDownstream(ctx context.Context, host string) (int, error) {
	addr := literalIP(host)
	if addr == nil || !peerIPAllowed(addr) {
		return 0, fmt.Errorf("%w %q", errFollowerAddress, host)
	}

	ctx, cancel := context.WithTimeout(ctx, peerAskTimeout)
	defer cancel()

	target := "http://" + net.JoinHostPort(addr.String(), downstreamPort) + "/api/multiroom/downstream"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("%w request: %w", errDownstream, err)
	}

	resp, err := inviteClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errDownstream, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%w: %s", errDownstream, resp.Status)
	}

	var body struct {
		MS int `json:"ms"`
	}

	err = json.NewDecoder(io.LimitReader(resp.Body, tinyBody)).Decode(&body)
	if err != nil {
		return 0, fmt.Errorf("%w answer: %w", errDownstream, err)
	}

	if body.MS < 0 || body.MS > maxDownstreamMS {
		return 0, fmt.Errorf("%w %d ms out of range", errDownstream, body.MS)
	}

	return body.MS, nil
}
