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
	link := &Link{
		sourcePath: filepath.Join(home, "bar-source"),
		choiceDir:  home,
		version:    version,
		bar:        atomic.Pointer[speaker.Soundbar]{},
		now:        atomic.Pointer[speaker.Tone]{},
		send:       atomic.Pointer[func(what string, frame []byte)]{},
		pick:       atomic.Pointer[func(source uint32)]{},
		ownsVolume: atomic.Bool{},
		alive:      atomic.Bool{},
		lipSync:    atomic.Int32{},
		AudioModes: nil,
		CECModes:   nil,
		HDMIInputs: nil,
	}
	link.AudioModes = &Choice{
		link: link, what: "audio mode", op: lpmAudioMode, wide: true, file: "bar-audio-mode",
		values: map[string]uint8{
			"direct": audioModeDirect, "normal": audioModeNormal, "dialog": audioModeDialog, "night": audioModeNight,
		},
		cur: atomic.Uint32{},
	}
	link.CECModes = &Choice{
		link: link, what: "CEC mode", op: lpmCECModeSet, wide: false, file: "bar-cec-mode",
		values: map[string]uint8{
			"off": cecModeOff, "on": cecModeOn, "alternate": cecModeAlternate, "fine": cecModeFine,
		},
		cur: atomic.Uint32{},
	}
	link.HDMIInputs = &Choice{
		link: link, what: "HDMI input button", op: lpmHDMIInputSet, wide: false, file: "bar-hdmi-input",
		values: map[string]uint8{"none": hdmiButtonNone, "1": hdmiButton1, "2": hdmiButton2, "3": hdmiButton3},
		cur:    atomic.Uint32{},
	}

	return link
}

// Stock's protobuf enum numbers for the choices.
const (
	audioModeDirect  = 1
	audioModeNormal  = 2
	audioModeDialog  = 3
	audioModeNight   = 4
	cecModeOff       = 0
	cecModeOn        = 1
	cecModeAlternate = 2
	cecModeFine      = 3
	hdmiButtonNone   = 0
	hdmiButton1      = 1
	hdmiButton2      = 2
	hdmiButton3      = 3
)

// stateFileMode is the mode of the files the link keeps its state in.
const stateFileMode = 0o644

// errNotAnswered is a setting asked for before the soundbar has answered.
var errNotAnswered = errors.New("the soundbar has not answered yet")

// errNotAnsweredLPM is errNotAnswered for an action that names the link.
var errNotAnsweredLPM = errors.New("lpm: the soundbar has not answered yet")

// errInputUnavailable is an input pick the link cannot make.
var errInputUnavailable = errors.New("lpm: input unavailable")

// errUnknown is a choice's value by a name it does not have; the message goes on to
// name the choice and the value.
var errUnknown = errors.New("unknown")

// errLipSyncRange is a lip-sync delay outside what the app offers.
var errLipSyncRange = fmt.Errorf("lip-sync delay must be 0 to %d ms", LipSyncMax)

// errLevel is a tone level outside the range the processor reported; the message goes on
// to name the level and the range.
var errLevel = errors.New("level")

// Soundbar asks the processor for its speakers and returns the list it last sent, nil
// until it has.
func (l *Link) Soundbar() *speaker.Soundbar {
	l.ask("speaker query", lpmSpeakerQuery)

	return l.bar.Load()
}

// SaveSource remembers the bar's source. Only a change reaches the NAND.
func (l *Link) SaveSource(source uint32) error {
	value := []byte(strconv.Itoa(int(source)) + "\n")

	old, err := os.ReadFile(l.sourcePath)
	if err == nil && bytes.Equal(old, value) {
		return nil
	}

	err = atomicjson.WriteFile(l.sourcePath, value, stateFileMode)
	if err != nil {
		return fmt.Errorf("lpm save source: %w", err)
	}

	return nil
}

// SavedInput is the processor's own input the bar was playing before the module
// restarted. Standby and the module's input need nothing: SoundTap starts idle anyway.
// ADAPTiQ does not last, so it never comes back.
func (l *Link) SavedInput() (uint32, bool) {
	raw, err := os.ReadFile(l.sourcePath)
	if err != nil {
		return 0, false
	}

	saved, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || saved <= lpmSourceStandby || saved >= lpmSourceCount ||
		saved == lpmSourceModule || saved == lpmSourceBT || saved == lpmSourceADAPTiQ {
		return 0, false
	}

	return uint32(saved), true
}

