// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"errors"
	"fmt"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// Alarm is the speaker's own wake-up alarm: at Time (24-hour "HH:MM", in the zone of
// Display.Timezone, else Timezone, else the system zone) it plays preset Slot. It lives
// on the speaker rather than in a browser because a browser tab is closed while its
// owner sleeps. Timezone is the zone of the browser that last saved the alarm: a speaker
// without a display has no town, and its system zone is UTC.
type Alarm struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time"`
	Slot     int    `json:"slot"`
	Timezone string `json:"timezone,omitempty"`
}

// DefaultAlarm is what an alarm that was never set looks like to the UI.
//
//nolint:gochecknoglobals // other packages compare against it; nothing assigns to it
var DefaultAlarm = Alarm{Enabled: false, Time: "07:00", Slot: 1, Timezone: ""}

// Why an alarm is refused; the text is what the web UI shows.
var (
	errAlarmTimeFormat = errors.New("alarm time must be HH:MM")
	errAlarmTimeRange  = errors.New("alarm time must be between 00:00 and 23:59")
	errAlarmSlot       = errors.New("alarm slot must be")
	errAlarmTimezone   = errors.New("unknown alarm timezone")
)

// decimal is the base of the alarm time's digits.
const decimal = 10

// ParseAlarmTime parses a 24-hour "HH:MM" and returns hour and minute. Exactly two
// digits each, so "7:00" and "07:00:00" are refused rather than guessed at.
func ParseAlarmTime(hhmm string) (int, int, error) {
	if len(hhmm) != len("HH:MM") || hhmm[2] != ':' {
		return 0, 0, errAlarmTimeFormat
	}

	for _, i := range []int{0, 1, 3, 4} {
		if hhmm[i] < '0' || hhmm[i] > '9' {
			return 0, 0, errAlarmTimeFormat
		}
	}

	hour := int(hhmm[0]-'0')*decimal + int(hhmm[1]-'0')

	minute := int(hhmm[3]-'0')*decimal + int(hhmm[4]-'0')
	if hour > 23 || minute > 59 {
		return 0, 0, errAlarmTimeRange
	}

	return hour, minute, nil
}

// Validate reports whether a is an alarm the scheduler can act on.
func (a Alarm) Validate() error {
	_, _, err := ParseAlarmTime(a.Time)
	if err != nil {
		return err
	}

	if a.Slot < 1 || a.Slot > speaker.PresetSlots {
		return fmt.Errorf("%w 1..%d", errAlarmSlot, speaker.PresetSlots)
	}

	if a.Timezone != "" {
		_, err = time.LoadLocation(a.Timezone)
		if err != nil {
			return fmt.Errorf("%w %q", errAlarmTimezone, a.Timezone)
		}
	}

	return nil
}

// AlarmConfig is the stored alarm with the defaults filled in for whatever an older
// settings file left out.
//
//nolint:gocritic // hugeParam: a value receiver, as callers read it off the copy Get returns
func (s Settings) AlarmConfig() Alarm {
	alarm := s.Alarm
	if alarm.Time == "" {
		alarm.Time = DefaultAlarm.Time
	}

	if alarm.Slot == 0 {
		alarm.Slot = DefaultAlarm.Slot
	}

	return alarm
}
