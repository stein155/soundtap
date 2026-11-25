// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/web"
)

// soundtapdStarts counts soundtapd's starts in this boot; /run is cleared by a reboot.
//
//nolint:gochecknoglobals // a test seam: tests count starts in a temp dir
var soundtapdStarts = "/run/soundtap/soundtapd-starts"

// countSoundtapdStart records this start and sets the restarts GET /api/health reports
// (ADR 0127): the init script's soundtapd_loop starts it again after every exit.
func countSoundtapdStart(log *slog.Logger) {
	if !onFirmware() {
		return // a computer's /run is not a speaker's
	}

	n, err := counters.CountStart(soundtapdStarts)
	if err != nil {
		log.Warn("count soundtapd start", "err", err)
	}

	counters.SoundtapdRestarts.Store(n)
}

// pprofAddr is where an alpha image serves Go's profiler: loopback only, so it is reached
// over the alpha image's SSH (ssh -L 6060:127.0.0.1:6060 root@<speaker>) and never from
// the network (ADR 0127). A production image has no SSH and serves none.
//
//nolint:gochecknoglobals // a test seam: tests listen on a free port
var pprofAddr = "127.0.0.1:6060"

// profileFile names the image's build profile; a test points it elsewhere.
//
//nolint:gochecknoglobals // a test seam: tests point it at a profile in a temp dir
var profileFile = web.ProfileFile

// pprofWanted reports whether the image's build profile (web.ProfileFile, which
// post-build.sh writes into the read-only rootfs) is alpha.
func pprofWanted(profileFile string) bool {
	b, err := os.ReadFile(profileFile)

	return err == nil && strings.TrimSpace(string(b)) == "alpha"
}

// pprofHandler is net/http/pprof on a mux of its own: the package also registers on
// http.DefaultServeMux, which nothing in soundtapd serves.
func pprofHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

// pprofHeaderTimeout bounds a request's headers on the profiler's port.
const pprofHeaderTimeout = 5 * time.Second

// startPprof serves the profiler on pprofAddr until ctx ends, on an alpha image only, and
// returns the address it listens on ("" for none). It is optional: a port in use is logged
// and the speaker runs on without it.
func startPprof(ctx context.Context, log *slog.Logger) string {
	if !onFirmware() || !pprofWanted(profileFile) {
		return ""
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", pprofAddr)
	if err != nil {
		log.Warn("pprof not served", "addr", pprofAddr, "err", err)

		return ""
	}
	// A CPU profile or trace runs for its ?seconds=, so no write deadline.
	srv := &http.Server{Handler: pprofHandler(), ReadHeaderTimeout: pprofHeaderTimeout}

	log.Info("pprof on loopback (alpha image)", "addr", listener.Addr().String())
	goSafe("pprof", func() {
		err := srv.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("pprof stopped", "err", err)
		}
	})
	goSafe("pprofShutdown", func() {
		<-ctx.Done()

		_ = srv.Close()
	})

	return listener.Addr().String()
}
