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

// healthProbeTimeout bounds one watchdog probe of the HTTP listener.
const healthProbeTimeout = 5 * time.Second

// Why the watchdog finds the speaker unhealthy.
var (
	errSpeakerHTTPStatus = errors.New("http: status")
	errPumpStalled       = errors.New("audio pump stalled")
)

// speakerHealthy is what the watchdog asks: the primary HTTP listener answers, and the
// audio pump (when there is one) is still moving: it writes silence through standby until
// it rests there (pumpRest), and a resting pump is not a stalled one.
func speakerHealthy(url string, pump audioHealth) func(context.Context) error {
	var last, lastGen uint64

	seen := false

	return func(ctx context.Context) error {
		cctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
		defer cancel()

		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, url, http.NoBody)
		// Probes our own loopback listener, which SafeTransport refuses.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%w %d", errSpeakerHTTPStatus, resp.StatusCode)
		}

		if pump == nil {
			return nil
		}

		mixed := pump().MixedBytes
		rest, gen, _ := pumpRest.state()
		stalled := seen && mixed == last && !rest && gen == lastGen
		last, lastGen, seen = mixed, gen, true

		if stalled {
			return errPumpStalled
		}

		return nil
	}
}
