// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"bytes"
	"testing"
)

func TestApplyChannel(t *testing.T) {
	t.Parallel()

	// Two frames, S16_LE: L=0x0201 R=0x0403, L=0x0605 R=0x0807.
	input := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	for channel, want := range map[Channel][]byte{
		ChannelStereo: {1, 2, 3, 4, 5, 6, 7, 8},
		ChannelLeft:   {1, 2, 1, 2, 5, 6, 5, 6},
		ChannelRight:  {3, 4, 3, 4, 7, 8, 7, 8},
		// (0x0201+0x0403)/2 = 0x0302, (0x0605+0x0807)/2 = 0x0706.
		ChannelMono: {2, 3, 2, 3, 6, 7, 6, 7},
	} {
		mixer := NewMixer(nil, false)
		mixer.SetChannel(channel)

		got := bytes.Clone(input)
		mixer.applyChannel(got)

		if !bytes.Equal(got, want) {
			t.Errorf("%s: %v, want %v", channel, got, want)
		}

		back, err := ParseChannel(channel.String())
		if err != nil || back != channel {
			t.Errorf("ParseChannel(%q) = %v, %v", channel.String(), back, err)
		}
	}
	// Full scale on both sides stays full scale; opposite signs cancel.
	mixer := NewMixer(nil, false)
	mixer.SetChannel(ChannelMono)

	loud := []byte{0xff, 0x7f, 0xff, 0x7f, 0x00, 0x80, 0xff, 0x7f}
	mixer.applyChannel(loud)

	if want := []byte{0xff, 0x7f, 0xff, 0x7f, 0xff, 0xff, 0xff, 0xff}; !bytes.Equal(loud, want) {
		t.Errorf("mono at full scale: %v, want %v", loud, want)
	}

	_, err := ParseChannel("centre")
	if err == nil {
		t.Error("an unknown channel parsed")
	}
}
