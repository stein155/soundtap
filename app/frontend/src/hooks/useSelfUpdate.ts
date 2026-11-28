// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { getReleases, getVersion, startUpdate } from '../lib/api';
import { failureText } from '../lib/failure';
import { useI18n } from '../lib/i18n';
import type { ReleaseRef, VersionInfo } from '../lib/types';
import { updateAvailable } from '../lib/version';
import {
  isCurrentRun,
  nextRun,
  runOf,
  schedule,
  setRun,
  shownProgress,
  openUpdateScreen,
  useUpdateRun,
  type UpdatePhase,
  type UpdateStage,
} from './updates';

export type { UpdatePhase, UpdateStage } from './updates';

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

// Self-update of soundtapd to the latest stable release.
export function useSelfUpdate(speaker: string, version: VersionInfo | null): SelfUpdate {
  const { t } = useI18n();
  const { stable, newer, unavailable, clockNotSet } = useReleases(speaker);
  const run = useUpdateRun(speaker);
  // A clock NTP has not set yet is the one reason worth naming: it passes on its own.
  const unavailableText = () =>
    clockNotSet ? `${t('updateUnavailable')} (${t('errClockNotSet')})` : t('updateUnavailable');

  const report = (next: UpdatePhase, message: string, stage?: UpdateStage) => {
    setRun(speaker, { phase: next, text: message, ...(stage && { stage }) });
  };

  // Polls the speaker until it answers on the target version. The timers live with the
  // update, not with this page: leaving the Updates page does not stop following it.
  const waitForVersion = (attempt: number, target: string | null) => {
    const from = version?.version;
    let polls = 0;
    setRun(speaker, { stage: 'download' });
    const tick = async () => {
      polls += 1;
      const v = await getVersion(speaker);
      if (!isCurrentRun(speaker, attempt)) return;
      // Without a known starting version a first read cannot be told from a change.
      const changed = from != null && !!v?.version && v.version !== from;
      setRun(speaker, { progress: v?.updateProgress ?? null });
      if (v?.version && (target ? v.version === target : changed)) {
        report('updated', `${t('updateDone')} ${v.version}`, 'check');
        return;
      }
      if (!v) setRun(speaker, { stage: 'restart' });
      if (polls >= MAX_POLLS) {
        report('error', t('updateError'));
        return;
      }
      schedule(speaker, tick, POLL_MS);
    };
    schedule(speaker, tick, FIRST_POLL_MS);
  };

  const install = async () => {
    if (runOf(speaker).phase === 'busy') return;
    const attempt = nextRun(speaker);
    setRun(speaker, { phase: 'busy', stage: null, progress: null, text: t('updating') });
    openUpdateScreen(speaker);
    try {
      const res = await startUpdate(undefined, speaker);
      if (!isCurrentRun(speaker, attempt)) return;
      if (res.status === 200 && res.body.status === 'current') report('done', t('upLatest'));
      else if (res.body.status === 'updating' || res.status === 202)
        waitForVersion(attempt, res.body.to || null);
      else report('error', failureText(res.body, t('updateError'), t));
    } catch {
      if (isCurrentRun(speaker, attempt)) report('error', t('updateError'));
    }
  };

  return {
    // The release about to be installed, for its notes.
    target: stable,
    // Whether that release is newer than what the speaker runs.
    newer,
    phase: run.phase,
    stage: run.stage,
    // A percentage while the speaker downloads and says how far it is, else null.
    progress: shownProgress(run),
    text: run.text || (unavailable ? unavailableText() : ''),
    install,
  };
}
