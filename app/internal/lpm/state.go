// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package lpm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// Link is the module's side of the link to the soundbar processor, and what it knows of
// the bar.
type Link struct {
	sourcePath string // the source the bar last played, across a restart of the module
	choiceDir  string // each choice's last value
	version    string // the app version the module reports to the processor

	// The processor's latest speaker list and tone, nil until it has sent them. send,
	// once the link is open, writes a frame to it; the settings page asks for a fresh list
	// or tone and reads the one it has, so the next read sees the answer.
	bar  atomic.Pointer[speaker.Soundbar]
	now  atomic.Pointer[speaker.Tone]
	send atomic.Pointer[func(what string, frame []byte)]
	// pick switches the bar to one of the processor's own inputs, as its remote does.
	pick atomic.Pointer[func(source uint32)]
	// ownsVolume is set once the processor has answered (MixerLevel).
	ownsVolume atomic.Bool
	// alive is set by the processor's first frame: before that nothing is sent to it.
	alive atomic.Bool
	// lipSync is the delay the bar's audio waits for the picture, in ms plus one; 0 while
	// no status has said.
	lipSync atomic.Int32

	// AudioModes: the 0x14 status carries it at 0x3f; 0 there is "unspecified".
	AudioModes *Choice
	// CECModes: stock's CECSelectModes, reported in 0xd7 at byte 4.
	CECModes *Choice
	// HDMIInputs: stock's HDMIInputSelection, the remote's source button that picks the
	// bar's HDMI input, reported in 0xda at byte 4.
	HDMIInputs *Choice
}

// New is the link of a module whose state lives in home (/mnt/nv/soundtap on a speaker):
// bar-source, the source the bar last played, and one file per choice. The processor
// reports a choice only when it changes or on a status, and a bar in standby sends neither,
// so after a restart of the module the app would otherwise have nothing to show until the
// bar woke.
func New(home, version string) *Link {
	l := &Link{sourcePath: filepath.Join(home, "bar-source"), choiceDir: home, version: version}
	l.AudioModes = &Choice{link: l, what: "audio mode", op: lpmAudioMode, wide: true, file: "bar-audio-mode",
		values: map[string]uint8{"direct": 1, "normal": 2, "dialog": 3, "night": 4}}
	l.CECModes = &Choice{link: l, what: "CEC mode", op: lpmCECModeSet, file: "bar-cec-mode",
		values: map[string]uint8{"off": 0, "on": 1, "alternate": 2, "fine": 3}}
	l.HDMIInputs = &Choice{link: l, what: "HDMI input button", op: lpmHDMIInputSet, file: "bar-hdmi-input",
		values: map[string]uint8{"none": 0, "1": 1, "2": 2, "3": 3}}
	return l
}

func (l *Link) ask(what string, op byte) {
	if send := l.send.Load(); send != nil {
		(*send)(what, lpmFrame(lpmProcessor, op, nil))
	}
}

// Soundbar asks the processor for its speakers and returns the list it last sent, nil
// until it has.
func (l *Link) Soundbar() *speaker.Soundbar {
	l.ask("speaker query", lpmSpeakerQuery)
	return l.bar.Load()
}

// SaveSource remembers the bar's source. Only a change reaches the NAND.
func (l *Link) SaveSource(source uint32) error {
	value := []byte(strconv.Itoa(int(source)) + "\n")
	if old, err := os.ReadFile(l.sourcePath); err == nil && bytes.Equal(old, value) {
		return nil
	}
	return atomicjson.WriteFile(l.sourcePath, value, 0o644)
}

// SavedInput is the processor's own input the bar was playing before the module
// restarted. Standby and the module's input need nothing: SoundTap starts idle anyway.
// ADAPTiQ does not last, so it never comes back.
func (l *Link) SavedInput() (source uint32, ok bool) {
	raw, err := os.ReadFile(l.sourcePath)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n <= lpmSourceStandby || n >= lpmSourceCount ||
		n == lpmSourceModule || n == lpmSourceBT || n == lpmSourceADAPTiQ {
		return 0, false
	}
	return uint32(n), true
}

// lpmPowerSource is the source to select when SoundTap's standby changes: standby on
// entering it, the module's input on leaving it.
func lpmPowerSource(wasStandby, standby bool) (source uint32, ok bool) {
	switch {
	case standby && !wasStandby:
		return lpmSourceStandby, true
	case !standby && wasStandby:
		return lpmSourceModule, true
	}
	return 0, false
}

// Choice is a soundbar setting the processor keeps as one small number: stock's audio
// mode, CEC mode and so on. Values are stock's protobuf enum numbers.
type Choice struct {
	link   *Link
	what   string
	op     byte // the frame that sets it
	wide   bool // the value goes out as a BE u16 (audio mode), else as one byte
	values map[string]uint8
	file   string        // under the link's home: the last value, for a restart in standby
	cur    atomic.Uint32 // value+1 as last reported or sent; 0 until then
}

// report keeps a value the processor sent; anything outside values is ignored.
func (c *Choice) report(v uint32) {
	for _, n := range c.values {
		if uint32(n) == v {
			c.keep(v)
			return
		}
	}
}

// Keep makes the value called name current and saves it, as a report of the processor
// does. A name outside the choice's values is ignored.
func (c *Choice) Keep(name string) {
	if n, ok := c.values[name]; ok {
		c.keep(uint32(n))
	}
}

