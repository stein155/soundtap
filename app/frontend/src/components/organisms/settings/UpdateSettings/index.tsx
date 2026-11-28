// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, type ReactElement } from 'react';
import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { Spinner } from '../../../atoms/Spinner';
import { Stack } from '../../../atoms/Stack';
import { CoreUpdate, useCoreLatest } from './CoreUpdate';
import { FirmwareUpload } from '../FirmwareUpload';
import { Accordion, useAccordionGroup } from '../../../molecules/Accordion';
import { SetMenu } from '../../../molecules/SetMenu';
import { useSelfUpdate, type UpdatePhase } from '../../../../hooks/useSelfUpdate';
import { following, openUpdateScreen, runOf } from '../../../../hooks/updates';
import { useI18n } from '../../../../lib/i18n';
import { shortVersionLabel, versionLabel } from '../../../../lib/version';
import type { VersionInfo } from '../../../../lib/types';
import { VersionCard } from './VersionCard';
import { useUpdateConfirm } from './UpdateConfirm';

// A tick only once the speaker is up to date: not before a check, nor after one failed; a
// spinner while it works.
function StatusIcon({ phase }: { readonly phase: UpdatePhase }) {
  if (phase === 'busy') return <Spinner />;
  return phase === 'done' || phase === 'updated' ? (
    <Icon.check width="16" height="16" aria-hidden="true" />
  ) : (
    <Icon.info width="16" height="16" aria-hidden="true" />
  );
}

// soundtapd replacing itself with the latest stable release. A newer one shows in the card
// drawn for a new version, with its notes; otherwise the card is the version the speaker
// runs, and the button asks it to look. Update on an offered release asks once first, as
// stock does. Once the speaker takes an update on, the update screen (UpdateScreen)
// follows it through.
function SelfUpdate({
  speaker,
  version,
}: {
  readonly speaker: string;
  readonly version: VersionInfo;
}) {
  const { t } = useI18n();
  const update = useSelfUpdate(speaker, version);
  const confirm = useUpdateConfirm(() => {
    void update.install();
  });
  const offer = update.newer ? update.target : null;
  const now = shortVersionLabel(version);
  const status = (
    <>
      <StatusIcon phase={update.phase} />
      <span role="status">{update.text || t('upIdle')}</span>
    </>
  );
  return (
    <>
      <VersionCard
        offer={!!offer}
        kicker={offer ? t('updatesNewVersion') : t('upFirmware')}
        title={offer ? `SoundTap app ${offer.tag.replace(/^app-v/u, '')}` : versionLabel(version)}
        meta={offer ? `${t('updatesNow', { version: now })} · ${t('updatesSigned')}` : status}
        notes={offer?.notes}
      >
        {offer && update.text && <Hint role="status">{update.text}</Hint>}
        <Button
          variant={offer ? 'stockholm' : 'ghost'}
          wide
          disabled={update.phase === 'busy'}
          onClick={
            offer
              ? confirm.ask
              : () => {
                  void update.install();
                }
          }
        >
          {offer ? t('updateInstall') : t('updateNow')}
        </Button>
      </VersionCard>
      {confirm.sheet}
      <Hint>{t('updateVerified')}</Hint>
    </>
  );
}

// Bose's own update files, kept by others: the link the installer gives too.
const BOSE_ARCHIVE = 'https://archive.org/download/bose-soundtouch-software-and-firmware/Firmware/';

// Where Bose's file can be found, the link in the sentence.
function ArchiveNote() {
  const { t } = useI18n();
  const [before, after = ''] = t('updatesArchive').split('{link}');
  return (
    <>
      {before}
      <a href={BOSE_ARCHIVE} target="_blank" rel="noreferrer">
        {t('updatesArchiveLink')}
      </a>
      {after}
    </>
  );
}

// The other ways to put software on the speaker, each a page of its own: a file of your
// choice, signed or your own unsigned build, or Bose's own update file to go back (the same
// upload, which knows Bose's file). Every upload asks for one press on the speaker, signed
// or not (ADR 0048). The installer sends owners here for the way back.
function OtherWays({ speaker }: { readonly speaker: string }) {
  const { t } = useI18n();
  const page = useAccordionGroup<'upload' | 'bose'>();
  return (
    <SetMenu title={t('updatesOther')} note={<ArchiveNote />}>
      <Accordion
        {...page('upload')}
        icon="upload"
        title={t('updatesUpload')}
        desc={t('updatesUploadSummary')}
      >
        <FirmwareUpload speaker={speaker} />
      </Accordion>
      <Accordion
        {...page('bose')}
        icon="undo"
        title={t('updatesBose')}
        desc={t('updatesBoseSummary')}
      >
        <Stack gap={8}>
          <Hint>{t('updatesBoseHint')}</Hint>
          <FirmwareUpload speaker={speaker} />
        </Stack>
      </Accordion>
    </SetMenu>
  );
}

// The Updates page: what an update does under its title, then this speaker's version in one
// card, every speaker at once, and the other ways (a file of your own, Bose's file to go
// back). A newer SoundTap Core on offer is the card: Core carries the app, so the app's own
// card waits until the speaker runs the newest Core. Otherwise the app's card offers a newer
// app or shows the one it runs. Coming back here while the speaker updates shows the update
// screen again.
export function UpdateSettings({
  speaker,
  version,
}: {
  readonly speaker: string;
  readonly version: VersionInfo | null;
}): ReactElement {
  const { t } = useI18n();
  useEffect(() => {
    if (following(runOf(speaker))) openUpdateScreen(speaker);
  }, [speaker]);
  const firmware = version?.updateMode === 'firmware';
  const core = useCoreLatest(speaker, firmware);
  return (
    <>
      <Note>{t('updatesIntro')}</Note>
      {core && <CoreUpdate key={speaker} speaker={speaker} latest={core} version={version} />}
      {!core && firmware && version.updatable && <SelfUpdate speaker={speaker} version={version} />}
      {!core && !(firmware && version.updatable) && (
        <VersionCard kicker={t('upFirmware')} title={versionLabel(version) || '—'} />
      )}
      {firmware ? <OtherWays speaker={speaker} /> : <Hint>{t('updatesOnSpeaker')}</Hint>}
    </>
  );
}
