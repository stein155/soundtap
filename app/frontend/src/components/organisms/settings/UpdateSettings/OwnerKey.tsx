// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { Icon } from '../../../atoms/Icon';
import { FormSection } from '../../../atoms/FormSection';
import { Hint } from '../../../atoms/Hint';
import { Stack } from '../../../atoms/Stack';
import { TextField } from '../../../atoms/TextField';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { LabeledField } from '../../../molecules/LabeledField';
import { StatusCard } from '../../../molecules/StatusCard';
import { getOwnerKey, removeOwnerKey, setOwnerKey } from '../../../../lib/api';
import { failureText } from '../../../../lib/failure';
import { useI18n } from '../../../../lib/i18n';
import type { OwnerKey as OwnerKeyState } from '../../../../lib/types';

// An ed25519 public key as release.ParseOwnerKey takes it: the 32 raw bytes in padded
// standard base64. The speaker checks it again, and refuses a point of small order too.
const KEY = /^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]=$/;

// The key field and the button that sets what it holds, after asking.
function KeyEntry({ onSet }: { onSet: (key: string) => Promise<void> }) {
  const { t } = useI18n();
  const [draft, setDraft] = useState('');
  const key = draft.trim();
  const invalid = key !== '' && !KEY.test(key);
  return (
    <>
      <LabeledField
        id="owner-key"
        label={t('ownerKeyLabel')}
        error={invalid ? t('ownerKeyInvalid') : null}
      >
        <TextField
          id="owner-key"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          maxLength={200}
        />
      </LabeledField>
      <ConfirmAction
        label={t('ownerKeySet')}
        warning={t('ownerKeySetWarn')}
        confirmLabel={t('ownerKeySet')}
        disabled={key === '' || invalid}
        onConfirm={() => onSet(key)}
      />
    </>
  );
}

// The owner's own signing key (ADR 0048): builds signed with it install on this speaker as
// a SoundTap release does, from the Updates page and from a USB stick. Setting and
// removing it wait for a key held on the speaker. The parent keys it by speaker.
export function OwnerKey({ speaker }: { speaker: string }) {
  const { t } = useI18n();
  const [state, setState] = useState<OwnerKeyState | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let current = true;
    void getOwnerKey(speaker).then((k) => {
      if (current) setState(k);
    });
    return () => {
      current = false;
    };
  }, [speaker]);

  // A speaker that predates the route, or has no environment to keep a key in.
  if (!state?.available) return null;

  const change = async (run: () => Promise<OwnerKeyState>, failed: string) => {
    setError('');
    try {
      setState(await run());
    } catch (err) {
      setError(failureText(err, failed, t));
    }
  };
  const Status = state.active ? Icon.check : Icon.info;

  return (
    <Stack gap={8} offset={8}>
      <FormSection spaced>{t('ownerKey')}</FormSection>
      <StatusCard icon={<Status width="22" height="22" />}>
        {state.active
          ? t('ownerKeyActive', { fingerprint: state.fingerprint ?? '' })
          : t('ownerKeyNone')}
      </StatusCard>
      {state.active ? (
        <ConfirmAction
          destructive
          label={t('ownerKeyRemove')}
          warning={t('ownerKeyRemoveWarn')}
          confirmLabel={t('ownerKeyRemove')}
          onConfirm={() => change(() => removeOwnerKey(speaker), t('ownerKeyRemoveFailed'))}
        />
      ) : (
        <KeyEntry
          onSet={(key) => change(() => setOwnerKey(key, speaker), t('ownerKeySetFailed'))}
        />
      )}
      <Hint>{t('ownerKeyHint')}</Hint>
      {error && (
        <Hint error role="alert">
          {error}
        </Hint>
      )}
    </Stack>
  );
}
