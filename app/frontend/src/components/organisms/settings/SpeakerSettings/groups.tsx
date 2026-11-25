// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Accordion, SubPage, type useAccordionGroup } from '../../../molecules/Accordion';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { ModelArt } from '../../../molecules/ModelArt';
import { Icon } from '../../../atoms/Icon';
import { Disc, Hero, Meta as HeroMeta, Name, Pencil, Text as HeroText } from './styled';
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
}): ReactElement {
  const { t } = useI18n();
  return (
    <>
      <Hero type="button" onClick={open.onToggle} aria-label={`${t('name')}: ${name}`}>
        <Disc aria-hidden="true">
          <ModelArt model={model} />
        </Disc>
        <HeroText>
          <Name>{name}</Name>
          <HeroMeta>
            {[model, versionLabel(page.version)]
              .filter((line) => line !== '')
              .map((line) => (
                <span key={line}>{line}</span>
              ))}
          </HeroMeta>
        </HeroText>
        <Pencil aria-hidden="true">
          <Icon.pencil width="20" height="20" />
        </Pencil>
      </Hero>
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
}): ReactElement {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const display = form.display;
  return (
    <SetMenu title={t('speaker')}>
      <SetSwitch
        icon="power"
        label={t('autoOff')}
        desc={t('autoOffHint')}
        on={form.autoOff >= 0}
        onToggle={() => commit('autoOff', form.autoOff >= 0 ? -1 : 0)}
      />
      <Accordion
        {...section('alarm')}
        icon="alarm"
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
          icon="display"
          title={t('page_display')}
          summary={display.clockOff ? t('off') : t('stateOn')}
        >
          <DisplaySettings
            config={display}
            onChange={(patch) => commit('display', { ...display, ...patch })}
          />
        </Accordion>
      )}
      {page.bluetooth?.adapter === true && (
        <Accordion
          {...section('bluetooth')}
          icon="bluetooth"
          title={t('page_bluetooth')}
          summary={bluetoothSummary(page.bluetooth, t)}
        >
          <BluetoothSettings speaker={speaker} initial={page.bluetooth} />
        </Accordion>
      )}
      {rooms.length > 1 && (
        <Accordion {...section('stereo')} icon="stereo" title={t('stereoPair')}>
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
  model,
  network,
  section,
  crashReports,
  onCrashReports,
}: {
  page: Page;
  room: Room;
  model: string;
  network: NetworkInfo | null;
  section: Open;
  crashReports: boolean;
  onCrashReports: (on: boolean) => void;
}): ReactElement {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const newer = useUpdateAvailable(speaker, page.version);
  return (
    <>
      <SetMenu title={t('connectionMaintenance')}>
        <Accordion
          {...section('wifi')}
          icon="wifi"
          title={t('wifiLabel')}
          summary={networkSummary(network, t)}
        >
          <NetworkSettings network={network} speaker={speaker} speakerName={room.name} />
        </Accordion>
        <Accordion
          {...section('updates')}
          icon="refresh"
          title={t('page_updates')}
          summary={newer ? undefined : versionLabel(page.version)}
          badge={newer ? t('updatesNewBadge') : undefined}
        >
          <UpdateSettings speaker={speaker} name={room.name} model={model} version={page.version} />
        </Accordion>
        <Accordion {...section('support')} icon="help" title={t('helpSupport')}>
          <SupportSettings speaker={speaker} />
        </Accordion>
        <Accordion {...section('developer')} icon="code" title={t('forDevelopers')}>
          <DeveloperSettings
            speaker={speaker}
            crashReports={crashReports}
            onCrashReports={onCrashReports}
          />
        </Accordion>
      </SetMenu>
    </>
  );
}
