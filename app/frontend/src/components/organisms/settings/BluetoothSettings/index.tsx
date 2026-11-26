// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useState, type ReactElement } from 'react';
import { Button } from '../../../atoms/Button';
import { Icon } from '../../../atoms/Icon';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { TextButton } from '../../../atoms/TextButton';
import { SetMenu } from '../../../molecules/SetMenu';
import { useI18n } from '../../../../lib/i18n';
import { forgetBluetoothDevice, pairBluetooth, type BluetoothStatus } from '../../../../lib/api';
import { usePageVisible } from '../../../../hooks/usePageVisible';
import { useSpeakerResource } from '../../../../hooks/speaker/resource';
import { discardResource, refreshResource } from '../../../../hooks/speaker/store';
import { Connected, Findable, Footer, Forget, Glyph, Name, Row, Value } from './styled';

// A phone connects and disconnects without telling this page, so poll while open.
const POLL_MS = 5000;

type Device = NonNullable<BluetoothStatus['devices']>[number];

// The paired phones and tablets, each with its own Forget, as in Bose's app.
function PairedDevices({
  devices,
  onForget,
}: {
  readonly devices: Device[];
  readonly onForget: (device: Device) => void;
}) {
  const { t } = useI18n();
  return (
    <SetMenu title={t('bluetoothDevices')}>
      {devices.length > 0 ? (
        devices.map((d) => {
          const name = d.name || d.address;
          return (
            <Row key={d.address}>
              <Glyph aria-hidden="true">
                <Icon.phone width="20" height="20" />
              </Glyph>
              <Name>{name}</Name>
              {d.connected && <Connected>{t('bluetoothConnected')}</Connected>}
              <Forget>
                <TextButton
                  aria-label={t('bluetoothForgetName', { name })}
                  onClick={() => onForget(d)}
                >
                  {t('wifiForget')}
                </TextButton>
              </Forget>
            </Row>
          );
        })
      ) : (
        <Row>
          <Glyph aria-hidden="true">
            <Icon.bluetooth width="20" height="20" />
          </Glyph>
          <Name>{t('btNone')}</Name>
        </Row>
      )}
    </SetMenu>
  );
}

// The name phones see.
function SpeakerAlias({ alias }: { readonly alias: string }): ReactElement {
  const { t } = useI18n();
  return (
    <SetMenu>
      <Row>
        <Glyph aria-hidden="true">
          <Icon.bluetooth width="20" height="20" />
        </Glyph>
        <Name>{t('bluetoothName')}</Name>
        <Value>{alias}</Value>
      </Row>
    </SetMenu>
  );
}

// What the speaker's own radio knows: the name phones see and who has paired.
export function BluetoothSettings({
  speaker,
  initial,
}: {
  readonly speaker: string;
  readonly initial: BluetoothStatus;
}): ReactElement {
  const { t } = useI18n();
  // The page's one copy of the speaker's Bluetooth (hooks/speaker/store.ts): the row's
  // summary above reads it too. Only the newest read lands there.
  const status = useSpeakerResource('bluetooth', speaker) ?? initial;
  const [result, setResult] = useState('');
  const refresh = useCallback(async () => {
    await refreshResource('bluetooth', speaker);
  }, [speaker]);
  // A hidden page does not poll the speaker.
  const visible = usePageVisible();
  useEffect(() => {
    if (!visible) return undefined;
    const id = window.setInterval(refresh, POLL_MS);
    return () => window.clearInterval(id);
  }, [refresh, visible]);

  const forget = async (device: Device) => {
    setResult('');
    // A poll sent before "forget" would bring the device back.
    discardResource('bluetooth', speaker);
    try {
      await forgetBluetoothDevice(device.address, speaker);
    } catch {
      setResult(t('bluetoothForgetFailed', { name: device.name || device.address }));
    }
    await refresh();
  };

  const pair = async () => {
    try {
      await pairBluetooth(speaker);
      setResult(''); // the button says it is findable, for as long as the speaker reports that
    } catch {
      setResult(t('bluetoothPairError'));
    }
    await refresh();
  };

  return (
    <>
      <Note>{t('bluetoothHint')}</Note>
      {status.alias && <SpeakerAlias alias={status.alias} />}
      <PairedDevices
        devices={status.devices ?? []}
        onForget={(d) => {
          void forget(d);
        }}
      />
      {result && (
        <Hint error role="status">
          {result}
        </Hint>
      )}
      <Footer>
        {status.discoverable && (
          <Findable role="status">
            {t('bluetoothFindableHint', { name: status.alias || t('speaker') })}
          </Findable>
        )}
        <Button
          wide
          variant="stockholm"
          disabled={status.discoverable}
          onClick={() => {
            void pair();
          }}
        >
          {status.discoverable ? t('bluetoothFindable') : t('bluetoothPair')}
        </Button>
      </Footer>
    </>
  );
}
