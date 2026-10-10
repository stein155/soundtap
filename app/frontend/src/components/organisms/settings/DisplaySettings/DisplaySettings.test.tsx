// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import type { DisplayConfig, Place } from '../../../../lib/types';
import { DisplaySettings } from '.';

vi.mock('../../../../lib/api', () => ({ geocode: vi.fn() }));

const town = (name: string) => ({
  name,
  region: '',
  country: 'NL',
  lat: name.length,
  lon: 5,
  timezone: 'Europe/Amsterdam',
});

describe('DisplaySettings town search', () => {
  it('keeps the newest results when an older search answers late', async () => {
    const answers: ((p: Place[]) => void)[] = [];
    vi.mocked(api.geocode).mockImplementation(() => new Promise((r) => answers.push(r)));
    const { container, findByText, queryByText } = renderWithI18n(
      <DisplaySettings config={{ city: '' } as DisplayConfig} speaker="" onChange={() => {}} />
    );
    const input = container.querySelector('#town-query')!;
    fireEvent.change(input, { target: { value: 'Oldtown' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.change(input, { target: { value: 'Newtown' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await act(async () => answers[1]([town('Newtown')]));
    await findByText(/Newtown/);
    await act(async () => answers[0]([town('Oldtown1')]));
    expect(queryByText(/Oldtown1/)).toBeNull();
  });
});
