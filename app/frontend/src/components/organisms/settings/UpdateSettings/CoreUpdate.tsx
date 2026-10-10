// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { FormSection } from '../../../atoms/FormSection';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { Spinner } from '../../../atoms/Spinner';
import { Stack } from '../../../atoms/Stack';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { ReportCard } from '../FirmwareUpload/ReportCard';
import { getReleases, reboot, startCoreUpdate } from '../../../../lib/api';
import { failureText } from '../../../../lib/failure';
import { useI18n } from '../../../../lib/i18n';
import type { FirmwareReport, ReleaseRef } from '../../../../lib/types';
import { ReleaseNotes } from './styled';

type Outcome = { text: string; error: boolean } | null;

// A newer SoundTap Core than the speaker runs: the speaker downloads it itself,
// after a press on it, into its spare slot; then it offers the reboot as an upload does.
// The parent keys it by speaker, so a switch starts it afresh.
export function CoreUpdate({ speaker }: { speaker: string }) {
  const { t } = useI18n();
  const [latest, setLatest] = useState<ReleaseRef | null>(null);
  const [busy, setBusy] = useState(false);
  const [report, setReport] = useState<FirmwareReport | null>(null);
  const [outcome, setOutcome] = useState<Outcome>(null);

  useEffect(() => {
    let current = true;
    void getReleases(speaker).then((r) => {
      if (current) setLatest(r?.core?.latest ?? null);
    });
    return () => {
      current = false;
    };
  }, [speaker]);

  if (!latest) return null;
  const version = latest.tag.replace(/^os-v/, '');

  const install = async () => {
    setBusy(true);
    setOutcome(null);
    try {
      const res = await startCoreUpdate(speaker);
      if ('status' in res) setOutcome({ text: t('upLatest'), error: false });
      else setReport(res);
    } catch (err) {
      setOutcome({ text: failureText(err, t('coreInstallFailed'), t), error: true });
    } finally {
      setBusy(false);
    }
  };

  const restart = async () => {
    try {
      await reboot(speaker);
      setOutcome({ text: t('firmwareRebooting'), error: false });
    } catch (err) {
      setOutcome({ text: failureText(err, t('firmwareRebootFailed'), t), error: true });
    }
  };

  return (
    <Stack gap={8} offset={8}>
      <FormSection spaced>{t('coreAvailable', { version })}</FormSection>
      {latest.notes && (
        <Note>
          <ReleaseNotes>{latest.notes}</ReleaseNotes>
        </Note>
      )}
      {!report && (
        <ConfirmAction
          label={t('coreInstall', { version })}
          warning={t('firmwareInstallWarn')}
          confirmLabel={t('firmwareInstallConfirm')}
          disabled={busy}
          onConfirm={install}
        />
      )}
      {busy && (
        <Hint role="status">
          <Spinner /> {t('coreInstalling')}
        </Hint>
      )}
      {report && <ReportCard report={report} busy={busy} onInstall={() => {}} onReboot={restart} />}
      {outcome && (
        <Hint error={outcome.error} role={outcome.error ? 'alert' : 'status'}>
          {outcome.text}
        </Hint>
      )}
    </Stack>
  );
}
