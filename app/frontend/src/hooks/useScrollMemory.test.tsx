// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { useScrollMemory } from './useScrollMemory';

function List({ at }: { readonly at: string }) {
  const [ref, onScroll] = useScrollMemory(at);
  return <div data-testid="body" ref={ref} onScroll={onScroll} />;
}

// Back lands where you were; a new level starts at the top.
describe('useScrollMemory', () => {
  it('keeps a scroll position per key', () => {
    const { getByTestId, rerender } = render(<List at="root" />);
    const el = getByTestId('body');
    el.scrollTop = 600;
    el.dispatchEvent(new Event('scroll'));
    rerender(<List at="root>jazz" />);
    expect(el.scrollTop).toBe(0);
    rerender(<List at="root" />);
    expect(el.scrollTop).toBe(600);
  });
});
