// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactNode, type ReactElement } from 'react';
import { useI18n } from '../../../../lib/i18n';
import type { NetworkInfo, WifiNetwork } from '../../../../lib/types';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { Icon } from '../../../atoms/Icon';
import { Spinner } from '../../../atoms/Spinner';
import { TextButton } from '../../../atoms/TextButton';
import { ChoiceList } from '../../../molecules/ChoiceList';
import { SignalGlyph } from './SignalGlyph';
import { Form, NoNetworks, NoNetworksTitle, Refresh, Searching } from './styled';
import { JoinForm, JoinStatus } from './JoinForm';
import { useWifiSurvey } from './useWifiSurvey';
import { badPassword, useWifiJoin, type Security, type WifiJoin } from './useWifiJoin';
import { ManualFields, PasswordField } from './WifiFields';

// Stockholm's popup, in place: search again, or type the network in.
function NothingFound({
  failed,
  onRefresh,
  onManual,
}: {
  failed: boolean;
  onRefresh?: (() => void) | undefined;
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
  // The first-run page's layout: Connect docked at the bottom, the eye in the password
  // field, and how the join goes above Connect.
  docked?: boolean | undefined;
  status?: ReactNode;
}

// The row after the networks that opens the form for one that does not show up; no SSID
// can hold this.
const OTHER = '\u0000other';

// The networks as a list to pick from, each with its signal and a lock when it has a
// password, and a last row for a network that is not in it.
function NetworkList({
  networks,
  chosen,
  onPick,
  onOther,
  disabled,
}: {
  networks: WifiNetwork[];
  chosen: WifiNetwork;
  onPick: (ssid: string) => void;
  onOther: () => void;
  disabled: boolean;
}) {
  const { t } = useI18n();
  return (
    <ChoiceList
      label={t('wifiNearby')}
      title={t('wifiNearby')}
      marked
      tall
      disabled={disabled}
      value={chosen.ssid}
      options={[
        ...networks.map((n) => ({
          value: n.ssid,
          label: n.ssid,
          glyph: <SignalGlyph signal={n.signal ?? ''} />,
          trail: n.secure ? (
            <Icon.lock width="16" height="16" role="img" aria-label={t('wifiSecured')} />
          ) : undefined,
        })),
        { value: OTHER, label: t('setupOtherNetwork'), glyph: <SignalGlyph signal="" /> },
      ]}
      onChange={(ssid) => (ssid === OTHER ? onOther() : onPick(ssid))}
    />
  );
}

// The surveyed networks and a password, as on Stockholm's first setup page; notice: what
// to know before Connect (the phone loses the speaker).
function SurveyForm({
  networks,
  survey,
  fixedSurvey = false,
  join,
  onSwitch,
  notice,
  docked,
  status,
}: FormProps & {
  networks: WifiNetwork[];
  survey: ReturnType<typeof useWifiSurvey>;
  fixedSurvey?: boolean | undefined;
  notice?: ReactNode;
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
    <JoinForm
      join={join}
      target={{ ssid: chosen.ssid, secure: chosen.secure, password }}
      docked={docked}
      status={status}
    >
      <NetworkList
        networks={networks}
        chosen={chosen}
        onPick={pick}
        onOther={onSwitch}
        disabled={busy}
      />
      {survey.failed && <Hint>{t('wifiScanFailed')}</Hint>}
      {refresh && (
        <Refresh>
          <TextButton onClick={refresh}>{t('wifiRescan')}</TextButton>
        </Refresh>
      )}
      {chosen.secure && (
        <PasswordField
          label={t('setupPasswordOf', { name: chosen.ssid })}
          value={password}
          onChange={(value) => setTyped({ ssid: chosen.ssid, password: value })}
          tooShort={badPassword(password)}
          eye={docked}
        />
      )}
      {notice}
    </JoinForm>
  );
}

// A network that does not show up, as on Stockholm's manual page.
function ManualForm({ join, onSwitch, docked, status }: FormProps) {
  const { t } = useI18n();
  const [ssid, setSsid] = useState('');
  const [security, setSecurity] = useState<Security>('wpa_or_wpa2');
  const [password, setPassword] = useState('');
  const secure = security !== 'none';
  return (
    <JoinForm
      join={join}
      target={{ ssid, secure, password }}
      docked={docked}
      status={status}
      after={
        <Button variant="ghost" wide disabled={join.phase.kind === 'busy'} onClick={onSwitch}>
          {t('back')}
        </Button>
      }
    >
      <ManualFields ssid={ssid} onSsid={setSsid} security={security} onSecurity={setSecurity} />
      {secure && (
        <PasswordField
          value={password}
          onChange={setPassword}
          tooShort={badPassword(password)}
          eye={docked}
        />
      )}
    </JoinForm>
  );
}

interface PickerProps {
  network: NetworkInfo | null;
  speaker?: string | undefined;
  // Runs before the switch (the first-run page saves the name first); a rejection
  // stops the switch and shows its message.
  beforeConnect?: () => Promise<unknown>;
  // The setup AP answers with the survey taken before it came up; asking again returns
  // the same list, so the first-run page offers no Refresh.
  fixedSurvey?: boolean | undefined;
  // The speaker's own .local name, for the first-run page's next steps; many Android and
  // Windows devices cannot open it, so the router's device list is named too.
  host?: string | undefined;
  // What to know before Connect, above it.
  notice?: ReactNode;
  // The first-run page's layout: Connect at the bottom of the screen with how the join
  // goes above it, and an eye in the password field.
  docked?: boolean | undefined;
}

// Join a Wi-Fi network the way Stockholm's setup does: one form with the surveyed networks
// and a password, and a manual form for a network that does not show up.
export function WifiPicker({
  network,
  speaker,
  beforeConnect,
  fixedSurvey = false,
  host = 'soundtap.local',
  notice,
  docked = false,
}: PickerProps): ReactElement {
  const { t } = useI18n();
  const survey = useWifiSurvey(speaker);
  const join = useWifiJoin(speaker, beforeConnect);
  const [manual, setManual] = useState(false);
  const toggle = () => {
    setManual(!manual);
    join.reset();
  };
  const status = <JoinStatus join={join} fixedSurvey={fixedSurvey} host={host} />;
  // Docked, the status goes above Connect, inside the form.
  const inForm = docked ? status : undefined;
  return (
    <>
      {manual ? (
        <ManualForm join={join} onSwitch={toggle} docked={docked} status={inForm} />
      ) : (
        <SurveyForm
          networks={(survey.networks ?? []).filter((n) => n.ssid !== network?.ssid)}
          survey={survey}
          fixedSurvey={fixedSurvey}
          join={join}
          onSwitch={toggle}
          notice={notice}
          docked={docked}
          status={inForm}
        />
      )}
      {!docked && status}
      {!fixedSurvey && <Hint>{t('wifiSetupHint')}</Hint>}
    </>
  );
}
