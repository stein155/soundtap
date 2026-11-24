// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import type { DisplayConfig } from '../../../../lib/types';
import { DisplaySettings } from '.';

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
      <DisplaySettings config={clock()} onChange={() => {}} />
    );
    expect(queryByRole('searchbox')).toBeNull();
    expect(queryByText(/weather/i)).toBeNull();
  });

  it('hides the format and brightness while the clock is off', () => {
    const { queryByText } = renderWithI18n(
      <DisplaySettings config={clock({ clockOff: true })} onChange={() => {}} />
    );
    expect(queryByText('Brightness')).toBeNull();
  });
});
