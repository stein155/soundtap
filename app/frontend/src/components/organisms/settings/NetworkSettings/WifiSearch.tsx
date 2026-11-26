// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import { Icon } from '../../../atoms/Icon';
import { Hint } from '../../../atoms/Hint';
import { Spinner } from '../../../atoms/Spinner';
import { SubPage } from '../../../molecules/Accordion';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { useI18n } from '../../../../lib/i18n';
import type { WifiNetwork } from '../../../../lib/types';
import { JoinSheet, type SheetTarget } from './JoinSheet';
import { SignalGlyph } from './SignalGlyph';
import { Glyph, PickRow, Searching, Text, Trail } from './styled';
import { useWifiSurvey } from './useWifiSurvey';

// One network nearby: its signal, its name, a lock when it has a password.
function NetworkRow({
  network,
  onPick,
}: {
  readonly network: WifiNetwork;
  readonly onPick: () => void;
}) {
  const { t } = useI18n();
  return (
    <PickRow onClick={onPick}>
      <Glyph aria-hidden="true">
        <SignalGlyph signal={network.signal ?? ''} />
      </Glyph>
      <Text>{network.ssid}</Text>
      <Trail>
        {network.secure && (
          <Icon.lock width="16" height="16" role="img" aria-label={t('wifiSecured')} />
        )}
        <Icon.chevron width="18" height="18" aria-hidden="true" />
      </Trail>
    </PickRow>
  );
}

// The survey's answer: the networks nearby, or why there are none.
function Nearby({
  survey,
  onPick,
}: {
  readonly survey: ReturnType<typeof useWifiSurvey>;
  readonly onPick: (network: WifiNetwork) => void;
}) {
  const { t } = useI18n();
  const networks = survey.networks ?? [];
  if (survey.scanning && networks.length === 0)
    return (
      <Searching role="status">
        <Spinner />
        {t('wifiSearching')}
      </Searching>
    );
  return (
    <>
      {survey.failed && <Hint>{t('wifiScanFailed')}</Hint>}
      {networks.length > 0 ? (
        <SetMenu title={t('wifiNearby')}>
          {networks.map((n) => (
            <NetworkRow key={n.ssid} network={n} onPick={() => onPick(n)} />
          ))}
        </SetMenu>
      ) : (
        !survey.failed && <Hint>{t('wifiNoNetworks')}</Hint>
      )}
    </>
  );
}

// Find a network: what the speaker hears around it (search again from the header), and a
// row for a network that does not show up. A network opens a sheet for its password.
export function WifiSearch({
  speaker,
  current,
  onBack,
}: {
  readonly speaker: string;
  // The network in use, left out of the list.
  readonly current?: string | undefined;
  readonly onBack: () => void;
}): ReactElement {
  const { t } = useI18n();
  const survey = useWifiSurvey(speaker);
  const [target, setTarget] = useState<SheetTarget | null>(null);
  const visible = {
    ...survey,
    networks: survey.networks?.filter((n) => n.ssid !== '' && n.ssid !== current) ?? null,
  };
  return (
    <SubPage
      open
      onBack={onBack}
      title={t('wifiFind')}
      action={{
        icon: 'refresh',
        label: t('wifiSearchAgain'),
        onPress: survey.refresh,
        disabled: survey.scanning,
      }}
    >
      <Nearby survey={visible} onPick={(network) => setTarget({ network })} />
      <SetMenu>
        <SetItem label={t('wifiOther')} onClick={() => setTarget({ other: true })} />
      </SetMenu>
      <JoinSheet target={target} speaker={speaker} onClose={() => setTarget(null)} />
    </SubPage>
  );
}
