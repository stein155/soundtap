// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"bufio"
	"encoding/binary"
	"maps"
	"os"
	"strconv"
	"strings"
)

// KeySourceToggle is the source key: AUX and Bluetooth on a tap, Bluetooth on a hold.
const KeySourceToggle = "SOURCE_TOGGLE"

// keyBluetooth is the Wireless Link's Bluetooth button, named as the source it selects.
const keyBluetooth = "BLUETOOTH"

// pressIsLower: the stock keypad driver reports a press as a lowercase letter.
const pressIsLower = true

// defaultKeymap is the stock ST10 Rhino jump table. /mnt/nv/buttons.map overrides it.
var defaultKeymap = map[int]string{
	0: "VOLUME_UP", 1: "PRESET_3", 2: "POWER", //nolint:goconst // key table
	4: "VOLUME_DOWN", 5: "PRESET_2", 6: "PRESET_1", //nolint:goconst // key table
	8: "PRESET_6", 9: "PRESET_5", 10: "PRESET_4", //nolint:goconst // key table
	12: KeySourceToggle,
}

var inputKeymap = map[uint16]string{
	2: "PRESET_1", 3: "PRESET_2", 4: "PRESET_3",
	5: "PRESET_4", 6: "PRESET_5", 7: "PRESET_6",
	114: "VOLUME_DOWN", 115: "VOLUME_UP", 116: "POWER",
	390: KeySourceToggle, // KEY_AUX
	237: keyBluetooth,    // KEY_BLUETOOTH, the Wireless Link's button
}

func decodeInputEvent(event []byte) (code uint16, pressed, ok bool) {
	if len(event) != 16 || binary.LittleEndian.Uint16(event[8:10]) != 1 {
		return 0, false, false
	}

	code = binary.LittleEndian.Uint16(event[10:12])
	value := int32(binary.LittleEndian.Uint32(event[12:16])) //nolint:gosec // G115: the kernel event value is -1, 0 or 1
	_, mapped := inputKeymap[code]

	return code, value == 1, mapped && (value == 0 || value == 1)
}

// The ST10 keypad under stock Linux is /dev/shelby-keypad (char 243:0, DEVNAME shelby-keypad
// — top-level, no snd/ prefix, unlike the DAC).
//
// WIRE FORMAT (reverse-engineered from shelby-keypad.ko, not guessed):
//
//	keypad_read() streams ONE byte per event: put_user(fifo_scancode + 0x41).
//	process_event() writes either `index` or `index|0x20` into the fifo.
//	⇒ each event byte is an ASCII letter:
//	     'A'+index  (uppercase, bit5 clear)  — one edge
//	     'a'+index  (lowercase, bit5 set)     — the other edge
//	   index = physical key position in the ST10 3x3 matrix plus direct source key.
//
// So it is NOT the old "(code,state)" 2-byte framing — it is 1 byte/event, and press vs
// release is the letter case. From the driver state machine (state0 press-detect emits
// index|0x20; state2 emits index) lowercase = PRESS, uppercase = RELEASE — this polarity is
// confirmed by the stock kernel and Rhino translation table. Press-and-hold is timed in
// soundtapd, because the driver emits only press/release edges.
//
// decodeEvent maps one keypad byte to (matrix index, pressed?). ok=false for non-letters.
func decodeEvent(b byte) (index int, pressed, ok bool) {
	switch {
	case b >= 'a' && b <= 'z':
		return int(b - 'a'), pressIsLower, true
	case b >= 'A' && b <= 'Z':
		return int(b - 'A'), !pressIsLower, true
	default:
		return 0, false, false
	}
}

// LoadKeymap is the keypad's scancode -> key map: the stock table with the lines of path
// (`<scancode> = <KEY>`) over it.
func LoadKeymap(path string) map[int]string {
	m := map[int]string{}
	maps.Copy(m, defaultKeymap)

	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if k, v, ok := strings.Cut(line, "="); ok {
			if idx, err := strconv.Atoi(strings.TrimSpace(k)); err == nil {
				m[idx] = strings.TrimSpace(v)
			}
		}
	}

	return m
}
