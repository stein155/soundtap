// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../../atoms/Icon';
import { useReportDirty } from '../../../../hooks/useReportDirty';
import { FormSection } from '../../../atoms/FormSection';
import { Hint } from '../../../atoms/Hint';
import { Stack } from '../../../atoms/Stack';
import { CheckRow } from '../../../molecules/CheckRow';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow, RowInput } from '../../../molecules/FieldRow';
import { PasswordInput } from '../../../molecules/PasswordInput';
import { SaveBar } from '../../../molecules/SaveBar';
import { StatusCard } from '../../../molecules/StatusCard';
import {
  DEFAULT_PORT,
  DEFAULT_TLS_PORT,
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
  return port === '' || (/^\d+$/.test(port) && +port >= 1 && +port <= 65535);
}

type Household = ReturnType<typeof useHouseholdMqtt>;

// Each online speaker's connection to the broker.
function BrokerLinks({ rooms, links }: { rooms: Room[]; links: Household['links'] }) {
  const { t } = useI18n();
  return (
    <FieldCard spaced>
      {rooms.map((room) => {
        const link = links[room.id];
        return (
          <FieldRow key={room.id} label={room.name} layout="value" wideLabel>
            <Link>
              <Dot $connected={!!link?.connected} aria-hidden="true" />
              {link?.connected ? t('mqttConnected') : link?.lastError || t('mqttDisconnected')}
            </Link>
          </FieldRow>
        );
      })}
    </FieldCard>
  );
}

// MQTT settings for the whole household; see useHouseholdMqtt.
export function Mqtt({ rooms, onDirty }: { rooms: Room[]; onDirty?: (dirty: boolean) => void }) {
  const { t } = useI18n();
  const mqtt = useHouseholdMqtt(rooms);
  const { form } = mqtt;
  useReportDirty(mqtt.dirty, onDirty);
  if (mqtt.loading) return <StatusCard busy />;
  if (mqtt.loadFailed || !form)
    return <StatusCard icon={<Icon.globe width="22" height="22" />}>{t('offline')}</StatusCard>;

  const badPort = !portOK(form.port);
  const set = <K extends keyof MqttForm>(field: K, value: MqttForm[K]) =>
    mqtt.setForm({ ...form, [field]: value });
  const textRow = (field: TextField, label: string, placeholder?: string) => (
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
    <Stack>
      <CheckRow
        on={form.enabled}
        title={t('mqttEnable')}
        desc={t('mqttEnableDesc')}
        onToggle={() => set('enabled', !form.enabled)}
      />
      <Broker $off={!form.enabled} disabled={!form.enabled}>
        <FormSection spaced>{t('mqttBroker')}</FormSection>
        <FieldCard>
          {textRow('host', t('mqttHost'), '192.168.1.10')}
          {textRow('port', t('mqttPort'), String(form.tls ? DEFAULT_TLS_PORT : DEFAULT_PORT))}
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
        </FieldCard>
        {mqtt.hasPassword && !form.password && <Hint>{t('mqttPasswordSet')}</Hint>}
        <Stack offset={10}>
          <CheckRow on={form.tls} title={t('mqttTls')} onToggle={() => set('tls', !form.tls)} />
        </Stack>
        <FormSection spaced>{t('mqttTopics')}</FormSection>
        <FieldCard>{textRow('discoveryPrefix', t('mqttDiscoveryPrefix'))}</FieldCard>
        <Hint>{t('mqttDiscoveryDesc')}</Hint>
      </Broker>
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
    </Stack>
  );
}
