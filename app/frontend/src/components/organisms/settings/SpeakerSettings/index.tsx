// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactNode } from 'react';
import { useReportDirty } from '../../../../hooks/useReportDirty';
import { failureText } from '../../../../lib/failure';
import { Icon } from '../../../atoms/Icon';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { Accordion, useAccordionGroup } from '../../../molecules/Accordion';
import { SaveBar } from '../../../molecules/SaveBar';
import { SetMenu } from '../../../molecules/SetMenu';
import { NameRow } from './NameRow';
import { Advanced, WifiButton } from './Advanced';
import { StatusCard } from '../../../molecules/StatusCard';
import { MonoSwitch, SoundSettings } from '../SoundSettings';
import { AlarmSettings } from '../AlarmSettings';
import { DisplaySettings } from '../DisplaySettings';
import { BluetoothSettings } from '../BluetoothSettings';
import { StereoPair } from '../StereoPair';
import { CrashNotice } from '../CrashReport';
import { useSpeakerSettings, type SpeakerForm } from '../../../../hooks/useSpeakerSettings';
import { useUpdateAvailable } from '../../../../hooks/useUpdateAvailable';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import type { BassCaps, Settings } from '../../../../lib/types';
import { alarmSummary, bluetoothSummary } from '../../../../lib/summaries';
import { speakerArg, type Room } from '../../../../hooks/useRooms';

// What to try when a speaker does not answer, and what its Wi-Fi light says.
const OFFLINE_HELP: I18nKey[] = [
  'offlineHelpPower',
  'offlineHelpReplug',
  'offlineHelpBlink',
  'offlineHelpAmber',
];

// The clock row: the town whose weather it shows, empty when it is off.
const clockSummary = (d: { clockOff?: boolean; city?: string }) => (!d.clockOff && d.city) || '';

// What a speaker reports when it cannot say what its bass can do.
const UNKNOWN_BASS = { actual: 0, target: 0, min: -9, max: 0, default: 0 };
const bassCapsOf = (s: { bass?: BassCaps }): BassCaps => s.bass ?? UNKNOWN_BASS;

type SpeakerSection = 'stereo' | 'alarm' | 'display' | 'bluetooth' | 'advanced';

interface Props {
  room: Room;
  rooms: Room[];
  onRenamed: () => void;
  onMediaServers: () => void;
  onDirty?: (dirty: boolean) => void;
}

// A speaker that did not answer: what to try.
function OfflineSpeaker() {
  const { t } = useI18n();
  return (
    <>
      <StatusCard icon={<Icon.speaker width="22" height="22" />}>{t('offline')}</StatusCard>
      {OFFLINE_HELP.map((key) => (
        <Note key={key}>{t(key)}</Note>
      ))}
    </>
  );
}

interface MenuProps extends Omit<Props, 'onDirty'> {
  page: ReturnType<typeof useSpeakerSettings>;
  settings: Settings;
  form: SpeakerForm;
  section: ReturnType<typeof useAccordionGroup<SpeakerSection>>;
  // The form's Save, shown again on each page that edits the form.
  saveBar: ReactNode;
  newer: boolean;
  // The crash report opt-in, which the System page switches (ADR 0126).
  crashReports: boolean;
  onCrashReports: (on: boolean) => void;
}

// The rows under the sound: alarm, clock, stereo pair, Bluetooth and Advanced Setup.
function SpeakerMenu(props: MenuProps) {
  const { page, settings, form, room, rooms, section, saveBar } = props;
  const { newer, onRenamed, onMediaServers, crashReports, onCrashReports } = props;
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const display = form.display;
  return (
    <SetMenu>
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
          summary={clockSummary(display) || t('off')}
        >
          <DisplaySettings
            config={display}
            speaker={speaker}
            onChange={(patch) => page.set('display', { ...display, ...patch })}
          />
          {saveBar}
        </Accordion>
      )}
      {rooms.length > 1 && (
        <Accordion {...section('stereo')} title={t('stereoPair')}>
          <StereoPair room={room} rooms={rooms} onChanged={onRenamed} />
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
      <Accordion
        {...section('advanced')}
        title={t('page_advanced')}
        summary={newer ? t('updateAvailable') : undefined}
      >
        <Advanced
          room={room}
          rooms={rooms}
          model={settings.model || room.model}
          network={settings.network ?? null}
          version={page.version}
          newer={newer}
          crashReports={crashReports}
          onCrashReports={onCrashReports}
          onMediaServers={onMediaServers}
        />
      </Accordion>
    </SetMenu>
  );
}

// One speaker's settings, laid out as Stockholm's Speaker Details: name and sound on the
// page itself, then rows for alarm, clock, stereo pair and Bluetooth, the technical pages
// behind Advanced Setup, and the Wi-Fi button at the bottom. Name, sound, alarm and clock
// are committed with Save; the rest act at once, each with its own button.
export function SpeakerSettings({ room, rooms, onRenamed, onMediaServers, onDirty }: Props) {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const page = useSpeakerSettings(speaker, room.name);
  const section = useAccordionGroup<SpeakerSection>();
  const [error, setError] = useState('');
  const { settings, form } = page;
  useReportDirty(page.dirty, onDirty);
  const newer = useUpdateAvailable(speaker, page.version);
  // Switched on the System page; the notice above the name follows at once.
  const [crashOn, setCrashOn] = useState<boolean | null>(null);

  if (settings === undefined) return <StatusCard busy />;
  if (!settings || !form) return <OfflineSpeaker />;

  const save = async () => {
    setError('');
    try {
      if (await page.save()) onRenamed();
    } catch (e) {
      setError(failureText(e, t('saveFailed'), t));
      throw e;
    }
  };
  // Name, sound, alarm and display are one form: its Save sits on each of their pages as
  // well as under the list, so an edit can be saved where it was made.
  const saveBar = (
    <>
      {error && <Hint error>{error}</Hint>}
      <SaveBar dirty={page.dirty} onSave={save} />
    </>
  );

  const crashReports = crashOn ?? settings.crashReports;
  return (
    <>
      <CrashNotice speaker={speaker} enabled={crashReports} />
      <NameRow
        label={t('name')}
        value={form.name}
        placeholder={t('namePlaceholder')}
        onChange={(v) => page.set('name', v)}
      />
      <SoundSettings
        bass={form.bass}
        caps={bassCapsOf(settings)}
        autoOff={form.autoOff}
        auxName={room.sources.includes('AUX') ? form.auxName : null}
        soundbar={settings.soundbar}
        speaker={speaker}
        onAuxName={(v) => page.set('auxName', v)}
        onBass={(v) => page.set('bass', v)}
        onAutoOff={(v) => page.set('autoOff', v)}
      />
      <MonoSwitch speaker={speaker} />
      {saveBar}
      <SpeakerMenu
        page={page}
        settings={settings}
        form={form}
        room={room}
        rooms={rooms}
        section={section}
        saveBar={saveBar}
        newer={newer}
        crashReports={crashReports}
        onCrashReports={setCrashOn}
        onRenamed={onRenamed}
        onMediaServers={onMediaServers}
      />
      <WifiButton network={settings.network ?? null} speaker={speaker} />
    </>
  );
}
