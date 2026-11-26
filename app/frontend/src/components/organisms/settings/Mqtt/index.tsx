// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactNode, type ReactElement } from 'react';
import { Icon } from '../../../atoms/Icon';
import { TextButton } from '../../../atoms/TextButton';
import { useReportDirty } from '../../../../hooks/useReportDirty';
import { Hint } from '../../../atoms/Hint';
import { Stack } from '../../../atoms/Stack';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow, RowInput } from '../../../molecules/FieldRow';
import { PasswordInput } from '../../../molecules/PasswordInput';
import { SaveBar } from '../../../molecules/SaveBar';
import { StatusCard } from '../../../molecules/StatusCard';
import {
  DEFAULT_PORT,
  DEFAULT_TLS_PORT,
  DEFAULT_DISCOVERY_PREFIX,
  useHouseholdMqtt,
  type MqttForm,
} from '../../../../hooks/useHouseholdMqtt';
import { useI18n } from '../../../../lib/i18n';
import type { Room } from '../../../../hooks/useRooms';
import { Broker, Dot, Link } from './styled';

// What the password field shows while a password is stored but not sent back.
const STORED_PASSWORD_MASK = '••••••••';

type TextField = 'host' | 'port' | 'username' | 'discoveryPrefix';

// A port is a whole number from 1 to 65535; empty means the default.
function portOK(raw: string): boolean {
  const port = raw.trim();
  return port === '' || (/^\d+$/u.test(port) && Number(port) >= 1 && Number(port) <= 65535);
}

type Household = ReturnType<typeof useHouseholdMqtt>;

// Each online speaker's connection to the broker.
function BrokerLinks({
  rooms,
  links,
}: {
  readonly rooms: Room[];
  readonly links: Household['links'];
}) {
  const { t } = useI18n();
  return (
    <FieldCard spaced>
      {rooms.map((room) => {
        const link = links[room.id];
        return (
          <FieldRow key={room.id} label={room.name} layout="value" wideLabel>
            <Link>
              <Dot $connected={link?.connected === true} aria-hidden="true" />
              {link?.connected === true
                ? t('mqttConnected')
                : link?.lastError || t('mqttDisconnected')}
            </Link>
          </FieldRow>
        );
      })}
    </FieldCard>
  );
}

// Whether the port, TLS or the discovery prefix differ from their defaults.
const movedFromDefaults = (f: MqttForm) =>
  f.tls ||
  !['', String(DEFAULT_PORT)].includes(f.port.trim()) ||
  f.discoveryPrefix.trim() !== DEFAULT_DISCOVERY_PREFIX;

type Row = (field: TextField, label: string, placeholder?: string) => ReactNode;

// The port, TLS and the discovery prefix: their defaults suit Home Assistant, so they wait
// behind "More options" unless one of them is set otherwise.
function MoreOptions({
  form,
  set,
  textRow,
}: {
  readonly form: MqttForm;
  readonly set: <K extends keyof MqttForm>(field: K, value: MqttForm[K]) => void;
  readonly textRow: Row;
}) {
  const { t } = useI18n();
  return (
    <Stack offset={10}>
      <FieldCard>
        {textRow('port', t('mqttPort'), String(form.tls ? DEFAULT_TLS_PORT : DEFAULT_PORT))}
      </FieldCard>
      <SetMenu>
        <SetSwitch on={form.tls} label={t('mqttTls')} onToggle={() => set('tls', !form.tls)} />
      </SetMenu>
      <SetMenu title={t('mqttTopics')} note={t('mqttDiscoveryDesc')}>
        {textRow('discoveryPrefix', t('mqttDiscoveryPrefix'))}
      </SetMenu>
    </Stack>
  );
}

// Under the form: each speaker's link to the broker, what failed, and Save.
function MqttFooter({
  mqtt,
  form,
  badPort,
}: {
  readonly mqtt: Household;
  readonly form: MqttForm;
  readonly badPort: boolean;
}) {
  const { t } = useI18n();
  return (
    <>
      {form.enabled && mqtt.online.length > 0 && (
        <BrokerLinks rooms={mqtt.online} links={mqtt.links} />
      )}
      {mqtt.failedOn.length > 0 && (
        <Hint error>{`${t('saveFailedOn')} ${mqtt.failedOn.join(', ')}`}</Hint>
      )}
      {badPort && <Hint error>{t('mqttPortInvalid')}</Hint>}
      <SaveBar
        dirty={mqtt.dirty && !badPort && (!form.enabled || !!form.host.trim())}
        onSave={mqtt.save}
      />
    </>
  );
}

// MQTT settings for the whole household; see useHouseholdMqtt.
export function Mqtt({
  rooms,
  onDirty,
}: {
  readonly rooms: Room[];
  readonly onDirty?: (dirty: boolean) => void;
}): ReactElement {
  const { t } = useI18n();
  const mqtt = useHouseholdMqtt(rooms);
  const { form } = mqtt;
  const [showMore, setMore] = useState(false);
  useReportDirty(mqtt.dirty, onDirty);
  if (mqtt.loading) return <StatusCard busy />;
  if (mqtt.loadFailed || !form)
    return <StatusCard icon={<Icon.globe width="22" height="22" />}>{t('offline')}</StatusCard>;

  const badPort = !portOK(form.port);
  const more = showMore || badPort || movedFromDefaults(form);
  const set = <K extends keyof MqttForm>(field: K, value: MqttForm[K]) =>
    mqtt.setForm({ ...form, [field]: value });
  const textRow: Row = (field, label, placeholder) => (
    <FieldRow label={label} htmlFor={`mq-${field}`}>
      <RowInput
        id={`mq-${field}`}
        value={form[field]}
        onChange={(e) => set(field, e.target.value)}
        placeholder={placeholder}
        inputMode={field === 'port' ? 'numeric' : undefined}
        autoComplete="off"
      />
    </FieldRow>
  );

  return (
    <Stack gap={8}>
      <SetMenu>
        <SetSwitch
          on={form.enabled}
          label={t('mqttEnable')}
          desc={t('mqttEnableDesc')}
          onToggle={() => set('enabled', !form.enabled)}
        />
      </SetMenu>
      <Broker $off={!form.enabled} disabled={!form.enabled}>
        <SetMenu title={t('mqttBroker')}>
          {textRow('host', t('mqttHost'), '192.168.1.10')}
          {textRow('username', t('mqttUser'))}
          <FieldRow label={t('mqttPassword')} htmlFor="mq-password">
            <PasswordInput
              id="mq-password"
              value={form.password}
              onChange={(v) => set('password', v)}
              placeholder={mqtt.hasPassword ? STORED_PASSWORD_MASK : ''}
              autoComplete="new-password"
            />
          </FieldRow>
        </SetMenu>
        {mqtt.hasPassword && !form.password && <Hint>{t('mqttPasswordSet')}</Hint>}
        {more ? (
          <MoreOptions form={form} set={set} textRow={textRow} />
        ) : (
          <TextButton onClick={() => setMore(true)}>{t('moreOptions')}</TextButton>
        )}
      </Broker>
      <MqttFooter mqtt={mqtt} form={form} badPort={badPort} />
    </Stack>
  );
}
