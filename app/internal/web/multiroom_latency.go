// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
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
var downstreamPort = "80"

func fetchDownstream(ctx context.Context, host string) (int, error) {
	ip := net.ParseIP(host)
	if ip == nil || !peerIPAllowed(ip) {
		return 0, fmt.Errorf("follower address %q", host)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+net.JoinHostPort(ip.String(), downstreamPort)+"/api/multiroom/downstream", nil)
	if err != nil {
		return 0, err
	}
	resp, err := inviteClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("downstream: %s", resp.Status)
	}
	var body struct {
		MS int `json:"ms"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&body); err != nil {
		return 0, err
	}
	if body.MS < 0 || body.MS > 5000 {
		return 0, fmt.Errorf("downstream %d ms out of range", body.MS)
	}
	return body.MS, nil
}
