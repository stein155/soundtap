// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package abl

// Remote control (datagram port 18), as stock's ABLServer speaks it to a Wave SoundTouch or
// a CineMate (docs/hardware/lisa/README.md). Every message is a 4-byte header
// [0x02][0x01][0x04][source id 0] and a body [type][0x01][...], sent to the broadcast room.
const (
	rcSourceSelection = 0x01
	rcKeyRegistration = 0x02
	rcCommand         = 0x03
	rcState           = 0x05
	rcDone            = 0x07
	rcBootDelay       = 0x0b
	rcActivateDone    = 0x0c

	rcActivate   = 0x80
	rcDeactivate = 0x40

	// rcBody is the byte after every body's type.
	rcBody = 0x01
	// rcMarker is the low nibble of a remote-control message's first byte.
	rcMarker = 0x02
	// rcTypeMask takes the type from a body's first byte.
	rcTypeMask = 0x0f
	// rcMinLen is the shortest remote-control message: the header and a body's type and
	// rcBody, padded to eight bytes.
	rcMinLen = 8
	// rcStateMsgLen is stock's state registration message, zero-padded.
	rcStateMsgLen = 0x2e
	// rcFlagRegistered is set in every key's flags on registration, as stock does.
	rcFlagRegistered = 0x04
	// rcFlagHold says the registration carries a hold time.
	rcFlagHold = 0x10
	// rcBootDelay13 is the boot delay stock sends a host, 13.
	rcBootDelay13 = 0x0d
	// rcPresets is the presets of a host's remote; rcStoreLast the last command that
	// stores one, 7 to 12.
	rcPresets   = 6
	rcStoreLast = 2 * rcPresets
	// What an activation was answered with.
	rcActivated    = 1
	rcNotActivated = 2
)

// The commands a host sends back for its remote's keys, beside the presets 1-6 (a press)
// and 7-12 (held: store).
const (
	rcCmdNext      = 0x0d
	rcCmdPrev      = 0x0e
	rcCmdStop      = 0x0f
	rcCmdPlayPause = 0x10
	rcCmdPlay      = 0x11
	rcCmdPause     = 0x12
	rcCmdBTPair    = 0x14
	rcCmdBTClear   = 0x15
)

func rcHeader() []byte { return []byte{0x02, 0x01, 0x04, 0x00} }

// rcKey is one key stock registers: the command the host sends back, its flags, group,
// class and index on the host's remote, and the 16-bit hold time (flag 0x10) in ms.
type rcKey struct {
	command, flags, group, class, index byte
	holdMs                              uint16
}

// rcKeys is stock's registration table (ABLServer's key registration routine), in its order.
//
//nolint:mnd // stock's table: the flags, groups, classes, indexes and hold times as it sends them
func rcKeys() []rcKey {
	var keys []rcKey
	for n := byte(1); n <= rcPresets; n++ { // presets, a press
		keys = append(keys, rcKey{command: n, flags: 0x8c, group: 0x11, class: 5, index: n, holdMs: 0})
	}

	keys = append(keys,
		rcKey{command: rcCmdNext, flags: 0x8c, group: 0x11, class: 3, index: 7, holdMs: 0},      // next track
		rcKey{command: rcCmdPrev, flags: 0x8c, group: 0x11, class: 3, index: 8, holdMs: 0},      // previous track
		rcKey{command: rcCmdPlayPause, flags: 0x8c, group: 0x11, class: 3, index: 3, holdMs: 0}, // play/pause
		rcKey{command: rcCmdPlay, flags: 0x8c, group: 0x11, class: 3, index: 1, holdMs: 0},      // play
		rcKey{command: rcCmdPause, flags: 0x8c, group: 0x11, class: 3, index: 2, holdMs: 0},     // pause
		rcKey{command: rcCmdStop, flags: 0x8c, group: 0x11, class: 3, index: 4, holdMs: 0},      // stop
	)
	for n := byte(1); n <= rcPresets; n++ { // presets held 1.5 s: store
		keys = append(keys, rcKey{command: rcPresets + n, flags: 0x1c, group: 0x11, class: 5, index: n, holdMs: 1500})
	}

	return keys
}

