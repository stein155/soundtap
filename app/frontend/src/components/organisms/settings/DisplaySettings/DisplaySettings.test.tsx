// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import type { DisplayConfig } from '../../../../lib/types';
import { clockText, DisplaySettings } from '.';

const clock = (patch: Partial<DisplayConfig> = {}): DisplayConfig => ({
  clockOff: false,
  hour12: false,
  timezone: 'Europe/Amsterdam',
  ...patch,
});

describe('DisplaySettings', () => {
  it('switches the clock off', () => {
    const onChange = vi.fn();
    const { getByText } = renderWithI18n(<DisplaySettings config={clock()} onChange={onChange} />);
    fireEvent.click(getByText('Clock in standby'));
    expect(onChange).toHaveBeenCalledWith({ clockOff: true });
  });

  it('offers no town, date or weather: the time alone', () => {
    const { queryByText, queryByRole } = renderWithI18n(
      <DisplaySettings config={clock()} onChange={vi.fn()} />
    );
    expect(queryByRole('searchbox')).toBeNull();
    expect(queryByText(/weather/iu)).toBeNull();
  });

  it('hides the format while the clock is off, and never offers a brightness', () => {
    const { queryByText, rerender } = renderWithI18n(
      <DisplaySettings config={clock({ clockOff: true })} onChange={vi.fn()} />
    );
    expect(queryByText('Show the time as')).toBeNull();
    rerender(<DisplaySettings config={clock()} onChange={vi.fn()} />);
    expect(queryByText('Show the time as')).toBeTruthy();
    expect(queryByText(/brightness/iu)).toBeNull();
  });

  it('draws the time as the standby screen does', () => {
    const evening = new Date(2026, 10, 25, 19, 42);
    expect(clockText(evening, false)).toBe('19:42');
    expect(clockText(evening, true)).toBe('7:42');
    expect(clockText(new Date(2026, 10, 25, 0, 5), true)).toBe('12:05');
    expect(clockText(new Date(2026, 10, 25, 7, 5), false)).toBe('07:05');
  });
});
