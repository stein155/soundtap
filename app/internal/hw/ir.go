// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// RunIR drives speaker actions from the Bose infrared receiver.
//
// HARDWARE (from the stock rhino_sm2.dtb/spotty_scm.dtb and ir.ko, not guessed):
//
//	Both DTBs carry a `bose,shelby-ir` node — on ST10 and ST20 alike it is
//	`serial@44e09000`, i.e. UART1 with a 48 MHz functional clock, repurposed as an
//	oversampling edge sampler: ir_probe programs a divisor of clock-frequency/32000
//	and each received UART byte is 8 samples of the IR line. /etc/init.d/soundtap
//	already modprobes the stock `ir` driver next to shelby-keypad.
//
//	The whole protocol is decoded in the kernel (IRDecodeThread ->
//	BoseDecode_EdgeEvent), so userspace never sees pulse timings. A frame is TWO
//	bytes: the second must be the exact bitwise complement of the first, or the
//	frame is dropped as an error. The accepted first byte is bit-reversed and that
//	single byte is what lands in the 16-byte IRKeyCodeFifo.
//
// WIRE FORMAT (disassembled from ir.ko, not guessed):
//
//	ir_read() is `__kfifo_to_user(fifo, buf, 1, &copied)`: exactly ONE byte per
//	read, and it is NON-BLOCKING — on an empty fifo it copies nothing and returns
//	0, which Go surfaces as io.EOF. A blocking read loop like readButtonDevice's
//	would spin a core flat on this single-core box, so the device is waited on with
//	select(2); ir_poll() reports POLLIN|POLLRDNORM when the fifo is non-empty.
//	ir_open() drains the fifo, so a stale code from before startup is never
//	replayed.
//
//	    byte != 0  press of that key code
//	    byte == 0  release: the receiver has stopped hearing a transmission
//
//	Both edges come from the driver, so this is NOT the press-only stream the
//	keypad has:
//
//	  - While a key is held the remote keeps retransmitting, and the driver
//	    suppresses every frame identical to the previous one (irCodeData_latch), so
//	    a hold produces exactly one code byte and then silence.
//	  - When the transmission stops, irStateTimer (armed at jiffies+14 on every
//	    accepted bit) fires, resets the decoder and pushes 0x00.
//	  - Pressing a different key while one is latched makes the driver inject 0x00
//	    *before* the new code, so a switch is always release-then-press.
//	  - 0x00 can never be a key code: a frame of 0x00/0xFF passes the complement
//	    check but then equals the cleared latch and is suppressed. So 0x00 is
//	    unambiguously the release marker, and 0x01..0xFF is the usable code space.
//
// irSynth therefore only has to forward those edges to the KeySink, which
// already times the volume ramp and the preset hold-to-store — the remote gets
// the same behaviour as the front panel from one tested implementation.
//
// The code -> action table is data rather than driver logic. The rootfs ships the
// canonical BoseApp table in /etc/soundtap/ir.map; /mnt/nv/ir.map replaces it for
// another remote. Capture custom codes with IR_LEARN=1.
const (
	// irReleaseGrace delays the release that a 0x00 asks for, and is cancelled if
	// the same code arrives again inside it.
	//
	// A hold survives only as long as the remote retransmits faster than the
	// driver's 14-jiffy state timer. That timer is in jiffies and this kernel's HZ
	// is not established, so on a fast HZ a long press could be chopped into
	// release/press pairs — which for a preset would mean replaying it over and
	// over. Absorbing a 0x00 that is immediately contradicted costs one grace
	// period of release latency and makes the hold behaviour independent of HZ.
	irReleaseGrace = 150 * time.Millisecond

	// irPollSlice bounds how long a read waits, so ctx cancellation is noticed
	// promptly on a device that may stay quiet for hours.
	irPollSlice = 200 * time.Millisecond

	// irUnknownQuiet is how long an unmapped code stays quiet after being logged
	// once. A remote for another device in the same room must not be able to fill
	// the log, but the first sighting of each code is exactly what the user needs
	// to write ir.map.
	irUnknownQuiet = 5 * time.Minute
)

