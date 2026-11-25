// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { LONG_LOST_MS, useLongLost } from './useLongLost';

afterEach(() => {
  vi.useRealTimers();
});

describe('useLongLost', () => {
  // A restart or a short Wi-Fi gap stays a banner; only a speaker that stays away gets the page.
  it('turns on only after the speaker stays away, and off once it answers', () => {
    vi.useFakeTimers();
    const { result, rerender } = renderHook(({ lost }) => useLongLost(lost), {
      initialProps: { lost: true },
    });
    expect(result.current).toBe(false);
    act(() => {
      vi.advanceTimersByTime(LONG_LOST_MS - 1);
    });
    expect(result.current).toBe(false);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(result.current).toBe(true);
    rerender({ lost: false });
    expect(result.current).toBe(false);
    // The next loss counts from nothing again.
    rerender({ lost: true });
    expect(result.current).toBe(false);
  });
});
