// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { spotifyStatus, statusView } from '../lib/spotify';
import { versionLabel } from '../lib/version';
import { useSpeakerResource } from './speaker/resource';
import { ensureResource, refreshResource } from './speaker/store';
import type { Settings } from '../lib/types';

export interface SpeakerInfo {
  settings: Settings | null;
  version: string;
  spotifyReady: boolean;
  reload: () => void;
}

// Slow-changing facts about the selected speaker: its settings, the soundtapd version. They
// are the page's one copy (speaker/store.ts), kept per speaker: after a room switch the
// previous room's facts (its group-volume policy above all) must not stand in for the new
// one's while it loads, and a save on the settings page shows here without a reload.
export function useSpeakerInfo(speaker: string): SpeakerInfo {
  const settings = useSpeakerResource('settings', speaker);
  const version = useSpeakerResource('version', speaker);
  const [spotifyReady, setSpotifyReady] = useState(false);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    void ensureResource('settings', speaker);
    void ensureResource('version', speaker);
  }, [speaker]);

  useEffect(() => {
    let live = true;
    spotifyStatus()
      .then((s) => live && setSpotifyReady(statusView(s) === 'ready'))
      .catch(() => live && setSpotifyReady(false));
    return () => {
      live = false;
    };
  }, [speaker, generation]);

  return {
    settings: settings ?? null,
    version: versionLabel(version),
    spotifyReady,
    reload: () => {
      void refreshResource('settings', speaker);
      void refreshResource('version', speaker);
      setGeneration((g) => g + 1);
    },
  };
}
