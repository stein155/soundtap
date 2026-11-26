// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, renderWithI18n } from '../../../../test/render';
import { getReleases, getSettings, getVersion } from '../../../../lib/api';
import type { Room } from '../../../../hooks/useRooms';
import { SettingsOverview } from '.';
import { room as roomFixture, releaseRef, releases, version } from '../../../../hooks/testFixtures';

vi.mock('../../../../lib/api', async (actual) => ({
  ...(await actual<typeof import('../../../../lib/api')>()),
  getSettings: vi.fn(),
  getVersion: vi.fn(),
  getReleases: vi.fn(),
}));

const room = (id: string, model: string, self = false, online = true): Room =>
  roomFixture({ id, name: id, model, ip: '192.0.2.1', online, self });

describe('SettingsOverview', () => {
  it('lists every speaker alike, the one serving the page too, and marks one with an update waiting', async () => {
    vi.mocked(getSettings).mockResolvedValue(null);
    vi.mocked(getVersion).mockImplementation(async (speaker) =>
      version({ version: speaker ? 'app-v1.0.0' : 'app-v1.1.0' })
    );
    vi.mocked(getReleases).mockImplementation(async (speaker) =>
      releases({
        current: speaker ? '1.0.0' : '1.1.0',
        stable: releaseRef({ tag: 'app-v1.1.0' }),
      })
    );
    const rooms = [
      room('Living room', 'SoundTouch 10', true),
      room('Bedroom', 'SoundTouch Wireless Link Adapter'),
      room('Kitchen', 'SoundTouch 20', false, false),
    ];
    const { findByRole, getByRole } = renderWithI18n(
      <SettingsOverview rooms={rooms} onSpeaker={vi.fn()} onIntegrations={vi.fn()} />
    );
    const living = getByRole('button', { name: /Living room.*SoundTouch 10/u });
    expect(living.textContent).not.toMatch(/this speaker/u);
    expect(getByRole('button', { name: /Kitchen.*Offline/u })).toBeTruthy();
    expect(await findByRole('button', { name: /Bedroom.*Wireless Link.*Update/u })).toBeTruthy();
    expect(getByRole('button', { name: /Living room/u }).textContent).not.toMatch(/Update/u);
  });

  it('reads the versions on the About page once, not again for every new rooms array', async () => {
    vi.mocked(getSettings).mockResolvedValue(null);
    vi.mocked(getVersion).mockResolvedValue(version({ os: '0.2.0', app: '0.2.0' }));
    vi.mocked(getReleases).mockResolvedValue(null);
    const rooms = () => [
      room('Living room', 'SoundTouch 10', true),
      room('Bedroom', 'SoundTouch 20'),
    ];
    const ui = (list: Room[]) => (
      <SettingsOverview rooms={list} onSpeaker={vi.fn()} onIntegrations={vi.fn()} />
    );
    const { getByRole, findAllByText, rerender } = renderWithI18n(ui(rooms()));
    fireEvent.click(getByRole('button', { name: /About SoundTap/u }));
    expect(await findAllByText('SoundTap Core 0.2.0 · app 0.2.0')).toHaveLength(2);
    const asked = vi.mocked(getVersion).mock.calls.length;
    // The page renders again with the same rooms in a new array, as it does on every push.
    rerender(ui(rooms()));
    rerender(ui(rooms()));
    expect(vi.mocked(getVersion).mock.calls.length).toBe(asked);
    expect(await findAllByText('SoundTap Core 0.2.0 · app 0.2.0')).toHaveLength(2);
  });
});
