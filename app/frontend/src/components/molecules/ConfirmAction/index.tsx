// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import type { ReactNode } from 'react';
import { Button } from '../../atoms/Button';
import { ConfirmSheet } from '../ConfirmSheet';

interface Props {
  label: string;
  // The dialog's question; the label when left out.
  title?: string;
  // What the action does, in the dialog, and the button there that commits it.
  warning: ReactNode;
  confirmLabel: ReactNode;
  onConfirm: () => Promise<void> | void;
  // An action that erases something reads as destructive at both steps.
  destructive?: boolean;
  disabled?: boolean;
  // Draws the first step itself (a row or a link rather than a button); call `ask` to go on.
  trigger?: (ask: () => void) => ReactNode;
}

// An action that touches NAND, drops playback or erases something: the press only asks,
// in a dialog, and its confirm commits. The button stays busy while the action runs.
export function ConfirmAction({
  label,
  title,
  warning,
  confirmLabel,
  onConfirm,
  destructive = false,
  disabled = false,
  trigger,
}: Props): ReactElement {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const ask = () => setAsking(true);
  const confirm = async () => {
    setBusy(true);
    try {
      await onConfirm();
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      {trigger ? (
        trigger(ask)
      ) : (
        <Button
          variant={destructive ? 'delete' : 'primary'}
          wide
          disabled={disabled || busy}
          aria-busy={busy}
          onClick={ask}
        >
          {label}
        </Button>
      )}
      <ConfirmSheet
        open={asking}
        title={title ?? label}
        body={warning}
        confirmLabel={confirmLabel}
        destructive={destructive}
        onConfirm={() => void confirm()}
        onClose={() => setAsking(false)}
      />
    </>
  );
}
