// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package abl is the wire format of the Bose link the SoundTouch adapter (lisa) speaks to its
// host product over UART0, as stock's abl.ko and ABLServer do (docs/hardware/lisa/README.md).
//
// The bus is one wire, half duplex, 19200 baud 8N2 to start; frames are told apart by idle
// gaps, not sync bytes. The adapter is the active master in room A and polls the rooms; the
// host product answers in its own. Every frame ends with a byte that makes the XOR of the
// whole frame zero.
//
// Master (bus.go) drives the bus as stock's master does, and Session (session.go) keeps the
// host's state on top of it. The master's timing from userspace (1.4 ms sync gaps, 0.4 ms
// reply slots) is worked out from stock and not proven on hardware. Nothing here builds a repository (port 16) message, a staging command, an
// ETAP test-mode (0x0f) or pedestal (0x21) command: those update or reconfigure the host.
package abl

import (
	"errors"
	"fmt"
)

// Master commands (frames with bit 7 clear): [cmd][dest room][arg][xor].
const (
	CmdPoll       = 0x00
	CmdOnOff      = 0x01 // arg 0x01 select me as the input, 0x80 off
	CmdAttenuate  = 0x02 // arg: the host class's volume or mute code
	CmdQueryInfo  = 0x0b // arg: QuerySpeakerType, QueryVersion, QuerySerial
	CmdKey        = 0x0d // arg: a key code; KeyPower wakes an old host
	CmdPowerMeOff = 0x10 // arg: delay in 100 ms units
)

// Command arguments.
const (
	ArgSelectMe      = 0x01
	ArgOff           = 0x80
	QuerySpeakerType = 0x10
	QueryVersion     = 0x12
	QuerySerial      = 0x13
	KeyPower         = 0x4c
	MuteInternal     = 0xf9
	UnmuteInternal   = 0xf8
	MuteUser         = 0xfd
	UnmuteUser       = 0xfe
)

// forbidden are master commands that update or reconfigure the host.
var forbidden = map[byte]string{0x0f: "ETAP test mode", 0x21: "pedestal comms"}

// ErrChecksum is a frame whose bytes do not XOR to zero.
var ErrChecksum = errors.New("abl: bad checksum")

// Checksum is the byte that makes the XOR of p and it zero.
func Checksum(p []byte) byte {
	var x byte
	for _, b := range p {
		x ^= b
	}
	return x
}

// MasterFrame is [cmd][room][arg][xor]. It refuses the commands that reconfigure the host.
func MasterFrame(cmd, room, arg byte) ([]byte, error) {
	if cmd&0x80 != 0 {
		return nil, fmt.Errorf("abl: 0x%02x is not a master command", cmd)
	}
	if why, ok := forbidden[cmd]; ok {
		return nil, fmt.Errorf("abl: command 0x%02x (%s) is never sent", cmd, why)
	}
	if room > 0x0e {
		return nil, fmt.Errorf("abl: room %d out of range", room)
	}
	f := []byte{cmd, room, arg}
	return append(f, Checksum(f)), nil
}

// PollFrame is the poll of room: [00][F0|room][xor].
func PollFrame(room byte) []byte {
	f := []byte{CmdPoll, 0xf0 | room&0x0f}
	return append(f, Checksum(f))
}

// PollReply is a client's answer to a poll: [80][zone<<4|room][mute<<7|vol][xor].
type PollReply struct {
	Zone, Room byte
	Muted      bool
	Volume     int // 0..99
}

// Source states a host reports in the zone nibble.
const (
	ZoneAdapter   = 0x0 // the adapter is selected (0x2 too)
	ZoneAdapter2  = 0x2
	ZoneHostLocal = 0xe // the host plays its own source
	ZoneOff       = 0xf
)

// ParsePollReply checks and decodes a poll reply.
func ParsePollReply(f []byte) (PollReply, error) {
	if len(f) != 4 || f[0] != 0x80 {
		return PollReply{}, fmt.Errorf("abl: not a poll reply: % x", f)
	}
	if Checksum(f) != 0 {
		return PollReply{}, ErrChecksum
	}
	return PollReply{Zone: f[1] >> 4, Room: f[1] & 0x0f, Muted: f[2]&0x80 != 0, Volume: int(f[2] & 0x7f)}, nil
}

// AdapterSelected reports whether the host's zone nibble says it plays the adapter.
func (r PollReply) AdapterSelected() bool { return r.Zone == ZoneAdapter || r.Zone == ZoneAdapter2 }

// InternetChecksum is the datagram layer's 16-bit one's-complement sum over big-endian words,
// an odd byte padded with zero.
func InternetChecksum(p []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(p); i += 2 {
		sum += uint32(p[i])<<8 | uint32(p[i+1])
	}
	if len(p)%2 == 1 {
		sum += uint32(p[len(p)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// P2P frames must not be a multiple of 14 bytes long: stock's ABLServer pads around a host
// firmware that crashes on them.
func P2PLengthOK(n int) bool { return n%14 != 0 }

// Host is the product the adapter hangs on.
type Host int

// Hosts, by stock's ABLServer classes.
const (
	HostUnknown Host = iota
	HostMarconi      // Lifestyle / VideoWave console (corey, otto)
	HostNelson       // Wave SoundTouch
	HostColin        // SA-4
	HostLuke         // SoundTouch Stereo JC
	HostTriode       // CineMate
)

func (h Host) String() string {
	return [...]string{"unknown", "marconi", "nelson", "colin", "luke", "triode"}[h]
}

// Product is the host's product name, as the adapter takes it.
func (h Host) Product() string {
	return [...]string{"", "Lifestyle / VideoWave", "Wave SoundTouch", "SoundTouch SA-4",
		"SoundTouch Stereo JC", "CineMate"}[h]
}

// HostFromDiscovery is the host a discovery announcement's device id names.
func HostFromDiscovery(id uint16) Host {
	switch id {
	case 0x1401, 0x1801:
		return HostMarconi
	case 0x30c0, 0x30a0:
		return HostNelson
	case 0x1c00:
		return HostTriode
	}
	return HostUnknown
}

// HostFromSpeakerType is an old host that does not announce itself, by its answer to
// CmdQueryInfo QuerySpeakerType.
func HostFromSpeakerType(t byte) Host {
	switch t {
	case 0x03:
		return HostColin
	case 0x11:
		return HostLuke
	}
	return HostUnknown
}

// AttenuationArg is CmdAttenuate's argument for volume 0..100 on host h, as stock maps it:
// the newer hosts take the volume (100 is sent as 99), the SA-4 an attenuation with a floor
// at volume 6, the Stereo JC a plain attenuation.
func AttenuationArg(h Host, volume int) byte {
	v := min(max(volume, 0), 100)
	switch h {
	case HostColin:
		return byte(100 - max(v, 6))
	case HostLuke:
		return byte(100 - v)
	default:
		return byte(min(v, 99))
	}
}

// OwnsInitialVolume reports whether the host keeps its own volume at power-on (stock
// productOwnsInitialVolume: false only for the SA-4).
func OwnsInitialVolume(h Host) bool { return h != HostColin }

// BaudFor is the link speed stock switches to for a host's capability flags: 460800 with bit
// 0x400, 230400 with 0x800, else the 19200 every host starts at (old hosts have no flags).
func BaudFor(capabilities uint16) int {
	switch {
	case capabilities&0x400 != 0:
		return 460800
	case capabilities&0x800 != 0:
		return 230400
	}
	return 19200
}
