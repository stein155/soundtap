// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { useI18n } from '../../../../lib/i18n';
import { failureText } from '../../../../lib/failure';
import { factoryReset, getSupportBundle } from '../../../../lib/api';

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

// Help: the support bundle, one zip of logs for a bug report.
export function SupportSettings({ speaker }: { speaker: string }) {
  return <SupportBundle speaker={speaker} />;
}

// Factory reset: presets, settings, name, pairings and Wi-Fi go, never the firmware; the
// speaker restarts into setup mode. Its own red row, apart from everything else.
export function FactoryReset({ speaker }: { speaker: string }) {
  const { t } = useI18n();
  const [result, setResult] = useState<{ text: string; error: boolean } | null>(null);
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
      <SetMenu note={t('factoryResetHint')}>
        <ConfirmAction
          destructive
          label={t('factoryReset')}
          warning={t('factoryResetAsk')}
          confirmLabel={t('eraseAllBtn')}
          onConfirm={reset}
          trigger={(ask) => <SetItem danger label={t('factoryReset')} onClick={ask} />}
        />
      </SetMenu>
      {result && (
        <Hint error={result.error} role="status">
          {result.text}
        </Hint>
      )}
    </>
  );
}
