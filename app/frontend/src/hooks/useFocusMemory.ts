// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useEffectEvent, type FocusEvent, type RefObject } from 'react';

// Moving to another level of a page removes the row that had the focus, which drops it to
// the body: a keyboard or screen reader then starts over at the top of the app.
// Going deeper lands on the page; going back lands on the row that opened that level.
export function useFocusMemory(
  open: boolean,
  key: string | undefined,
  page: RefObject<HTMLElement | null>,
  body: RefObject<HTMLElement | null>
): (e: FocusEvent<HTMLElement>) => void {
  const rows = useRef(new Map<string | undefined, string>());
  const restore = useEffectEvent(() => {
    const active = document.activeElement;
    if (!open || (active !== document.body && active !== page.current)) return;
    const name = rows.current.get(key);
    const candidates = body.current?.querySelectorAll<HTMLElement>('button, a, [tabindex]') ?? [];
    const row = name ? [...candidates].find((el) => el.textContent === name) : undefined;
    (row ?? page.current)?.focus({ preventScroll: true });
  });
  useEffect(() => restore(), [key]);
  return (e: FocusEvent<HTMLElement>) => {
    if (e.target.textContent) rows.current.set(key, e.target.textContent);
  };
}
