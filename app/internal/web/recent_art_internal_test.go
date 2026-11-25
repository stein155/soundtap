// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// A recent entry must never carry the speaker's file:// display icon: the browser can't
// load it and the tile then shows no logo at all.
func TestBrowserArtDropsLocalIcons(t *testing.T) {
	t.Parallel()

	for art, want := range map[string]string{
		speaker.InternetRadioIcon: "",
		"":                        "",
		"http://cdn-radiotime-logos.tunein.com/s6712g.png": "http://cdn-radiotime-logos.tunein.com/s6712g.png",
		"https://i.scdn.co/image/ab67":                     "https://i.scdn.co/image/ab67",
	} {
		if got := browserArt(art); got != want {
			t.Errorf("browserArt(%q) = %q, want %q", art, got, want)
		}
	}
}
