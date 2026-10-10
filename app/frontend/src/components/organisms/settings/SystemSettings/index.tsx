// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef, useState } from 'react';
import { Icon } from '../../../atoms/Icon';
import { Button, ButtonLink } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { useI18n } from '../../../../lib/i18n';
import { apiURL } from '../../../../lib/base';
import { failureText } from '../../../../lib/failure';
import { factoryReset, getSupportBundle, restoreBackup } from '../../../../lib/api';
import { CrashReportSwitch } from '../CrashReport';

// saveBlob hands a downloaded file to the browser as if a link had been clicked.
function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

// The support bundle: one zip for a bug report. A fetch rather than a link, so the page
// can say that the speaker waits for a press (ADR 0055) and say so when it was refused.
function SupportBundle({ speaker }: { speaker: string }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const download = async () => {
    setBusy(true);
    setError('');
    try {
      const { blob, name } = await getSupportBundle(speaker);
      saveBlob(blob, name);
    } catch (e) {
      setError(failureText(e, t('supportBundleFailed'), t));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Button variant="ghost" wide onClick={download} disabled={busy} aria-busy={busy}>
        <Icon.download width="18" height="18" />
        {t('supportBundle')}
      </Button>
      <Hint>{t('supportHint')}</Hint>
      {error && (
        <Hint error role="alert">
          {error}
        </Hint>
      )}
    </>
  );
}

// The support bundle and the backup, straight from the speaker.
function Downloads({ speaker }: { speaker: string }) {
  const { t } = useI18n();
  return (
    <>
      <SupportBundle speaker={speaker} />
      <ButtonLink
        variant="ghost"
        wide
        href={apiURL('/api/backup', speaker)}
        download="soundtap-backup.json"
      >
        <Icon.download width="18" height="18" />
        {t('backupDownload')}
      </ButtonLink>
    </>
  );
}

// Support and recovery: download the support bundle, choose whether crashes are offered for
// a report, keep or bring back a backup of the presets and settings, or reset the speaker to
// factory settings (presets, settings, name, pairings and Wi-Fi; never the firmware).
export function SystemSettings({
  speaker,
  crashReports,
  onCrashReports,
}: {
  speaker: string;
  // The crash report opt-in (ADR 0126); without it the switch is not shown.
  crashReports?: boolean;
  onCrashReports?: (on: boolean) => void;
}) {
  const { t } = useI18n();
  const [result, setResult] = useState<{ text: string; error: boolean } | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const [backup, setBackup] = useState<File | null>(null);
  const restore = async () => {
    if (!backup) return;
    try {
      await restoreBackup(backup, speaker);
      setResult({ text: t('restoreDone'), error: false });
    } catch (e) {
      // A restore that was accepted and failed halfway is not a refused file.
      setResult({ text: failureText(e, t('restoreFailed'), t), error: true });
    } finally {
      setBackup(null);
    }
  };
  const reset = async () => {
    try {
      await factoryReset(speaker);
      // Says what comes next: the speaker restarts into setup mode.
      setResult({ text: t('resetNextSteps'), error: false });
    } catch {
      setResult({ text: t('resetFailed'), error: true });
    }
  };
  return (
    <>
      <Downloads speaker={speaker} />
      {crashReports !== undefined && onCrashReports && (
        <CrashReportSwitch speaker={speaker} on={crashReports} onChange={onCrashReports} />
      )}
      <ConfirmAction
        label={t('backupRestore')}
        warning={backup ? `${backup.name}: ${t('restoreAsk')}` : t('restoreAsk')}
        confirmLabel={t('backupRestore')}
        onConfirm={restore}
        // Picking the file is the first step; Cancel goes back to picking one.
        trigger={(ask) => (
          <>
            <input
              ref={fileRef}
              type="file"
              accept=".json,application/json"
              hidden
              onChange={(e) => {
                const file = e.target.files?.[0] ?? null;
                e.target.value = '';
                if (!file) return;
                setBackup(file);
                ask();
              }}
            />
            <Button variant="ghost" wide onClick={() => fileRef.current?.click()}>
              {t('backupRestore')}
            </Button>
          </>
        )}
      />
      <ConfirmAction
        destructive
        label={t('factoryReset')}
        warning={t('factoryResetAsk')}
        confirmLabel={t('eraseAllBtn')}
        onConfirm={reset}
      />
      <Hint>{t('factoryResetHint')}</Hint>
      {result && (
        <Hint error={result.error} role="status">
          {result.text}
        </Hint>
      )}
    </>
  );
}
