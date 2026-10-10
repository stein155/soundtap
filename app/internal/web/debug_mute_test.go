// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import "testing"

// The debug bundle reports the line the daemon resolves, not a second rule of its own.
func TestDebugMuteGPIOAsksTheDaemon(t *testing.T) {
	s := &Server{}
	if got := s.debugMuteGPIO(); got != "<unknown: no resolver>" {
		t.Fatalf("no resolver: got %q", got)
	}
	s.ReconfigureForTest(func(o *Options) { o.Health.MuteLine = func() string { return "/sys/class/gpio/gpio561/value" } })
	if got := s.debugMuteGPIO(); got != "/sys/class/gpio/gpio561/value" {
		t.Fatalf("got %q", got)
	}
}
