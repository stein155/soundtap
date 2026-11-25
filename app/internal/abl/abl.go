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
// reply slots) is worked out from stock and not proven on hardware. Nothing here builds a
// repository (port 16) message, a staging command, an ETAP test-mode (0x0f) or pedestal
// (0x21) command: those update or reconfigure the host.
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

// Master commands that update or reconfigure the host: never sent.
const (
	cmdETAPTestMode = 0x0f
	cmdPedestal     = 0x21
)

// Frame layout: the first byte's top bit marks a client frame; the second byte holds the
// zone (or the poll marker) above the room.
const (
	clientBit    = 0x80
	roomMask     = 0x0f
	nibbleBits   = 4
	pollRoomBits = 0xf0
	// maxRoom is the highest room a master command addresses.
	maxRoom = 0x0e
	// masterFrameLen is a master command's frame: [cmd][room][arg][xor].
	masterFrameLen = 4
	// pollReplyLen is a poll reply's frame: [80][zone<<4|room][mute<<7|vol][xor].
	pollReplyLen = 4
	muteBit      = 0x80
	volumeMask   = 0x7f
	byteBits     = 8
	byteMask     = 0xff
)

// ErrChecksum is a frame whose bytes do not XOR to zero.
var ErrChecksum = errors.New("abl: bad checksum")

// Errors of the frame builders and parsers; each message goes on with the frame's details.
var (
	errNotMaster    = errors.New("is not a master command")
	errNeverSent    = errors.New("is never sent")
	errRoomRange    = errors.New("out of range")
	errNotPollReply = errors.New("abl: not a poll reply")
)

// forbiddenReason says why a master command that updates or reconfigures the host is never
// sent, and whether cmd is one.
func forbiddenReason(cmd byte) (string, bool) {
	switch cmd {
	case cmdETAPTestMode:
		return "ETAP test mode", true
	case cmdPedestal:
		return "pedestal comms", true
	default:
		return "", false
	}
}

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
	if cmd&clientBit != 0 {
		return nil, fmt.Errorf("abl: 0x%02x %w", cmd, errNotMaster)
	}

	if why, ok := forbiddenReason(cmd); ok {
		return nil, fmt.Errorf("abl: command 0x%02x (%s) %w", cmd, why, errNeverSent)
	}

	if room > maxRoom {
		return nil, fmt.Errorf("abl: room %d %w", room, errRoomRange)
	}

	frame := []byte{cmd, room, arg, 0}
	frame[masterFrameLen-1] = Checksum(frame[:masterFrameLen-1])

	return frame, nil
}

// PollFrame is the poll of room: [00][F0|room][xor].
func PollFrame(room byte) []byte {
	frame := []byte{CmdPoll, pollRoomBits | room&roomMask, 0}
	frame[2] = Checksum(frame[:2])

	return frame
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
func ParsePollReply(frame []byte) (PollReply, error) {
	if len(frame) != pollReplyLen || frame[0] != clientPollReply {
		return PollReply{Zone: 0, Room: 0, Muted: false, Volume: 0}, fmt.Errorf("%w: % x", errNotPollReply, frame)
	}

	if Checksum(frame) != 0 {
		return PollReply{Zone: 0, Room: 0, Muted: false, Volume: 0}, ErrChecksum
	}

	return PollReply{
		Zone:   frame[1] >> nibbleBits,
		Room:   frame[1] & roomMask,
		Muted:  frame[2]&muteBit != 0,
		Volume: int(frame[2] & volumeMask),
	}, nil
}

// AdapterSelected reports whether the host's zone nibble says it plays the adapter.
func (r PollReply) AdapterSelected() bool { return r.Zone == ZoneAdapter || r.Zone == ZoneAdapter2 }

// InternetChecksum is the datagram layer's 16-bit one's-complement sum over big-endian words,
// an odd byte padded with zero.
func InternetChecksum(data []byte) uint16 {
	const wordBits, wordMask = 16, 0xffff

	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(data[i])<<byteBits | uint32(data[i+1])
	}

	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << byteBits
	}

	for sum>>wordBits != 0 {
		sum = sum&wordMask + sum>>wordBits
	}

	return ^uint16(sum)
}

// p2pBadMultiple is the length a P2P frame must not be a multiple of.
const p2pBadMultiple = 14

// P2PLengthOK reports whether a P2P frame may be n bytes long: not a multiple of 14 bytes,
// as stock's ABLServer pads around a host firmware that crashes on them.
func P2PLengthOK(n int) bool { return n%p2pBadMultiple != 0 }

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
	return [...]string{
		"", "Lifestyle / VideoWave", "Wave SoundTouch", "SoundTouch SA-4",
		"SoundTouch Stereo JC", "CineMate",
	}[h]
}

// The device ids hosts announce themselves with.
const (
	idMarconi  = 0x1401
	idMarconi2 = 0x1801
	idNelson   = 0x30c0
	idNelson2  = 0x30a0
	idTriode   = 0x1c00
)

// The speaker types old hosts answer QuerySpeakerType with.
const (
	speakerTypeColin = 0x03
	speakerTypeLuke  = 0x11
)

// HostFromDiscovery is the host a discovery announcement's device id names.
func HostFromDiscovery(id uint16) Host {
	switch id {
	case idMarconi, idMarconi2:
		return HostMarconi
	case idNelson, idNelson2:
		return HostNelson
	case idTriode:
		return HostTriode
	default:
		return HostUnknown
	}
}

// HostFromSpeakerType is an old host that does not announce itself, by its answer to
// CmdQueryInfo QuerySpeakerType.
func HostFromSpeakerType(speakerType byte) Host {
	switch speakerType {
	case speakerTypeColin:
		return HostColin
	case speakerTypeLuke:
		return HostLuke
	default:
		return HostUnknown
	}
}

// The volume scale: SoundTap's 0..100, the most the newer hosts are sent, and the SA-4's
// floor.
const (
	volumeMax        = 100
	newerVolumeMax   = 99
	colinVolumeFloor = 6
)

// AttenuationArg is CmdAttenuate's argument for volume 0..100 on host h, as stock maps it:
// the newer hosts take the volume (100 is sent as 99), the SA-4 an attenuation with a floor
// at volume 6, the Stereo JC a plain attenuation.
func AttenuationArg(host Host, volume int) byte {
	level := min(max(volume, 0), volumeMax)

	if host == HostColin {
		return byte(volumeMax - max(level, colinVolumeFloor))
	}

	if host == HostLuke {
		return byte(volumeMax - level)
	}

	return byte(min(level, newerVolumeMax))
}

// OwnsInitialVolume reports whether the host keeps its own volume at power-on (stock
// productOwnsInitialVolume: false only for the SA-4).
func OwnsInitialVolume(h Host) bool { return h != HostColin }

// BaudFor is the link speed stock switches to for a host's capability flags: 460800 with bit
// 0x400, 230400 with 0x800, else the 19200 every host starts at (old hosts have no flags).
func BaudFor(capabilities uint16) int {
	const (
		capFast, capMedium              = 0x400, 0x800
		baudFast, baudMedium, baudStart = 460800, 230400, 19200
	)

	switch {
	case capabilities&capFast != 0:
		return baudFast
	case capabilities&capMedium != 0:
		return baudMedium
	default:
		return baudStart
	}
}
