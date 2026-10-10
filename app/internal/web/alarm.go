// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"time"

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// The wake-up alarm. Like the sleep timer it runs on the speaker: an alarm that lives in
// a browser tab only goes off if somebody left a phone awake on the night stand with the
// page open, which is not an alarm.
//
// The check is a poll, not a timer aimed at the next alarm time. A 20-second tick is
// three chances inside every minute, it needs no rescheduling when the alarm, the town
// (and so the zone) or the system clock changes, and it costs one settings read. What
// it does need is a memory of the minute it already acted on, or three ticks would mean
// three alarms.

// alarmEvery is the poll interval. It must stay well under a minute: a tick that lands
// on both sides of the alarm minute and never inside it misses the alarm.
const alarmEvery = 20 * time.Second

// runAlarm checks the alarm every interval until ctx ends.
func (s *Server) runAlarm(ctx context.Context, every time.Duration) {
	if s.settings == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.checkAlarm(ctx, s.wallClock())
	}
}

func (s *Server) wallClock() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// alarmZone is where the alarm's "07:00" is read: the zone of the town picked for the
// standby clock, else the zone of the browser that saved the alarm (a speaker without a
// display has no town), else the system zone (UTC on a rootfs that ships none).
// Loaded zones are cached by name: LoadLocation reads and parses a zoneinfo file, and
// this runs on every tick.
func (s *Server) alarmZone() *time.Location {
	current := s.settings.Get()
	tz := current.Display.Timezone
	if tz == "" {
		tz = current.Alarm.Timezone
	}
	if tz == "" {
		return time.Local
	}
	if loc, ok := s.alarmZones.Load(tz); ok {
		return loc.(*time.Location)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	s.alarmZones.Store(tz, loc)
	return loc
}

// alarmDue reports whether local -- a tick, in the alarm's zone -- is when the alarm for
// hhmm should go off: inside its minute, or, on the day a clock change skips that
// minute (an alarm at 02:30 on the night summer time starts at 02:00), inside the first
// minute after the jump. The second case is a tick whose minute lies past the alarm
// while the instant one minute earlier lay before it, which on a day without a jump
// cannot happen: consecutive minutes have no whole minute between them. On the night
// the clock goes back the alarm minute happens twice; alarmFired's key keeps that to
// one alarm.
func alarmDue(local time.Time, hhmm string) bool {
	if local.Format("15:04") == hhmm {
		return true
	}
	at := local.Format("2006-01-02") + " " + hhmm
	prev := local.Add(-time.Minute).Format("2006-01-02 15:04")
	return prev < at && at < local.Format("2006-01-02 15:04")
}

// alarmSlot is the preset an alarm plays: the one it was set to or, once that was cleared,
// the first one stored. The settings page offers the same one.
func alarmSlot(slot int, presets []speaker.Preset) (int, bool) {
	first := 0
	for _, p := range presets {
		if p.Slot == slot {
			return slot, true
		}
		if first == 0 || p.Slot < first {
			first = p.Slot
		}
	}
	return first, first != 0
}

// checkAlarm plays the alarm's preset when now, in the alarm's zone, is the alarm
// minute and that minute has not been acted on yet. It reports whether it played.
//
// The minute is claimed before the presets are read and the preset played, and the
// lock is not held across that I/O: a tick that comes along meanwhile sees the claim
// and leaves it alone. A failure to read the presets or to play gives the claim back, so
// the next tick inside the same minute tries again; having no presets keeps it, because
// trying again will not store one and the log would only repeat itself.
func (s *Server) checkAlarm(ctx context.Context, now time.Time) bool {
	alarm := s.settings.Get().AlarmConfig()
	if !alarm.Enabled {
		return false
	}
	// A clock that never synced reads 1970 or the build date's day: its 07:00 is not the
	// owner's, so it goes off at a random hour. Better silent and logged.
	if now.Year() < 2020 {
		s.log.Warn("alarm: the clock is not set; skipped", "now", now)
		return false
	}
	run := ctx
	local := now.In(s.alarmZone())
	if !alarmDue(local, alarm.Time) {
		return false
	}
	// The clock restored from flash after a power cut can be hours behind; stock trusts
	// its clock only after the first NTP sync, and so does the alarm.
	if !clocksync.Synced() {
		s.log.Warn("alarm: the clock has not synced since boot; skipped", "now", now)
		return false
	}
	// The key carries the alarm time as well as the date: moving the alarm to later the
	// same morning is a new alarm, not one that already went off.
	key := local.Format("2006-01-02") + " " + alarm.Time
	s.alarmMu.Lock()
	if s.alarmFired == key {
		s.alarmMu.Unlock()
		return false
	}
	previous := s.alarmFired
	s.alarmFired = key
	s.alarmMu.Unlock()
	unclaim := func() {
		s.alarmMu.Lock()
		if s.alarmFired == key {
			s.alarmFired = previous
		}
		s.alarmMu.Unlock()
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	presets, err := s.backend.Presets(ctx)
	if err != nil {
		unclaim()
		s.log.Warn("alarm: presets unavailable", "err", err)
		return false
	}
	s.alarmVolume(ctx)
	slot, ok := alarmSlot(alarm.Slot, presets)
	if !ok {
		s.log.Warn("alarm: no presets left; the fallback tone wakes instead", "slot", alarm.Slot)
		actx := s.newAlarmRun(run)
		safego.Go("alarm.fallback", func() { s.alarmFallback(actx, 0) })
		return false
	}
	s.snapshotRecent(ctx)
	if err := s.backend.PlayPreset(ctx, slot); err != nil {
		unclaim()
		s.log.Warn("alarm: play failed", "slot", slot, "err", err)
		return false
	}
	s.log.Info("alarm", "time", alarm.Time, "slot", slot)
	s.hub.nudge()
	// The preset started, which is not the same as the stream playing: without internet
	// it buffers and stays silent. The fallback tone checks after a while.
	actx, wait := s.newAlarmRun(run), alarmFallbackAfter
	safego.Go("alarm.fallback", func() { s.alarmFallback(actx, wait) })
	return true
}

// alarmToneVolume is the fallback tone's level: the tone has one job, which is to wake
// somebody whose music did not start, so it does not depend on the volume left behind.
const alarmToneVolume = 70

// alarmMinVolume is the least an alarm plays at: a speaker left at 0 must still wake.
const alarmMinVolume = 20

// The raise lasts as long as the alarm: dismissing it puts the owner's level back.
func (s *Server) alarmVolume(ctx context.Context) {
	v, err := s.backend.Volume(ctx)
	if err != nil || (v >= alarmMinVolume && !muted(s.backend)) {
		return
	}
	// Setting a level unmutes, and Volume() keeps reporting the level a muted speaker
	// returns to, so a muted speaker above the floor is set to that same level.
	target := max(v, alarmMinVolume)
	if err := s.backend.SetVolume(ctx, target); err != nil {
		s.log.Warn("alarm: could not raise the volume", "err", err)
		return
	}
	if target == v {
		return
	}
	s.alarmMu.Lock()
	if !s.alarmRaised {
		s.alarmRaised, s.alarmRaisedFrom = true, v
	}
	s.alarmMu.Unlock()
}

// restoreAlarmVolume puts back the volume the alarm raised from, unless someone turned
// the speaker up or down while it rang: then their level is the one they want. It runs
// on its own goroutine, because standby reaches dismissAlarm on the backend's.
func (s *Server) restoreAlarmVolume(from int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if v, err := s.backend.Volume(ctx); err != nil || v != alarmMinVolume {
		return
	}
	if err := s.backend.SetVolume(ctx, from); err != nil {
		s.log.Warn("alarm: could not restore the volume", "volume", from, "err", err)
	}
}

// alarmFallbackAfter is how long a started preset gets to reach PLAY_STATE. alarmBeeps
// beeps, alarmBeepEvery apart, then stop: an alarm that never ends is worse than one
// that gives up after a minute.
var (
	alarmFallbackAfter = 45 * time.Second
	alarmBeepEvery     = 3 * time.Second
)

const (
	alarmBeeps = 20
	// AlarmClip is the built-in clip the fallback plays; soundtapd registers it with
	// SetBuiltinClip. Without it there is no fallback, only the log line.
	AlarmClip = "alarm.wav"
)

// newAlarmRun gives one alarm its own lifetime: standby or disabling the alarm ends
// it through dismissAlarm, so a fallback still waiting cannot wake a speaker the user
// already turned off. A newer alarm replaces an older run.
func (s *Server) newAlarmRun(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	s.alarmMu.Lock()
	if s.alarmStop != nil {
		s.alarmStop()
	}
	s.alarmStop = cancel
	s.alarmMu.Unlock()
	return ctx
}

// dismissAlarm ends the running alarm's fallback, if any, and gives back the volume
// it raised.
func (s *Server) dismissAlarm() {
	s.alarmMu.Lock()
	if s.alarmStop != nil {
		s.alarmStop()
		s.alarmStop = nil
	}
	raised, from := s.alarmRaised, s.alarmRaisedFrom
	s.alarmRaised = false
	s.alarmMu.Unlock()
	if raised {
		safego.Go("alarm.restoreVolume", func() { s.restoreAlarmVolume(from) })
	}
}

// alarmFallback beeps when, after wait, the speaker is not playing. Pressing power
// (standby) dismisses the alarm and so stops it.
func (s *Server) alarmFallback(ctx context.Context, wait time.Duration) {
	s.audioMu.Lock()
	_, ok := s.builtinClips[AlarmClip]
	s.audioMu.Unlock()
	if !ok || s.notifyBase == "" {
		return
	}
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
	if np, err := s.backend.NowPlaying(ctx); err == nil && np.PlayStatus == "PLAY_STATE" {
		return
	}
	if ctx.Err() != nil {
		return
	}
	s.log.Warn("alarm: nothing is playing; fallback tone")
	// Out of standby first, so that standby during the beeps means somebody pressed power.
	s.backend.Wake(ctx)
	for i := range alarmBeeps {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(alarmBeepEvery):
			}
		}
		if np, err := s.backend.NowPlaying(ctx); err == nil && np.Source == "STANDBY" {
			return
		}
		bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.backend.PlayNotification(bctx, speaker.Notification{URL: s.notifyBase + "/api/speaker/audio/" + AlarmClip, Volume: alarmToneVolume})
		cancel()
		if err != nil {
			s.log.Warn("alarm: fallback tone failed", "err", err)
			return
		}
	}
}
