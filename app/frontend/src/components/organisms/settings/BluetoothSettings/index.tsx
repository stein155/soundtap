// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useState } from 'react';
import { Button } from '../../../atoms/Button';
import { Icon } from '../../../atoms/Icon';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { StatusCard } from '../../../molecules/StatusCard';
import { useI18n } from '../../../../lib/i18n';
import { clearBluetoothPairings, pairBluetooth, type BluetoothStatus } from '../../../../lib/api';
import { useSpeakerResource } from '../../../../hooks/speaker/resource';
import { discardResource, refreshResource } from '../../../../hooks/speaker/store';

// A phone connects and disconnects without telling this page, so poll while open.
const POLL_MS = 5000;

// What the speaker's own radio knows: the name phones see and who has paired.
export function BluetoothSettings({
  speaker,
  initial,
}: {
  speaker: string;
  initial: BluetoothStatus;
}) {
  const { t } = useI18n();
  // The page's one copy of the speaker's Bluetooth (hooks/speaker/store.ts): the row's
  // summary above reads it too. Only the newest read lands there.
  const status = useSpeakerResource('bluetooth', speaker) ?? initial;
  const [result, setResult] = useState('');
  const refresh = useCallback(async () => {
    await refreshResource('bluetooth', speaker);
  }, [speaker]);
  useEffect(() => {
    const id = window.setInterval(refresh, POLL_MS);
    return () => window.clearInterval(id);
  }, [refresh]);

  const forgetAll = async () => {
    // A poll sent before "forget" would bring the phones back.
    discardResource('bluetooth', speaker);
    try {
      await clearBluetoothPairings(speaker);
      setResult(t('bluetoothForgetDone'));
    } catch {
      setResult(t('bluetoothForgetError'));
    }
    await refresh();
  };

  const pair = async () => {
    try {
      await pairBluetooth(speaker);
      setResult(''); // the button says it is pairing, for as long as the speaker reports that
    } catch {
      setResult(t('bluetoothPairError'));
    }
    await refresh();
  };

  const devices = status.devices ?? [];
  return (
    <>
      <Note>{t('bluetoothHint')}</Note>
      {status.alias && (
        <FieldCard>
          <FieldRow label={t('bluetoothName')} layout="value">
            {status.alias}
          </FieldRow>
        </FieldCard>
      )}
      {devices.length ? (
        <>
          <FieldCard>
            {devices.map(({ address, name, connected }) => (
              <FieldRow key={address} label={name || address} layout="value" wideLabel>
                {connected ? t('bluetoothConnected') : ''}
              </FieldRow>
            ))}
          </FieldCard>
          <ConfirmAction
            destructive
            label={t('bluetoothForget')}
            warning={t('bluetoothForgetWarn')}
            confirmLabel={t('bluetoothForget')}
            onConfirm={forgetAll}
          />
        </>
      ) : (
        <StatusCard icon={<Icon.bluetooth width="22" height="22" />}>{t('btNone')}</StatusCard>
      )}
      <Button wide variant="stockholm" disabled={status.discoverable} onClick={pair}>
        {status.discoverable ? t('bluetoothPairing') : t('bluetoothPair')}
      </Button>
      {result && <Hint role="status">{result}</Hint>}
    </>
  );
}
