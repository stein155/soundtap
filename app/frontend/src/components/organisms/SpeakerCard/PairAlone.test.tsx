// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../test/render';
import * as multiroom from '../../../lib/api/multiroom';
import type { Room } from '../../../hooks/useRooms';
import { PairAlone } from './PairAlone';

vi.mock('../../../lib/api/multiroom', () => ({ getChannel: vi.fn(), setChannel: vi.fn() }));

const room: Room = {
  id: 'Kitchen',
  name: 'Kitchen',
  model: 'st10',
  ip: '',
  online: true,
  self: false,
  now: null,
  volume: 10,
  sources: [],
  linkedTo: null,
};

describe('PairAlone', () => {
  it('says a half pair plays alone and gives it both channels back', async () => {
    vi.mocked(multiroom.getChannel).mockResolvedValue('left');
    vi.mocked(multiroom.setChannel).mockResolvedValue(undefined);
    const { findByText, getByRole, queryByRole } = renderWithI18n(
      <PairAlone room={room} grouped={false} />
    );
    await findByText(/plays only left/);
    fireEvent.click(getByRole('button', { name: 'Unpair' }));
    expect(multiroom.setChannel).toHaveBeenCalledWith('stereo', 'Kitchen');
    await waitFor(() => expect(queryByRole('button', { name: 'Unpair' })).toBeNull());
  });

  it('says nothing for a stereo speaker', async () => {
    vi.mocked(multiroom.getChannel).mockResolvedValue('stereo');
    const { container } = renderWithI18n(<PairAlone room={room} grouped={false} />);
    await waitFor(() => expect(multiroom.getChannel).toHaveBeenCalled());
    expect(container.textContent).toBe('');
  });
});
