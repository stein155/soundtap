// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { getVersion, startCoreUpdate } from '../lib/api';
import { failureText } from '../lib/failure';
import { useI18n } from '../lib/i18n';
import type { UpdatePhase, UpdateStage } from './useSelfUpdate';
import { UPDATE_MAX_POLLS, UPDATE_POLL_MS } from './useSelfUpdate';
import { useUpdateProgress } from './useUpdateProgress';

// The speaker answers POST /api/core-update once the new slot is written and selected, and
// restarts into it on its own a moment later (ADR 0048). It is away while it restarts and
// answers again on the new Core.
const FIRST_POLL_MS = 6000;
// After a finished update this page still runs the old bundle; reload into the new one.
const RELOAD_MS = 2200;

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
  const [phase, setPhase] = useState<UpdatePhase>('idle');
  const [stage, setStage] = useState<UpdateStage | null>(null);
  const [text, setText] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const generation = useRef(0);

  useEffect(
    () => () => {
      generation.current += 1;
      clearTimeout(timer.current);
    },
    []
  );

  useEffect(() => {
    if (phase !== 'updated') return undefined;
    const id = setTimeout(() => window.location.reload(), RELOAD_MS);
    return () => clearTimeout(id);
  }, [phase]);

  const report = (next: UpdatePhase, message: string, nextStage: UpdateStage | null = null) => {
    setPhase(next);
    setText(message);
    setStage(nextStage);
  };

  // Done once the speaker answers on the new Core; one that came back on another version
  // fell back to the one it had.
  const waitForCore = (run: number) => {
    let polls = 0;
    let away = false;
    const tick = async () => {
      polls += 1;
      const v = await getVersion(speaker);
      if (run !== generation.current) return;
      if (v?.os === target) {
        report('updated', `${t('updateDone')} ${target}`, 'check');
        return;
      }
      if (!v) {
        away = true;
        setStage('restart');
      } else if (away) {
        report('error', t('coreInstallFailed'));
        return;
      }
      if (polls >= UPDATE_MAX_POLLS) {
        report('error', t('updateError'));
        return;
      }
      timer.current = setTimeout(tick, UPDATE_POLL_MS);
    };
    timer.current = setTimeout(tick, FIRST_POLL_MS);
  };

  const install = async () => {
    if (phase === 'busy' || phase === 'updated') return;
    const run = ++generation.current;
    report('busy', '', 'download');
    try {
      const res = await startCoreUpdate(speaker);
      if (run !== generation.current) return;
      if ('status' in res) {
        report('done', t('upLatest'));
        return;
      }
      if (res.installed === true && res.restarting === true) {
        setStage('restart');
        waitForCore(run);
        return;
      }
      // Installed but not restarting on its own, or refused: the speaker's own words.
      report(res.installed === true ? 'done' : 'error', res.reason || t('coreInstallFailed'));
    } catch (err) {
      if (run === generation.current) report('error', failureText(err, t('coreInstallFailed'), t));
    }
  };

  const progress = useUpdateProgress(speaker, phase === 'busy' && stage === 'download');
  return { phase, stage, text, install, progress };
}