// lpmPowerSource is the source to select when SoundTap's standby changes: standby on
// entering it, the module's input on leaving it.
func lpmPowerSource(wasStandby, standby bool) (uint32, bool) {
	switch {
	case standby && !wasStandby:
		return lpmSourceStandby, true
	case !standby && wasStandby:
		return lpmSourceModule, true
	default:
		return 0, false
	}
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

// Keep makes the value called name current and saves it, as a report of the processor
// does. A name outside the choice's values is ignored.
func (c *Choice) Keep(name string) {
	if n, ok := c.values[name]; ok {
		c.setCurrent(uint32(n))
	}
}

// Load takes the saved value back after a restart, until the processor reports one.
func (c *Choice) Load() {
	raw, err := os.ReadFile(filepath.Join(c.link.choiceDir, c.file))
	if err != nil {
		return
	}

	saved, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
	if err == nil {
		c.report(uint32(saved))
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
	value, ok := c.values[name]
	if !ok {
		return fmt.Errorf("%w %s %q", errUnknown, c.what, name)
	}

	send := c.link.send.Load()
	if send == nil || !c.link.alive.Load() {
		return errNotAnswered
	}

	payload := []byte{value}
	if c.wide {
		payload = binary.BigEndian.AppendUint16(nil, uint16(value))
	}

	(*send)(c.what, lpmFrame(lpmProcessor, c.op, payload))
	c.setCurrent(uint32(value))

	return nil
}

// report keeps a value the processor sent; anything outside values is ignored.
func (c *Choice) report(v uint32) {
	for _, n := range c.values {
		if uint32(n) == v {
			c.setCurrent(v)

			return
		}
	}
}

// setCurrent makes v current and saves it; only a change reaches the NAND.
func (c *Choice) setCurrent(v uint32) {
	if c.cur.Swap(v+1) == v+1 {
		return
	}

	path, value := filepath.Join(c.link.choiceDir, c.file), []byte(strconv.Itoa(int(v))+"\n")

	err := atomicjson.WriteFile(path, value, stateFileMode)
	if err != nil {
		slog.Warn("lpm save "+c.what, "comp", "lpm", "err", err)
	}
}

// LipSyncMax is the most the app offers. A4VServer holds no range (the processor
// clamps); widen it once the bar shows a larger delay taking effect.
const LipSyncMax = 300

// LipSync is the delay the bar's audio waits for the picture, in ms, and whether a status
// has said yet.
func (l *Link) LipSync() (int, bool) {
	v := l.lipSync.Load()

	return int(v) - 1, v > 0
}

// SetLipSync sends the delay the bar's audio waits for the picture.
func (l *Link) SetLipSync(delay int) error {
	if delay < 0 || delay > LipSyncMax {
		return errLipSyncRange
	}

	send := l.send.Load()
	if send == nil || !l.alive.Load() {
		return errNotAnswered
	}

	(*send)("lip-sync", lpmFrame(lpmProcessor, lpmLipSyncSet, binary.BigEndian.AppendUint32(nil, uint32(delay))))
	l.lipSync.Store(int32(delay) + 1)

	return nil
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
		return errNotAnswered
	}

	levels := [lpmToneControls]int{bass, treble, centre, rear}
	for i, c := range []speaker.ToneControl{now.Bass, now.Treble, now.Centre, now.Rear} {
		if c.Max <= c.Min || levels[i] < c.Min || levels[i] > c.Max {
			return fmt.Errorf("%w %d is outside what the soundbar allows (%d to %d)", errLevel, levels[i], c.Min, c.Max)
		}
	}

	(*send)("tone", lpmToneFrame(levels))

	return nil
}

// StartADAPTiQ switches the bar to its ADAPTiQ source. The processor runs the calibration
// itself and picks the previous source when it is done, as its own remote's start does.
func (l *Link) StartADAPTiQ() error {
	pick := l.pick.Load()
	if pick == nil {
		return errNotAnsweredLPM
	}

	(*pick)(lpmSourceADAPTiQ)

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
	pick := l.pick.Load()
	if pick == nil {
		return errInputUnavailable
	}

	switch source {
	case sourceTV:
		(*pick)(lpmSourceTV)
	case sourceBluetooth:
		(*pick)(lpmSourceBT)
	case sourceHDMI:
		input := uint32(lpmSourceHDMI1)
		if v := l.HDMIInputs.values[l.HDMIInputs.Get()]; v > 0 {
			input = uint32(v)
		}

		(*pick)(input)
	default:
		return errInputUnavailable
	}

	return nil
}

func (l *Link) ask(what string, op byte) {
	if send := l.send.Load(); send != nil {
		(*send)(what, lpmFrame(lpmProcessor, op, nil))
	}
}

// statusMode keeps the audio mode and lip-sync delay a 0x14 status reports.
func (l *Link) statusMode(status []byte) {
	l.lipSync.Store(int32(binary.BigEndian.Uint16(status[0x48:])) + 1)
	l.AudioModes.report(uint32(status[0x3f]))
}
