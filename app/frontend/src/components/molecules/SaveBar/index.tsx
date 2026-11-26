// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState, type ReactElement } from 'react';
import { Button } from '../../atoms/Button';
import { useI18n } from '../../../lib/i18n';
import { Bar } from './styled';

const SAVED_MS = 2000;

interface Props {
  readonly dirty: boolean;
  // Rejects when the save failed; the caller shows why.
  readonly onSave: () => Promise<void>;
}

// The sticky Save button under a settings form. It says "Saved" for a moment after a
// save that went through, until the next edit.
export function SaveBar({ dirty, onSave }: Props): ReactElement | null {
  const { t } = useI18n();
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const save = async () => {
    setBusy(true);
    try {
      await onSave();
      setSaved(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setSaved(false), SAVED_MS);
    } catch {
      setSaved(false);
    } finally {
      setBusy(false);
    }
  };
  // Only while there is something to save (or just was): sections that act on their own,
  // like Wi-Fi or updates, no longer sit above a greyed-out Save.
  if (!dirty && !saved && !busy) return null;
  return (
    <Bar>
      <Button
        wide
        disabled={!dirty || busy}
        onClick={() => {
          void save();
        }}
      >
        {saved && !dirty ? t('saved') : t('save')}
      </Button>
    </Bar>
  );
}
