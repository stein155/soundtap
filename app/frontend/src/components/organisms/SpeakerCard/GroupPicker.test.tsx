// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../test/render';
import type { Room } from '../../../hooks/useRooms';
import { GroupPicker } from './GroupPicker';

const room = (id: string, linkedTo: string | null = null): Room => ({
  id,
  name: id,
  model: 'st10',
  ip: '',
  online: true,
  self: false,
  now: null,
  volume: 10,
  sources: [],
  linkedTo,
});

describe('GroupPicker', () => {
  // Each speaker's toggle was compared with itself, so one playing along never showed
  // as joined and a tap on it joined it again instead of letting it go.
  it('shows a speaker that plays along as joined, and a tap lets it go', () => {
    const grouping = { link: vi.fn(), unlink: vi.fn(), linkMany: vi.fn(), unlinkMany: vi.fn() };
    const lead = room('Kitchen');
    const follower = room('Attic', 'Kitchen');
    const { getByRole } = renderWithI18n(
      <GroupPicker
        room={lead}
        rooms={[lead, follower, room('Bedroom')]}
        grouping={grouping}
        onReconnect={() => {}}
      />
    );
    const attic = getByRole('button', { name: /Attic/ });
    expect(attic.getAttribute('aria-pressed')).toBe('true');
    expect(attic.textContent).toContain('Kitchen');
    expect(getByRole('button', { name: /Bedroom/ }).getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(attic);
    expect(grouping.unlink).toHaveBeenCalledWith(follower);
    expect(grouping.link).not.toHaveBeenCalled();
  });
});
