// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"cmp"
	"context"
	"strconv"

	"github.com/stein155/soundtap/app/internal/speaker"
)

func (s *Speaker) StorePreset(ctx context.Context, slot int, source, itemType, location, name, art string) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	if slot < 1 || slot > speaker.PresetSlots {
		return errPresetSlot
	}

	err = speaker.CheckPreset(name, art)
	if err != nil {
		return err //nolint:wrapcheck // CheckPreset words it for the client, which shows it as it is.
	}

	return s.savePreset(slot, &Preset{Source: source, Type: itemType, Location: location, Name: name, Art: art})
}

// savePreset persists a preset change (nil removes the slot) and only then applies it
// in memory.
func (s *Speaker) savePreset(slot int, preset *Preset) error {
	s.presetMu.Lock()
	defer s.presetMu.Unlock()

	s.lock()
	persist := s.presetChange
	s.unlock()

	if persist != nil {
		var stored *speaker.Preset
		if preset != nil {
			stored = &speaker.Preset{
				Slot: slot, StationID: stationIDFromLocation(preset.Location), Source: preset.Source, Type: preset.Type,
				Location: preset.Location, Name: preset.Name, Logo: preset.Art,
			}
		}

		err := persist(slot, stored)
		if err != nil {
			return err
		}
	}

	s.lock()

	if preset == nil {
		delete(s.presets, slot)
	} else {
		s.presets[slot] = *preset
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
func (s *Speaker) SwapPresets(ctx context.Context, slotA, slotB int) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	if slotA < 1 || slotA > speaker.PresetSlots || slotB < 1 || slotB > speaker.PresetSlots {
		return errPresetSlot
	}

	if slotA == slotB {
		return nil
	}

	s.presetMu.Lock()
	defer s.presetMu.Unlock()

	s.lock()
	persist := s.presetSwap
	_, hasA := s.presets[slotA]
	_, hasB := s.presets[slotB]
	s.unlock()

	if !hasA && !hasB {
		return nil
	}

	if persist != nil {
		err := persist(slotA, slotB)
		if err != nil {
			return err
		}
	}

	s.lock()
	first, hasA := s.presets[slotA]
	second, hasB := s.presets[slotB]
	delete(s.presets, slotA)
	delete(s.presets, slotB)

	if hasA {
		s.presets[slotB] = first
	}

	if hasB {
		s.presets[slotA] = second
	}

	notify := s.onPlayChange
	s.unlock()

	if notify != nil {
		notify()
	}

	return nil
}

func (s *Speaker) RemovePreset(ctx context.Context, slot int) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	return s.savePreset(slot, nil)
}

// RememberSpotify records what the web UI just started on Spotify, so the speaker can
// name it and a held preset key can store it. librespot claims the amp a moment later
// (SetSource); if Spotify already owns it, the item applies at once.
func (s *Speaker) RememberSpotify(uri, name, art string) {
	s.lock()

	if s.source == sourceSpotify {
		s.location, s.station, s.art = uri, name, art
		s.spotifyItem = Preset{Source: "", Type: "", Location: "", Name: "", Art: ""}
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

// StoreCurrentAsPreset saves the currently-playing content to the slot named by key
// ("PRESET_1".."PRESET_6"), mirroring stock's press-and-hold "store". No-op unless a
// storable source (TuneIn or UPnP with a location) is playing. Returns true only
// after durable storage and the in-memory update both succeed.
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
	default: // a station
	}
	// Spotify picked on a phone has no list name here: the track names it.
	p := Preset{Source: s.source, Type: itemType, Location: s.location, Name: cmp.Or(s.station, s.track), Art: s.art}
	s.unlock()

	err := s.savePreset(slot, &p)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *Speaker) PlayPreset(ctx context.Context, slot int) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	s.Wake(ctx)

	return s.Key(ctx, "PRESET_"+strconv.Itoa(slot))
}
