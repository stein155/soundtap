// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { FormSection } from '../../../atoms/FormSection';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { Spinner } from '../../../atoms/Spinner';
import { Stack } from '../../../atoms/Stack';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { StatusCard } from '../../../molecules/StatusCard';
import { CoreUpdate } from './CoreUpdate';
import { useSelfUpdate, type UpdatePhase } from '../../../../hooks/useSelfUpdate';
import { useUpdateAll, type UpdateAllState } from '../../../../hooks/useUpdateAll';
import type { Room } from '../../../../hooks/useRooms';
import type { I18nKey } from '../../../../lib/i18n';
import { useI18n } from '../../../../lib/i18n';
import { modelLabel } from '../../../../lib/format';
import { versionLabel } from '../../../../lib/version';
import type { VersionInfo } from '../../../../lib/types';
import { Card, Overlay, ReleaseNotes, Text, Title } from './styled';

// Shown once the new version is being served: this page still runs the old bundle,
// and reloads itself shortly (or now, on request).
function Reloading({ text }: { text: string }) {
  const { t } = useI18n();
  return (
    <Overlay role="alertdialog" aria-modal="true" aria-label={text}>
      <Card>
        <Spinner />
        <Title>{text}</Title>
        <Text>{t('updateReloading')}</Text>
        <Button variant="ghost" onClick={() => window.location.reload()}>
          {t('reloadNow')}
        </Button>
      </Card>
    </Overlay>
  );
}

// A tick only once the speaker is up to date: not before a check, nor after one failed.
const StatusIcon = ({ phase }: { phase: UpdatePhase }) =>
  phase === 'done' || phase === 'updated' ? (
    <Icon.check width="22" height="22" />
  ) : (
    <Icon.info width="22" height="22" />
  );

// soundtapd replacing itself with the latest stable release, with the notes of what it is
// about to install.
function SelfUpdate({ speaker, version }: { speaker: string; version: VersionInfo }) {
  const { t } = useI18n();
  const update = useSelfUpdate(speaker, version);
  return (
    <Stack gap={8} offset={8}>
      {update.target?.notes && (
        <>
          <FormSection spaced>{t('whatsNew')}</FormSection>
          <Note>
            <ReleaseNotes>{update.target.notes}</ReleaseNotes>
          </Note>
        </>
      )}
      <StatusCard busy={update.phase === 'busy'} icon={<StatusIcon phase={update.phase} />}>
        {update.text || t('upIdle')}
      </StatusCard>
      <Button
        variant="ghost"
        wide
        disabled={update.phase === 'busy'}
        onClick={() => void update.install()}
      >
        {t('updateNow')}
      </Button>
      <Hint>{t('updateVerified')}</Hint>
      {update.phase === 'updated' && <Reloading text={update.text} />}
    </Stack>
  );
}

const STATE_TEXT: Record<UpdateAllState, I18nKey> = {
  queued: 'updateAllQueued',
  busy: 'updateAllBusy',
  current: 'upLatest',
  done: 'updateAllDone',
  failed: 'updateError',
};

// Every speaker at once: each one's progress, then the button that starts the round.
function UpdateAll({ rooms }: { rooms: Room[] }) {
  const { t } = useI18n();
  const all = useUpdateAll(rooms);
  if (all.order.length < 2) return null;
  return (
    <Stack gap={8} offset={8}>
      <FormSection spaced>{t('updateAll')}</FormSection>
      {Object.keys(all.states).length > 0 && (
        <FieldCard>
          {all.order.map((r) => {
            const state = all.states[r.id];
            return (
              <FieldRow key={r.id} label={r.name} layout="value" wideLabel>
                {state ? t(STATE_TEXT[state]) : '—'}
              </FieldRow>
            );
          })}
        </FieldCard>
      )}
      <Button variant="ghost" wide disabled={all.running} onClick={() => void all.run()}>
        {t('updateAll')}
      </Button>
      <Hint>{t('updateAllHint')}</Hint>
    </Stack>
  );
}

// The Updates accordion: model and version, then this speaker's update routes: the app
// from its releases, a newer SoundTap Core, and a firmware image.
export function UpdateSettings({
  speaker,
  model,
  version,
  rooms = [],
}: {
  speaker: string;
  model: string;
  version: VersionInfo | null;
  rooms?: Room[];
}) {
  const { t } = useI18n();
  return (
    <>
      <FieldCard>
        <FieldRow label={t('upModel')} layout="value">
          {modelLabel(model) || '—'}
        </FieldRow>
        <FieldRow label={t('upFirmware')} layout="value">
          {versionLabel(version) || '—'}
        </FieldRow>
      </FieldCard>
      {version?.updateMode === 'firmware' ? (
        <>
          {version.updatable && <SelfUpdate speaker={speaker} version={version} />}
          <UpdateAll rooms={rooms} />
          <CoreUpdate key={speaker} speaker={speaker} />
        </>
      ) : (
        <Hint>{t('updatesOnSpeaker')}</Hint>
      )}
    </>
  );
}
