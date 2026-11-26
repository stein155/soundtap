// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { useEffect, useEffectEvent, useState, type ReactElement } from 'react';
import { Hint } from '../../../atoms/Hint';
import { Accordion, useAccordionGroup } from '../../../molecules/Accordion';
import { ChoiceList } from '../../../molecules/ChoiceList';
import { SetItem, SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { useHouseholdPrefs } from '../../../../hooks/useHouseholdPrefs';
import { useUpdateAvailable } from '../../../../hooks/useUpdateAvailable';
import { useI18n, LANGS } from '../../../../lib/i18n';
import { getVersion } from '../../../../lib/api';
import { versionLabel } from '../../../../lib/version';
import type { VersionInfo } from '../../../../lib/types';
import { speakerArg, type Room } from '../../../../hooks/useRooms';
import { FieldRow } from '../../../molecules/FieldRow';
import { ModelArt } from '../../../molecules/ModelArt';

// The model as it fits on one line next to the speaker's name: "SoundTouch 10",
// "Wireless Link".
const shortModel = (model?: string) =>
  (model || '').replace(/^SoundTouch Wireless Link(?: Adapter)?$/u, 'Wireless Link');

type OverviewSection = 'language' | 'about';

interface Props {
  readonly rooms: Room[];
  readonly onSpeaker: (id: string) => void;
  readonly onIntegrations: () => void;
}

// The first level of settings, as Stockholm's: every speaker, then the household's
// settings, then this app's own, each a group with its title.
export function SettingsOverview({ rooms, onSpeaker, onIntegrations }: Props): ReactElement {
  const { t, lang, setLang, langFailed } = useI18n();
  const section = useAccordionGroup<OverviewSection>();
  const prefs = useHouseholdPrefs(rooms);
  return (
    <>
      <SetMenu title={t('speakers')}>
        {rooms.map((room) => (
          <SpeakerRow key={room.id} room={room} onOpen={() => onSpeaker(room.id)} />
        ))}
      </SetMenu>
      <SetMenu title={t('household')}>
        <SetItem
          icon="link"
          label={t('integrations')}
          meta={integrationsSummary(prefs, t('off'))}
          onClick={onIntegrations}
        />
      </SetMenu>
      <SetMenu title={t('thisApp')}>
        <Accordion
          {...section('language')}
          icon="globe"
          title={t('language')}
          summary={LANGS.find((l) => l.code === lang)?.label}
        >
          <ChoiceList
            label={t('language')}
            value={lang}
            options={LANGS.map((l) => ({ value: l.code, label: l.label }))}
            onChange={setLang}
          />
          {langFailed && (
            <Hint error role="alert">
              {t('languageFailed')}
            </Hint>
          )}
        </Accordion>
        <Accordion {...section('about')} icon="info" title={t('about')}>
          <About rooms={rooms} />
        </Accordion>
      </SetMenu>
    </>
  );
}

// A speaker's row: its drawing, its name, and under it the model or that it is offline; an
// Update badge when a newer release is out.
function SpeakerRow({ room, onOpen }: { readonly room: Room; readonly onOpen: () => void }) {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const [version, setVersion] = useState<VersionInfo | null>(null);
  useEffect(() => {
    if (!room.online) return undefined;
    let live = true;
    void getVersion(speaker).then((v) => live && setVersion(v));
    return () => {
      live = false;
    };
  }, [speaker, room.online]);
  const newer = useUpdateAvailable(speaker, version);
  // Every speaker serves the same app, so the list names no speaker as "this one".
  const model = shortModel(room.model);
  return (
    <SetItem
      art={<ModelArt model={room.model} />}
      label={room.name}
      desc={room.online ? model : t('offline')}
      badge={newer ? t('updateBadge') : undefined}
      onClick={onOpen}
    />
  );
}

// A text page's type, as Stockholm's About: under the speakers, apart from them.
const AboutText = styled.div`
  padding-top: 4px;
  font-size: 14px;
  line-height: 1.5;

  p {
    margin: 0;
  }

  p + p {
    margin-top: 10px;
  }
`;

// Every speaker as Stockholm's About lists them, each under its name: model, IP address and
// the software it runs; then the licence, and that SoundTap is not Bose's.
function About({ rooms }: { readonly rooms: Room[] }) {
  const { t } = useI18n();
  const [versions, setVersions] = useState<Record<string, string>>({});
  // Read again when the online set changes, not on every render: the page hands over a new
  // rooms array each time, and each one used to ask every speaker again and drop the answers
  // still on their way.
  const online = rooms.filter((r) => r.online).map((r) => [r.id, speakerArg(r)] as const);
  const key = JSON.stringify(online);
  const readVersions = useEffectEvent((live: () => boolean) => {
    for (const [id, speaker] of online) {
      void getVersion(speaker).then(
        (v) => live() && setVersions((all) => ({ ...all, [id]: versionLabel(v) }))
      );
    }
  });
  useEffect(() => {
    let live = true;
    readVersions(() => live);
    return () => {
      live = false;
    };
  }, [key]);
  return (
    <>
      {rooms.map((room) => (
        <SetMenu key={room.id} title={room.name}>
          <FieldRow label={t('upModel')} layout="value">
            {shortModel(room.model)}
          </FieldRow>
          <FieldRow label={t('ipAddress')} layout="value">
            {room.online ? room.ip : t('offline')}
          </FieldRow>
          {versions[room.id] && (
            <FieldRow label={t('upFirmware')} layout="value">
              {versions[room.id]}
            </FieldRow>
          )}
        </SetMenu>
      ))}
      <AboutText>
        <p>{t('aboutLicense')}</p>
        <p>{t('aboutNotBose')}</p>
      </AboutText>
    </>
  );
}

// What is switched on, in one line: "Spotify · AirPlay · MQTT".
function integrationsSummary(prefs: ReturnType<typeof useHouseholdPrefs>, off: string) {
  return (
    [
      prefs.integrations?.spotify === true && 'Spotify',
      prefs.integrations?.airplay === true && 'AirPlay',
      prefs.mqttHost && 'MQTT',
    ]
      .filter(Boolean)
      .join(' · ') || off
  );
}

// What SoundTap connects to outside the speakers: the receivers phones and apps play to
// (music services), and the smart-home systems that see and steer the speakers. Every
// row says on or off and opens a page of its own.
export type Receiver = 'spotify' | 'airplay' | 'applehome';

export function Integrations({
  rooms,
  onOpen,
}: {
  readonly rooms: Room[];
  readonly onOpen: (page: Receiver | 'mqtt') => void;
}): ReactElement {
  const { t } = useI18n();
  const { integrations, mqttHost } = useHouseholdPrefs(rooms);
  const state = (on?: boolean) => (on === true ? t('stateOn') : t('off'));
  return (
    <>
      <SetMenu title={t('services')}>
        <SetItem
          label="Spotify Connect"
          meta={state(integrations?.spotify)}
          onClick={() => onOpen('spotify')}
        />
        <SetItem
          label="AirPlay"
          meta={state(integrations?.airplay)}
          onClick={() => onOpen('airplay')}
        />
      </SetMenu>
      <SetMenu title={t('integrationsMore')}>
        <SetItem
          label="Apple Home"
          meta={state(integrations?.airplay)}
          onClick={() => onOpen('applehome')}
        />
        <SetItem
          label={t('page_mqtt')}
          meta={mqttHost || t('off')}
          onClick={() => onOpen('mqtt')}
        />
      </SetMenu>
    </>
  );
}

// One receiver's page: its switch and what switching it off does. Apple Home finds the
// speakers through the AirPlay receiver, so its page holds the AirPlay switch.
export function Integration({
  rooms,
  which,
}: {
  readonly rooms: Room[];
  readonly which: Receiver;
}): ReactElement | null {
  const { t } = useI18n();
  const prefs = useHouseholdPrefs(rooms);
  const { integrations } = prefs;
  if (!integrations) return null;
  const key = which === 'spotify' ? 'spotify' : 'airplay';
  const hint = {
    spotify: 'spotifyHint',
    airplay: 'airplayHint',
    applehome: 'appleHomeHint',
  } as const;
  return (
    <SetMenu note={t(hint[which])}>
      <SetSwitch
        label={{ spotify: 'Spotify Connect', airplay: 'AirPlay', applehome: 'Apple Home' }[which]}
        on={integrations[key]}
        onToggle={() => {
          void prefs.saveIntegrations({ ...integrations, [key]: !integrations[key] });
        }}
      />
    </SetMenu>
  );
}
