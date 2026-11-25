// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import type { Releases, VersionInfo } from '../../../../lib/types';
import { UpdateSettings } from '.';

vi.mock('../../../../lib/api', () => ({
  getReleases: vi.fn(),
  getVersion: vi.fn(),
  startUpdate: vi.fn(),
  startCoreUpdate: vi.fn(),
  reboot: vi.fn(),
  uploadFirmware: vi.fn(),
}));

const running: VersionInfo = {
  version: '0.1.0',
  os: '0.1.0',
  app: '0.1.0',
  commit: '',
  updatable: true,
  updateMode: 'firmware',
  api: 1,
  capabilities: [],
};

const releases = (stable: string | null): Releases => ({
  current: '0.1.0',
  updatable: true,
  stable: stable ? { tag: stable, name: '', notes: 'Faster setup', publishedAt: '' } : null,
  core: { current: '0.1.0', latest: null },
});

const page = (version: VersionInfo = running) =>
  renderWithI18n(
    <UpdateSettings speaker="Kitchen" name="Kitchen" model="ST10" version={version} />
  );

describe('UpdateSettings', () => {
  beforeEach(() => vi.clearAllMocks());

  it('offers a newer app in the card drawn for a new version, asks once, no press', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(releases('app-v0.2.0'));
    vi.mocked(api.startUpdate).mockResolvedValue({
      ok: true,
      status: 202,
      body: { status: 'updating', to: '0.2.0' },
    });
    const { findByRole, getByRole, getByText } = page();
    const card = await findByRole('region', { name: 'SoundTap app 0.2.0' });
    expect(card.textContent).toContain('New version');
    expect(getByText('Now: 0.1.0 · app 0.1.0 · signed')).toBeTruthy();
    fireEvent.click(getByRole('button', { name: "What's new" }));
    expect(getByText('Faster setup')).toBeTruthy();
    fireEvent.click(getByRole('button', { name: 'Update' }));
    expect(await findByRole('dialog', { name: 'Update' })).toBeTruthy();
    expect(api.startUpdate).not.toHaveBeenCalled();
    fireEvent.click(getByRole('button', { name: 'Start' }));
    await waitFor(() => expect(api.startUpdate).toHaveBeenCalledWith(undefined, 'Kitchen'));
  });

  it('shows the version it runs when nothing newer is out', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(releases('app-v0.1.0'));
    const { findByRole, getByRole, queryByText } = page();
    expect(await findByRole('region', { name: 'SoundTap Core 0.1.0 · app 0.1.0' })).toBeTruthy();
    expect(getByRole('status').textContent).toBe('Not checked yet');
    expect(getByRole('button', { name: 'Update now' })).toBeTruthy();
    expect(queryByText('New version')).toBeNull();
  });

  it('shows a newer Core as the one card, not the app card beside it', async () => {
    vi.mocked(api.getReleases).mockResolvedValue({
      ...releases('app-v0.2.0'),
      core: {
        current: '0.1.0',
        latest: { tag: 'os-v0.2.0', name: '', notes: 'New kernel', publishedAt: '' },
      },
    });
    const { findByRole, queryByRole, getAllByRole } = page();
    expect(await findByRole('region', { name: 'SoundTap Core 0.2.0' })).toBeTruthy();
    expect(queryByRole('region', { name: 'SoundTap app 0.2.0' })).toBeNull();
    expect(getAllByRole('button', { name: 'Update' })).toHaveLength(1);
  });

  it('lists the other ways, with the archive linked in its sentence', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(releases(null));
    const { findByRole, getByRole } = page();
    const other = await findByRole('region', { name: 'Other ways' });
    expect(getByRole('button', { name: /^Upload a file/ }).textContent).toContain('.stu');
    expect(getByRole('button', { name: /^Back to Bose/ })).toBeTruthy();
    const link = getByRole('link', { name: 'archive on archive.org' });
    expect(link.getAttribute('href')).toContain('archive.org');
    expect(other.textContent).toContain(
      'No update file from Bose? It is kept in a public archive on archive.org, run by neither Bose nor SoundTap.'
    );
  });

  it('only shows the version where the speaker cannot update itself', () => {
    const { getByRole, queryByRole, getByText } = page({ ...running, updateMode: 'unmanaged' });
    expect(getByRole('region', { name: 'SoundTap Core 0.1.0 · app 0.1.0' })).toBeTruthy();
    expect(getByText('Updates are only available on the speaker itself.')).toBeTruthy();
    expect(queryByRole('button', { name: /^Upload a file/ })).toBeNull();
    expect(api.getReleases).not.toHaveBeenCalled();
  });
});
