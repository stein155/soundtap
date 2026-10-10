// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderHook } from '@testing-library/react';
import { useEscape } from './useEscape';

describe('useEscape', () => {
  it('closes only the overlay opened last', () => {
    const below = vi.fn();
    const top = vi.fn();
    renderHook(() => useEscape(true, below));
    const upper = renderHook(() => useEscape(true, top));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(top).toHaveBeenCalledTimes(1);
    expect(below).not.toHaveBeenCalled();

    upper.unmount();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(below).toHaveBeenCalledTimes(1);
  });
});
