// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Accordion, SubPage, type useAccordionGroup } from '../../../molecules/Accordion';
import { SetItem, SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { ModelArt } from '../../../molecules/ModelArt';
import { NameRow } from './NameRow';
import { AlarmSettings } from '../AlarmSettings';
import { DisplaySettings } from '../DisplaySettings';
import { BluetoothSettings } from '../BluetoothSettings';
import { StereoPair } from '../StereoPair';
import { NetworkSettings } from '../NetworkSettings';
import { UpdateSettings } from '../UpdateSettings';
import { SupportSettings } from '../SystemSettings';
import { DeveloperSettings } from '../DeveloperSettings';
import type { useSpeakerSettings, SpeakerForm } from '../../../../hooks/useSpeakerSettings';
import { useUpdateAvailable } from '../../../../hooks/useUpdateAvailable';
import { useI18n } from '../../../../lib/i18n';
import type { NetworkInfo } from '../../../../lib/types';
import { versionLabel } from '../../../../lib/version';
import { alarmSummary, bluetoothSummary, networkSummary } from '../../../../lib/summaries';
import { speakerArg, type Room } from '../../../../hooks/useRooms';

export type Section =
  | 'name'
  | 'alarm'
  | 'display'
  | 'bluetooth'
  | 'stereo'
  | 'wifi'
  | 'updates'
  | 'support'
  | 'developer';

export type Page = ReturnType<typeof useSpeakerSettings>;
export type Open = ReturnType<typeof useAccordionGroup<Section>>;
export type Commit = <K extends keyof SpeakerForm>(field: K, value: SpeakerForm[K]) => void;

// The speaker at the top, as on its card: its drawing, name, model and version. Its row
// opens the page to rename it, with its own Save.
export function SpeakerHeader({
  page,
  name,
  model,
  open,
  saveBar,
}: {
  page: Page;
  name: string;
  model: string;
  open: ReturnType<Open>;
  saveBar: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <>
      <SetMenu>
        <SetItem
          art={<ModelArt model={model} />}
          label={name}
          meta={[model, versionLabel(page.version)].filter(Boolean).join(' · ')}
          onClick={open.onToggle}
        />
      </SetMenu>
      <SubPage open={open.open} onBack={open.onToggle} title={t('name')}>
        <NameRow
          label={t('name')}
          value={page.form?.name ?? ''}
          placeholder={t('namePlaceholder')}
          onChange={(v) => page.set('name', v)}
        />
        {saveBar}
      </SubPage>
    </>
  );
}

// The Speaker group: idle standby, alarm, the ST20's clock, Bluetooth and the stereo pair.
export function SpeakerGroup({
  page,
  form,
  room,
  rooms,
  section,
  commit,
  saveBar,
  onRenamed,
}: {
  page: Page;
  form: SpeakerForm;
  room: Room;
  rooms: Room[];
  section: Open;
  commit: Commit;
  saveBar: ReactNode;
  onRenamed: () => void;
}) {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const display = form.display;
  return (
    <SetMenu title={t('speaker')}>
      <SetSwitch
        label={t('autoOff')}
        desc={t('autoOffHint')}
        on={form.autoOff >= 0}
        onToggle={() => commit('autoOff', form.autoOff >= 0 ? -1 : 0)}
      />
      <Accordion
        {...section('alarm')}
        title={t('page_alarm')}
        summary={alarmSummary(form.alarm, page.presets, t)}
      >
        <AlarmSettings
          alarm={form.alarm}
          presets={page.presets}
          speakerName={form.name || room.name}
          onChange={(a) => page.set('alarm', a)}
        />
        {saveBar}
      </Accordion>
      {display && (
        <Accordion
          {...section('display')}
          title={t('page_display')}
          summary={display.clockOff ? t('off') : t('stateOn')}
        >
          <DisplaySettings
            config={display}
            onChange={(patch) => commit('display', { ...display, ...patch })}
          />
        </Accordion>
      )}
      {page.bluetooth?.adapter && (
        <Accordion
          {...section('bluetooth')}
          title={t('page_bluetooth')}
          summary={bluetoothSummary(page.bluetooth, t)}
        >
          <BluetoothSettings speaker={speaker} initial={page.bluetooth} />
        </Accordion>
      )}
      {rooms.length > 1 && (
        <Accordion {...section('stereo')} title={t('stereoPair')}>
          <StereoPair room={room} rooms={rooms} onChanged={onRenamed} />
        </Accordion>
      )}
    </SetMenu>
  );
}

// Connection and Maintenance: Wi-Fi, then updates, help and what only developers need.
export function SystemGroups({
  page,
  room,
  rooms,
  model,
  network,
  section,
  crashReports,
  onCrashReports,
}: {
  page: Page;
  room: Room;
  rooms: Room[];
  model: string;
  network: NetworkInfo | null;
  section: Open;
  crashReports: boolean;
  onCrashReports: (on: boolean) => void;
}) {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const newer = useUpdateAvailable(speaker, page.version);
  return (
    <>
      <SetMenu title={t('connection')}>
        <Accordion {...section('wifi')} title={t('wifiLabel')} summary={networkSummary(network, t)}>
          <NetworkSettings network={network} speaker={speaker} speakerName={room.name} />
        </Accordion>
      </SetMenu>
      <SetMenu title={t('maintenance')}>
        <Accordion
          {...section('updates')}
          title={t('page_updates')}
          summary={newer ? t('updateAvailable') : versionLabel(page.version)}
        >
          <UpdateSettings speaker={speaker} model={model} version={page.version} rooms={rooms} />
        </Accordion>
        <Accordion {...section('support')} title={t('helpSupport')}>
          <SupportSettings speaker={speaker} />
        </Accordion>
        <Accordion {...section('developer')} title={t('forDevelopers')}>
          <DeveloperSettings
            speaker={speaker}
            version={page.version}
            crashReports={crashReports}
            onCrashReports={onCrashReports}
          />
        </Accordion>
      </SetMenu>
    </>
  );
}
