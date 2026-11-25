// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, type RefObject } from 'react';

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

// Modal focus for a dialog while it is open: focus moves into it, Tab cycles inside it,
// and on close focus returns to whatever opened it.
export function useDialogFocus(dialog: RefObject<HTMLElement | null>, open: boolean): void {
  useEffect(() => {
    const root = dialog.current;
    if (!open || !root) return;
    const active = document.activeElement;
    // Whatever had focus, a control or a drawing in one, takes it back on close.
    const opener = active instanceof HTMLElement || active instanceof SVGElement ? active : null;
    root.focus({ preventScroll: true });
    const trap = (e: KeyboardEvent) => {
      if (e.key !== 'Tab') return;
      const items = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)];
      const first = items.at(0);
      const last = items.at(-1);
      if (!first || !last) return;
      if (e.shiftKey && (document.activeElement === first || document.activeElement === root)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    root.addEventListener('keydown', trap);
    return () => {
      root.removeEventListener('keydown', trap);
      opener?.focus({ preventScroll: true });
    };
  }, [dialog, open]);
}
