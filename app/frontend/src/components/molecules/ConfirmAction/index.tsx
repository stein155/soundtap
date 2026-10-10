// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import type { ReactNode } from 'react';
import { Button } from '../../atoms/Button';
import { Hint } from '../../atoms/Hint';
import { useI18n } from '../../../lib/i18n';
import { Actions } from './styled';

interface Props {
  label: ReactNode;
  // The warning shown once the action is asked for, and the button that commits it.
  warning: ReactNode;
  confirmLabel: ReactNode;
  onConfirm: () => Promise<void> | void;
  // An action that erases something reads as destructive at both steps.
  destructive?: boolean;
  disabled?: boolean;
  // Draws the first step itself (a link rather than a button); call `ask` to go on.
  trigger?: (ask: () => void) => ReactNode;
  // Cancel at the second step also undoes the first (a picked file is put back).
  onCancel?: () => void;
}

// An action that touches NAND, drops playback or erases something: the first press only
// asks, the second commits.
export function ConfirmAction({
  label,
  warning,
  confirmLabel,
  onConfirm,
  destructive,
  disabled,
  trigger,
  onCancel,
}: Props) {
  const { t } = useI18n();
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  if (!asking && trigger) return <>{trigger(() => setAsking(true))}</>;
  if (!asking)
    return (
      <Button
        variant={destructive ? 'delete' : 'primary'}
        wide
        disabled={disabled}
        onClick={() => setAsking(true)}
      >
        {label}
      </Button>
    );
  const confirm = async () => {
    setBusy(true);
    try {
      await onConfirm();
    } finally {
      setBusy(false);
      setAsking(false);
    }
  };
  return (
    <>
      <Hint error>{warning}</Hint>
      <Actions>
        {/* A running operation cannot be called off; Cancel would only hide it. */}
        <Button
          variant="ghost"
          disabled={busy}
          onClick={() => {
            setAsking(false);
            onCancel?.();
          }}
        >
          {t('cancel')}
        </Button>
        <Button
          variant={destructive ? 'danger' : 'primary'}
          disabled={disabled || busy}
          onClick={() => void confirm()}
        >
          {confirmLabel}
        </Button>
      </Actions>
    </>
  );
}
