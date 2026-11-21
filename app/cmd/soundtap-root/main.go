// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Command soundtap-root is the root helper soundtapd asks for the few things its own user
// may not do, and the step that starts soundtapd as that user (ADR 0066).
//
//	soundtap-root serve                     answer soundtapd on internal/rootd's socket
//	soundtap-root exec-soundtapd PATH ARGS  become soundtapd, with its capabilities, and run PATH
//
// init.d/soundtap starts both as root. Neither takes a path or a command from soundtapd.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/rootd"
)

// The user soundtapd runs as and the groups it has besides its own: audio for the source
// bus FIFO (ADR 0066).
const (
	daemonUser = "soundtapd"
)

var daemonGroups = []string{"audio"}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(serve())
	case "exec-soundtapd":
		if len(os.Args) < 3 {
			usage()
		}
		env := append(os.Environ(), rootd.EnvVar+"="+rootd.DefaultSocket)
		err := childproc.ExecAs(daemonUser, daemonGroups, childproc.SoundtapdCaps, os.Args[2], os.Args[2:], env)
		fmt.Fprintln(os.Stderr, "soundtap-root: exec-soundtapd:", err)
		os.Exit(1)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: soundtap-root serve | exec-soundtapd PATH [ARGS...]")
	os.Exit(2)
}

func serve() int {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("comp", "soundtap-root")
	if os.Geteuid() != 0 {
		log.Error("must run as root")
		return 1
	}
	uid, gid, err := childproc.Lookup(daemonUser)
	if err != nil {
		log.Error("no soundtapd user", "err", err)
		return 1
	}
	if err := os.MkdirAll(rootd.Dir, 0o750); err != nil {
		log.Error("state directory", "err", err)
		return 1
	}
	l, err := rootd.Listen(rootd.DefaultSocket, int(gid))
	if err != nil {
		log.Error("listen", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log.Info("listening", "socket", rootd.DefaultSocket, "uid", uid)
	if err := rootd.NewServer(log, int(uid), int(gid)).Serve(ctx, l); err != nil {
		log.Error("serve", "err", err)
		return 1
	}
	return 0
}
