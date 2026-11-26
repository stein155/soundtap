// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import { Button, Heading } from 'react-aria-components';
import { createGlobalStyle } from 'styled-components';
import { Icon } from '../../../atoms/Icon';
import { Hint } from '../../../atoms/Hint';
import { SheetModal } from '../../../molecules/SheetModal';
import { useI18n } from '../../../../lib/i18n';
import type { WifiNetwork } from '../../../../lib/types';
import { SheetBody } from './styled';
import { badPassword, useWifiJoin, type Security, type WifiJoin } from './useWifiJoin';
import { ManualFields, PasswordField } from './WifiFields';
import { JoinForm } from './JoinForm';

// The sheet is as tall as its form, not the page.
const FitSheet = createGlobalStyle`
  .sheet.wifi-join {
    height: auto;
    max-height: calc(100dvh - max(56px, env(safe-area-inset-top) + 8px));
    overflow-y: auto;
    border-radius: 14px 14px 0 0;
    background: var(--surface);
    box-shadow: 0 -10px 30px rgba(0, 0, 0, 0.18);
  }
`;

// What the sheet joins: a network from the survey, or one typed in.
export type SheetTarget = { network: WifiNetwork } | { other: true };

// What joining says while and after it runs.
function JoinStatus({ join }: { readonly join: WifiJoin }) {
  const { t } = useI18n();
  const { kind, text } = join.phase;
  return (
    <>
      {text && (
        <Hint error={kind === 'error'} role="status">
          {text}
        </Hint>
      )}
      {kind === 'busy' && <Hint>{t('wifiPleaseWait')}</Hint>}
    </>
  );
}

// A surveyed network: its password when it has one, and Connect.
function NetworkForm({
  network,
  join,
}: {
  readonly network: WifiNetwork;
  readonly join: WifiJoin;
}) {
  const { t } = useI18n();
  const [password, setPassword] = useState('');
  return (
    <JoinForm join={join} target={{ ssid: network.ssid, secure: network.secure, password }}>
      {network.secure && (
        <PasswordField
          label={t('wifiPasswordShort')}
          value={password}
          onChange={setPassword}
          tooShort={badPassword(password)}
        />
      )}
    </JoinForm>
  );
}

// A network that does not show up (a hidden one): its name, security and password.
function OtherForm({ join }: { readonly join: WifiJoin }) {
  const { t } = useI18n();
  const [ssid, setSsid] = useState('');
  const [security, setSecurity] = useState<Security>('wpa_or_wpa2');
  const [password, setPassword] = useState('');
  const secure = security !== 'none';
  return (
    <JoinForm join={join} target={{ ssid, secure, password }}>
      <ManualFields ssid={ssid} onSsid={setSsid} security={security} onSecurity={setSecurity} />
      {secure && (
        <PasswordField
          label={t('wifiPasswordShort')}
          value={password}
          onChange={setPassword}
          tooShort={badPassword(password)}
        />
      )}
    </JoinForm>
  );
}

// The sheet over the search page: titled with the network, its password, and Connect.
// Tied to one target: another network starts with an empty form.
function JoinBody({ target, speaker }: { readonly target: SheetTarget; readonly speaker: string }) {
  const join = useWifiJoin(speaker);
  return (
    <SheetBody>
      {'network' in target ? (
        <NetworkForm network={target.network} join={join} />
      ) : (
        <OtherForm join={join} />
      )}
      <JoinStatus join={join} />
    </SheetBody>
  );
}

export function JoinSheet({
  target,
  speaker,
  onClose,
}: {
  readonly target: SheetTarget | null;
  readonly speaker: string;
  readonly onClose: () => void;
}): ReactElement {
  const { t } = useI18n();
  const title = target && 'network' in target ? target.network.ssid : t('wifiOther');
  return (
    <>
      <FitSheet />
      <SheetModal
        open={!!target}
        onClose={onClose}
        label={t('wifiJoinNamed', { name: title })}
        className="wifi-join"
      >
        {(close) => (
          <>
            <span className="ract-grab" aria-hidden="true" />
            <div className="sheet-head">
              <Heading slot="title" className="sheet-title">
                {title}
              </Heading>
              <Button className="x-btn" aria-label={t('close')} onPress={close}>
                <Icon.close />
              </Button>
            </div>
            {target && <JoinBody key={title} target={target} speaker={speaker} />}
          </>
        )}
      </SheetModal>
    </>
  );
}
