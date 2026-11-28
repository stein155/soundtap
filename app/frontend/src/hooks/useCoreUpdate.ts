// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { getVersion, startCoreUpdate } from '../lib/api';
import { failureText } from '../lib/failure';
import { useI18n } from '../lib/i18n';
import { UPDATE_MAX_POLLS, UPDATE_POLL_MS } from './useSelfUpdate';
import {
  isCurrentRun,
  nextRun,
  openUpdateScreen,
  runOf,
  schedule,
  setRun,
  shownProgress,
  useUpdateRun,
  type UpdatePhase,
  type UpdateStage,
} from './updates';

// The speaker answers POST /api/core-update once the new slot is written and selected, and
// restarts into it on its own a moment later (ADR 0048). It is away while it restarts and
// answers again on the new Core.
const FIRST_POLL_MS = 6000;

export interface CoreUpdate {
  phase: UpdatePhase;
  stage: UpdateStage | null;
  text: string;
  install: () => Promise<void>;
  progress: number | null;
}

// install updates SoundTap Core (the card asks once first): the speaker downloads the release itself, installs it,
// restarts into it, and this page follows it away and back (as useSelfUpdate does for the
// app). target is the Core version on offer ("0.2.0"). A report without a restart, or a
// failure, ends in text on the card; the speaker never restarts after a failure. While the
// speaker downloads and writes the release, progress is how far it says it is.
export function useCoreUpdate(speaker: string, target: string): CoreUpdate {
  const { t } = useI18n();
  const run = useUpdateRun(speaker);

  const report = (next: UpdatePhase, message: string, nextStage: UpdateStage | null = null) => {
    setRun(speaker, { phase: next, text: message, stage: nextStage });
  };

  // Done once the speaker answers on the new Core; one that came back on another version
  // fell back to the one it had. The timers live with the update, not with this page.
  const waitForCore = (attempt: number) => {
    let polls = 0;
    let away = false;
    const tick = async () => {
      polls += 1;
      const v = await getVersion(speaker);
      if (!isCurrentRun(speaker, attempt)) return;
      if (v?.os === target) {
        report('updated', `${t('updateDone')} ${target}`, 'check');
        return;
      }
      if (!v) {
        away = true;
        setRun(speaker, { stage: 'restart' });
      } else if (away) {
        report('error', t('coreInstallFailed'));
        return;
      }
      if (polls >= UPDATE_MAX_POLLS) {
        report('error', t('updateError'));
        return;
      }
      schedule(speaker, tick, UPDATE_POLL_MS);
    };
    schedule(speaker, tick, FIRST_POLL_MS);
  };

  // How far the speaker says it is while the request stays open (it downloads and writes the
  // release meanwhile): GET /api/version's updateProgress, null when it does not say.
  const watchProgress = (attempt: number) => {
    const watching = () => {
      const now = runOf(speaker);
      return isCurrentRun(speaker, attempt) && now.phase === 'busy' && now.stage === 'download';
    };
    const tick = async () => {
      if (!watching()) return;
      const v = await getVersion(speaker);
      if (!watching()) return;
      setRun(speaker, { progress: v?.updateProgress ?? null });
      schedule(speaker, tick, UPDATE_POLL_MS);
    };
    schedule(speaker, tick, UPDATE_POLL_MS / 2);
  };

  const install = async () => {
    const { phase } = runOf(speaker);
    if (phase === 'busy' || phase === 'updated') return;
    const attempt = nextRun(speaker);
    setRun(speaker, { progress: null });
    report('busy', '', 'download');
    openUpdateScreen(speaker);
    watchProgress(attempt);
    try {
      const res = await startCoreUpdate(speaker);
      if (!isCurrentRun(speaker, attempt)) return;
      if ('status' in res) {
        report('done', t('upLatest'));
        return;
      }
      if (res.installed === true && res.restarting === true) {
        setRun(speaker, { stage: 'restart' });
        waitForCore(attempt);
        return;
      }
      // Installed but not restarting on its own, or refused: the speaker's own words.
      report(res.installed === true ? 'done' : 'error', res.reason || t('coreInstallFailed'));
    } catch (err) {
      if (isCurrentRun(speaker, attempt))
        report('error', failureText(err, t('coreInstallFailed'), t));
    }
  };

  return {
    phase: run.phase,
    stage: run.stage,
    text: run.text,
    install,
    progress: shownProgress(run),
  };
}
