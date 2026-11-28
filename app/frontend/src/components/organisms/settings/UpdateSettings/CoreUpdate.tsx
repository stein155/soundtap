// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { useCoreUpdate } from '../../../../hooks/useCoreUpdate';
import { getReleases } from '../../../../lib/api';
import { useI18n } from '../../../../lib/i18n';
import { shortVersionLabel } from '../../../../lib/version';
import type { ReleaseRef, VersionInfo } from '../../../../lib/types';
import { VersionCard } from './VersionCard';
import { useUpdateConfirm } from './UpdateConfirm';

// The newest SoundTap Core when it is newer than the speaker's, else null; enabled only
// where the speaker updates its own firmware. A switch to another speaker drops what the
// previous one was still answering.
export function useCoreLatest(speaker: string, enabled = true): ReleaseRef | null {
  const [latest, setLatest] = useState<{ speaker: string; ref: ReleaseRef } | null>(null);
  useEffect(() => {
    if (!enabled) return undefined;
    let current = true;
    void getReleases(speaker).then((r) => {
      const ref = r?.core?.latest;
      if (current) setLatest(ref ? { speaker, ref } : null);
    });
    return () => {
      current = false;
    };
  }, [speaker, enabled]);
  return enabled && latest?.speaker === speaker ? latest.ref : null;
}

// A newer SoundTap Core than the speaker runs, in the card drawn for a new version. Update
// asks once, as stock does, and then the speaker downloads it itself into its spare slot, without a press (a
// signed release it fetches, ADR 0048), restarts into it on its own, and the update screen
// (UpdateScreen) follows it away and back. The parent keys it by speaker, so a switch
// starts it afresh.
export function CoreUpdate({
  speaker,
  latest,
  version,
}: {
  readonly speaker: string;
  readonly latest: ReleaseRef;
  readonly version: VersionInfo | null;
}): ReactElement {
  const { t } = useI18n();
  const number = latest.tag.replace(/^os-v/u, '');
  const update = useCoreUpdate(speaker, number);
  const confirm = useUpdateConfirm(() => {
    void update.install();
  });
  const now = shortVersionLabel(version);
  const following = update.phase === 'busy' || update.phase === 'updated';

  return (
    <>
      <VersionCard
        offer
        kicker={t('updatesNewVersion')}
        title={`SoundTap Core ${number}`}
        meta={[now && t('updatesNow', { version: now }), t('updatesSigned')]
          .filter(Boolean)
          .join(' · ')}
        notes={latest.notes}
      >
        {update.text && (
          <Hint
            error={update.phase === 'error'}
            role={update.phase === 'error' ? 'alert' : 'status'}
          >
            {update.text}
          </Hint>
        )}
        {update.phase !== 'done' && (
          <Button variant="stockholm" wide disabled={following} onClick={confirm.ask}>
            {t('updateInstall')}
          </Button>
        )}
      </VersionCard>
      {confirm.sheet}
    </>
  );
}