// keep makes v current and saves it; only a change reaches the NAND.
func (c *Choice) keep(v uint32) {
	if c.cur.Swap(v+1) == v+1 {
		return
	}
	if err := atomicjson.WriteFile(filepath.Join(c.link.choiceDir, c.file), []byte(strconv.Itoa(int(v))+"\n"), 0o644); err != nil {
		slog.Warn("lpm save "+c.what, "comp", "lpm", "err", err)
	}
}

// Load takes the saved value back after a restart, until the processor reports one.
func (c *Choice) Load() {
	raw, err := os.ReadFile(filepath.Join(c.link.choiceDir, c.file))
	if err != nil {
		return
	}
	if v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32); err == nil {
		c.report(uint32(v))
	}
}

// Get is the current value's name, or "" while unknown.
func (c *Choice) Get() string {
	cur := c.cur.Load()
	for name, n := range c.values {
		if uint32(n)+1 == cur {
			return name
		}
	}
	return ""
}

// Set sends one of values. The processor confirms it later; until then it reads as sent.
func (c *Choice) Set(name string) error {
	n, ok := c.values[name]
	if !ok {
		return fmt.Errorf("unknown %s %q", c.what, name)
	}
	send := c.link.send.Load()
	if send == nil || !c.link.alive.Load() {
		return errors.New("the soundbar has not answered yet")
	}
	p := []byte{n}
	if c.wide {
		p = binary.BigEndian.AppendUint16(nil, uint16(n))
	}
	(*send)(c.what, lpmFrame(lpmProcessor, c.op, p))
	c.keep(uint32(n))
	return nil
}

// LipSyncMax is the most the app offers. A4VServer holds no range (the processor
// clamps); widen it once the bar shows a larger delay taking effect.
const LipSyncMax = 300

// LipSync is the delay the bar's audio waits for the picture, in ms, and whether a status
// has said yet.
func (l *Link) LipSync() (ms int, ok bool) {
	v := l.lipSync.Load()
	return int(v) - 1, v > 0
}

// SetLipSync sends the delay the bar's audio waits for the picture.
func (l *Link) SetLipSync(ms int) error {
	if ms < 0 || ms > LipSyncMax {
		return fmt.Errorf("lip-sync delay must be 0 to %d ms", LipSyncMax)
	}
	send := l.send.Load()
	if send == nil || !l.alive.Load() {
		return errors.New("the soundbar has not answered yet")
	}
	(*send)("lip-sync", lpmFrame(lpmProcessor, lpmLipSyncSet, binary.BigEndian.AppendUint32(nil, uint32(ms))))
	l.lipSync.Store(int32(ms) + 1)
	return nil
}

// statusMode keeps the audio mode and lip-sync delay a 0x14 status reports.
func (l *Link) statusMode(status []byte) {
	l.lipSync.Store(int32(binary.BigEndian.Uint16(status[0x48:])) + 1)
	l.AudioModes.report(uint32(status[0x3f]))
}

// Tone asks the processor for its tone and returns the one it last sent, nil until it has.
func (l *Link) Tone() *speaker.Tone {
	l.ask("tone query", lpmToneQuery)
	return l.now.Load()
}

// SetTone sends the levels once each lies within the range the processor reported.
func (l *Link) SetTone(bass, treble, centre, rear int) error {
	now, send := l.now.Load(), l.send.Load()
	if now == nil || send == nil {
		return errors.New("the soundbar has not answered yet")
	}
	levels := [4]int{bass, treble, centre, rear}
	for i, c := range []speaker.ToneControl{now.Bass, now.Treble, now.Centre, now.Rear} {
		if c.Max <= c.Min || levels[i] < c.Min || levels[i] > c.Max {
			return fmt.Errorf("level %d is outside what the soundbar allows (%d to %d)", levels[i], c.Min, c.Max)
		}
	}
	(*send)("tone", lpmToneFrame(levels))
	return nil
}

// StartADAPTiQ switches the bar to its ADAPTiQ source. The processor runs the calibration
// itself and picks the previous source when it is done, as its own remote's start does.
func (l *Link) StartADAPTiQ() error {
	p := l.pick.Load()
	if p == nil {
		return errors.New("lpm: the soundbar has not answered yet")
	}
	(*p)(lpmSourceADAPTiQ)
	return nil
}

// lpmModuleInput is the processor input the module's audio plays on: its Bluetooth input
// while SoundTap plays Bluetooth, so the bar shows that, the module's input otherwise.
func lpmModuleInput(source string) uint32 {
	if source == sourceBluetooth {
		return lpmSourceBT
	}
	return lpmSourceModule
}

// PickInput is the app's pick of a user source on the ST300: TV, Bluetooth, or HDMI, which
// picks the input the remote's HDMI button is assigned to (HDMI 1 while none is), as that button does.
func (l *Link) PickInput(source string) error {
	p := l.pick.Load()
	if p == nil {
		return errors.New("lpm: input unavailable")
	}
	switch source {
	case sourceTV:
		(*p)(lpmSourceTV)
	case sourceBluetooth:
		(*p)(lpmSourceBT)
	case sourceHDMI:
		n := uint32(lpmSourceHDMI1)
		if v := l.HDMIInputs.values[l.HDMIInputs.Get()]; v > 0 {
			n = uint32(v)
		}
		(*p)(n)
	default:
		return errors.New("lpm: input unavailable")
	}
	return nil
}
