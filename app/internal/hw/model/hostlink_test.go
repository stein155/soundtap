// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package model

import (
	"os"
	"strings"
	"testing"
)

// TestAdapterLinkDevice holds the SoundTouch adapter's Bose link to the UART0 of its board,
// and off the BCO's UART: on every SCM tree /dev/ttyS0 is UART5, where the BCO's loaders
// listen, so the SCM adapter's tree names UART0 serial1 and soundtapd opens /dev/ttyS1.
func TestAdapterLinkDevice(t *testing.T) {
	for variant, want := range map[string]string{"lisa2": "/dev/ttyS0", "lisa": "/dev/ttyS1"} {
		m, err := forVariants([]string{variant})
		if err != nil {
			t.Fatal(err)
		}

		if !m.HostLink || m.HostLinkDev != want {
			t.Errorf("%s: host link %v on %q, want %q", variant, m.HostLink, m.HostLinkDev, want)
		}
	}

	for name, profile := range profiles {
		if m := profile(); m.HostLink || m.HostLinkDev != "" {
			t.Errorf("%s has a host link on %q", name, m.HostLinkDev)
		}
	}

	const dts = "../../../../os/board/common/dts/ti/omap/"

	scm, err := os.ReadFile(dts + "am335x-soundtap-lisa-scm.dts")
	if err != nil {
		t.Fatal(err)
	}

	st20, err := os.ReadFile(dts + "am335x-soundtap-st20.dts")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(st20), "serial0 = &uart5;") {
		t.Fatal("the ST20 tree no longer names the BCO's UART5 serial0: check the adapter's alias")
	}

	if !strings.Contains(string(scm), "serial1 = &uart0;") {
		t.Error("the SCM adapter's tree does not name UART0 serial1: it would take /dev/ttyS0 from the BCO")
	}
}
