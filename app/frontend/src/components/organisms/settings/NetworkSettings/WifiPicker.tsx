// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactNode } from 'react';
import { useI18n } from '../../../../lib/i18n';
import type { NetworkInfo, WifiNetwork } from '../../../../lib/types';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { Spinner } from '../../../atoms/Spinner';
import { TextButton } from '../../../atoms/TextButton';
import { Form, NoNetworks, NoNetworksTitle, Refresh, Searching } from './styled';
import { useWifiSurvey } from './useWifiSurvey';
import {
  badPassword,
  canJoin,
  useWifiJoin,
  type JoinTarget,
  type Security,
  type WifiJoin,
} from './useWifiJoin';
import { ManualFields, NetworkSelect, PasswordField } from './WifiFields';

// The form around a network to join: Connect, and only once for what it holds.
function JoinForm({
  join,
  target,
  children,
  after,
}: {
  join: WifiJoin;
  target: JoinTarget;
  children: ReactNode;
  after?: ReactNode;
}) {
  const { t } = useI18n();
  const { kind } = join.phase;
  const locked = !canJoin(target) || kind === 'busy' || kind === 'done';
  return (
    <Form
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        if (!locked) void join.connect(target);
      }}
    >
      {children}
      <Button type="submit" variant="stockholm" wide disabled={locked}>
        {t('wifiConnect')}
      </Button>
      {after}
    </Form>
  );
}

// Stockholm's popup, in place: search again, or type the network in.
function NothingFound({
  failed,
  onRefresh,
  onManual,
}: {
  failed: boolean;
  onRefresh?: () => void;
  onManual: () => void;
}) {
  const { t } = useI18n();
  return (
    <Form as="div">
      {failed ? (
        <Hint>{t('wifiScanFailed')}</Hint>
      ) : (
        <NoNetworks>
          <NoNetworksTitle>{t('wifiNoNetworks')}</NoNetworksTitle>
          {onRefresh && <Note>{t('wifiNoNetworksBody')}</Note>}
        </NoNetworks>
      )}
      {onRefresh && (
        <Button variant="stockholm" wide onClick={onRefresh}>
          {t('wifiRescan')}
        </Button>
      )}
      <Button variant="ghost" wide onClick={onManual}>
        {t('wifiManualSetup')}
      </Button>
    </Form>
  );
}

interface FormProps {
  join: WifiJoin;
  onSwitch: () => void;
}

// The surveyed networks and a password, as on Stockholm's first setup page.
function SurveyForm({
  networks,
  survey,
  fixedSurvey,
  join,
  onSwitch,
}: FormProps & {
  networks: WifiNetwork[];
  survey: ReturnType<typeof useWifiSurvey>;
  fixedSurvey?: boolean;
}) {
  const { t } = useI18n();
  const [picked, setPicked] = useState('');
  // Tied to its network: a refresh can move the selection to another SSID.
  const [typed, setTyped] = useState({ ssid: '', password: '' });
  const chosen = networks.find((n) => n.ssid === picked) ?? networks.at(0);
  const password = typed.ssid === chosen?.ssid ? typed.password : '';
  const busy = join.phase.kind === 'busy';
  const refresh = fixedSurvey || busy ? undefined : survey.refresh;
  if (survey.scanning)
    return (
      <Searching role="status">
        <Spinner />
        {t('wifiSearching')}
      </Searching>
    );
  if (!chosen)
    return <NothingFound failed={survey.failed} onRefresh={refresh} onManual={onSwitch} />;
  const pick = (ssid: string) => {
    setPicked(ssid);
    setTyped({ ssid: '', password: '' });
    join.reset();
  };
  return (
    <JoinForm join={join} target={{ ssid: chosen.ssid, secure: chosen.secure, password }}>
      <NetworkSelect networks={networks} chosen={chosen} onPick={pick} disabled={busy} />
      {survey.failed && <Hint>{t('wifiScanFailed')}</Hint>}
      {refresh && (
        <Refresh>
          <TextButton onClick={refresh}>{t('wifiRescan')}</TextButton>
        </Refresh>
      )}
      {chosen.secure && (
        <PasswordField
          value={password}
          onChange={(value) => setTyped({ ssid: chosen.ssid, password: value })}
          tooShort={badPassword(password)}
        />
      )}
      <TextButton disabled={busy} onClick={onSwitch}>
        {t('wifiOtherNetwork')}
      </TextButton>
    </JoinForm>
  );
}

// A network that does not show up, as on Stockholm's manual page.
function ManualForm({ join, onSwitch }: FormProps) {
  const { t } = useI18n();
  const [ssid, setSsid] = useState('');
  const [security, setSecurity] = useState<Security>('wpa_or_wpa2');
  const [password, setPassword] = useState('');
  const secure = security !== 'none';
  return (
    <JoinForm
      join={join}
      target={{ ssid, secure, password }}
      after={
        <Button variant="ghost" wide disabled={join.phase.kind === 'busy'} onClick={onSwitch}>
          {t('back')}
        </Button>
      }
    >
      <ManualFields ssid={ssid} onSsid={setSsid} security={security} onSecurity={setSecurity} />
      {secure && (
        <PasswordField value={password} onChange={setPassword} tooShort={badPassword(password)} />
      )}
    </JoinForm>
  );
}

interface PickerProps {
  network: NetworkInfo | null;
  speaker?: string;
  // Runs before the switch (the first-run page saves the name first); a rejection
  // stops the switch and shows its message.
  beforeConnect?: () => Promise<unknown>;
  // The setup AP answers with the survey taken before it came up; asking again returns
  // the same list, so the first-run page offers no Refresh.
  fixedSurvey?: boolean;
  // The speaker's own .local name, for the first-run page's next steps; many Android and
  // Windows devices cannot open it, so the router's device list is named too.
  host?: string;
}

// Join a Wi-Fi network the way Stockholm's setup does: one form with the surveyed networks
// and a password, and a manual form for a network that does not show up.
export function WifiPicker({
  network,
  speaker,
  beforeConnect,
  fixedSurvey,
  host = 'soundtap.local',
}: PickerProps) {
  const { t } = useI18n();
  const survey = useWifiSurvey(speaker);
  const join = useWifiJoin(speaker, beforeConnect);
  const [manual, setManual] = useState(false);
  const toggle = () => {
    setManual(!manual);
    join.reset();
  };
  const { kind, text } = join.phase;
  return (
    <>
      {manual ? (
        <ManualForm join={join} onSwitch={toggle} />
      ) : (
        <SurveyForm
          networks={(survey.networks ?? []).filter((n) => n.ssid !== network?.ssid)}
          survey={survey}
          fixedSurvey={fixedSurvey}
          join={join}
          onSwitch={toggle}
        />
      )}
      {text && (
        <Hint error={kind === 'error'} role="status">
          {text}
        </Hint>
      )}
      {kind === 'busy' && <Hint>{t('wifiPleaseWait')}</Hint>}
      {kind === 'done' && fixedSurvey && (
        <>
          <Note>{t('setupNextSteps', { host })}</Note>
          <Note>{t('setupRouterHint', { device: host.replace(/\.local$/, '') })}</Note>
        </>
      )}
      <Hint>{t('wifiSetupHint')}</Hint>
    </>
  );
}
