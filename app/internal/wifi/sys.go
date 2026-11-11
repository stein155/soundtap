// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"context"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/safego"
)

// OS is the speaker's Sys: real commands, signals and interfaces.
type OS struct{}

// Run runs name to its end. Its own process group keeps a signal meant for soundtapd away
// from a network change halfway; a run past ctx gets SIGTERM, then SIGKILL. Every command
// here configures the network, so it gets the network capabilities soundtapd's own user
// holds (childproc.NetTool, ADR 0066); Start's daemons too.
func (OS) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	childproc.NetTool(cmd)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	return cmd.CombinedOutput()
}

// Start starts name in its own process group with soundtapd's stderr, which is the log, as
// its output, and reaps it when it ends. It outlives soundtapd: the daemons keep the
// network up across a restart of the daemon.
func (OS) Start(name string, args ...string) (int, <-chan struct{}, error) {
	// Never cancelled: the daemon is meant to stay.
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	childproc.NetTool(cmd)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	exited := make(chan struct{})
	safego.Go("wifi-reap", func() {
		_ = cmd.Wait()
		close(exited)
	})
	return cmd.Process.Pid, exited, nil
}

// Signal sends sig to pid.
func (OS) Signal(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// HasIPv4 reports whether iface holds an IPv4 address.
func (OS) HasIPv4(iface string) bool {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return false
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return true
		}
	}
	return false
}
