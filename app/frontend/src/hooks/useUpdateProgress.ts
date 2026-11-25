// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { getVersion } from '../lib/api';
import { UPDATE_POLL_MS } from './useSelfUpdate';

// How far the speaker's update is while watching, as GET /api/version's updateProgress says
// (0-99); null when it does not say or the read fails, so no number is made up. For an
// update whose request stays open while the speaker downloads and writes it (a Core release),
// where nothing else reads the version meanwhile.
export function useUpdateProgress(speaker: string, watching: boolean): number | null {
  const [progress, setProgress] = useState<number | null>(null);
  useEffect(() => {
    if (!watching) return undefined;
    let live = true;
    let id: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      const v = await getVersion(speaker);
      if (!live) return;
      setProgress(v?.updateProgress ?? null);
      id = setTimeout(tick, UPDATE_POLL_MS);
    };
    id = setTimeout(tick, UPDATE_POLL_MS / 2);
    return () => {
      live = false;
      clearTimeout(id);
      setProgress(null);
    };
  }, [speaker, watching]);
  return watching ? progress : null;
}
