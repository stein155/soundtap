// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef, useState, type ChangeEvent, type RefObject } from 'react';
import { failureText } from '../../../../lib/failure';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import { reboot, uploadFirmware } from '../../../../lib/api';
import { useSignedPick } from '../../../../hooks/useSignedPick';
import type { FirmwareReport } from '../../../../lib/types';

export type Outcome = { text: string; error: boolean } | null;

export interface FirmwareUploadState {
  file: File | null;
  report: FirmwareReport | null;
  // Whether the picked .stu came with its .sig.
  signed: boolean;
  progress: number | null;
  outcome: Outcome;
  inputRef: RefObject<HTMLInputElement | null>;
  onChange: (e: ChangeEvent<HTMLInputElement>) => Promise<void>;
  install: () => void;
  restart: () => Promise<void>;
}

// The firmware flow's state: the picked .stu and its signature, the speaker's report on
// it, upload progress and the last outcome. Install and reboot are the caller's to confirm.
export function useFirmwareUpload(speaker: string): FirmwareUploadState {
  const { t } = useI18n();
  const [file, setFile] = useState<File | null>(null);
  const [signature, setSignature] = useState<string | undefined>();
  const [report, setReport] = useState<FirmwareReport | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [outcome, setOutcome] = useState<Outcome>(null);
  // The check's report: it names the confirmation the install needs.
  const checked = useRef<FirmwareReport | null>(null);

  // Uploads `target`; with `install` it is also written to the inactive slot.
  // The signature travels with its file: read from state it was the previous pick's,
  // because the pick and its check happen in the same tick.
  const upload = async (
    target: File,
    sig: string | undefined,
    install: boolean,
    { failure, current }: { failure: [I18nKey, I18nKey]; current: () => boolean }
  ) => {
    setOutcome(null);
    setProgress(0);
    try {
      const confirm = install ? checked.current?.confirm : undefined;
      const answer = await uploadFirmware(target, {
        install,
        onProgress: setProgress,
        speaker,
        signature: sig,
        confirm,
      });
      if (current()) {
        // eslint-disable-next-line require-atomic-updates -- current() says no later pick replaced this one
        if (!install) checked.current = answer;
        setReport(answer);
      }
    } catch (err) {
      if (current())
        setOutcome({ text: `${failureText(err, t(failure[0]), t)} ${t(failure[1])}`, error: true });
    } finally {
      setProgress(null);
    }
  };

  const check = (picked: File | null, sig: string | undefined, current: () => boolean) => {
    setFile(picked);
    setReport(null);
    checked.current = null;
    setOutcome(null);
    if (!picked) return;
    if (picked.name.toLowerCase().endsWith('.stu'))
      void upload(picked, sig, false, {
        failure: ['firmwareCheckFailed', 'firmwareCheckFailedHint'],
        current,
      });
    else setOutcome({ text: t('firmwareNotStu'), error: true });
  };

  // A pick is a .stu, optionally with its .sig; the speaker checks it at once.
  const { inputRef, onChange, latest } = useSignedPick(
    (picked, sig, current) => {
      setSignature(sig);
      check(picked, sig, current);
    },
    (err) => setOutcome({ text: failureText(err, t('firmwareCheckFailed'), t), error: true })
  );

  const install = () => {
    if (file)
      void upload(file, signature, true, {
        failure: ['firmwareInstallFailed', 'firmwareInstallFailedHint'],
        current: latest(),
      });
  };

  const restart = async () => {
    try {
      await reboot(speaker);
      setOutcome({ text: t('firmwareRebooting'), error: false });
    } catch (err) {
      setOutcome({ text: failureText(err, t('firmwareRebootFailed'), t), error: true });
    }
  };

  // Whether the picked .stu came with its .sig: without one, the press is all it installs on.
  const signed = !!signature;

  return { file, report, signed, progress, outcome, inputRef, onChange, install, restart };
}
