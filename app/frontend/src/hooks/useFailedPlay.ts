// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef } from 'react';
import { useI18n } from '../lib/i18n';
import type { NowPlaying } from '../lib/types';

// A selection the speaker could not start falls back to stopped with no other sign, as if
// the tap never happened. Says so once per selection, however often the speaker retries it,
// and not for a failure already there when the page opened or a room was switched to.
export function useFailedPlay(
  speaker: string | undefined,
  now: NowPlaying | null | undefined,
  notify: (text: string) => void
): void {
  const { t } = useI18n();
  const failed = now?.failed === true;
  const station = now?.stationName;
  const playing = now?.playStatus === 'PLAY_STATE';
  const told = useRef({ speaker, station, done: failed });
  useEffect(() => {
    const before = told.current;
    const fresh = before.speaker === speaker;
    const done = fresh && !playing && before.station === station ? before.done : !fresh && failed;
    told.current = { speaker, station, done: done || failed };
    if (failed && !done) notify(t('playFailed'));
  }, [speaker, station, playing, failed, notify, t]);
}
