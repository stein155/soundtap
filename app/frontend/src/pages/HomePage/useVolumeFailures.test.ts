// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useVolumeFailures } from './useVolumeFailures';

describe('useVolumeFailures', () => {
  it('says each failure once, under the name of that moment, and not again on a rename', () => {
    const told = vi.fn();
    const failed = vi.fn((name: string) => () => {
      told(name);
    });
    const { rerender } = renderHook(
      ({ failures, name }: { failures: number; name: string }) =>
        useVolumeFailures(failures, failed, name),
      { initialProps: { failures: 0, name: 'Kitchen' } }
    );
    expect(told).not.toHaveBeenCalled();
    rerender({ failures: 1, name: 'Kitchen' });
    expect(told).toHaveBeenCalledExactlyOnceWith('Kitchen');
    rerender({ failures: 1, name: 'Keuken' });
    expect(told).toHaveBeenCalledOnce();
    rerender({ failures: 2, name: 'Keuken' });
    expect(told).toHaveBeenLastCalledWith('Keuken');
    expect(told).toHaveBeenCalledTimes(2);
  });
});
