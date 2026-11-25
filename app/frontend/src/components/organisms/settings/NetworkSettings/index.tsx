// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { TextButton } from '../../../atoms/TextButton';
import { Icon } from '../../../atoms/Icon';
import {
  dismissWifiFellBack,
  forgetWifi,
  getSavedWifi,
  getWifiFellBack,
  type SavedWifi,
} from '../../../../lib/api';
import { Hint } from '../../../atoms/Hint';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import type { NetworkInfo } from '../../../../lib/types';
import { SignalGlyph } from './SignalGlyph';
import { Current, Detail, Glyph, Row, Text } from './styled';
import { WifiPicker } from './WifiPicker';
import { WifiSearch } from './WifiSearch';

export { WifiPicker };

const SIGNAL_LABEL: Partial<Record<string, I18nKey>> = {
  excellent: 'sigExcellent',
  good: 'sigGood',
  fair: 'sigFair',
  poor: 'sigPoor',
};

const WIFI_FAIL: Partial<Record<string, I18nKey>> = {
  not_found: 'wifiFailNotFound',
  wrong_password: 'wifiFailPassword',
  refused: 'wifiFailRefused',
  no_address: 'wifiFailAddress',
  timeout: 'wifiFailTimeout',
};

// "Signal good · 192.0.2.21": the signal in words and the address, each when known.
function statusLine(n: NetworkInfo, t: ReturnType<typeof useI18n>['t']): string {
  const word = n.signal && SIGNAL_LABEL[n.signal];
  const signal = word ? t('wifiSignalIs', { level: t(word).toLocaleLowerCase() }) : '';
  return [n.type === 'ethernet' ? t('ethernetLabel') : signal, n.ip].filter(Boolean).join(' · ');
}

// The connection in use: the network, its signal and address. On Ethernet there is no
// SSID, and saying so is the diagnosis for "my Wi-Fi settings do nothing".
// The connection's name: wired, or the Wi-Fi network it is on.
function connectionName(
  network: NetworkInfo,
  wired: boolean,
  t: ReturnType<typeof useI18n>['t']
): string {
  if (wired) return t('ethernetLabel');
  return network.ssid || t('wifiLabel');
}

// What to know about a working connection: wired, or a weak Wi-Fi signal.
function Diagnosis({ wired, poor }: { wired: boolean; poor: boolean }): ReactElement | null {
  const { t } = useI18n();
  if (wired) return <Hint>{t('netDiagWired')}</Hint>;
  return poor ? <Hint>{t('netDiagPoor')}</Hint> : null;
}

function NetworkStatus({ network }: { network: NetworkInfo | null }) {
  const { t } = useI18n();
  const wired = network?.type === 'ethernet';
  return (
    <>
      <SetMenu title={t('wifiConnectedTo')}>
        <Row>
          <Glyph aria-hidden="true">
            {wired ? <Icon.link /> : <SignalGlyph signal={network?.signal ?? ''} />}
          </Glyph>
          <Text>
            <Current>{network ? connectionName(network, wired, t) : t('notConnected')}</Current>
            {network && <Detail>{statusLine(network, t)}</Detail>}
          </Text>
        </Row>
      </SetMenu>
      {network?.ip ? (
        <Diagnosis wired={wired} poor={network.signal === 'poor'} />
      ) : (
        <Hint error>{t('netDiagNoIP')}</Hint>
      )}
    </>
  );
}

// New settings that did not join: the speaker went back to its last network, and says why
// once, until dismissed.
export function FellBackNotice({ speaker }: { speaker: string }): ReactElement | null {
  const { t } = useI18n();
  const [reason, setReason] = useState('');
  useEffect(() => {
    let current = true;
    void getWifiFellBack(speaker).then((r) => current && setReason(r));
    return () => {
      current = false;
    };
  }, [speaker]);
  const why = WIFI_FAIL[reason];
  if (!why) return null;
  const dismiss = () => {
    setReason('');
    void dismissWifiFellBack(speaker).catch(() => undefined); // it shows again on the next visit
  };
  return (
    <>
      <Hint error role="status">
        {t('wifiFellBack')} {t(why)}
      </Hint>
      <TextButton onClick={dismiss}>{t('close')}</TextButton>
    </>
  );
}

// The networks the speaker remembers and joins on its own when in range. The one in use
// has no Forget: the speaker would only join it again.
function SavedNetworks({
  network,
  speaker,
  speakerName,
}: {
  network: NetworkInfo | null;
  speaker: string;
  speakerName: string;
}) {
  const { t } = useI18n();
  const [saved, setSaved] = useState<SavedWifi[]>([]);
  const [error, setError] = useState('');
  useEffect(() => {
    let current = true;
    void getSavedWifi(speaker).then((list) => current && setSaved(list));
    return () => {
      current = false;
    };
  }, [speaker]);
  const others = saved.filter((n) => n.ssid !== network?.ssid);
  if (!others.length) return null;
  const forget = async (id: string) => {
    setError('');
    try {
      await forgetWifi(id, speaker);
      setSaved((list) => list.filter((n) => n.id !== id));
    } catch (err) {
      // Still listed: nothing was forgotten. Say so, and whether the speaker said no.
      const status =
        typeof err === 'object' && err !== null && 'status' in err && typeof err.status === 'number'
          ? err.status
          : undefined;
      const refused = status !== undefined && status >= 400 && status < 500;
      setError(t(refused ? 'actionRefused' : 'actionFailed', { name: speakerName }));
    }
  };
  return (
    <>
      <SetMenu title={t('wifiSavedNetworks')}>
        {others.map((n) => (
          <Row key={n.id}>
            <Glyph aria-hidden="true">
              <Icon.home />
            </Glyph>
            <Text>{n.ssid}</Text>
            <TextButton
              aria-label={`${t('wifiForget')} ${n.ssid}`}
              onClick={() => void forget(n.id)}
            >
              {t('wifiForget')}
            </TextButton>
          </Row>
        ))}
      </SetMenu>
      {error && (
        <Hint error role="alert">
          {error}
        </Hint>
      )}
    </>
  );
}

// The Wi-Fi page of a speaker: what it is on, the networks it remembers and a row that
// opens the page for finding another network. No survey runs until it is opened.
export function NetworkSettings({
  network,
  speaker,
  speakerName,
}: {
  network: NetworkInfo | null;
  speaker: string;
  speakerName: string;
}): ReactElement {
  const { t } = useI18n();
  const [finding, setFinding] = useState(false);
  if (finding)
    return (
      <WifiSearch speaker={speaker} current={network?.ssid} onBack={() => setFinding(false)} />
    );
  return (
    <>
      <FellBackNotice speaker={speaker} />
      <NetworkStatus network={network} />
      <SavedNetworks network={network} speaker={speaker} speakerName={speakerName} />
      <SetMenu>
        <SetItem icon="plus" label={t('wifiFind')} onClick={() => setFinding(true)} />
      </SetMenu>
    </>
  );
}
