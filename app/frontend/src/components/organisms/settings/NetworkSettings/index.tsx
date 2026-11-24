// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { TextButton } from '../../../atoms/TextButton';
import {
  dismissWifiFellBack,
  forgetWifi,
  getSavedWifi,
  getWifiFellBack,
  type SavedWifi,
} from '../../../../lib/api';
import { Hint } from '../../../atoms/Hint';
import { SubPage } from '../../../molecules/Accordion';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import type { NetworkInfo } from '../../../../lib/types';
import { WifiPicker } from './WifiPicker';

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

const band = (mhz: number) => (mhz >= 5925 ? '6 GHz' : mhz >= 4900 ? '5 GHz' : '2.4 GHz');

// What signal_poll adds to the signal word: dBm, band and link speed, each when known.
function linkDetail(n: NetworkInfo | null): string[] {
  return [
    n?.dbm ? `${n.dbm} dBm` : '',
    n?.mhz ? band(n.mhz) : '',
    n?.linkMbps ? `${n.linkMbps} Mbit/s` : '',
  ].filter(Boolean);
}

// The connection in use: interface, address and, on Wi-Fi, signal. On Ethernet there is
// no SSID, and saying so is the diagnosis for "my Wi-Fi settings do nothing".
function NetworkStatus({ network }: { network: NetworkInfo | null }) {
  const { t } = useI18n();
  const wired = network?.type === 'ethernet';
  const signal = network?.signal;
  return (
    <>
      <FieldCard>
        <FieldRow label={t('connection')} layout="value">
          {!network
            ? t('notConnected')
            : wired
              ? t('ethernetLabel')
              : network.ssid || t('wifiLabel')}
        </FieldRow>
        <FieldRow label={t('ipAddress')} layout="value">
          {network?.ip || '—'}
        </FieldRow>
        {!wired && (
          <FieldRow label={t('signal')} layout="value">
            {signal
              ? [
                  SIGNAL_LABEL[signal] ? t(SIGNAL_LABEL[signal]) : signal,
                  ...linkDetail(network),
                ].join(' · ')
              : '—'}
          </FieldRow>
        )}
      </FieldCard>
      {!network?.ip ? (
        <Hint error>{t('netDiagNoIP')}</Hint>
      ) : wired ? (
        <Hint>{t('netDiagWired')}</Hint>
      ) : (
        signal === 'poor' && <Hint>{t('netDiagPoor')}</Hint>
      )}
    </>
  );
}

// New settings that did not join: the speaker went back to its last network, and says why
// once, until dismissed.
export function FellBackNotice({ speaker }: { speaker: string }) {
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
      const status = (err as { status?: number } | undefined)?.status;
      const refused = !!status && status >= 400 && status < 500;
      setError(t(refused ? 'actionRefused' : 'actionFailed', { name: speakerName }));
    }
  };
  return (
    <>
      <FieldCard>
        {others.map((n) => (
          <FieldRow key={n.id} label={n.ssid} layout="value" wideLabel>
            <TextButton
              aria-label={`${t('wifiForget')} ${n.ssid}`}
              onClick={() => void forget(n.id)}
            >
              {t('wifiForget')}
            </TextButton>
          </FieldRow>
        ))}
      </FieldCard>
      {error && (
        <Hint error role="alert">
          {error}
        </Hint>
      )}
    </>
  );
}

// The Wi-Fi page of a speaker: what it is on, the networks it remembers and a row that
// opens the page for joining another network. No survey runs until it is pressed.
export function NetworkSettings({
  network,
  speaker,
  speakerName,
}: {
  network: NetworkInfo | null;
  speaker: string;
  speakerName: string;
}) {
  const { t } = useI18n();
  const [joining, setJoining] = useState(false);
  if (joining)
    return (
      <SubPage open onBack={() => setJoining(false)} title={t('wifiNetworks')}>
        <WifiPicker network={network} speaker={speaker} />
      </SubPage>
    );
  return (
    <>
      <FellBackNotice speaker={speaker} />
      <NetworkStatus network={network} />
      <SavedNetworks network={network} speaker={speaker} speakerName={speakerName} />
      <SetMenu>
        <SetItem icon="wifi" label={t('wifiConnectTitle')} onClick={() => setJoining(true)} />
      </SetMenu>
    </>
  );
}
