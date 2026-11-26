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
import { SoundSettings, hasSound } from '../SoundSettings';
import { NetworkSettings } from '../NetworkSettings';
import { UpdateSettings } from '../UpdateSettings';
import { SupportSettings } from '../SystemSettings';
import { DeveloperSettings } from '../DeveloperSettings';
import type { useSpeakerSettings, SpeakerForm } from '../../../../hooks/useSpeakerSettings';
import { useUpdateAvailable } from '../../../../hooks/useUpdateAvailable';
import { useI18n } from '../../../../lib/i18n';
import type { BassCaps, NetworkInfo, Soundbar } from '../../../../lib/types';
import { versionLabel } from '../../../../lib/version';
import { alarmSummary, bluetoothSummary, hasBass, networkSummary } from '../../../../lib/summaries';
import { formatBass } from '../../../../lib/format';
import { speakerArg, type Room } from '../../../../hooks/useRooms';

export type Section =
  | 'name'
  | 'sound'
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
  readonly page: Page;
  readonly name: string;
  readonly model: string;
  readonly open: ReturnType<Open>;
  readonly saveBar: ReactNode;
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

// What the Sound page shows and how its bass goes to the speaker.
export interface Sound {
  caps: BassCaps;
  soundbar: Soundbar | null | undefined;
  mono: boolean;
  onBass: (bass: number) => void;
  // The speaker's name, for what a failure there says.
  name: string;
}

// The Sound row: its page, on every speaker that has anything to set there, with the
// bass beside it where that is all it has.
function SoundRow({
  sound,
  bass,
  speaker,
  open,
}: {
  readonly sound: Sound;
  readonly bass: number;
  readonly speaker: string | undefined;
  readonly open: ReturnType<Open>;
}): ReactElement | null {
  const { t } = useI18n();
  if (!hasSound(sound.caps, sound.soundbar, sound.mono)) return null;
  const summary =
    sound.soundbar === undefined && hasBass(sound.caps)
      ? `${t('bass')} ${formatBass(bass)}`
      : undefined;
  return (
    <Accordion {...open} icon="volume" title={t('sound')} summary={summary}>
      <SoundSettings
        bass={bass}
        caps={sound.caps}
        soundbar={sound.soundbar}
        speaker={speaker}
        name={sound.name}
        mono={sound.mono}
        onBass={sound.onBass}
      />
    </Accordion>
  );
}

// The Speaker group: its Sound page, idle standby, alarm, the ST20's clock, Bluetooth and the stereo pair.
export function SpeakerGroup({
  page,
  form,
  room,
  rooms,
  section,
  commit,
  saveBar,
  onRenamed,
  sound,
}: {
  readonly page: Page;
  readonly form: SpeakerForm;
  readonly room: Room;
  readonly rooms: Room[];
  readonly section: Open;
  readonly commit: Commit;
  readonly saveBar: ReactNode;
  readonly onRenamed: () => void;
  readonly sound: Sound;
}): ReactElement {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const { display } = form;
  return (
    <SetMenu title={t('speaker')}>
      <SoundRow sound={sound} bass={form.bass} speaker={speaker} open={section('sound')} />
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
  readonly page: Page;
  readonly room: Room;
  readonly model: string;
  readonly network: NetworkInfo | null;
  readonly section: Open;
  readonly crashReports: boolean;
  readonly onCrashReports: (on: boolean) => void;
}): ReactElement {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const newer = useUpdateAvailable(speaker, page.version);
  return (
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
  );
}
