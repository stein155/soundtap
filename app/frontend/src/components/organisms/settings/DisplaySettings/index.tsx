// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Stack } from '../../../atoms/Stack';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { SegmentedControl } from '../../../molecules/SegmentedControl';
import { useI18n } from '../../../../lib/i18n';
import type { DisplayConfig } from '../../../../lib/types';

// The ST20 standby screen: the time alone, as stock's, in the zone of the browser that
// saves these settings.
export function DisplaySettings({
  config,
  onChange,
}: {
  config: DisplayConfig;
  onChange: (patch: Partial<DisplayConfig>) => void;
}) {
  const { t } = useI18n();
  const clockOn = !config.clockOff;
  return (
    <Stack gap={8}>
      <SetMenu>
        <SetSwitch
          on={clockOn}
          label={t('standbyClock')}
          onToggle={() => onChange({ clockOff: clockOn })}
        />
      </SetMenu>
      {clockOn && (
        <FieldCard>
          <FieldRow label={t('clockFormat')} layout="value">
            <SegmentedControl
              label={t('clockFormat')}
              value={config.hour12}
              options={[
                { value: false, label: t('hour24') },
                { value: true, label: t('hour12') },
              ]}
              onChange={(hour12) => onChange({ hour12 })}
            />
          </FieldRow>
          <FieldRow label={t('clockBrightness')} layout="value">
            <SegmentedControl
              label={t('clockBrightness')}
              value={config.brightness || 80}
              options={[25, 50, 80].map((value) => ({ value, label: `${value}%` }))}
              onChange={(brightness) => onChange({ brightness })}
            />
          </FieldRow>
        </FieldCard>
      )}
    </Stack>
  );
}
