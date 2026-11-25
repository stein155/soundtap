// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import type { SearchMode } from '../../components/organisms/SearchSheet';
import type { FavoriteEntry } from '../../lib/types';

type Layer =
  | 'drawer'
  | 'presets'
  | 'settings'
  | 'mediaServers'
  | 'speaker'
  | 'speakers'
  | 'nowPlaying'
  | 'volume';
const NONE: Record<Layer, boolean> = {
  drawer: false,
  presets: false,
  settings: false,
  mediaServers: false,
  speaker: false,
  speakers: false,
  nowPlaying: false,
  volume: false,
};

// What a long press in a browse list holds, on its way to a preset.
type Saving = { entry: FavoriteEntry; store: (slot: number) => void };

// What is open on the home page: the layers over it, and the source page under them.
export function useLayers() {
  // Layers open independently, so one opened from another (the presets from now playing)
  // closes back onto it.
  const [open, setOpen] = useState(NONE);
  const show = (layer: Layer) => setOpen((o) => ({ ...o, [layer]: true }));
  const hide = (layer: Layer) => setOpen((o) => ({ ...o, [layer]: false }));
  // The speaker settings opens at: none from the menu, one from its card's gear.
  const [settingsFor, setSettingsFor] = useState<string | null>(null);
  const showSettings = (speakerId: string | null) => {
    setSettingsFor(speakerId);
    show('settings');
  };
  const [search, setSearch] = useState<SearchMode | null>(null);
  // The whole recently played list, a main page like the sources.
  const [allRecent, setAllRecent] = useState(false);
  const [saving, setSaving] = useState<Saving | null>(null);
  const holdForPreset = (entry: FavoriteEntry, store: (slot: number) => void) => {
    setSaving({ entry, store });
    show('presets');
  };
  // A source page is a main page: the layers above it close first.
  const openSearch = (mode: SearchMode) => {
    setOpen(NONE);
    setAllRecent(false);
    setSearch(mode);
  };
  return {
    open,
    show,
    hide,
    settingsFor,
    showSettings,
    search,
    setSearch,
    allRecent,
    setAllRecent,
    saving,
    setSaving,
    holdForPreset,
    openSearch,
  };
}

export type Layers = ReturnType<typeof useLayers>;