// irActions are the speakerstate keys an ir.map entry may name. NEXT_TRACK and
// PREV_TRACK only do something on a source that carries a control channel (today
// Bluetooth AVRCP); on radio or AUX they are accepted and ignored, because a remote
// that has the buttons should not fail to load its map over them.
var irActions = map[string]bool{
	"POWER": true, "VOLUME_UP": true, "VOLUME_DOWN": true, "MUTE": true, //nolint:goconst // key names
	"PLAY": true, "PAUSE": true, "PLAY_PAUSE": true, "STOP": true,
	"NEXT_TRACK": true, "PREV_TRACK": true,
	"AUX_INPUT": true, KeySourceToggle: true, keyBluetooth: true,
	"PRESET_1": true, "PRESET_2": true, "PRESET_3": true, //nolint:goconst // key names
	"PRESET_4": true, "PRESET_5": true, "PRESET_6": true, //nolint:goconst // key names
}

// LoadIRKeymap parses `<code> = <ACTION>` lines, where code is decimal or 0x-hex.
// Unparsable lines and unknown actions are reported and skipped rather than
// silently dropped: a typo in a hand-written map on a headless speaker is
// otherwise invisible.
func LoadIRKeymap(path string, log *slog.Logger) map[byte]string {
	m := map[byte]string{}

	b, err := os.ReadFile(path) //nolint:gosec // G703: IR_MAP, or the speaker's own map
	if err != nil {
		return m
	}

	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		k, v, ok := strings.Cut(line, "=")
		if !ok {
			log.Warn("ir.map: not a code=action line", "file", path, "line", n+1)

			continue
		}

		code, err := parseIRCode(strings.TrimSpace(k))
		if err != nil {
			log.Warn("ir.map: bad code", "file", path, "line", n+1, "err", err)

			continue
		}

		action := strings.ToUpper(strings.TrimSpace(v))
		if !irActions[action] {
			log.Warn("ir.map: unknown action", "file", path, "line", n+1, "action", action)

			continue
		}

		m[code] = action
	}

	return m
}

func irMapPath(configured, persistent, stock string) string {
	if configured != "" {
		return configured
	}

	if _, err := os.Stat(persistent); !os.IsNotExist(err) {
		return persistent
	}

	return stock
}

// parseIRCode accepts 0x1a, 1A (hex only with the prefix) and 26.
func parseIRCode(s string) (byte, error) {
	base := 10
	if l := strings.ToLower(s); strings.HasPrefix(l, "0x") {
		s, base = s[2:], 16
	}

	v, err := strconv.ParseUint(s, base, 8)
	if err != nil {
		return 0, fmt.Errorf("code %q is not a 1..255 value", s)
	}

	if v == 0 {
		// The driver reserves it for the release marker and can never deliver it as
		// a key, so a 0x00 line is a misreading of a capture, not a binding.
		return 0, errors.New("code 0x00 is the release marker, not a key")
	}

	return byte(v), nil
}

// irSource is one open receiver. It exists so the edge synthesis can be tested
// without a real /dev/ir.
type irSource interface {
	// next waits up to timeout for one code. ok=false means the timeout expired
	// with nothing decoded, which is the normal idle case and not an error.
	next(timeout time.Duration) (code byte, ok bool, err error)
	Close() error
}

// irReleaseCode is the byte the driver pushes when it stops hearing a
// transmission. It is not a key and can never be one — see the wire format above.
const irReleaseCode = 0

// irSynth forwards the receiver's press/release edges to a KeySink.
// Everything here runs on the single read loop goroutine, so the sink keeps its
// "one device, one goroutine" invariant and needs no lock.
type irSynth struct {
	keys  KeySink
	km    map[byte]string
	log   *slog.Logger
	grace time.Duration
	now   func() time.Time

	learn bool // report codes only; drive nothing

	held      byte   // code currently down (valid while heldName != "")
	heldName  string // action currently down, "" when nothing is
	pending   bool   // a 0x00 asked for a release; releaseAt is when it happens
	releaseAt time.Time

	unknown map[byte]time.Time // last time each unmapped code was logged

	order int // index into irTransforms once a code has matched ir.map, -1 before
}

