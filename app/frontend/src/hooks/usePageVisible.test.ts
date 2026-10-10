// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePageVisible } from './usePageVisible';

describe('usePageVisible', () => {
  it('follows document visibility', () => {
    let state: DocumentVisibilityState = 'visible';
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state });
    const { result } = renderHook(() => usePageVisible());
    expect(result.current).toBe(true);
    act(() => {
      state = 'hidden';
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(result.current).toBe(false);
  });
});
