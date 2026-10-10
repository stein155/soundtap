// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import { CoreUpdate } from './CoreUpdate';

vi.mock('../../../../lib/api', () => ({
  getReleases: vi.fn(),
  startCoreUpdate: vi.fn(),
  reboot: vi.fn().mockResolvedValue(undefined),
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
};

describe('CoreUpdate', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows nothing when no newer Core is out', async () => {
    vi.mocked(api.getReleases).mockResolvedValue({
      ...newer,
      core: { current: '0.2.0', latest: null },
    });
    const { container } = renderWithI18n(<CoreUpdate speaker="Kitchen" />);
    await waitFor(() => expect(api.getReleases).toHaveBeenCalledWith('Kitchen'));
    expect(container.textContent).toBe('');
  });

  it('shows nothing for a speaker that predates the Core lane', async () => {
    const { core: _, ...old } = newer;
    vi.mocked(api.getReleases).mockResolvedValue(old);
    const { container } = renderWithI18n(<CoreUpdate speaker="" />);
    await waitFor(() => expect(api.getReleases).toHaveBeenCalled());
    expect(container.textContent).toBe('');
  });

  it('installs the newer Core after asking, then offers the reboot', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(newer);
    vi.mocked(api.startCoreUpdate).mockResolvedValue(installed);
    const { findByText, findByRole, getByRole } = renderWithI18n(<CoreUpdate speaker="Kitchen" />);
    expect(await findByText('SoundTap Core 0.2.0 is available')).toBeTruthy();
    expect(await findByText('Kernel fixes')).toBeTruthy();
    fireEvent.click(getByRole('button', { name: 'Install SoundTap Core 0.2.0' }));
    expect(api.startCoreUpdate).not.toHaveBeenCalled();
    fireEvent.click(getByRole('button', { name: 'Yes, install' }));
    await waitFor(() => expect(api.startCoreUpdate).toHaveBeenCalledWith('Kitchen'));
    fireEvent.click(await findByRole('button', { name: 'Reboot into the new firmware' }));
    fireEvent.click(getByRole('button', { name: 'Yes, reboot' }));
    await waitFor(() => expect(api.reboot).toHaveBeenCalledWith('Kitchen'));
  });

  it('says so when the install fails', async () => {
    vi.mocked(api.getReleases).mockResolvedValue(newer);
    vi.mocked(api.startCoreUpdate).mockRejectedValue(
      Object.assign(new Error('press a button on the device to confirm'), {
        status: 403,
        code: 'not_confirmed',
      })
    );
    const { findByRole, getByRole } = renderWithI18n(<CoreUpdate speaker="" />);
    fireEvent.click(await findByRole('button', { name: 'Install SoundTap Core 0.2.0' }));
    fireEvent.click(getByRole('button', { name: 'Yes, install' }));
    expect((await findByRole('alert')).textContent).toBe(
      'SoundTap Core could not be installed. (nobody held a button on the speaker in time)'
    );
  });
});
