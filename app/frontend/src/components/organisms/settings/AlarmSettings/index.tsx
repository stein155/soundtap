// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { LabeledField } from '../../../molecules/LabeledField';
import { SelectField } from '../../../molecules/SelectField';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { TextField } from '../../../atoms/TextField';
import { useI18n } from '../../../../lib/i18n';
import type { AlarmConfig, Preset } from '../../../../lib/types';
import { Fields } from './styled';

interface Props {
  readonly alarm: AlarmConfig;
  readonly presets: (Preset | null)[];
  readonly speakerName: string;
  readonly onChange: (alarm: AlarmConfig) => void;
}

// The services a preset can hold besides radio, named after the preset so a playlist
// reads as one: "4 · Sunday morning (Spotify)". Brand names, not translated.
const SERVICE: Record<string, string> = { SPOTIFY: 'Spotify', UPNP: 'UPnP' };

// "slot · name", and the service when it is not radio.
export function presetChoice(slot: number, preset: Pick<Preset, 'name' | 'source'>): string {
  const service = SERVICE[preset.source ?? ''];
  return `${slot} · ${preset.name}${service ? ` (${service})` : ''}`;
}

const storedPresets = (presets: (Preset | null)[]) =>
  presets.flatMap((p, i) => (p ? [{ slot: i + 1, label: presetChoice(i + 1, p) }] : []));

// The wake-up alarm: on/off, a time and the preset it plays. It runs on the speaker,
// so it goes off with every phone asleep. When the preset it was set to has since been
// cleared, the first stored one is offered instead.
export function AlarmSettings({ alarm, presets, speakerName, onChange }: Props): ReactElement {
  const { t } = useI18n();
  const stored = storedPresets(presets);
  const slot = stored.some((p) => p.slot === alarm.slot) ? alarm.slot : stored.at(0)?.slot;
  // What is saved is what is shown: any edit stores the offered preset, not the cleared one.
  const shown = { ...alarm, slot: slot ?? alarm.slot };
  return (
    <>
      <Note>{t('alarmIntro')}</Note>
      <SetMenu>
        <SetSwitch
          icon="alarm"
          on={alarm.enabled}
          label={t('alarmEnable')}
          desc={t('alarmDesc', { name: speakerName })}
          // Without presets it can't be switched on, but one that is on must always be able
          // to go off: the speaker then wakes with its fallback tone.
          disabled={stored.length === 0 && !alarm.enabled}
          onToggle={() => onChange({ ...shown, enabled: !alarm.enabled })}
        />
      </SetMenu>
      {stored.length === 0 && <Hint>{t('alarmNeedsPreset')}</Hint>}
      {alarm.enabled && slot !== undefined && (
        <SetMenu title={t('alarmWhen')}>
          <Fields>
            <LabeledField id="alarm-time" label={t('alarmTime')}>
              <TextField
                id="alarm-time"
                type="time"
                value={alarm.time}
                onChange={(e) => {
                  if (e.target.value) onChange({ ...shown, time: e.target.value });
                }}
              />
            </LabeledField>
            <LabeledField id="alarm-slot" label={t('alarmPreset')}>
              <SelectField
                id="alarm-slot"
                label={t('alarmPreset')}
                value={String(slot)}
                options={stored.map((p) => ({ value: String(p.slot), label: p.label }))}
                onChange={(v) => onChange({ ...shown, slot: Number(v) })}
              />
            </LabeledField>
          </Fields>
        </SetMenu>
      )}
    </>
  );
}
