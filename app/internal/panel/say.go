// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//nolint:goconst // the English texts are the keys of the word tables, repeated in every language
package panel

import "github.com/stein155/soundtap/app/oled/screen"

// saying is how the panel shows one of its messages: an icon says what a word would, a
// small line under the title, the dots, and a smaller title for a long one.
type saying struct {
	icon      screen.Icon
	iconLevel int
	line      string // a key of Text, "" for none
	small     bool
	dots      int
}

// dimIcon is the grey of an icon that says something is not there (skip unavailable);
// workingDot the lit one of the dots under what is being done.
const (
	dimIcon    = 6
	workingDot = 2
)

// sayings are the messages soundtapd shows by their English text, with the design's icons.
// A text not here is shown alone.
//
//nolint:gochecknoglobals // a constant table
var sayings = map[string]saying{
	"Action unavailable": {icon: screen.IconSkip, iconLevel: dimIcon, line: "", small: true, dots: 0},
	"Can't connect":      {icon: screen.IconBluetoothOff, iconLevel: 0, line: "", small: true, dots: 0},
	"Update failed":      {icon: screen.IconCross, iconLevel: 0, line: "", small: false, dots: 0},
	"Updating":           {icon: screen.IconLock, iconLevel: 0, line: "Do not unplug", small: false, dots: 0},
	"Resetting": {
		icon: screen.IconNone, iconLevel: 0, line: "Do not unplug", small: false, dots: workingDot,
	},
	"Setup mode on":        {icon: screen.IconCheck, iconLevel: 0, line: "", small: false, dots: 0},
	"Setup mode off":       {icon: screen.IconCheck, iconLevel: 0, line: "", small: false, dots: 0},
	"Confirmed":            {icon: screen.IconCheck, iconLevel: 0, line: "", small: false, dots: 0},
	"Not confirmed":        {icon: screen.IconCross, iconLevel: 0, line: "", small: false, dots: 0},
	"Pairing list cleared": {icon: screen.IconCheck, iconLevel: 0, line: "", small: false, dots: 0},
	"Preset store failed":  {icon: screen.IconCross, iconLevel: 0, line: "", small: true, dots: 0},
	"Playback error":       {icon: screen.IconWarn, iconLevel: 0, line: "", small: false, dots: 0},
	"Amp fault":            {icon: screen.IconWarn, iconLevel: 0, line: "No sound", small: false, dots: 0},
}

// Questions are what a request that waits for a key held on the speaker asks (the
// confirmation lane's prompts): the panel shows CONFIRM over the question and how to answer.
//
//nolint:gochecknoglobals // a constant table
var questions = map[string]bool{
	"Reset?": true, "Restore?": true, "Restart?": true, "Change Wi-Fi?": true, "Setup mode?": true,
	"Unpair?": true, "Install?": true, "Share logs?": true, "Continue?": true,
}

// confirmDot is the lit one of the confirm screen's dots.
const confirmDot = 1

// SayScreen is english, one of soundtapd's messages, in the panel's language and the
// design's layout for it: a question asks for a held key, a known message gets its icon,
// any other text is shown alone.
func SayScreen(english string) *screen.Frame {
	if questions[english] {
		return screen.Confirm(Text("CONFIRM"), Text(english), Text("Hold a button"), confirmDot)
	}

	say, ok := sayings[english]
	if !ok {
		return screen.Message(Text(english))
	}

	line := ""
	if say.line != "" {
		line = Text(say.line)
	}

	return screen.Notice(say.icon, Text(english), line,
		screen.NoticeOpt{IconLevel: say.iconLevel, Small: say.small, Dots: say.dots})
}

// Say is SayScreen as the display manager takes it.
func Say(english string) []byte { return SayScreen(english).Pix(fullLevel) }