// rcBluetoothKeys are the SM2 adapter's extra keys: Bluetooth pairing (held 0.75 s) and
// clearing the pairings (held 10 s). Stock registers them only on the SM2, which has
// Bluetooth.
//
//nolint:mnd // stock's table: the flags, classes, indexes and hold times as it sends them
func rcBluetoothKeys() []rcKey {
	return []rcKey{
		{command: rcCmdBTPair, flags: 0x1c, group: 0, class: 2, index: 0x11, holdMs: 750},
		{command: rcCmdBTClear, flags: 0x1c, group: 0, class: 2, index: 0x11, holdMs: 10000},
	}
}

// rcKeyRegistration is one key registration message, sequence seq.
func rcKeyRegistrationMsg(key rcKey, seq byte) []byte {
	flags := key.flags | rcFlagRegistered

	body := []byte{rcKeyRegistration, rcBody, 10, seq, key.command, flags, key.group, 0x01, key.class, key.index}
	if flags&rcFlagHold != 0 {
		body = append(body, byte(key.holdMs>>byteBits), byte(key.holdMs&byteMask))
		body[2] = 12
	}

	return append(rcHeader(), body...)
}

// rcStateRegistrationMsg is stock's one state registration: state {0, 0, 0, 1}.
func rcStateRegistrationMsg(seq byte) []byte {
	msg := make([]byte, rcStateMsgLen)
	copy(msg, rcHeader())
	copy(msg[4:], []byte{rcState, rcBody, 0, seq, 0x03, 0, 1})

	return msg
}

func rcDoneMsg(keySeq, stateSeq byte) []byte {
	return append(rcHeader(), rcDone, rcBody, 0, stateSeq, 0, keySeq)
}

func rcBootDelayMsg(v uint16) []byte {
	return append(rcHeader(), rcBootDelay, rcBody, byte(v>>byteBits), byte(v&byteMask))
}

// rcActivateDoneMsg answers an activation: result is rcActivated or rcNotActivated.
func rcActivateDoneMsg(result byte) []byte {
	return append(rcHeader(), rcActivateDone, rcBody, result)
}

// RegistrationMessages are what stock sends a Wave or CineMate once it is found: the boot
// delay (13), the state registration, every key and the registration done.
//
//nolint:revive // flag-parameter: bluetooth says which adapter this is, whose keys these are
func RegistrationMessages(bluetooth bool) [][]byte {
	out := [][]byte{rcBootDelayMsg(rcBootDelay13), rcStateRegistrationMsg(1)}

	keys := rcKeys()
	if bluetooth {
		keys = append(keys, rcBluetoothKeys()...)
	}

	var seq byte
	for _, k := range keys {
		seq++
		out = append(out, rcKeyRegistrationMsg(k, seq))
	}

	return append(out, rcDoneMsg(seq, 1))
}

// Key actions the host's remote asks for, as soundtapd keys.
const (
	KeyPlay      = "PLAY"
	KeyPause     = "PAUSE"
	KeyStop      = "STOP"
	KeyPlayPause = "PLAY_PAUSE"
	KeyNext      = "NEXT_TRACK"
	KeyPrev      = "PREV_TRACK"
	KeyBTPair    = "BLUETOOTH_PAIR"
	KeyBTClear   = "BLUETOOTH_CLEAR"
)

// KeyAction maps a command the host sent to what SoundTap does: a key, or a preset to play
// (1..6) or to store (store true). Unknown commands (the rating keys among them) give "".
func KeyAction(command byte) (string, int, bool) {
	if command >= 1 && command <= rcPresets {
		return "", int(command), false
	}

	if command > rcPresets && command <= rcStoreLast {
		return "", int(command) - rcPresets, true
	}

	key := map[byte]string{
		rcCmdNext: KeyNext, rcCmdPrev: KeyPrev, rcCmdStop: KeyStop, rcCmdPlayPause: KeyPlayPause,
		rcCmdPlay: KeyPlay, rcCmdPause: KeyPause, rcCmdBTPair: KeyBTPair, rcCmdBTClear: KeyBTClear,
	}[command] // the rating keys among the rest: ""

	return key, 0, false
}

// rcMessage reads a remote-control message: its type and body, and whether it is one.
func rcMessage(msg []byte) (byte, []byte, bool) {
	if len(msg) < rcMinLen || msg[0]&rcTypeMask != rcMarker {
		return 0, nil, false
	}

	return msg[4] & rcTypeMask, msg[4:], true
}
