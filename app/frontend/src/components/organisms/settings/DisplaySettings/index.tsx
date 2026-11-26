// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { FormSection } from '../../../atoms/FormSection';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { SegmentedControl } from '../../../molecules/SegmentedControl';
import { useI18n } from '../../../../lib/i18n';
import type { DisplayConfig } from '../../../../lib/types';
import { Pad, Preview } from './styled';

// The time as the standby screen draws it: hours and minutes, without AM/PM beside it.
export function clockText(at: Date, hour12: boolean): string {
  const h = at.getHours();
  // 0 and 12 o'clock both read 12 on a 12-hour clock.
  const hour = hour12 ? String(h % 12 === 0 ? 12 : h % 12) : String(h).padStart(2, '0');
  return `${hour}:${String(at.getMinutes()).padStart(2, '0')}`;
}

// The ST20 standby screen: the time alone, as stock's, in the zone of the browser that
// saves these settings. No brightness: stock's clock does not dim, and walks against
// burn-in instead. Every change goes to the speaker at once.
export function DisplaySettings({
  config,
  onChange,
}: {
  readonly config: DisplayConfig;
  readonly onChange: (patch: Partial<DisplayConfig>) => void;
}): ReactElement {
  const { t } = useI18n();
  const clockOn = !config.clockOff;
  return (
    <>
      <Note>{t('displayIntro')}</Note>
      {clockOn && <Preview aria-hidden="true">{clockText(new Date(), config.hour12)}</Preview>}
      <SetMenu>
        <SetSwitch
          icon="clock"
          on={clockOn}
          label={t('standbyClock')}
          onToggle={() => onChange({ clockOff: clockOn })}
        />
      </SetMenu>
      {clockOn && (
        <SetMenu title={t('displayClock')}>
          <Pad>
            <FormSection>{t('clockFormat')}</FormSection>
            <SegmentedControl
              label={t('clockFormat')}
              value={config.hour12}
              options={[
                { value: false, label: t('hour24') },
                { value: true, label: t('hour12') },
              ]}
              onChange={(hour12) => onChange({ hour12 })}
            />
          </Pad>
        </SetMenu>
      )}
      <Hint>{t('displayLive')}</Hint>
    </>
  );
}
