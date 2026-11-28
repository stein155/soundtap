// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import type { VersionInfo } from '../../../../lib/types';
import { room } from '../../../../hooks/testFixtures';
import { CoreUpdate, useCoreLatest } from './CoreUpdate';
import { UpdateScreen } from './UpdateScreen';

vi.mock('../../../../lib/api', () => ({
  getReleases: vi.fn(),
  startCoreUpdate: vi.fn(),
  getVersion: vi.fn().mockResolvedValue(null),
}));

const newer = {
  current: 'app-v0.1.0',
  updatable: true,
  stable: null,
  core: {
    current: '0.1.0',
    latest: {
      tag: 'os-v0.2.0',
      name: 'SoundTap Core 0.2.0',
      notes: 'Kernel fixes',
      publishedAt: '',
    },
  },
};

const installed = {
  size: 1,
  modules: [],
  kernel: 'mainline',
  models: ['ST10'],
  installable: true,
  installed: true,
  restarting: true,
};

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

// The card shows where the Updates page shows it: once the speaker names a newer Core. The
// update screen is the app's, over every page.
function Core({ speaker }: { readonly speaker: string }) {
  const latest = useCoreLatest(speaker);
  return (
    <>
      {latest && <CoreUpdate speaker={speaker} latest={latest} version={running} />}
      <UpdateScreen rooms={[room({ id: speaker, name: 'Kitchen', model: 'st10' })]} />
    </>
  );
}

describe('CoreUpdate', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows nothing when no newer Core is out', async () => {
    vi.mocked(api.getReleases).mockResolvedValue({
      ...newer,
      core: { current: '0.2.0', latest: null },
    });
    const { container } = renderWithI18n(<Core speaker="Kitchen" />);
    await waitFor(() => expect(api.getReleases).toHaveBeenCalledWith('Kitchen'));
    expect(container.textContent).toBe('');
  });

  it('shows nothing for a speaker that predates the Core lane', async () => {
    const { core: _core, ...old } = newer;
    vi.mocked(api.getReleases).mockResolvedValue(old);
    const { container } = renderWithI18n(<Core speaker="" />);
    await waitFor(() => expect(api.getReleases).toHaveBeenCalled());
    expect(container.textContent).toBe('');
  });

  // One question, as stock asks, and no press: the speaker restarts on its own and the
  // update screen follows it.
  it('updates the newer Core after one confirmation and follows the speaker', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(newer);
    vi.mocked(api.startCoreUpdate).mockResolvedValue(installed);
    const { findByRole, getByRole, getByText, queryByText, queryByRole } = renderWithI18n(
      <Core speaker="Kitchen" />
    );
    const card = await findByRole('region', { name: 'SoundTap Core 0.2.0' });
    expect(card.textContent).toContain('New version');
    expect(getByText('Now: 0.1.0 · app 0.1.0 · signed')).toBeTruthy();
    // The notes wait behind "What's new".
    expect(queryByText('Kernel fixes')).toBeNull();
    fireEvent.click(getByRole('button', { name: 'What’s new' }));
    expect(getByText('Kernel fixes')).toBeTruthy();
    expect(queryByRole('alertdialog')).toBeNull();
    fireEvent.click(getByRole('button', { name: 'Update' }));
    const ask = await findByRole('dialog', { name: 'Update' });
    expect(ask.textContent).toContain(
      'Updating takes a few minutes. The speaker restarts and is back by itself. Start now?'
    );
    expect(api.startCoreUpdate).not.toHaveBeenCalled();
    fireEvent.click(getByRole('button', { name: 'Start' }));
    await waitFor(() => expect(api.startCoreUpdate).toHaveBeenCalledWith('Kitchen'));
    expect(
      await findByRole('alertdialog', { name: 'The speaker is updating itself' })
    ).toBeTruthy();
    expect(queryByRole('button', { name: 'Yes, install' })).toBeNull();
    expect(queryByRole('button', { name: 'Reboot into the new firmware' })).toBeNull();
  });

  it('says so when the install fails', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(newer);
    vi.mocked(api.startCoreUpdate).mockRejectedValue(
      Object.assign(new Error('press a button on the device to confirm'), {
        status: 403,
        code: 'not_confirmed',
      })
    );
    const { findByRole, queryByRole } = renderWithI18n(<Core speaker="" />);
    fireEvent.click(await findByRole('button', { name: 'Update' }));
    fireEvent.click(await findByRole('button', { name: 'Start' }));
    expect((await findByRole('alert')).textContent).toBe(
      'SoundTap Core couldn’t be installed. (nobody held a button on the speaker in time)'
    );
    expect(queryByRole('alertdialog')).toBeNull();
  });

  it('starts nothing when the question is cancelled', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(newer);
    const { findByRole, queryByRole } = renderWithI18n(<Core speaker="Kitchen" />);
    fireEvent.click(await findByRole('button', { name: 'Update' }));
    fireEvent.click(await findByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(queryByRole('dialog', { name: 'Update' })).toBeNull());
    expect(api.startCoreUpdate).not.toHaveBeenCalled();
  });
});
