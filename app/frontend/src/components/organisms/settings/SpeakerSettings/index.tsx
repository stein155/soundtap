// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState, type ReactElement } from 'react';
import { useReportDirty } from '../../../../hooks/useReportDirty';
import { failureText } from '../../../../lib/failure';
import { Icon } from '../../../atoms/Icon';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { useAccordionGroup } from '../../../molecules/Accordion';
import { SaveBar } from '../../../molecules/SaveBar';
import { StatusCard } from '../../../molecules/StatusCard';
import { FactoryReset } from '../SystemSettings';
import { CrashNotice } from '../CrashReport';
import { useSpeakerSettings } from '../../../../hooks/useSpeakerSettings';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import type { BassCaps } from '../../../../lib/types';
import { speakerArg, type Room } from '../../../../hooks/useRooms';
import {
  SpeakerGroup,
  SpeakerHeader,
  SystemGroups,
  type Commit,
  type Page,
  type Section,
} from './groups';

// What to try when a speaker does not answer, and what its Wi-Fi light says.
const OFFLINE_HELP: I18nKey[] = [
  'offlineHelpPower',
  'offlineHelpReplug',
  'offlineHelpBlink',
  'offlineHelpAmber',
];

// What a speaker reports when it cannot say what its bass can do.
const UNKNOWN_BASS = { actual: 0, target: 0, min: -9, max: 0, default: 0 };
const bassCapsOf = (s: { bass?: BassCaps }): BassCaps => s.bass ?? UNKNOWN_BASS;

// How long the bass slider rests before its value goes to the speaker.
const BASS_SEND_MS = 400;

interface Props {
  room: Room;
  rooms: Room[];
  onRenamed: () => void;
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

// What the page's controls do: a switch or choice goes to the speaker at once, the bass
// once the slider rests, and a typed page (the name, the alarm) on its Save. error is the
// last refusal.
function useSpeakerActions(page: Page, onRenamed: () => void) {
  const { t } = useI18n();
  const [error, setError] = useState('');
  const bassTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(bassTimer.current), []);
  const fail = (e: unknown) => setError(failureText(e, t('saveFailed'), t));
  const commit: Commit = (field, value) => {
    setError('');
    page.commit(field, value).catch(fail);
  };
  const onBass = (bass: number) => {
    page.set('bass', bass);
    window.clearTimeout(bassTimer.current);
    bassTimer.current = window.setTimeout(() => commit('bass', bass), BASS_SEND_MS);
  };
  const save = async () => {
    setError('');
    try {
      if (await page.save()) onRenamed();
    } catch (e) {
      fail(e);
      throw e;
    }
  };
  const saveBar = (
    <>
      {error && <Hint error>{error}</Hint>}
      <SaveBar dirty={page.dirty} onSave={save} />
    </>
  );
  return { error, commit, onBass, saveBar };
}

// One speaker's settings, laid out as Stockholm's Speaker Details in groups: the speaker
// itself (its name opens a page to change it), Speaker (Sound first), Connection and Maintenance,
// with the factory reset apart at the bottom. A switch, slider or choice goes to the
// speaker at once; the name and the alarm, which are typed, have their own Save.
export function SpeakerSettings({ room, rooms, onRenamed, onDirty }: Props): ReactElement {
  const speaker = speakerArg(room);
  const page = useSpeakerSettings(speaker, room.name);
  const section = useAccordionGroup<Section>();
  const { settings, form } = page;
  useReportDirty(page.dirty, onDirty);
  // Switched on the developer page; the notice above the name follows at once.
  const [crashOn, setCrashOn] = useState<boolean | null>(null);
  const { error, commit, onBass, saveBar } = useSpeakerActions(page, onRenamed);

  if (settings === undefined) return <StatusCard busy />;
  if (!settings || !form) return <OfflineSpeaker />;

  const model = settings.model || room.model;
  return (
    <>
      <CrashNotice speaker={speaker} enabled={crashOn ?? settings.crashReports} />
      {error && (
        <Hint error role="alert">
          {error}
        </Hint>
      )}
      <SpeakerHeader
        page={page}
        name={form.name || room.name}
        model={model}
        open={section('name')}
        saveBar={saveBar}
      />
      <SpeakerGroup
        page={page}
        form={form}
        room={room}
        rooms={rooms}
        section={section}
        commit={commit}
        saveBar={saveBar}
        onRenamed={onRenamed}
        sound={{
          caps: bassCapsOf(settings),
          soundbar: settings.soundbar,
          mono: model === 'SoundTouch 10',
          onBass,
        }}
      />
      <SystemGroups
        page={page}
        room={room}
        model={model}
        network={settings.network ?? null}
        section={section}
        crashReports={crashOn ?? settings.crashReports}
        onCrashReports={setCrashOn}
      />
      <FactoryReset speaker={speaker} />
    </>
  );
}
