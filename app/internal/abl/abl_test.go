// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package abl

import (
	"bytes"
	"errors"
	"testing"
)

func TestFramesXORToZero(t *testing.T) {
	f, err := MasterFrame(CmdOnOff, 3, ArgSelectMe)
	if err != nil || Checksum(f) != 0 || !bytes.Equal(f[:3], []byte{0x01, 0x03, 0x01}) {
		t.Fatalf("frame % x, %v", f, err)
	}
	if p := PollFrame(5); !bytes.Equal(p, []byte{0x00, 0xf5, 0xf5}) {
		t.Fatalf("poll % x", p)
	}
}

func TestReconfiguringCommandsAreRefused(t *testing.T) {
	for _, cmd := range []byte{0x0f, 0x21, 0x80} {
		if _, err := MasterFrame(cmd, 1, 0); err == nil {
			t.Fatalf("command 0x%02x was built", cmd)
		}
	}
}

func TestPollReply(t *testing.T) {
	f := []byte{0x80, 0x23, 0x80 | 42}
	f = append(f, Checksum(f))
	r, err := ParsePollReply(f)
	if err != nil || r.Zone != 2 || r.Room != 3 || !r.Muted || r.Volume != 42 || !r.AdapterSelected() {
		t.Fatalf("reply %+v, %v", r, err)
	}
	f[2] ^= 1
	if _, err := ParsePollReply(f); !errors.Is(err, ErrChecksum) {
		t.Fatalf("corrupt reply: %v", err)
	}
}

func TestInternetChecksum(t *testing.T) {
	// RFC 1071's example words 0001 f203 f4f5 f6f7 sum to ddf2; the checksum is its complement.
	if c := InternetChecksum([]byte{0x00, 0x01, 0xf2, 0x03, 0xf4, 0xf5, 0xf6, 0xf7}); c != ^uint16(0xddf2) {
		t.Fatalf("checksum %04x", c)
	}
}

func TestHostsAndVolumes(t *testing.T) {
	if HostFromDiscovery(0x30c0) != HostNelson || HostFromDiscovery(0x1c00) != HostTriode ||
		HostFromDiscovery(0x1801) != HostMarconi || HostFromSpeakerType(0x11) != HostLuke ||
		HostFromSpeakerType(3) != HostColin || HostFromDiscovery(0x1234) != HostUnknown {
		t.Fatal("host classification")
	}
	for _, c := range []struct {
		h    Host
		v    int
		want byte
	}{{HostNelson, 100, 99}, {HostNelson, 40, 40}, {HostColin, 2, 94}, {HostColin, 50, 50}, {HostLuke, 30, 70}} {
		if got := AttenuationArg(c.h, c.v); got != c.want {
			t.Fatalf("%v volume %d -> %d, want %d", c.h, c.v, got, c.want)
		}
	}
	if BaudFor(0x0003) != 19200 || BaudFor(0x0e0f) != 460800 || BaudFor(0x0400) != 460800 || BaudFor(0x0800) != 230400 {
		t.Fatal("baud")
	}
	if OwnsInitialVolume(HostColin) || !OwnsInitialVolume(HostLuke) {
		t.Fatal("initial volume ownership")
	}
	if P2PLengthOK(28) || !P2PLengthOK(27) {
		t.Fatal("P2P length rule")
	}
}
