// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"net/http"
)

// getTone is the soundbar's tone levels with their ranges. Only a soundbar has them, and
// not before its processor has answered.
func (s *Server) getTone(w http.ResponseWriter, _ *http.Request) {
	if s.tone == nil {
		apiError(w, http.StatusNotFound, "this speaker has no tone controls")

		return
	}

	tone := s.tone()
	if tone == nil {
		apiError(w, http.StatusServiceUnavailable, "the soundbar has not answered yet")

		return
	}

	writeJSON(w, http.StatusOK, tone)
}

// putTone sets all four levels at once, as the processor takes them.
func (s *Server) putTone(w http.ResponseWriter, r *http.Request) {
	if s.setTone == nil {
		apiError(w, http.StatusNotFound, "this speaker has no tone controls")

		return
	}

	var body struct {
		Bass   *int `json:"bass"`
		Treble *int `json:"treble"`
		Centre *int `json:"centre"`
		Rear   *int `json:"rear"`
	}

	err := decodeJSON(w, r, tinyBody, &body)
	if err != nil ||
		body.Bass == nil || body.Treble == nil || body.Centre == nil || body.Rear == nil {
		refuseBody(w, "bass, treble, centre and rear are required", err)

		return
	}

	err = s.setTone(*body.Bass, *body.Treble, *body.Centre, *body.Rear)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())

		return
	}

	s.getTone(w, nil)
}

// startADAPTiQ starts the soundbar's calibration. It plays test tones and replaces the room
// calibration when it finishes, so the caller has to mean it.
func (s *Server) startADAPTiQ(w http.ResponseWriter, r *http.Request) {
	if s.startCalibration == nil {
		apiError(w, http.StatusNotFound, "this speaker has no ADAPTiQ")

		return
	}

	var body struct {
		Confirm bool `json:"confirm"`
	}

	err := decodeJSON(w, r, tinyBody, &body)
	if err != nil || !body.Confirm {
		refuseBody(w, "confirm must be true", err)

		return
	}

	err = s.startCalibration()
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, err.Error())

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type soundbarChoice struct {
	get func() string
	set func(string) error
}

// soundbarChoiceValues are a soundbar's one-of-a-few settings and their values, in the
// order the app offers them: stock's audio mode, CEC mode and the remote's
// HDMI input button.
func soundbarChoiceValues() map[string][]string {
	return map[string][]string{
		"audio-mode": {"normal", "dialog", "night", "direct"},
		"cec-mode":   {"on", "alternate", "fine", "off"},
		"hdmi-input": {"none", "1", "2", "3"},
	}
}

// getChoice answers {mode, modes} for one of soundbarChoiceValues. mode is "" while
// unknown: the processor reports CEC mode and HDMI input only after one was set, so the
// app must be able to offer the choice before it knows the current one.
func (s *Server) getChoice(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		choice, ok := s.choices[name]
		if !ok {
			apiError(w, http.StatusNotFound, "this speaker has no "+name)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"mode": choice.get(), "modes": soundbarChoiceValues()[name]})
	}
}

func (s *Server) putChoice(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		choice, ok := s.choices[name]
		if !ok {
			apiError(w, http.StatusNotFound, "this speaker has no "+name)

			return
		}

		var body struct {
			Mode string `json:"mode"`
		}

		err := decodeJSON(w, r, tinyBody, &body)
		if err != nil || body.Mode == "" {
			refuseBody(w, "mode is required", err)

			return
		}

		err = choice.set(body.Mode)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())

			return
		}

		s.getChoice(name)(w, r)
	}
}

// getLipSync answers {ms, max}; ms is null until the soundbar has told.
func (s *Server) getLipSync(w http.ResponseWriter, _ *http.Request) {
	if s.lipSync == nil {
		apiError(w, http.StatusNotFound, "this speaker has no lip-sync delay")

		return
	}

	var ms *int
	if v, ok := s.lipSync(); ok {
		ms = &v
	}

	writeJSON(w, http.StatusOK, map[string]any{"ms": ms, "max": s.lipSyncMax})
}

func (s *Server) putLipSync(w http.ResponseWriter, r *http.Request) {
	if s.setLipSync == nil {
		apiError(w, http.StatusNotFound, "this speaker has no lip-sync delay")

		return
	}

	var body struct {
		MS *int `json:"ms"`
	}

	err := decodeJSON(w, r, tinyBody, &body)
	if err != nil || body.MS == nil {
		refuseBody(w, "ms is required", err)

		return
	}

	err = s.setLipSync(*body.MS)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())

		return
	}

	s.getLipSync(w, r)
}
