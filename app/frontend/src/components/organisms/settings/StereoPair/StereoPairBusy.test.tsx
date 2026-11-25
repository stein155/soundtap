// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { StereoPair } from './index';
import { getChannel, inviteSpeaker, setChannel } from '../../../../lib/api';
import type { Room } from '../../../../hooks/useRooms';
import { room as roomFixture } from '../../../../hooks/testFixtures';

vi.mock('../../../../lib/api', async (actual) => ({
  ...(await actual<typeof import('../../../../lib/api')>()),
  getChannel: vi.fn(),
  inviteSpeaker: vi.fn(),
  setChannel: vi.fn(),
}));

const room = (id: string): Room => roomFixture({ id, name: id, ip: `10.0.0.${id}` });

describe('StereoPair', () => {
  it('gives the button back when the refresh after an action fails', async () => {
    vi.mocked(getChannel).mockResolvedValueOnce('stereo').mockRejectedValue(new Error('gone'));
    vi.mocked(inviteSpeaker).mockResolvedValue(undefined);
    vi.mocked(setChannel).mockResolvedValue(undefined);
    render(<StereoPair room={room('1')} rooms={[room('1'), room('2')]} onChanged={vi.fn()} />);
    const start = await screen.findByRole('button', { name: 'Pair' });
    fireEvent.click(start);
    await waitFor(() => expect(vi.mocked(getChannel).mock.calls.length).toBe(2));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Pair' })).toBeEnabled());
  });

  it('names the side this speaker plays, and what that leaves the partner', async () => {
    vi.mocked(getChannel).mockResolvedValue('stereo');
    render(<StereoPair room={room('1')} rooms={[room('1'), room('2')]} onChanged={vi.fn()} />);
    expect(await screen.findByRole('radio', { name: '2', checked: true })).toBeTruthy();
    expect(screen.getByRole('radiogroup', { name: '1 plays' })).toBeTruthy();
    expect(screen.getByText('2 then plays right.')).toBeTruthy();
    fireEvent.click(screen.getByRole('radio', { name: 'Right' }));
    expect(screen.getByRole('radio', { name: 'Right', checked: true })).toBeTruthy();
    expect(screen.getByText('2 then plays left.')).toBeTruthy();
  });
});
