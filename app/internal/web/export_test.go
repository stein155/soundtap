// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"fmt"
	"net/http/httptest"

	"github.com/stein155/soundtap/app/internal/multiroom"
)

// Test hook. The support bundle tails absolute paths under /tmp, so the redaction test
// has to redirect them: pointing it at the real /tmp/soundtap.log made it skip whenever
// that file existed, which is always on a device and often when suites run in parallel.
func SetLogFilesForTest(name, path string) func() {
	saved := logFiles
	logFiles = []struct{ name, path string }{{name, path}}

	return func() { logFiles = saved }
}

// SetSystemRootForTest roots the system paths the support bundle reads (/tmp, /run,
// /mnt/nv) in a fake tree.
func SetSystemRootForTest(root string) func() {
	saved := sysRoot
	sysRoot = root

	return func() { sysRoot = saved }
}

// SetBluetoothPresentForTest fakes whether the kernel has a Bluetooth adapter.
func SetBluetoothPresentForTest(present bool) func() {
	saved := bluetoothPresent
	bluetoothPresent = func() bool { return present }

	return func() { bluetoothPresent = saved }
}

// SetOSVersionFileForTest points /api/version at another os-version file.
func SetOSVersionFileForTest(path string) func() {
	saved := osVersionFile
	osVersionFile = path

	return func() { osVersionFile = saved }
}

// RoutesForTest lists the patterns the server's Handler registers.
func (s *Server) RoutesForTest() []string {
	s.Handler()

	return append([]string(nil), s.routes...)
}

// LogFilePathsForTest lists the logs the support bundle of a speaker with home tails.
func LogFilePathsForTest(home string) []string {
	srv := new(Server)
	srv.homeDir = home
	logs := srv.logPaths()
	paths := make([]string, 0, len(logs))

	for _, lg := range logs {
		paths = append(paths, lg.path)
	}

	return paths
}

// SetWifiFallbackFileForTest points the fallback notice at another file.
func SetWifiFallbackFileForTest(path string) func() {
	saved := wifiFallbackFile
	wifiFallbackFile = path

	return func() { wifiFallbackFile = saved }
}

// SetWifiJoinFailedFileForTest points /api/wifi/saved at another join-failure file.
func SetWifiJoinFailedFileForTest(path string) func() {
	saved := wifiJoinFailedFile
	wifiJoinFailedFile = path

	return func() { wifiJoinFailedFile = saved }
}

// NeedsPressForTest reports whether a request to method and path waits for a press on the speaker.
func NeedsPressForTest(method, target string) bool {
	return needsPress(httptest.NewRequestWithContext(context.Background(), method, target, nil))
}

// The restore route and its body limits, for the backup size test.
const (
	RestorePath    = restorePath
	MaxRequestBody = maxRequestBody
	BackupMaxBytes = backupMaxBytes
)

// setFollow writes the follow file the way the multiroom Group does, for tests that
// start from a speaker already in a group.
func (s *Server) setFollow(host, id string) error {
	err := multiroom.WriteFollow(s.multiroomServerPath(), host, id)
	if err != nil {
		return fmt.Errorf("write follow: %w", err)
	}

	return nil
}

// ReconfigureForTest changes the Options the server was built from: fn edits them and the
// server takes them over again, as New would have. Tests use it to give a server built by
// a shared helper the hooks one test needs.
func (s *Server) ReconfigureForTest(fn func(*Options)) {
	o := s.opts
	fn(&o)
	s.configure(&o)
}

// Attached is every group of hooks handed over after New.
type Attached = attached

// AttachForTest changes the groups attached after New: fn edits them and the server takes
// every group over again, as the Attach calls would have.
func (s *Server) AttachForTest(fn func(*Attached)) {
	a := s.attached
	fn(&a)
	s.AttachAudio(a.Audio)
	s.AttachSpotify(a.Spotify)
	s.AttachSoundbar(a.Soundbar)
	s.AttachUPnP(a.UPnP)
}
