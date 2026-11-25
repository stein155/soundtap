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

// The user soundtapd runs as and the group it has besides its own: audio for the source
// bus FIFO (ADR 0066).
const (
	daemonUser  = "soundtapd"
	daemonGroup = "audio"
)

// The arguments each subcommand needs: the program and the subcommand, and for
// exec-soundtapd also the PATH it becomes.
const (
	argsSubcommand = 2
	argsExec       = 3
)

const (
	exitFailed = 1
	exitUsage  = 2
)

// stateDirMode lets soundtapd's group into rootd's directory and no one else.
const stateDirMode = 0o750

func main() {
	os.Exit(run())
}

// run is the command; it returns the process's exit code.
func run() int {
	if len(os.Args) < argsSubcommand {
		return usage()
	}

	switch os.Args[1] {
	case "serve":
		return serve()
	case "exec-soundtapd":
		if len(os.Args) < argsExec {
			return usage()
		}

		env := append(os.Environ(), rootd.EnvVar+"="+rootd.DefaultSocket)
		groups := []string{daemonGroup}
		err := childproc.ExecAs(daemonUser, groups, childproc.SoundtapdCaps, os.Args[2], os.Args[2:], env)
		fmt.Fprintln(os.Stderr, "soundtap-root: exec-soundtapd:", err)

		return exitFailed
	default:
		return usage()
	}
}

func usage() int {
	fmt.Fprintln(os.Stderr, "usage: soundtap-root serve | exec-soundtapd PATH [ARGS...]")

	return exitUsage
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

	err = os.MkdirAll(rootd.Dir, stateDirMode)
	if err != nil {
		log.Error("state directory", "err", err)

		return 1
	}

	listener, err := rootd.Listen(rootd.DefaultSocket, int(gid))
	if err != nil {
		log.Error("listen", "err", err)

		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Info("listening", "socket", rootd.DefaultSocket, "uid", uid)

	err = rootd.NewServer(log, int(uid), int(gid)).Serve(ctx, listener)
	if err != nil {
		log.Error("serve", "err", err)

		return 1
	}

	return 0
}
