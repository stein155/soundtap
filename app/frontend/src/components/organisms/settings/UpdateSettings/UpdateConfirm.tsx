// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import { ConfirmSheet } from '../../../molecules/ConfirmSheet';
import { useI18n } from '../../../../lib/i18n';

// An update asks first, as stock does: one short question, then it starts. `ask` opens the
// question; the sheet goes with the returned element.
export interface UpdateConfirm {
  ask: () => void;
  sheet: ReactElement;
}

export function useUpdateConfirm(start: () => void): UpdateConfirm {
  const { t } = useI18n();
  const [asking, setAsking] = useState(false);
  const sheet = (
    <ConfirmSheet
      open={asking}
      title={t('updateInstall')}
      body={t('updateConfirmBody')}
      confirmLabel={t('adaptiqStart')}
      onConfirm={start}
      onClose={() => setAsking(false)}
    />
  );
  return { ask: () => setAsking(true), sheet };
}
