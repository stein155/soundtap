// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"github.com/stein155/soundtap/app/internal/safego"
)

func (s *Server) chimeEvent(name string) {
	if s.eventChime != nil {
		chime := s.eventChime

		safego.Go("web.eventChime", func() { chime(name) })
	}
}
