// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef } from 'react';
import type { useI18n } from '../../lib/i18n';
import type { Notify, ReportFailure } from '../useErrorReporter';
import type { SelectedRoom } from '../useSelectedRoom';

// What every part of usePlayback works with.
export interface PlaybackContext {
  live: SelectedRoom['live'];
  speaker: string;
  lead: SelectedRoom['lead'];
  roomsState: SelectedRoom['roomsState'];
  failed: ReportFailure;
  // failed, said of the selected group's lead.
  leadFailed: ReturnType<ReportFailure>;
  notify?: Notify | undefined;
  t: ReturnType<typeof useI18n>['t'];
  // The duplicate-tap guard: begin is false while the same target is still on its way.
  begin: (key: string) => boolean;
  settle: (key: string) => void;
}

export interface Settling {
  begin: (key: string) => boolean;
  settle: (key: string) => void;
}

// A tap can register twice (touch then click, or an impatient double-tap), firing two
// play requests that fight each other. A repeat tap on the same target is ignored until
// its request has settled. The key is the speaker plus the preset slot or station id,
// never the display name: two stations or two rooms can share a name.
export function useSettling(live: SelectedRoom['live']): Settling {
  const switching = useRef<Set<string>>(new Set());
  const begin = (key: string) => {
    if (switching.current.has(key)) return false;
    switching.current.add(key);
    return true;
  };
  const settle = (key: string) => {
    switching.current.delete(key);
    void live.refreshNowPlaying();
  };
  return { begin, settle };
}
