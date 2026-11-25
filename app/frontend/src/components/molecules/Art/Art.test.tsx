// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent, act } from '@testing-library/react';
import { Art } from './index';
import { must } from '../../../test/must';

const ART_RETRY_MS = 30_000;

describe('Art', () => {
  it('tries a failed picture again after a while', () => {
    vi.useFakeTimers();
    const { container } = render(<Art src="/logo.png" name="Radio One" />);
    fireEvent.error(must(container.querySelector('img'), 'the artwork'));
    expect(container.querySelector('img')).toBeNull();
    act(() => {
      vi.advanceTimersByTime(ART_RETRY_MS);
    });
    expect(container.querySelector('img')).not.toBeNull();
    vi.useRealTimers();
  });
});
