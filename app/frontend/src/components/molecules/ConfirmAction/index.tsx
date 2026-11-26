// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import type { ReactNode } from 'react';
import { Button } from '../../atoms/Button';
import { ConfirmSheet } from '../ConfirmSheet';

interface Props {
  readonly label: string;
  // The dialog's question; the label when left out.
  readonly title?: string;
  // What the action does, in the dialog, and the button there that commits it.
  readonly warning: ReactNode;
  readonly confirmLabel: ReactNode;
  readonly onConfirm: () => Promise<void> | void;
  // An action that erases something reads as destructive at both steps.
  readonly destructive?: boolean;
  readonly disabled?: boolean;
  // Draws the first step itself (a row or a link rather than a button); call `ask` to go on.
  readonly trigger?: (ask: () => void) => ReactNode;
}

// An action that touches NAND, drops playback or erases something: the press only asks,
// in a dialog, and its confirm commits. The button stays busy while the action runs.
// onConfirm says how a failure went itself; one it lets through ends here, not as an
// unhandled rejection.
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
    } catch {
      // Already the action's own to report: the button comes back for another try.
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
        onConfirm={() => {
          void confirm();
        }}
        onClose={() => setAsking(false)}
      />
    </>
  );
}
