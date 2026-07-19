// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"cmp"
	"context"
	"fmt"
	"strconv"

	"github.com/stein155/soundtap/app/internal/speaker"
)

func (s *Speaker) StorePreset(ctx context.Context, slot int, source, itemType, location, name, art string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if slot < 1 || slot > speaker.PresetSlots {
		return fmt.Errorf("preset slot out of range")
	}
	if err := speaker.CheckPreset(name, art); err != nil {
		return err
	}
	return s.savePreset(slot, &Preset{Source: source, Type: itemType, Location: location, Name: name, Art: art})
}

// savePreset persists a preset change (nil removes the slot) and only then applies it
// in memory.
func (s *Speaker) savePreset(slot int, p *Preset) error {
	s.presetMu.Lock()
	defer s.presetMu.Unlock()
	s.lock()
	persist := s.presetChange
	s.unlock()
	if persist != nil {
		var stored *speaker.Preset
		if p != nil {
			stored = &speaker.Preset{Slot: slot, StationID: stationIDFromLocation(p.Location), Source: p.Source, Type: p.Type, Location: p.Location, Name: p.Name, Logo: p.Art}
		}
		if err := persist(slot, stored); err != nil {
			return err
		}
	}
	s.lock()
	if p == nil {
		delete(s.presets, slot)
	} else {
		s.presets[slot] = *p
	}
	notify := s.onPlayChange
	s.unlock()
	if notify != nil {
		notify()
	}
	return nil
}

// SwapPresets exchanges what two slots hold; an empty slot swaps too, which moves the
// other one. It holds presetMu from the read to the write and persists once, so neither
// an interruption nor another preset writer (a second swap, a held preset key) can lose
// or duplicate a preset. A failed write changes nothing.
func (s *Speaker) SwapPresets(ctx context.Context, a, b int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a < 1 || a > speaker.PresetSlots || b < 1 || b > speaker.PresetSlots {
		return fmt.Errorf("preset slot out of range")
	}
	if a == b {
		return nil
	}
	s.presetMu.Lock()
	defer s.presetMu.Unlock()
	s.lock()
	persist := s.presetSwap
	_, hasA := s.presets[a]
	_, hasB := s.presets[b]
	s.unlock()
	if !hasA && !hasB {
		return nil
	}
	if persist != nil {
		if err := persist(a, b); err != nil {
			return err
		}
	}
	s.lock()
	pa, hasA := s.presets[a]
	pb, hasB := s.presets[b]
	delete(s.presets, a)
	delete(s.presets, b)
	if hasA {
		s.presets[b] = pa
	}
	if hasB {
		s.presets[a] = pb
	}
	notify := s.onPlayChange
	s.unlock()
	if notify != nil {
		notify()
	}
	return nil
}

func (s *Speaker) RemovePreset(ctx context.Context, slot int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.savePreset(slot, nil)
}

// StoreCurrentAsPreset saves the currently-playing content to the slot named by key
// ("PRESET_1".."PRESET_6"), mirroring stock's press-and-hold "store". No-op unless a
// storable source (TuneIn or UPnP with a location) is playing. Returns true only
// after durable storage and the in-memory update both succeed.
// RememberSpotify records what the web UI just started on Spotify, so the speaker can
// name it and a held preset key can store it. librespot claims the amp a moment later
// (SetSource); if Spotify already owns it, the item applies at once.
func (s *Speaker) RememberSpotify(uri, name, art string) {
	s.lock()
	if s.source == sourceSpotify {
		s.location, s.station, s.art = uri, name, art
		s.spotifyItem = Preset{}
	} else {
		s.spotifyItem = Preset{Source: sourceSpotify, Type: "uri", Location: uri, Name: name, Art: art}
	}
	notify := s.onPlayChange
	s.unlock()
	if notify != nil {
		notify()
	}
}

// FollowSpotify takes over what Spotify really plays after a phone picked something else on
// this speaker: a long press stores that, not what was started here. "" says it
// cannot be stored.
func (s *Speaker) FollowSpotify(uri string) {
	s.lock()
	defer s.unlock()
	if s.source == sourceSpotify && s.location != uri {
		s.location, s.station = uri, ""
	}
}

func (s *Speaker) StoreCurrentAsPreset(key string) (bool, error) {
	slot, ok := presetSlot(key)
	if !ok {
		return false, nil
	}
	s.lock()
	if (s.source != sourceTuneIn && s.source != sourceUPnP && s.source != sourceSpotify) || s.location == "" {
		s.unlock()
		return false, nil // push-only/AUX sources, and Spotify started from a phone, have no recallable locator
	}
	itemType := "stationurl"
	switch s.source {
	case sourceUPnP:
		itemType = "audioItem"
	case sourceSpotify:
		itemType = "uri"
	}
	// Spotify picked on a phone has no list name here: the track names it.
	p := Preset{Source: s.source, Type: itemType, Location: s.location, Name: cmp.Or(s.station, s.track), Art: s.art}
	s.unlock()
	if err := s.savePreset(slot, &p); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Speaker) PlayPreset(ctx context.Context, slot int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.Wake(ctx)
	return s.Key(ctx, "PRESET_"+strconv.Itoa(slot))
}
