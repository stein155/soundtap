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
)

func rcHeader() []byte { return []byte{0x02, 0x01, 0x04, 0x00} }

// rcKey is one key stock registers: the command the host sends back, its flags, group,
// class and index on the host's remote, and the 16-bit hold time (flag 0x10) in ms.
type rcKey struct {
	command, flags, group, class, index byte
	holdMs                              uint16
}

// rcKeys is stock's registration table (ABLServer's key registration routine), in its order.
var rcKeys = func() []rcKey {
	var k []rcKey
	for n := byte(1); n <= 6; n++ { // presets, a press
		k = append(k, rcKey{command: n, flags: 0x8c, group: 0x11, class: 5, index: n})
	}
	k = append(k,
		rcKey{command: 0x0d, flags: 0x8c, group: 0x11, class: 3, index: 7}, // next track
		rcKey{command: 0x0e, flags: 0x8c, group: 0x11, class: 3, index: 8}, // previous track
		rcKey{command: 0x10, flags: 0x8c, group: 0x11, class: 3, index: 3}, // play/pause
		rcKey{command: 0x11, flags: 0x8c, group: 0x11, class: 3, index: 1}, // play
		rcKey{command: 0x12, flags: 0x8c, group: 0x11, class: 3, index: 2}, // pause
		rcKey{command: 0x0f, flags: 0x8c, group: 0x11, class: 3, index: 4}, // stop
	)
	for n := byte(1); n <= 6; n++ { // presets held 1.5 s: store
		k = append(k, rcKey{command: 6 + n, flags: 0x1c, group: 0x11, class: 5, index: n, holdMs: 1500})
	}
	return k
}()

// rcBluetoothKeys are the SM2 adapter's extra keys: Bluetooth pairing (held 0.75 s) and
// clearing the pairings (held 10 s). Stock registers them only on the SM2, which has
// Bluetooth.
var rcBluetoothKeys = []rcKey{
	{command: 0x14, flags: 0x1c, class: 2, index: 0x11, holdMs: 750},
	{command: 0x15, flags: 0x1c, class: 2, index: 0x11, holdMs: 10000},
}

// rcKeyRegistration is one key registration message, sequence seq.
func rcKeyRegistrationMsg(k rcKey, seq byte) []byte {
	flags := k.flags | 0x04
	body := []byte{rcKeyRegistration, 0x01, 10, seq, k.command, flags, k.group, 0x01, k.class, k.index}
	if flags&0x10 != 0 {
		body = append(body, byte(k.holdMs>>8), byte(k.holdMs&0xff))
		body[2] = 12
	}
	return append(rcHeader(), body...)
}

// rcStateRegistrationMsg is stock's one state registration: state {0, 0, 0, 1}.
func rcStateRegistrationMsg(seq byte) []byte {
	msg := make([]byte, 0x2e)
	copy(msg, rcHeader())
	copy(msg[4:], []byte{rcState, 0x01, 0, seq, 0x03, 0, 1})
	return msg
}

func rcDoneMsg(keySeq, stateSeq byte) []byte {
	return append(rcHeader(), rcDone, 0x01, 0, stateSeq, 0, keySeq)
}

func rcBootDelayMsg(v uint16) []byte {
	return append(rcHeader(), rcBootDelay, 0x01, byte(v>>8), byte(v&0xff))
}

func rcActivateDoneMsg(activated bool) []byte {
	r := byte(2)
	if activated {
		r = 1
	}
	return append(rcHeader(), rcActivateDone, 0x01, r)
}

// RegistrationMessages are what stock sends a Wave or CineMate once it is found: the boot
// delay (13), the state registration, every key and the registration done.
func RegistrationMessages(bluetooth bool) [][]byte {
	out := [][]byte{rcBootDelayMsg(0x0d), rcStateRegistrationMsg(1)}
	keys := rcKeys
	if bluetooth {
		keys = append(append([]rcKey(nil), rcKeys...), rcBluetoothKeys...)
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
func KeyAction(command byte) (key string, preset int, store bool) {
	switch {
	case command >= 1 && command <= 6:
		return "", int(command), false
	case command >= 7 && command <= 12:
		return "", int(command) - 6, true
	}
	switch command {
	case 0x0d:
		return KeyNext, 0, false
	case 0x0e:
		return KeyPrev, 0, false
	case 0x0f:
		return KeyStop, 0, false
	case 0x10:
		return KeyPlayPause, 0, false
	case 0x11:
		return KeyPlay, 0, false
	case 0x12:
		return KeyPause, 0, false
	case 0x14:
		return KeyBTPair, 0, false
	case 0x15:
		return KeyBTClear, 0, false
	}
	return "", 0, false
}

// rcMessage reads a remote-control message: its type and body. ok is false for anything else.
func rcMessage(d []byte) (typ byte, body []byte, ok bool) {
	if len(d) < 8 || d[0]&0x0f != 0x02 {
		return 0, nil, false
	}
	return d[4] & 0x0f, d[4:], true
}
