// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import { BluetoothSettings } from '.';

vi.mock('../../../../lib/api', () => ({
  getBluetooth: vi.fn(),
  clearBluetoothPairings: vi.fn().mockResolvedValue(undefined),
  pairBluetooth: vi.fn().mockResolvedValue(undefined),
}));

describe('BluetoothSettings', () => {
  it('stops saying it is pairing when the window closes', async () => {
    const pairing = 'Findable for five minutes: pair from your phone now';
    vi.mocked(api.getBluetooth).mockResolvedValueOnce({ devices: [], discoverable: true } as never);
    const { findByRole, findByText, queryByText } = renderWithI18n(
      <BluetoothSettings speaker="" initial={{ devices: [] } as never} />
    );
    fireEvent.click(await findByRole('button', { name: 'Pair a device' }));
    expect(await findByText(pairing)).toBeTruthy();
    vi.mocked(api.getBluetooth).mockResolvedValue({ devices: [] } as never);
    await findByRole('button', { name: 'Pair a device' }, { timeout: 6000 });
    expect(queryByText(pairing)).toBeNull();
  }, 8000);

  it('keeps forgotten devices gone when an older poll answers late', async () => {
    vi.useFakeTimers();
    const phone = { devices: [{ address: 'aa', name: 'Phone', connected: false }] };
    let late: (s: typeof phone) => void = () => {};
    vi.mocked(api.getBluetooth)
      .mockReturnValueOnce(new Promise((r) => (late = r)) as never)
      .mockResolvedValue({ devices: [] } as never);
    const { getAllByText, queryByText } = renderWithI18n(
      <BluetoothSettings speaker="" initial={phone as never} />
    );
    await act(() => vi.advanceTimersByTimeAsync(5000)); // the poll that will answer late
    fireEvent.click(getAllByText('Forget all devices')[0]);
    await act(async () => fireEvent.click(getAllByText('Forget all devices').at(-1)!));
    expect(queryByText('Phone')).toBeNull();
    await act(async () => late(phone));
    expect(queryByText('Phone')).toBeNull();
    vi.useRealTimers();
  });
});
