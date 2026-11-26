// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { getReleases, getVersion, startUpdate } from '../lib/api';
import { failureText } from '../lib/failure';
import { useI18n } from '../lib/i18n';
import type { ReleaseRef, VersionInfo } from '../lib/types';
import { updateAvailable } from '../lib/version';

export type UpdatePhase = 'idle' | 'busy' | 'done' | 'updated' | 'error';

// Where an update the speaker took on is, as far as this page can tell from the outside: the
// speaker downloads and checks the release while it still answers with the old version, is
// away while it restarts, and answers with the new one once it is back. null until the
// speaker has taken the update on.
export type UpdateStage = 'download' | 'restart' | 'check';

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
  const [newer, setNewer] = useState(false);
  const [unavailable, setUnavailable] = useState(false);
  const [clockNotSet, setClockNotSet] = useState(false);
  useEffect(() => {
    let current = true;
    void getReleases(speaker).then((r) => {
      if (!current) return;
      setStable(r?.stable ?? null);
      setNewer(!!r?.current && updateAvailable(r.current, r.stable?.tag));
      setUnavailable(!r || !!r.unavailable);
      setClockNotSet(r?.unavailableCode === 'clock_not_set');
    });
    return () => {
      current = false;
    };
  }, [speaker]);
  return { stable, newer, unavailable, clockNotSet };
}

export interface SelfUpdate {
  // The release about to be installed, for its notes.
  target: ReleaseRef | null;
  // Whether that release is newer than what the speaker runs.
  newer: boolean;
  phase: UpdatePhase;
  stage: UpdateStage | null;
  // A percentage while the speaker downloads and says how far it is, else null.
  progress: number | null;
  text: string;
  install: () => Promise<void>;
}

// Once the new version answers, the page reloads into it.
function useReloadWhenUpdated(phase: UpdatePhase): void {
  useEffect(() => {
    if (phase !== 'updated') return undefined;
    const id = setTimeout(() => window.location.reload(), RELOAD_MS);
    return () => clearTimeout(id);
  }, [phase]);
}

// Self-update of soundtapd to the latest stable release.
export function useSelfUpdate(speaker: string, version: VersionInfo | null): SelfUpdate {
  const { t } = useI18n();
  const { stable, newer, unavailable, clockNotSet } = useReleases(speaker);
  const [phase, setPhase] = useState<UpdatePhase>('idle');
  const [stage, setStage] = useState<UpdateStage | null>(null);
  // How far the download is, as the speaker says while it still runs the old version.
  const [progress, setProgress] = useState<number | null>(null);
  const [text, setText] = useState('');
  const pollGeneration = useRef(0);
  const pollTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // A switch to another speaker drops the update the previous one was still running.
  useEffect(
    () => () => {
      pollGeneration.current += 1;
      clearTimeout(pollTimer.current);
      setPhase('idle');
      setText('');
    },
    [speaker]
  );

  useReloadWhenUpdated(phase);

  // A clock NTP has not set yet is the one reason worth naming: it passes on its own.
  const unavailableText = () =>
    clockNotSet ? `${t('updateUnavailable')} (${t('errClockNotSet')})` : t('updateUnavailable');

  const report = (next: UpdatePhase, message: string) => {
    setPhase(next);
    setText(message);
  };

  const waitForVersion = (target: string | null) => {
    const from = version?.version;
    pollGeneration.current += 1;
    const generation = pollGeneration.current;
    let polls = 0;
    setStage('download');
    const tick = async () => {
      polls += 1;
      const v = await getVersion(speaker);
      if (generation !== pollGeneration.current) return;
      // Without a known starting version a first read cannot be told from a change.
      const changed = from != null && !!v?.version && v.version !== from;
      setProgress(v?.updateProgress ?? null);
      if (v?.version && (target ? v.version === target : changed)) {
        setStage('check');
        report('updated', `${t('updateDone')} ${v.version}`);
        return;
      }
      if (!v) setStage('restart');
      if (polls >= MAX_POLLS) {
        report('error', t('updateError'));
        return;
      }
      pollTimer.current = setTimeout(tick, POLL_MS);
    };
    pollTimer.current = setTimeout(tick, FIRST_POLL_MS);
  };

  const install = async () => {
    if (phase === 'busy') return;
    setStage(null);
    setProgress(null);
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
    // Whether that release is newer than what the speaker runs.
    newer,
    phase,
    stage,
    // A percentage while the speaker downloads and says how far it is, else null.
    progress: stage === 'download' ? progress : null,
    text: text || (unavailable ? unavailableText() : ''),
    install,
  };
}
