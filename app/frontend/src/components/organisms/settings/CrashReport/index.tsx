// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { Button, ButtonLink } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { TextButton } from '../../../atoms/TextButton';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { useI18n } from '../../../../lib/i18n';
import { getCrashReport, recordCrashReport, saveSettings } from '../../../../lib/api';
import type { CrashReport } from '../../../../lib/types';
import { Actions, Label, Notice, Preview } from './styled';

// The owner's opt-in to crash reports (ADR 0126), on the System page. Off by default.
export function CrashReportSwitch({
  speaker,
  on,
  onChange,
}: {
  speaker: string;
  on: boolean;
  onChange: (on: boolean) => void;
}): ReactElement {
  const { t } = useI18n();
  const [failed, setFailed] = useState(false);
  const toggle = async () => {
    setFailed(false);
    try {
      await saveSettings({ crashReports: !on }, speaker);
      onChange(!on);
    } catch {
      setFailed(true);
    }
  };
  return (
    <>
      <SetMenu title={t('crashReportsTitle')}>
        <SetSwitch
          on={on}
          label={t('crashReportsSetting')}
          desc={t('crashReportsSettingHint')}
          onToggle={() => void toggle()}
        />
      </SetMenu>
      {failed && (
        <Hint error role="alert">
          {t('crashReportsSettingFailed')}
        </Hint>
      )}
    </>
  );
}

// A crash on the speaker, offered for a report once the owner turned reports on. The
// report is shown word for word before it leaves; it leaves only as the link the owner
// opens on GitHub, and is sent only when they submit it there. Reported or dismissed, the
// speaker does not offer that crash again.
export function CrashNotice({
  speaker,
  enabled,
}: {
  speaker: string;
  enabled: boolean;
}): ReactElement | null {
  const { t } = useI18n();
  const [crash, setCrash] = useState<CrashReport | null>(null);
  const [previewing, setPreviewing] = useState(false);
  useEffect(() => {
    if (!enabled) return undefined;
    let current = true;
    void getCrashReport(speaker).then((c) => current && setCrash(c));
    return () => {
      current = false;
    };
  }, [speaker, enabled]);
  if (!enabled || !crash) return null;
  const done = (outcome: 'reported' | 'dismissed') => {
    setCrash(null);
    setPreviewing(false);
    // On failure the speaker offers it again next time, which is the safe side.
    void recordCrashReport(crash.id, outcome, speaker).catch(() => undefined);
  };
  return (
    <Notice aria-label={t('crashReportAction')}>
      <Hint role="status">{t('crashReportFound')}</Hint>
      {previewing ? (
        <>
          <Note>{t('crashReportPreview')}</Note>
          <Label>{t('crashReportTitleLabel')}</Label>
          <Preview data-testid="crash-title">{crash.title}</Preview>
          <Label>{t('crashReportBodyLabel')}</Label>
          <Preview data-testid="crash-body">{crash.body}</Preview>
          <Note>
            {t('crashReportAttach', {
              path: `${t('connectionMaintenance')} › ${t('helpSupport')}`,
            })}
          </Note>
          <Actions>
            <ButtonLink
              href={crash.url}
              target="_blank"
              rel="noopener noreferrer"
              onClick={() => done('reported')}
            >
              {t('crashReportOpen')}
            </ButtonLink>
            <TextButton onClick={() => setPreviewing(false)}>{t('cancel')}</TextButton>
          </Actions>
        </>
      ) : (
        <Actions>
          <Button onClick={() => setPreviewing(true)}>{t('crashReportAction')}</Button>
          <TextButton onClick={() => done('dismissed')}>{t('crashReportDismiss')}</TextButton>
        </Actions>
      )}
    </Notice>
  );
}
