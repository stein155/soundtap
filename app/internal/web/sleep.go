// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stein155/soundtap/app/internal/sleeptimer"
)

// The sleep timer and the power switch: the two ways a person turns the speaker off
// from the interface, and the one place that decides what "off" does.
//
// The timer runs here, on the speaker, and not in the page. A sleep timer is set by
// somebody about to stop looking at their phone; a countdown in a browser tab dies with
// the tab, the screen lock or the phone's battery saver, and the radio plays on all
// night. It is deliberately in memory only: a speaker that reboots comes up in standby,
// so a timer that survived the reboot would have nothing left to turn off.

// sleepMaxMinutes is the longest timer the API accepts. Four hours covers falling asleep
// to a long programme; anything longer is a typo, not a sleep timer.
const sleepMaxMinutes = 240

// sleepMaxBody bounds the small JSON bodies of POST /api/sleep and POST /api/power.
const sleepMaxBody = 1024

func (s *Server) getSleep(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sleep.View())
}

// postSleep sets ({"minutes": 1..240}) or cancels ({"minutes": 0}) the sleep timer. A
// new value replaces the old one rather than adding to it, which is what the buttons in
// the UI mean. Setting one in standby is refused (409): there is nothing to turn off.
func (s *Server) postSleep(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Minutes *int `json:"minutes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, sleepMaxBody)).Decode(&body); err != nil || body.Minutes == nil {
		s.badRequest(w, "minutes required", err)
		return
	}
	if *body.Minutes < 0 || *body.Minutes > sleepMaxMinutes {
		s.badRequest(w, fmt.Sprintf("minutes must be 0..%d", sleepMaxMinutes), nil)
		return
	}
	// A timer set in standby would cut off whatever is played next at the old endsAt.
	if *body.Minutes > 0 && s.inStandby(r.Context()) {
		apiError(w, http.StatusConflict, "the speaker is in standby")
		return
	}
	unit := s.sleepUnit
	if unit <= 0 {
		unit = time.Minute
	}
	retry := s.sleepRetry
	if retry <= 0 {
		retry = sleeptimer.RetryEvery
	}
	s.sleep.Set(*body.Minutes, unit, retry, s.sleepExpired)
	if *body.Minutes > 0 {
		s.log.Info("sleep timer set", "minutes", *body.Minutes)
	}
	s.hub.nudge()
	writeJSON(w, http.StatusOK, s.sleep.View())
}

// sleepExpired is the timer going off: exactly what POST /api/stop does. run is the
// timer's own context, cancelled when the timer is cancelled or replaced, so a timer
// stopped while this runs does not reach standby. A failure is returned so the timer
// tries again (sleeptimer.Attempts).
func (s *Server) sleepExpired(run context.Context, attempt int) error {
	ctx, cancel := context.WithTimeout(run, 6*time.Second+s.offSlack)
	defer cancel()
	if err := s.enterStandby(ctx); err != nil {
		if run.Err() != nil {
			return err // cancelled or replaced: nothing failed
		}
		if attempt >= sleeptimer.Attempts {
			s.log.Warn("sleep timer: standby failed; giving up", "attempt", attempt, "err", err)
		} else {
			s.log.Warn("sleep timer: standby failed; will retry", "attempt", attempt, "err", err)
		}
		s.hub.nudge()
		return err
	}
	s.log.Info("sleep timer: standby")
	return nil
}

// enterStandby is "off" from a person: POST /api/stop, POST /api/power {"on":false} and
// the sleep timer all come here, so they cannot drift apart. It records what was playing
// in the recent list first (standby wipes it), then puts the speaker in standby. What
// standby itself implies -- the sleep timer has nothing left to do, followers are let go
// -- is standbyEntered, which runs for every standby, not just these.
func (s *Server) enterStandby(ctx context.Context) error {
	s.snapshotRecent(ctx)
	// The snapshot is I/O; a sleep timer cancelled meanwhile must not still turn off.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.backend.EnterStandby(ctx); err != nil {
		return err
	}
	if !s.standbyWatched.Load() {
		// A backend that does not report standby itself: do it here, for the paths
		// that come through this function at least.
		s.standbyEntered()
	}
	s.hub.nudge()
	return nil
}

// standbyNotifier is a backend that reports every transition into standby -- the
// POWER key, auto-off, POST /api/source STANDBY, MQTT, a source session ending -- not
// only the ones the web API asked for. The in-process speaker state is one.
type standbyNotifier interface {
	OnStandby(fn func())
}

// WatchStandby has b report every standby to this server, so the sleep timer is
// cancelled and followers are released however the speaker was turned off. soundtapd
// wires its speaker state here.
func (s *Server) WatchStandby(b standbyNotifier) {
	b.OnStandby(s.standbyEntered)
	s.standbyWatched.Store(true)
}

// standbyEntered is what standby means beyond the amp going quiet, whatever caused it:
// a running sleep timer has nothing left to turn off, a sounding alarm is dismissed, and the followers are released
// when this speaker's owner asked for groups not to outlive it (a stereo pair always
// outlives it: standby is the pair's, see WatchLeaderPower). Followers keep playing
// a stream that has gone silent otherwise. Both steps are cheap and non-blocking, so
// this is safe to run on the backend's goroutine.
//
// The sleep timer is cancelled only once standby has happened: if the speaker could not
// be turned off, a timer that tries again is still what the person wanted. A timer set
// while an older one was turning the speaker off is not the one that ran out, and stays.
func (s *Server) standbyEntered() {
	s.sleep.CancelFiring()
	s.dismissAlarm()
	if s.settings != nil && !s.settings.Get().Multiroom.KeepGroups() && !s.paired() {
		s.group.ReleaseFollowers(s.groupEnv())
	}
	s.hub.nudge()
}

// inStandby reports whether the speaker is known to be in standby.
func (s *Server) inStandby(ctx context.Context) bool {
	np, err := s.backend.NowPlaying(ctx)
	return err == nil && np.Source == standbySource
}

// power is the on/off switch.
//
// Off is standby, exactly as POST /api/stop. On is Backend.Wake, and on this speaker that
// means less than "start playing what played before": speakerstate leaves standby into
// the last source it can replay by itself -- a tuned TuneIn station, which starts
// buffering and plays again, or AUX. A source that needs a phone to push to it (Spotify,
// AirPlay, Bluetooth, UPnP) has no session left after standby, so waking into it would
// show a source producing nothing; the speaker wakes into TuneIn instead, which without a
// station is on but silent (STOP_STATE). Waking a speaker that is already on does nothing.
func (s *Server) power(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, sleepMaxBody)).Decode(&body); err != nil || body.On == nil {
		s.badRequest(w, "on required", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second+s.offSlack)
	defer cancel()
	if !*body.On {
		if err := s.enterStandby(ctx); err != nil {
			s.fail(w, "power off failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"on": false})
		return
	}
	// On is the power key, not a bare wake: it resumes what played last.
	if s.inStandby(ctx) {
		if err := s.backend.Key(ctx, "POWER"); err != nil {
			s.fail(w, "power on failed", err)
			return
		}
	}
	s.hub.nudge()
	writeJSON(w, http.StatusOK, map[string]bool{"on": true})
}
