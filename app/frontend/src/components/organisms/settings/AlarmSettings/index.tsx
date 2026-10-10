// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { CheckRow } from '../../../molecules/CheckRow';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow, RowSelect, RowTime } from '../../../molecules/FieldRow';
import { Hint } from '../../../atoms/Hint';
import { Stack } from '../../../atoms/Stack';
import { useI18n } from '../../../../lib/i18n';
import type { AlarmConfig, Preset } from '../../../../lib/types';

interface Props {
  alarm: AlarmConfig;
  presets: (Preset | null)[];
  speakerName: string;
  onChange: (alarm: AlarmConfig) => void;
}

const storedPresets = (presets: (Preset | null)[]) =>
  presets.flatMap((p, i) => (p ? [{ slot: i + 1, name: p.name }] : []));

// The wake-up alarm: on/off, a time and the preset it plays. It runs on the speaker,
// so it goes off with every phone asleep. When the preset it was set to has since been
// cleared, the first stored one is offered instead.
export function AlarmSettings({ alarm, presets, speakerName, onChange }: Props) {
  const { t } = useI18n();
  const stored = storedPresets(presets);
  const slot = stored.some((p) => p.slot === alarm.slot) ? alarm.slot : stored.at(0)?.slot;
  // What is saved is what is shown: any edit stores the offered preset, not the cleared one.
  const shown = { ...alarm, slot: slot ?? alarm.slot };
  return (
    <>
      <CheckRow
        on={alarm.enabled}
        title={t('alarmEnable')}
        desc={t('alarmDesc', { name: speakerName })}
        // Without presets it can't be switched on, but one that is on must always be able to
        // go off: the speaker then wakes with its fallback tone.
        disabled={!stored.length && !alarm.enabled}
        onToggle={() => onChange({ ...shown, enabled: !alarm.enabled })}
      />
      {!stored.length && <Hint>{t('alarmNeedsPreset')}</Hint>}
      {alarm.enabled && slot !== undefined && (
        <Stack offset={8}>
          <FieldCard>
            <FieldRow label={t('alarmTime')} htmlFor="alarm-time" layout="value">
              <RowTime
                id="alarm-time"
                type="time"
                value={alarm.time}
                onChange={(e) => e.target.value && onChange({ ...shown, time: e.target.value })}
              />
            </FieldRow>
            <FieldRow label={t('alarmStation')} htmlFor="alarm-slot" layout="value">
              <RowSelect
                id="alarm-slot"
                value={slot}
                onChange={(e) => onChange({ ...shown, slot: Number(e.target.value) })}
              >
                {stored.map((p) => (
                  <option key={p.slot} value={p.slot}>
                    {p.slot} · {p.name}
                  </option>
                ))}
              </RowSelect>
            </FieldRow>
          </FieldCard>
        </Stack>
      )}
    </>
  );
}
