// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import styled from 'styled-components';
import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { StatusCard } from '../../../molecules/StatusCard';
import { useI18n } from '../../../../lib/i18n';
import { useNotify } from '../../../../hooks/useNotify';
import { failureText } from '../../../../lib/failure';
import { factoryReset, getSupportBundle } from '../../../../lib/api';

// The card's inside, with room around the button.
const Pad = styled.div`
  display: grid;
  padding: 16px var(--page-gutter, 16px);

  & > button {
    margin-top: 0;
  }
`;

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
function SupportBundle({ speaker }: { readonly speaker: string }) {
  const { t } = useI18n();
  const notify = useNotify();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const download = async () => {
    setBusy(true);
    setError('');
    try {
      const { blob, name } = await getSupportBundle(speaker);
      saveBlob(blob, name);
    } catch (e) {
      // A failure is a passing message: the page's toast, as everywhere else.
      const text = failureText(e, t('supportBundleFailed'), t);
      if (notify) notify(text);
      else setError(text);
    } finally {
      setBusy(false);
    }
  };
  return (
    <SetMenu title={t('supportBundle')}>
      <Pad>
        <Button
          variant="ghost"
          wide
          onClick={() => {
            void download();
          }}
          disabled={busy}
          aria-busy={busy}
        >
          <Icon.download width="18" height="18" />
          {t('supportDownload')}
        </Button>
        <Hint>{t('supportContents')}</Hint>
        <StatusCard icon={<Icon.press width="22" height="22" />}>{t('supportPress')}</StatusCard>
        {error && (
          <Hint error role="alert">
            {error}
          </Hint>
        )}
      </Pad>
    </SetMenu>
  );
}

// Help: the support bundle, one zip of logs for a bug report.
export function SupportSettings({ speaker }: { readonly speaker: string }): ReactElement {
  return <SupportBundle speaker={speaker} />;
}

// Factory reset: presets, settings, name, pairings and Wi-Fi go, never the firmware; the
// speaker restarts into setup mode. Its own red row, apart from everything else.
export function FactoryReset({ speaker }: { readonly speaker: string }): ReactElement {
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
      <SetMenu apart>
        <ConfirmAction
          destructive
          label={t('eraseAllBtn')}
          title={t('factoryResetAsk')}
          warning={t('factoryResetHint')}
          confirmLabel={t('eraseAllBtn')}
          onConfirm={reset}
          trigger={(ask) => <SetItem danger label={t('eraseAllBtn')} onClick={ask} />}
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
