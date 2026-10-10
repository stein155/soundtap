// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { getReleases, getVersion, startUpdate } from '../lib/api';
import { failureText } from '../lib/failure';
import { useI18n } from '../lib/i18n';
import type { ReleaseRef, VersionInfo } from '../lib/types';

export type UpdatePhase = 'idle' | 'busy' | 'done' | 'updated' | 'error';

// The speaker restarts mid-update, so /api/version drops out for a while; poll it until
// the speaker is back on the target version. The speaker may take five minutes for the
// app and five more for its signature (update.go downloadTimeout) before it restarts, and
// it answers with the old version all that time. Giving up sooner called a running update
// failed, so the wait covers both downloads plus a minute to restart.
const FIRST_POLL_MS = 6000;
export const UPDATE_POLL_MS = 4000;
export const UPDATE_MAX_POLLS = (11 * 60_000) / UPDATE_POLL_MS;
const POLL_MS = UPDATE_POLL_MS;
const MAX_POLLS = UPDATE_MAX_POLLS;
// After a finished update this page still runs the old bundle; reload into the new one.
const RELOAD_MS = 2200;

// The releases a speaker can update to. A switch to another speaker drops whatever the
// previous one was still answering. clockNotSet: the speaker does not ask GitHub before
// NTP has set its clock since boot.
function useReleases(speaker: string) {
  const [stable, setStable] = useState<ReleaseRef | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [clockNotSet, setClockNotSet] = useState(false);
  useEffect(() => {
    let current = true;
    void getReleases(speaker).then((r) => {
      if (!current) return;
      setStable(r?.stable ?? null);
      setUnavailable(!r || !!r.unavailable);
      setClockNotSet(r?.unavailableCode === 'clock_not_set');
    });
    return () => {
      current = false;
    };
  }, [speaker]);
  return { stable, unavailable, clockNotSet };
}

// Self-update of soundtapd to the latest stable release.
export function useSelfUpdate(speaker: string, version: VersionInfo | null) {
  const { t } = useI18n();
  const { stable, unavailable, clockNotSet } = useReleases(speaker);
  const [phase, setPhase] = useState<UpdatePhase>('idle');
  const [text, setText] = useState('');
  const pollGeneration = useRef(0);
  const pollTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // A switch to another speaker drops the update the previous one was still running.
  useEffect(() => {
    return () => {
      pollGeneration.current += 1;
      clearTimeout(pollTimer.current);
      setPhase('idle');
      setText('');
    };
  }, [speaker]);

  useEffect(() => {
    if (phase !== 'updated') return undefined;
    const id = setTimeout(() => window.location.reload(), RELOAD_MS);
    return () => clearTimeout(id);
  }, [phase]);

  // A clock NTP has not set yet is the one reason worth naming: it passes on its own.
  const unavailableText = () =>
    clockNotSet ? `${t('updateUnavailable')} (${t('errClockNotSet')})` : t('updateUnavailable');

  const report = (next: UpdatePhase, message: string) => {
    setPhase(next);
    setText(message);
  };

  const waitForVersion = (target: string | null) => {
    const from = version?.version;
    const generation = ++pollGeneration.current;
    let polls = 0;
    const tick = async () => {
      polls += 1;
      const v = await getVersion(speaker);
      if (generation !== pollGeneration.current) return;
      // Without a known starting version a first read cannot be told from a change.
      const changed = from != null && !!v?.version && v.version !== from;
      if (v?.version && (target ? v.version === target : changed))
        return report('updated', `${t('updateDone')} ${v.version}`);
      if (polls >= MAX_POLLS) return report('error', t('updateError'));
      pollTimer.current = setTimeout(tick, POLL_MS);
    };
    pollTimer.current = setTimeout(tick, FIRST_POLL_MS);
  };

  const install = async () => {
    if (phase === 'busy') return;
    report('busy', t('updating'));
    const generation = pollGeneration.current;
    try {
      const res = await startUpdate(undefined, speaker);
      if (generation !== pollGeneration.current) return;
      if (res.status === 200 && res.body.status === 'current') report('done', t('upLatest'));
      else if (res.body.status === 'updating' || res.status === 202)
        waitForVersion(res.body.to || null);
      else report('error', failureText(res.body, t('updateError'), t));
    } catch {
      if (generation === pollGeneration.current) report('error', t('updateError'));
    }
  };

  return {
    // The release about to be installed, for its notes.
    target: stable,
    phase,
    text: text || (unavailable ? unavailableText() : ''),
    install,
  };
}
