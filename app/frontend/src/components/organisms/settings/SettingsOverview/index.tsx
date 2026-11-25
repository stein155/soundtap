// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { useEffect, useState } from 'react';
import { FormSection } from '../../../atoms/FormSection';
import { Stack } from '../../../atoms/Stack';
import { Hint } from '../../../atoms/Hint';
import { Accordion, useAccordionGroup } from '../../../molecules/Accordion';
import { ChoiceList } from '../../../molecules/ChoiceList';
import { SetItem, SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { useHouseholdPrefs } from '../../../../hooks/useHouseholdPrefs';
import { useI18n, LANGS } from '../../../../lib/i18n';
import { getVersion } from '../../../../lib/api';
import { versionLabel } from '../../../../lib/version';
import { speakerArg, type Room } from '../../../../hooks/useRooms';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { ModelArt } from '../../../molecules/ModelArt';

// The model as it fits on one line next to the speaker's name: "SoundTouch 10",
// "Wireless Link".
const shortModel = (model?: string) =>
  (model || '').replace(/^SoundTouch Wireless Link( Adapter)?$/, 'Wireless Link');

type OverviewSection = 'language' | 'about';

interface Props {
  rooms: Room[];
  onSpeaker: (id: string) => void;
  onIntegrations: () => void;
}

// The first level of settings, as Stockholm's: every speaker, then the household's
// settings, then this app's own, each a group with its title.
export function SettingsOverview({ rooms, onSpeaker, onIntegrations }: Props) {
  const { t, lang, setLang, langFailed } = useI18n();
  const section = useAccordionGroup<OverviewSection>();
  const prefs = useHouseholdPrefs(rooms);
  return (
    <>
      <SetMenu title={t('speakers')}>
        {rooms.map((room) => (
          <SetItem
            key={room.id}
            art={<ModelArt model={room.model} />}
            label={room.name}
            meta={room.online ? shortModel(room.model) : t('offline')}
            onClick={() => onSpeaker(room.id)}
          />
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

// A text page's type, as Stockholm's About.
const AboutText = styled.p`
  margin: 0;
  font-size: 14.4px;
  line-height: 1.5;
`;

// What this app is, then every speaker as Stockholm's About lists them: model, IP
// address and the firmware it runs, its licence, and that it is not Bose's.
function About({ rooms }: { rooms: Room[] }) {
  const { t } = useI18n();
  const [versions, setVersions] = useState<Record<string, string>>({});
  useEffect(() => {
    let live = true;
    for (const room of rooms.filter((r) => r.online)) {
      void getVersion(speakerArg(room)).then(
        (v) => live && setVersions((all) => ({ ...all, [room.id]: versionLabel(v) }))
      );
    }
    return () => {
      live = false;
    };
  }, [rooms]);
  return (
    <Stack>
      {rooms.map((room, i) => (
        <div key={room.id}>
          <FormSection spaced={i > 0}>{room.name}</FormSection>
          <FieldCard>
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
          </FieldCard>
        </div>
      ))}
      <AboutText>{t('aboutLicense')}</AboutText>
      <AboutText>{t('aboutNotBose')}</AboutText>
    </Stack>
  );
}

// What is switched on, in one line: "Spotify · AirPlay · MQTT".
const integrationsSummary = (prefs: ReturnType<typeof useHouseholdPrefs>, off: string) =>
  [
    prefs.integrations?.spotify && 'Spotify',
    prefs.integrations?.airplay && 'AirPlay',
    prefs.mqttHost && 'MQTT',
  ]
    .filter(Boolean)
    .join(' · ') || off;

// What SoundTap connects to outside the speakers: the receivers phones and apps play to
// (music services), and the smart-home systems that see and steer the speakers. Every
// row says on or off and opens a page of its own.
export type Receiver = 'spotify' | 'airplay' | 'applehome';

export function Integrations({
  rooms,
  onOpen,
}: {
  rooms: Room[];
  onOpen: (page: Receiver | 'mqtt') => void;
}) {
  const { t } = useI18n();
  const { integrations, mqttHost } = useHouseholdPrefs(rooms);
  const state = (on?: boolean) => (on ? t('stateOn') : t('off'));
  return (
    <>
      <FormSection>{t('services')}</FormSection>
      <SetMenu>
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
      <FormSection spaced>{t('integrationsMore')}</FormSection>
      <SetMenu>
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
export function Integration({ rooms, which }: { rooms: Room[]; which: Receiver }) {
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
    <Stack gap={8}>
      <SetMenu>
        <SetSwitch
          label={{ spotify: 'Spotify Connect', airplay: 'AirPlay', applehome: 'Apple Home' }[which]}
          on={integrations[key]}
          onToggle={() =>
            void prefs.saveIntegrations({ ...integrations, [key]: !integrations[key] })
          }
        />
      </SetMenu>
      <Hint>{t(hint[which])}</Hint>
    </Stack>
  );
}
