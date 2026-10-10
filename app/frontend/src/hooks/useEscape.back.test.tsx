// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useEscape } from './useEscape';

const pop = () => {
  window.history.replaceState(null, '');
  window.dispatchEvent(new PopStateEvent('popstate'));
};
const tick = () => new Promise((r) => setTimeout(r, 0));

// The device's back closes the top overlay instead of leaving the app.
describe('useEscape and the back button', () => {
  it('runs only the top overlay on back, and keeps an entry while one stays open', async () => {
    const lower = vi.fn();
    const upper = vi.fn();
    const before = window.history.length;
    renderHook(() => useEscape(true, lower));
    const top = renderHook(() => useEscape(true, upper));
    expect(window.history.length).toBe(before + 1);
    pop();
    expect(upper).toHaveBeenCalledTimes(1);
    expect(lower).not.toHaveBeenCalled();
    await tick();
    expect(window.history.length).toBe(before + 2);
    top.unmount();
    pop();
    expect(lower).toHaveBeenCalledTimes(1);
  });

  // Stepping back on close could leave the app for the site before it.
  it('never steps back through history by itself', async () => {
    const back = vi.spyOn(window.history, 'back');
    const before = window.history.length;
    const useStack = ({ first }: { first: boolean }) => {
      useEscape(first, () => {});
      useEscape(!first, () => {});
    };
    const { rerender, unmount } = renderHook(useStack, { initialProps: { first: true } });
    rerender({ first: false });
    unmount();
    await tick();
    expect(back).not.toHaveBeenCalled();
    expect(window.history.length).toBeLessThanOrEqual(before + 1);
    back.mockRestore();
  });

  it('reuses the entry for the next overlay', () => {
    pop();
    const before = window.history.length;
    renderHook(() => useEscape(true, () => {})).unmount();
    renderHook(() => useEscape(true, () => {})).unmount();
    expect(window.history.length).toBe(before + 1);
  });
});
