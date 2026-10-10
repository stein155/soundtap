// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { Button } from '../../../atoms/Button';
import { Accordion, SubPage, useAccordionGroup } from '../../../molecules/Accordion';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { FellBackNotice, NetworkSettings, WifiPicker } from '../NetworkSettings';
import { UpdateSettings } from '../UpdateSettings';
import { SystemSettings } from '../SystemSettings';
import { useI18n } from '../../../../lib/i18n';
import { networkSummary } from '../../../../lib/summaries';
import { versionLabel } from '../../../../lib/version';
import type { NetworkInfo, VersionInfo } from '../../../../lib/types';
import { speakerArg, type Room } from '../../../../hooks/useRooms';

// The Updates row: the version, and a note when a newer stable is out.
const updatesSummary = (label: string, newer: boolean, note: string) =>
  newer ? `${label} · ${note}` : label;

// What Stockholm keeps off the speaker's own page: the connection's details, media
// servers, updates and the system tools.
export function Advanced({
  room,
  rooms,
  model,
  network,
  version,
  newer,
  crashReports,
  onCrashReports,
  onMediaServers,
}: {
  room: Room;
  rooms: Room[];
  model: string;
  network: NetworkInfo | null;
  version: VersionInfo | null;
  newer: boolean;
  crashReports: boolean;
  onCrashReports: (on: boolean) => void;
  onMediaServers: () => void;
}) {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const section = useAccordionGroup<'network' | 'updates' | 'system'>();
  return (
    <SetMenu>
      <Accordion
        {...section('network')}
        title={t('page_network')}
        summary={networkSummary(network, t)}
      >
        <NetworkSettings network={network} speaker={speaker} speakerName={room.name} />
      </Accordion>
      <SetItem label={t('page_mediaservers')} onClick={onMediaServers} />
      <Accordion
        {...section('updates')}
        title={t('page_updates')}
        summary={updatesSummary(versionLabel(version), newer, t('updateAvailable'))}
      >
        <UpdateSettings speaker={speaker} model={model} version={version} rooms={rooms} />
      </Accordion>
      <Accordion {...section('system')} title={t('page_system')}>
        <SystemSettings
          speaker={speaker}
          crashReports={crashReports}
          onCrashReports={onCrashReports}
        />
      </Accordion>
    </SetMenu>
  );
}

// Stockholm's big button under the speaker's settings: join another Wi-Fi network.
export function WifiButton({ network, speaker }: { network: NetworkInfo | null; speaker: string }) {
  const { t } = useI18n();
  const [joining, setJoining] = useState(false);
  return (
    <>
      <FellBackNotice speaker={speaker} />
      <Button variant="stockholm" wide onClick={() => setJoining(true)}>
        {t('wifiConnectTitle')}
      </Button>
      <SubPage open={joining} onBack={() => setJoining(false)} title={t('wifiNetworks')}>
        <WifiPicker network={network} speaker={speaker} />
      </SubPage>
    </>
  );
}
