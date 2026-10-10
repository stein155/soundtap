// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useLongPress } from './useLongPress';

describe('useLongPress', () => {
  it('cancels every hold when two pointers went down', () => {
    vi.useFakeTimers();
    const onHold = vi.fn();
    const { result } = renderHook(() => useLongPress(onHold, () => {}));
    result.current.onPointerDown();
    vi.advanceTimersByTime(100);
    result.current.onPointerDown();
    result.current.onPointerUp();
    vi.advanceTimersByTime(1000);
    expect(onHold).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it('holds from the keyboard, and leaves other keys alone', () => {
    const onHold = vi.fn();
    const { result } = renderHook(() => useLongPress(onHold, () => {}));
    const press = (key: string, shiftKey = false) =>
      result.current.onKeyDown({ key, shiftKey, preventDefault: () => {} } as never);
    press('Enter');
    press('F10');
    expect(onHold).not.toHaveBeenCalled();
    press('ContextMenu');
    press('F10', true);
    expect(onHold).toHaveBeenCalledTimes(2);
  });
});
