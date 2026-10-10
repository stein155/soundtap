// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"time"

	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/snapcast"
)

var snapcastIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:#-]{0,127}$`)

func (s *Server) getMultiroom(w http.ResponseWriter, r *http.Request) {
	status, ok := s.multiroomStatus(w, r)
	if !ok {
		return
	}
	if s.multiroomActive != nil {
		s.multiroomActive(multiroom.HasListener(status, s.snapclientID))
	}
	names := map[string]string{}
	for _, dev := range s.knownDevices(Device{}) {
		names[dev.ID] = dev.Name
	}
	name, volume := "", 0
	if s.settings != nil {
		current := s.settings.Get()
		name, volume = current.Name, current.Audio.Volume
	}
	writeJSON(w, http.StatusOK, multiroom.Clients(status, s.snapclientID, names, name, volume))
}

func (s *Server) putMultiroomVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !snapcastIDRE.MatchString(id) {
		s.badRequest(w, "invalid client id", nil)
		return
	}
	var body struct {
		Percent *int  `json:"percent"`
		Muted   *bool `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Percent == nil || body.Muted == nil {
		s.badRequest(w, "percent and muted required", err)
		return
	}
	if *body.Percent < 0 || *body.Percent > 100 {
		s.badRequest(w, "percent must be 0..100", nil)
		return
	}
	if !s.multiroomWired(w) {
		return
	}
	volume, err := multiroom.SetVolume(r.Context(), s.groupEnv(), id, snapcast.Volume{Percent: *body.Percent, Muted: *body.Muted})
	if err != nil {
		s.failMultiroom(w, err)
		return
	}
	writeJSON(w, http.StatusOK, volume)
}

func (s *Server) putMultiroomMembership(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !snapcastIDRE.MatchString(id) {
		s.badRequest(w, "invalid client id", nil)
		return
	}
	var body struct {
		Member *bool `json:"member"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Member == nil {
		s.badRequest(w, "member required", err)
		return
	}
	if !s.multiroomWired(w) {
		return
	}
	if err := s.group.SetMember(r.Context(), s.groupEnv(), id, *body.Member); err != nil {
		s.failMultiroom(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"member": *body.Member})
}

// failMultiroom answers a failed multiroom.SetVolume or Group.SetMember.
func (s *Server) failMultiroom(w http.ResponseWriter, err error) {
	var failed *multiroom.JoinError
	switch {
	case errors.Is(err, multiroom.ErrNoLocal):
		apiError(w, http.StatusConflict, multiroom.ErrNoLocal.Error())
	case errors.Is(err, multiroom.ErrNoClient):
		apiError(w, http.StatusNotFound, multiroom.ErrNoClient.Error())
	case errors.Is(err, multiroom.ErrRemoveLocal):
		s.badRequest(w, multiroom.ErrRemoveLocal.Error(), nil)
	case errors.As(err, &failed):
		s.fail(w, failed.Step.Error(), failed.Err)
	default:
		s.fail(w, "multiroom failed", err)
	}
}

// multiroomActivePath is the marker that keeps the multiroom tap on across a restart.
func (s *Server) multiroomActivePath() string {
	if s.homeDir == "" {
		return ""
	}
	return filepath.Join(s.homeDir, "multiroom-enabled")
}

// multiroomWired answers 503 when this speaker has no snapserver.
func (s *Server) multiroomWired(w http.ResponseWriter) bool {
	if s.multiroom == nil {
		apiError(w, http.StatusServiceUnavailable, "multiroom unavailable")
		return false
	}
	return true
}

func (s *Server) multiroomStatus(w http.ResponseWriter, r *http.Request) (snapcast.Status, bool) {
	if !s.multiroomWired(w) {
		return snapcast.Status{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := s.multiroom.GetStatus(ctx)
	if err != nil {
		s.fail(w, "multiroom status failed", err)
		return snapcast.Status{}, false
	}
	return status, true
}

// multiroomStandbyEvery thins WatchMultiroom out in standby: every 15 s at the 3 s tick.
const multiroomStandbyEvery = 5

// WatchMultiroom keeps the tap and the DAC delay in step with Snapcast. A follower can join
// from its own page, which this speaker never hears about through its API, and with the tap
// off while nobody listens a solo speaker no longer pays snapserver to encode its audio.
func (s *Server) WatchMultiroom(ctx context.Context, every time.Duration) {
	if s.multiroom == nil || s.multiroomActive == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for tick := 0; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// In standby nothing plays to share: snapserver is asked on every
		// multiroomStandbyEvery-th tick only, and again on the first tick after waking.
		if tick%multiroomStandbyEvery != 0 && s.inStandby(ctx) {
			continue
		}
		multiroom.SyncListeners(ctx, s.groupEnv())
	}
}

// standbySource is the source name of a speaker that is off.
const standbySource = "STANDBY"