func newIRSynth(keys KeySink, km map[byte]string, grace time.Duration, learn bool, log *slog.Logger) *irSynth {
	return &irSynth{
		keys: keys, km: km, log: log, grace: grace, learn: learn,
		now:     time.Now,
		unknown: map[byte]time.Time{},
		order:   -1,
	}
}

// code handles one byte from the receiver.
func (s *irSynth) code(c byte) {
	now := s.now()
	if s.learn {
		// The capture path: say what arrived and what it is currently bound to,
		// and drive nothing. The 0x00 markers are reported too — where they fall
		// is exactly what says whether this remote's holds survive.
		if c == irReleaseCode {
			s.log.Info("ir learn", "code", hexCode(c), "meaning", "release")

			return
		}

		s.log.Info("ir learn", "code", hexCode(c), "decimal", c, "mapped", s.km[c])

		return
	}

	if c == irReleaseCode {
		if s.heldName != "" && !s.pending {
			s.pending, s.releaseAt = true, now.Add(s.grace)
		}

		return
	}

	c = s.resolve(c)

	action, mapped := s.km[c]
	if !mapped {
		s.reportUnknown(c, now)

		return
	}

	if s.heldName != "" && s.held == c {
		// The same key again: the driver only re-sends a latched code after it
		// reset itself, so this is a hold whose 0x00 was premature. Withdraw the
		// release instead of replaying the press.
		s.pending = false

		return
	}

	if s.heldName != "" {
		// A different key. The driver injects 0x00 before it, so this normally
		// only settles a release that is already pending — but release the old key
		// unconditionally, or its volume ramp would never stop.
		s.release()
	}

	s.heldName, s.held, s.pending = action, c, false
	s.keys.Handle(int(c), true, action)
}

// resolve reads a code the way ir.map spells it. ir.ko hands over exactly that byte, so
// the first reading already matches; the lirc decoder cannot know whether the map's
// codes are its byte, the complement or either one bit-reversed, so the first press of
// a mapped key settles it and the choice is logged. No two keys of the stock map
// collide under any one reading, which is what makes trying all four safe.
func (s *irSynth) resolve(c byte) byte {
	if s.order >= 0 {
		return irTransforms[s.order].fn(c)
	}

	for i, t := range irTransforms {
		v := t.fn(c)
		if _, ok := s.km[v]; ok && v != irReleaseCode {
			s.order = i
			s.log.Info("ir code reading settled", "reading", t.name, "raw", hexCode(c), "code", hexCode(v))

			return v
		}
	}

	return c
}

// tick fires a release whose grace period has run out.
func (s *irSynth) tick() {
	if !s.pending || s.now().Before(s.releaseAt) {
		return
	}

	s.release()
}

func (s *irSynth) release() {
	code, action := s.held, s.heldName
	s.heldName, s.pending = "", false
	s.keys.Handle(int(code), false, action)
}

// reset drops the held key without emitting a release action, for when the device
// disappears mid-press. The sink's Reset cancels the ramp and the hold-to-store timer
// it may have started.
func (s *irSynth) reset() {
	s.heldName, s.pending = "", false
	s.keys.Reset()
}

func (s *irSynth) reportUnknown(c byte, now time.Time) {
	if last, seen := s.unknown[c]; seen && now.Sub(last) < irUnknownQuiet {
		return
	}

	s.unknown[c] = now
	// Info, not Debug: on a speaker with no ir.map yet this line IS the
	// documentation of the user's remote. The quiet window above keeps a
	// neighbouring remote from turning that into a flood.
	s.log.Info("unmapped ir code", "code", hexCode(c), "decimal", c)
}

