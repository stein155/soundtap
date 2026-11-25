// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Channel is which input channel a speaker plays: both, as a stereo speaker, one of
// them on both outputs, as half of a stereo pair, or their mix on both.
type Channel int32

const (
	ChannelStereo Channel = iota
	ChannelLeft
	ChannelRight
	ChannelMono
)

func (c Channel) String() string {
	if c == ChannelLeft {
		return "left"
	}

	if c == ChannelRight {
		return "right"
	}

	if c == ChannelMono {
		return "mono"
	}

	return "stereo"
}

// errUnknownChannel is ParseChannel's answer to a name String does not give.
var errUnknownChannel = errors.New("unknown channel")

// ParseChannel reads the names String gives.
func ParseChannel(name string) (Channel, error) {
	for _, c := range []Channel{ChannelStereo, ChannelLeft, ChannelRight, ChannelMono} {
		if name == c.String() {
			return c, nil
		}
	}

	return ChannelStereo, fmt.Errorf("%w %q", errUnknownChannel, name)
}

// SetChannel picks the channel the sink gets. The multiroom tap keeps both.
func (m *Mixer) SetChannel(c Channel) { m.channel.Store(int32(c)) }

// applyChannel copies the selected channel over the other in each frame. A mono source
// has the same samples in both and so plays unchanged.
func (m *Mixer) applyChannel(pcm []byte) {
	from := 0
	channel := Channel(m.channel.Load())

	if channel == ChannelStereo {
		return
	}

	if channel == ChannelRight {
		from = bytesPerSamp
	}

	if channel == ChannelMono {
		// Half of each, so the mix cannot clip.
		for i := 0; i+frameBytes <= len(pcm); i += frameBytes {
			l := int16(binary.LittleEndian.Uint16(pcm[i:]))
			r := int16(binary.LittleEndian.Uint16(pcm[i+bytesPerSamp:]))
			v := uint16((int32(l) + int32(r)) >> 1)
			binary.LittleEndian.PutUint16(pcm[i:], v)
			binary.LittleEndian.PutUint16(pcm[i+bytesPerSamp:], v)
		}

		return
	}

	to := bytesPerSamp - from
	for i := 0; i+frameBytes <= len(pcm); i += frameBytes {
		copy(pcm[i+to:i+to+bytesPerSamp], pcm[i+from:i+from+bytesPerSamp])
	}
}
