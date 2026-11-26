// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"encoding/binary"
	"testing"
)

// decodeEvent is the firmware-derived wire format: 1 byte/event, 'A'+i = one edge,
// 'a'+i = the other, i = matrix index. pressIsLower=true ⇒ lowercase is press.
func TestDecodeEvent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		b       byte
		index   int
		pressed bool
		ok      bool
	}{
		{'a', 0, true, true},   // key 0 press
		{'A', 0, false, true},  // key 0 release
		{'g', 6, true, true},   // key 6 press (preset 1)
		{'J', 9, false, true},  // key 9 release
		{'p', 15, true, true},  // last matrix cell
		{'0', 0, false, false}, // non-letter → not an event
		{' ', 0, false, false},
	}
	for _, testCase := range cases {
		idx, pressed, ok := decodeEvent(testCase.b)
		if ok != testCase.ok || (ok && (idx != testCase.index || pressed != testCase.pressed)) {
			t.Errorf("decodeEvent(%q) = (%d,%v,%v); want (%d,%v,%v)",
				testCase.b, idx, pressed, ok, testCase.index, testCase.pressed, testCase.ok)
		}
	}
}

func TestDecodeInputEvent(t *testing.T) {
	t.Parallel()

	event := make([]byte, 16)
	binary.LittleEndian.PutUint16(event[8:10], 1)  // EV_KEY
	binary.LittleEndian.PutUint16(event[10:12], 2) // KEY_1
	binary.LittleEndian.PutUint32(event[12:16], 1) // press

	if code, pressed, ok := decodeInputEvent(event); !ok || !pressed || code != 2 {
		t.Fatalf("press = (%d,%v,%v)", code, pressed, ok)
	}

	binary.LittleEndian.PutUint32(event[12:16], 2) // autorepeat

	if _, _, ok := decodeInputEvent(event); ok {
		t.Fatal("autorepeat accepted")
	}
}

func TestLoadKeymapOverride(t *testing.T) {
	t.Parallel()

	// no override file → defaults intact
	keymap := LoadKeymap("")
	if keymap[0] != "VOLUME_UP" || keymap[6] != "PRESET_1" || keymap[10] != "PRESET_4" ||
		keymap[12] != "SOURCE_TOGGLE" {
		t.Fatalf("defaults wrong: %v", keymap)
	}
}
