// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import type { BluetoothStatus } from '../../../../lib/api/settings';
import { BluetoothSettings } from '.';

vi.mock('../../../../lib/api', () => ({
  getBluetooth: vi.fn(),
  forgetBluetoothDevice: vi.fn().mockResolvedValue(undefined),
  pairBluetooth: vi.fn().mockResolvedValue(undefined),
}));

// What the speaker answers: no name and not findable unless a test says so.
const status = (over: Partial<BluetoothStatus> = {}): BluetoothStatus => ({
  adapter: true,
  alias: '',
  powered: true,
  discoverable: false,
  devices: [],
  ...over,
});

const phoneDevice = { address: '60:F4:45:11:22:33', name: 'Phone', connected: true };
const two = status({
  alias: 'Kitchen',
  devices: [phoneDevice, { address: 'AA:BB:CC:DD:EE:FF', name: 'Tablet', connected: false }],
});

describe('BluetoothSettings', () => {
  it('stops saying it is findable when the window closes', async () => {
    const findable = 'Findable now';
    vi.mocked(api.getBluetooth).mockResolvedValueOnce(status({ discoverable: true }));
    const { findByRole, findByText, queryByText } = renderWithI18n(
      <BluetoothSettings speaker="" initial={status()} />
    );
    fireEvent.click(await findByRole('button', { name: 'Pair a device' }));
    expect(await findByText(findable)).toBeTruthy();
    vi.mocked(api.getBluetooth).mockResolvedValue(status());
    await findByRole('button', { name: 'Pair a device' }, { timeout: 6000 });
    expect(queryByText(findable)).toBeNull();
  }, 8000);

  it('forgets one device by its address and offers no forget-all', async () => {
    vi.mocked(api.getBluetooth).mockResolvedValue({ ...two, devices: [phoneDevice] });
    const { getByRole, getByText, queryByText, findByText } = renderWithI18n(
      <BluetoothSettings speaker="Kitchen" initial={two} />
    );
    expect(getByText('Connected')).toBeTruthy();
    expect(queryByText(/forget all/iu)).toBeNull();
    await act(async () => fireEvent.click(getByRole('button', { name: 'Forget Tablet' })));
    expect(api.forgetBluetoothDevice).toHaveBeenCalledWith('AA:BB:CC:DD:EE:FF', 'Kitchen');
    expect(await findByText('Phone')).toBeTruthy();
    expect(queryByText('Tablet')).toBeNull();
  });

  it('says so when a device could not be forgotten', async () => {
    vi.mocked(api.forgetBluetoothDevice).mockRejectedValueOnce(new Error('refused'));
    vi.mocked(api.getBluetooth).mockResolvedValue(two);
    const { getByRole, findByText } = renderWithI18n(
      <BluetoothSettings speaker="" initial={two} />
    );
    await act(async () => fireEvent.click(getByRole('button', { name: 'Forget Phone' })));
    expect(await findByText('Couldn’t forget Phone')).toBeTruthy();
  });

  it('keeps a forgotten device gone when an older poll answers late', async () => {
    vi.useFakeTimers();
    const phone = status({ devices: [{ address: 'aa', name: 'Phone', connected: false }] });
    let late: (s: BluetoothStatus) => void = vi.fn();
    vi.mocked(api.getBluetooth)
      .mockReturnValueOnce(
        new Promise((r) => {
          late = r;
        })
      )
      .mockResolvedValue(status());
    const { getByRole, queryByText } = renderWithI18n(
      <BluetoothSettings speaker="" initial={phone} />
    );
    await act(async () => vi.advanceTimersByTimeAsync(5000)); // the poll that will answer late
    await act(async () => fireEvent.click(getByRole('button', { name: 'Forget Phone' })));
    expect(queryByText('Phone')).toBeNull();
    await act(async () => late(phone));
    expect(queryByText('Phone')).toBeNull();
    vi.useRealTimers();
  });
});
