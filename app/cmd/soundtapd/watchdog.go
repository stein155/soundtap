// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// speakerHealthy is what the watchdog asks: the primary HTTP listener answers, and the
// audio pump (when there is one) is still moving: it writes silence through standby until
// it rests there (pumpRest), and a resting pump is not a stalled one.
func speakerHealthy(url string, pump audioHealth) func(context.Context) error {
	var last, lastGen uint64
	seen := false
	return func(ctx context.Context) error {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
		// Probes our own loopback listener, which SafeTransport refuses.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("http: status %d", resp.StatusCode)
		}
		if pump == nil {
			return nil
		}
		mixed := pump().MixedBytes
		rest, gen, _ := pumpRest.state()
		stalled := seen && mixed == last && !rest && gen == lastGen
		last, lastGen, seen = mixed, gen, true
		if stalled {
			return errors.New("audio pump stalled")
		}
		return nil
	}
}
