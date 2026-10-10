// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useErrorReporter } from './useErrorReporter';
import { useI18n } from '../lib/i18n';

vi.mock('../lib/i18n', async (actual) => ({
  ...(await actual<typeof import('../lib/i18n')>()),
  useI18n: vi.fn(),
}));

const speak = (word: string) =>
  vi.mocked(useI18n).mockReturnValue({ t: () => word } as unknown as ReturnType<typeof useI18n>);

describe('useErrorReporter', () => {
  it('drops a toast when the language changes, and a late failure uses the new one', () => {
    speak('kapot');
    const { result, rerender } = renderHook(() => useErrorReporter());
    const report = result.current.failed('Keuken');
    act(() => report());
    expect(result.current.message).toBe('kapot');

    speak('broken');
    rerender();
    expect(result.current.message).toBe('');

    act(() => report()); // the catch handler of a request started before the change
    expect(result.current.message).toBe('broken');
  });
});
