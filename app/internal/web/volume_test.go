// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"errors"
	"github.com/stein155/soundtap/app/internal/web/firmware"
	"testing"
)

func TestNotificationVolumeByModel(t *testing.T) {
	for _, tc := range []struct {
		model  string
		volume int
		want   int
	}{
		{"st10", 0, 50},
		{"st10", 5, 10},
		{"st10", 90, 70},
		{"wla", 5, 10},
		{"wla", 90, 90},
		{"wla", 200, 100},
		{"", 90, 70},
	} {
		s := &Server{fw: firmware.New(nil, "")}
		s.fw.SetModel(func() (string, error) {
			if tc.model == "" {
				return "", errors.New("unknown model")
			}
			return tc.model, nil
		})
		if got := s.notificationVolume(tc.volume); got != tc.want {
			t.Errorf("%s volume %d = %d, want %d", tc.model, tc.volume, got, tc.want)
		}
	}
}
