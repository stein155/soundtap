// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"errors"
	"testing"

	"github.com/stein155/soundtap/app/internal/web/firmware"
)

// errNoModelRead is a model that could not be read.
var errNoModelRead = errors.New("unknown model")

func TestNotificationVolumeByModel(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
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
		srv := new(Server)
		srv.fw = firmware.New(nil, "")
		srv.fw.SetModel(func() (string, error) {
			if testCase.model == "" {
				return "", errNoModelRead
			}

			return testCase.model, nil
		})

		if got := srv.notificationVolume(testCase.volume); got != testCase.want {
			t.Errorf("%s volume %d = %d, want %d", testCase.model, testCase.volume, got, testCase.want)
		}
	}
}
