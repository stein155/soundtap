// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import type { UPnPItem } from '../../../../lib/types';
import { MediaServers } from '.';

vi.mock('../../../../lib/api', () => ({
  getUPnPServers: vi.fn().mockResolvedValue([{ usn: 'u', friendlyName: 'NAS' }]),
  searchUPnPServers: vi.fn().mockResolvedValue([{ usn: 'v', friendlyName: 'New NAS' }]),
  browseUPnP: vi.fn(),
  isUPnPContainer: () => true,
  playUPnP: vi.fn(),
}));

describe('MediaServers', () => {
  it('opens a server once while its first browse is out', async () => {
    let answer: (items: UPnPItem[]) => void = () => {};
    vi.mocked(api.browseUPnP).mockReturnValue(new Promise((r) => (answer = r)));
    const { findByText } = renderWithI18n(<MediaServers speaker="" />);
    const nas = await findByText('NAS');
    fireEvent.click(nas);
    fireEvent.click(nas);
    expect(api.browseUPnP).toHaveBeenCalledTimes(1);
    answer([{ id: 'a', title: 'Albums' } as UPnPItem]);
    expect(await findByText('Albums')).toBeTruthy();
  });

  it('searches on "search again" instead of rereading the list', async () => {
    const { findByText, getByRole } = renderWithI18n(<MediaServers speaker="" />);
    await findByText('NAS');
    fireEvent.click(getByRole('button', { name: 'Search for media servers' }));
    expect(await findByText('New NAS')).toBeTruthy();
    expect(api.searchUPnPServers).toHaveBeenCalledTimes(1);
  });
});
