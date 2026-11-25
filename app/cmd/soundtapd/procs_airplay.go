// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
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
const (
	procNetUDP       = "/proc/net/udp"
	nqptpControlPort = 9000
	nqptpWait        = 20 * time.Second
)

// udpListening reports whether a socket in a /proc/net/udp table is bound to port.
func udpListening(table string, port int) bool {
	udpTable, err := os.ReadFile(table)
	if err != nil {
		return false
	}

	want := fmt.Sprintf(":%04X", port)
	for _, line := range strings.Split(string(udpTable), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 1 && strings.HasSuffix(f[1], want) {
			return true
		}
	}

	return false
}

const (
	procNetRoute       = "/proc/net/route"
	librespotRouteWait = 60 * time.Second
)

// hasDefaultRoute reports whether a /proc/net/route table holds an IPv4 default route.
func hasDefaultRoute(table string) bool {
	routes, err := os.ReadFile(table)
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(routes), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 1 && f[1] == "00000000" {
			return true
		}
	}

	return false
}

// waitPoll is how often waitUntil asks again.
const waitPoll = 100 * time.Millisecond

// waitUntil polls ready; false once limit has passed or ctx ends.
func waitUntil(ctx context.Context, limit time.Duration, ready func() bool) bool {
	deadline := time.Now().Add(limit)

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
		case <-time.After(waitPoll):
		}
	}
}

func shairportArgs(conf, name string) []string {
	return []string{"-c", conf, "-a", name}
}

const (
	shairportBaseConf = "/etc/shairport-sync.conf"
	shairportRunConf  = "/run/soundtap/shairport-sync.conf"
	// shairportConfMode lets shairport-sync's user read the pinned copy.
	shairportConfMode = 0o644
	// airplayDeviceIDDigits is a MAC in hex: 12 digits, 48 bits.
	airplayDeviceIDDigits = 12
	airplayDeviceIDBits   = 48
)

// errShairportNoGeneral is a base configuration without the section the device ID goes in.
var errShairportNoGeneral = errors.New("no general section")

// shairportConf writes base with the AirPlay device ID pinned to the speaker's device ID
// and returns its path; on any failure it returns base. Unpinned, shairport-sync takes the
// MAC of the first interface it finds. On the ST20 that is bcolink0, whose MAC is new
// every boot, so Music Assistant and iOS saw a new AirPlay speaker after each reboot. On
// the ST10 and WLA the device ID is that same MAC, so their AirPlay ID stays as it was.
func shairportConf(base, out, deviceID string, log *slog.Logger) string {
	deviceNumber, err := strconv.ParseUint(deviceID, 16, airplayDeviceIDBits)
	if err != nil || len(deviceID) != airplayDeviceIDDigits {
		log.Warn("airplay device id not pinned", "device_id", deviceID)

		return base
	}

	conf, err := os.ReadFile(base)
	if err == nil && !strings.Contains(string(conf), "general = {") {
		err = errShairportNoGeneral
	}

	if err == nil {
		pin := fmt.Sprintf("general = {\n\tairplay_device_id = 0x%012XL;", deviceNumber)
		pinned := strings.Replace(string(conf), "general = {", pin, 1)
		err = os.WriteFile(out, []byte(pinned), shairportConfMode) //nolint:gosec // moved unchanged
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

// ownTreeMode keeps a daemon's tree to its own user.
const ownTreeMode = 0o700

// ownTree creates dir if needed and hands it and everything under it to the system user
// name, so a daemon that dropped root can still use the files an older build left behind.
func ownTree(dir, name string, log *slog.Logger) {
	uid, gid, err := childproc.Lookup(name)
	if err != nil {
		return // supervise refuses to start the daemon and says why
	}

	mkdirErr := os.MkdirAll(dir, ownTreeMode)
	if mkdirErr != nil {
		log.Warn("mkdir", "path", dir, "err", mkdirErr)

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

	err = os.Chmod(path, mode)
	if err != nil {
		log.Warn("chmod", "path", path, "err", err)
	}
}
