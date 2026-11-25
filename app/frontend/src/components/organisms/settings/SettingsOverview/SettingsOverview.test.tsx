// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../../../../test/render';
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
  it('names the speaker serving the page and marks one with an update waiting', async () => {
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
    expect(getByRole('button', { name: /Living room.*SoundTouch 10 · this speaker/ })).toBeTruthy();
    expect(getByRole('button', { name: /Kitchen.*Offline/ })).toBeTruthy();
    expect(await findByRole('button', { name: /Bedroom.*Wireless Link.*Update/ })).toBeTruthy();
    expect(getByRole('button', { name: /Living room/ }).textContent).not.toMatch(/Update/);
  });
});
