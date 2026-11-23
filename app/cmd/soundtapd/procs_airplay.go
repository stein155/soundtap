// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// shairport-sync registers with nqptp over its UDP control port once at startup and
// silently falls back to Classic AirPlay when nothing answers. nqptp only creates its
// /dev/shm interface when that registration arrives, so the port is the readiness signal.
var (
	procNetUDP       = "/proc/net/udp"
	nqptpControlPort = 9000
	nqptpWait        = 20 * time.Second
)

// udpListening reports whether a socket in a /proc/net/udp table is bound to port.
func udpListening(table string, port int) bool {
	b, err := os.ReadFile(table)
	if err != nil {
		return false
	}
	want := fmt.Sprintf(":%04X", port)
	for _, line := range strings.Split(string(b), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 1 && strings.HasSuffix(f[1], want) {
			return true
		}
	}
	return false
}

var (
	procNetRoute       = "/proc/net/route"
	librespotRouteWait = 60 * time.Second
)

// hasDefaultRoute reports whether a /proc/net/route table holds an IPv4 default route.
func hasDefaultRoute(table string) bool {
	b, err := os.ReadFile(table)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 1 && f[1] == "00000000" {
			return true
		}
	}
	return false
}

// waitUntil polls ready; false once max has passed or ctx ends.
func waitUntil(ctx context.Context, max time.Duration, ready func() bool) bool { //nolint:revive,predeclared // moved unchanged
	deadline := time.Now().Add(max)
	for {
		if ready() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func shairportArgs(conf, name string) []string {
	return []string{"-c", conf, "-a", name}
}

const (
	shairportBaseConf = "/etc/shairport-sync.conf"
	shairportRunConf  = "/run/soundtap/shairport-sync.conf"
)

// shairportConf writes base with the AirPlay device ID pinned to the speaker's device ID
// and returns its path; on any failure it returns base. Unpinned, shairport-sync takes the
// MAC of the first interface it finds. On the ST20 that is bcolink0, whose MAC is new
// every boot, so Music Assistant and iOS saw a new AirPlay speaker after each reboot. On
// the ST10 and WLA the device ID is that same MAC, so their AirPlay ID stays as it was.
func shairportConf(base, out, deviceID string, log *slog.Logger) string {
	id, err := strconv.ParseUint(deviceID, 16, 48)
	if err != nil || len(deviceID) != 12 {
		log.Warn("airplay device id not pinned", "device_id", deviceID)
		return base
	}
	b, err := os.ReadFile(base)
	if err == nil && !strings.Contains(string(b), "general = {") {
		err = fmt.Errorf("no general section")
	}
	if err == nil {
		pinned := strings.Replace(string(b), "general = {", fmt.Sprintf("general = {\n\tairplay_device_id = 0x%012XL;", id), 1)
		err = os.WriteFile(out, []byte(pinned), 0o644) //nolint:gosec // moved unchanged
	}
	if err != nil {
		log.Warn("airplay device id not pinned", "err", err)
		return base
	}
	return out
}

// spotifyStateDir holds spotify-source.sh's pause stamp: /run/soundtap is root's.
// spotifyDir and spotifySystemDir are in home.go.
const spotifyStateDir = "/run/soundtap/spotify"

// ownTree creates dir if needed and hands it and everything under it to the system user
// name, so a daemon that dropped root can still use the files an older build left behind.
func ownTree(dir, name string, log *slog.Logger) {
	uid, gid, err := childproc.Lookup(name)
	if err != nil {
		return // supervise refuses to start the daemon and says why
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Warn("mkdir", "path", dir, "err", err)
		return
	}
	if !mayChown() {
		return // init.d/soundtap hands the trees over at boot (ADR 0066)
	}
	err = filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, int(uid), int(gid)) //nolint:gosec // moved unchanged
	})
	if err != nil {
		log.Warn("chown", "path", dir, "user", name, "err", err)
	}
}

// securePath tightens path's mode if it exists. Older builds left the credentials
// world-readable.
func securePath(path string, mode os.FileMode, log *slog.Logger) {
	if !mayChown() {
		return // the files are librespot's user's; init.d/soundtap tightens them
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() == mode {
		return
	}
	if err := os.Chmod(path, mode); err != nil {
		log.Warn("chmod", "path", path, "err", err)
	}
}
