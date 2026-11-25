// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { KeyboardEvent } from 'react';

const STEP: Partial<Record<string, number>> = {
  ArrowRight: 1,
  ArrowDown: 1,
  ArrowLeft: -1,
  ArrowUp: -1,
};

export interface RadioKeys {
  tabIndex: (index: number) => 0 | -1;
  onKeyDown: (e: KeyboardEvent<HTMLElement>) => void;
}

// Keyboard behaviour of an ARIA radio group: the group is one tab stop (the checked
// radio, or the first when none is), the arrows move focus and selection, wrapping at the
// ends, and Home/End jump to the first and last. Space and Enter already click the button.
// onKeyDown goes on each radio, the element that has the focus.
export function useRadioKeys(
  count: number,
  selected: number,
  select: (index: number) => void
): RadioKeys {
  const stop = selected >= 0 ? selected : 0;
  const tabIndex = (index: number) => (index === stop ? 0 : -1);
  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    const step = STEP[e.key];
    let next: number;
    if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = count - 1;
    else if (step !== undefined) next = (stop + step + count) % count;
    else return;
    e.preventDefault();
    select(next);
    const group = e.currentTarget.closest('[role="radiogroup"]');
    Array.from(group?.querySelectorAll<HTMLElement>('[role="radio"]') ?? [])
      .at(next)
      ?.focus();
  };
  return { tabIndex, onKeyDown };
}
