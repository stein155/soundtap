// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import type { SearchMode } from '../../components/organisms/SearchSheet';
import type { FavoriteEntry } from '../../lib/types';

type Layer = 'drawer' | 'presets' | 'settings' | 'speakers' | 'group' | 'nowPlaying' | 'volume';
const NONE: Record<Layer, boolean> = {
  drawer: false,
  presets: false,
  settings: false,
  speakers: false,
  group: false,
  nowPlaying: false,
  volume: false,
};

// What a long press in a browse list holds, on its way to a preset.
interface Saving {
  entry: FavoriteEntry;
  store: (slot: number) => void;
}

// What is open on the home page: the layers over it, and the source page under them.
export interface Layers {
  open: Record<Layer, boolean>;
  show: (layer: Layer) => void;
  hide: (layer: Layer) => void;
  settingsFor: string | null;
  showSettings: (speakerId: string | null) => void;
  search: SearchMode | null;
  setSearch: (mode: SearchMode | null) => void;
  allRecent: boolean;
  setAllRecent: (all: boolean) => void;
  // The media servers, a source of their own from the menu: a main page like the others.
  mediaServers: boolean;
  setMediaServers: (open: boolean) => void;
  openMediaServers: () => void;
  saving: Saving | null;
  setSaving: (saving: Saving | null) => void;
  holdForPreset: (entry: FavoriteEntry, store: (slot: number) => void) => void;
  openSearch: (mode: SearchMode) => void;
}

export function useLayers(): Layers {
  // Layers open independently, so one opened from another (the presets from now playing)
  // closes back onto it.
  const [open, setOpen] = useState(NONE);
  const show = (layer: Layer) => setOpen((o) => ({ ...o, [layer]: true }));
  const hide = (layer: Layer) => setOpen((o) => ({ ...o, [layer]: false }));
  // The speaker settings opens at: none from the menu, one from its card's gear.
  const [settingsFor, setSettingsFor] = useState<string | null>(null);
  const [search, setSearch] = useState<SearchMode | null>(null);
  // The whole recently played list, a main page like the sources.
  const [allRecent, setAllRecent] = useState(false);
  const [mediaServers, setMediaServers] = useState(false);
  // Settings is a main page too: the other layers close, the bars stay below it.
  const showSettings = (speakerId: string | null) => {
    setSettingsFor(speakerId);
    setOpen({ ...NONE, settings: true });
    setSearch(null);
    setAllRecent(false);
    setMediaServers(false);
  };
  const [saving, setSaving] = useState<Saving | null>(null);
  const holdForPreset = (entry: FavoriteEntry, store: (slot: number) => void) => {
    setSaving({ entry, store });
    show('presets');
  };
  // A source page is a main page: the layers above it close first.
  const openSearch = (mode: SearchMode) => {
    setOpen(NONE);
    setAllRecent(false);
    setMediaServers(false);
    setSearch(mode);
  };
  const openMediaServers = () => {
    setOpen(NONE);
    setAllRecent(false);
    setSearch(null);
    setMediaServers(true);
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
    mediaServers,
    setMediaServers,
    openMediaServers,
    saving,
    setSaving,
    holdForPreset,
    openSearch,
  };
}