func hexCode(c byte) string { return fmt.Sprintf("0x%02x", c) }

// run pumps one open device until it errors or ctx ends.
func (s *irSynth) run(ctx context.Context, dev irSource) {
	for ctx.Err() == nil {
		timeout := irPollSlice
		if s.pending {
			if d := s.releaseAt.Sub(s.now()); d < timeout {
				// Wake exactly when the release is due, so a held volume key
				// stops ramping on time instead of up to a slice late.
				timeout = max(d, 0)
			}
		}

		code, ok, err := dev.next(timeout)
		if err != nil {
			return
		}

		if ok {
			s.code(code)

			continue
		}

		s.tick()
	}
}

// IRDevice names the receiver to open, or "" when this speaker has none. configured
// is the model's receiver (model.Model.IRDev); IR_DEVICE overrides it.
func IRDevice(configured string) string {
	dev := os.Getenv("IR_DEVICE")
	if dev == "" {
		dev = configured
	}
	// "" is how the model profile says a model has no receiver at all -- the WLA sets it. Without
	// it here the empty path went to the open loop and was retried for half a minute at
	// every boot, on hardware that has nothing to open.
	if dev == "" || dev == "off" || dev == "-" {
		return ""
	}
	// ir.ko's /dev/ir only exists on the stock kernel. The mainline one drives the same
	// receiver through gpio-ir-receiver and rc-core, and its raw timings are on lirc0.
	if _, err := os.Stat(dev); err != nil && dev == "/dev/ir" { //nolint:gosec // G703: IR_DEVICE, or the model's receiver
		if _, err := os.Stat("/dev/lirc0"); err == nil {
			return "/dev/lirc0"
		}
	}

	return dev
}

// RunIR reads the receiver dev until ctx ends and hands its key edges to keys. The key
// map is IR_MAP, else userMap when it exists, else the rootfs's stock map.
func RunIR(ctx context.Context, dev, userMap string, keys KeySink, log *slog.Logger) {
	mapPath := irMapPath(os.Getenv("IR_MAP"), userMap, "/etc/soundtap/ir.map")
	km := LoadIRKeymap(mapPath, log)
	learn := os.Getenv("IR_LEARN") == "1"

	grace := irReleaseGrace
	if ms, err := strconv.Atoi(os.Getenv("IR_RELEASE_GRACE_MS")); err == nil && ms >= 0 {
		grace = time.Duration(ms) * time.Millisecond
	}

	switch {
	case learn:
		log.Info("ir learn mode: codes are reported, no actions are taken", "device", dev)
	case len(km) == 0:
		// Not an error: no remote has been captured yet. Codes still get logged
		// once each by reportUnknown, which is what fills in ir.map.
		log.Info("no ir keymap; press remote keys and read the logged codes",
			"map", mapPath, "device", dev)
	default:
		log.Info("ir keymap loaded", "map", mapPath, "keys", len(km), "device", dev)
	}

	s := newIRSynth(keys, km, grace, learn, log)
	// The node is created by devtmpfs when init.d modprobes `ir`, which happens well
	// before soundtapd starts — so a few retries cover a slow module load, and
	// anything longer means this kernel has no Bose IR driver (the mainline image, or
	// a dev machine). Give up then instead of stat'ing a path that will never exist
	// for the rest of the speaker's uptime.
	const openAttempts = 15 // ~30 s at the 2 s retry below

	for attempt := 0; ctx.Err() == nil; {
		d, err := openIRDevice(dev)
		if err != nil {
			attempt++
			if attempt >= openAttempts {
				log.Info("no ir receiver on this kernel, giving up", "device", dev, "err", err)

				return
			}

			if !waitToReopen(ctx) {
				return
			}

			continue
		}

		attempt = 0

		s.run(ctx, d)
		s.reset() // device closed mid-press: no repeat is coming

		_ = d.Close()

		if !waitToReopen(ctx) { // see ReadKeyDevice: no hot reopen loop
			return
		}
	}
}
