// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

// Why a language or display setting is refused; the text is what the web UI shows.
var (
	errNotLanguage     = errors.New("is not a language code")
	errClockNeedsZone  = errors.New("the standby clock needs a timezone")
	errUnknownTimezone = errors.New("unknown timezone")
)

var languageRe = regexp.MustCompile(`^[a-z]{2}(-[A-Za-z]{2,4})?$`)

// ValidLanguage reports whether code is a UI language code ("en", "nl", "pt-BR").
// Every writer checks it, so a stored language is one a backup restore accepts.
func ValidLanguage(code string) bool { return languageRe.MatchString(code) }

// SetLanguage updates the UI language and persists.
func (s *Store) SetLanguage(code string) error {
	if code != "" && !ValidLanguage(code) {
		return fmt.Errorf("language %q %w", code, errNotLanguage)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if code != "" {
		next := s.s
		next.Language = code

		return s.replaceLocked(&next)
	}

	return nil
}

// SetName updates the identity used by SoundTap and its streaming receivers.
func (s *Store) SetName(name string) error {
	name, err := NormalizeName(name)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.Name = name

	return s.replaceLocked(&next)
}

// SetAudio persists the current volume and tone. Called from a coalescing loop, not
// from the volume path itself: a held volume key steps every 160 ms and this lands on
// NAND, which is worth exactly one write once the user has stopped turning the knob.
func (s *Store) SetAudio(audio Audio) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	audio.AutoOff = next.Audio.AutoOff // owned by SetAutoOff, not by the audio sampler
	next.Audio = audio

	return s.replaceLocked(&next)
}

// SetAutoOff sets the idle-standby timeout in minutes: 0 restores the default,
// negative disables auto-off entirely (see Audio.AutoOff).
func (s *Store) SetAutoOff(minutes int) error {
	minutes = NormalizeAutoOff(minutes) // canonical "disabled" is -1

	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.Audio.AutoOff = minutes

	return s.replaceLocked(&next)
}

// SetChannel persists the channel this speaker plays (Settings.Channel).
func (s *Store) SetChannel(channel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.Channel = channel

	return s.replaceLocked(&next)
}

// SetMQTT replaces the MQTT bridge config and persists it.
func (s *Store) SetMQTT(cfg *MQTT) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.MQTT = *cfg

	return s.replaceLocked(&next)
}

// NormalizeDisplay validates a standby-screen configuration and returns it in the
// form it is stored in.
func NormalizeDisplay(display Display) (Display, error) {
	if display.Timezone == "" {
		if !display.ClockOff {
			return display, errClockNeedsZone
		}

		return display, nil
	}

	_, err := time.LoadLocation(display.Timezone)
	if err != nil {
		return display, fmt.Errorf("%w %q", errUnknownTimezone, display.Timezone)
	}

	return display, nil
}

// MaxAutoOff is the longest idle-standby timeout: a week, in minutes. Far longer would
// overflow time.Duration on the way to the timer and read as "off".
const MaxAutoOff = 7 * 24 * 60

// NormalizeAutoOff is the stored form of an idle-standby timeout: negative means off
// and is kept as -1, and a longer one than MaxAutoOff is cut to it.
func NormalizeAutoOff(minutes int) int {
	if minutes < 0 {
		return -1
	}

	return min(minutes, MaxAutoOff)
}

// SetDisplay persists the standby-screen configuration.
func (s *Store) SetDisplay(display Display) error {
	display, err := NormalizeDisplay(display)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.Display = display

	return s.replaceLocked(&next)
}

// SetAlarm validates and persists the wake-up alarm.
func (s *Store) SetAlarm(alarm Alarm) error {
	err := alarm.Validate()
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.Alarm = alarm

	return s.replaceLocked(&next)
}

// SetMultiroom persists the group preferences.
func (s *Store) SetMultiroom(m Multiroom) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.Multiroom = m

	return s.replaceLocked(&next)
}

// Update applies edit to a copy of the current settings and persists the result as one
// atomic write, all under the store lock: a concurrent writer cannot interleave between
// the read and the write, so two patches to different fields cannot lose each other.
// When edit returns an error nothing is written; when the write fails memory keeps the
// old settings. edit must not call back into the store.
func (s *Store) Update(edit func(*Settings) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.s
	next.SourceNames = append([]SourceName(nil), s.s.SourceNames...)

	next.CrashReports.Handled = slices.Clone(s.s.CrashReports.Handled)

	err := edit(&next)
	if err != nil {
		return err
	}

	return s.replaceLocked(&next)
}
