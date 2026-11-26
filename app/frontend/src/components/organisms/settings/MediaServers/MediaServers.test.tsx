// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import type { UPnPItem } from '../../../../lib/types';
import { MediaServers } from '.';

vi.mock('../../../../lib/api', () => ({
  getUPnPServers: vi.fn().mockResolvedValue([{ usn: 'u', friendlyName: 'NAS' }]),
  searchUPnPServers: vi.fn().mockResolvedValue([{ usn: 'v', friendlyName: 'New NAS' }]),
  browseUPnP: vi.fn(),
  isUPnPContainer: (item: UPnPItem) => !item.url,
  playUPnP: vi.fn().mockResolvedValue(undefined),
}));

const folder = (id: string, title: string): UPnPItem => ({ id, title, class: '' });
const track = (id: string, title: string): UPnPItem => ({
  id,
  title,
  class: '',
  url: `http://nas/${id}`,
});

describe('MediaServers', () => {
  beforeEach(() => vi.clearAllMocks());

  it('names the top-level folders under each server, read once', async () => {
    vi.mocked(api.browseUPnP).mockResolvedValue([
      folder('m', 'Music'),
      folder('a', 'Audiobooks'),
      track('t', 'Loose track'),
    ]);
    const { findByText, getByRole } = renderWithI18n(
      <MediaServers speaker="Kitchen" speakerName="Kitchen" />
    );
    expect(await findByText('Music, Audiobooks')).toBeTruthy();
    expect(api.browseUPnP).toHaveBeenCalledTimes(1);
    expect(api.browseUPnP).toHaveBeenCalledWith('u', '0', 'Kitchen');
    fireEvent.click(getByRole('button', { name: 'Search again' }));
    await findByText('New NAS');
    await vi.waitFor(() => expect(api.browseUPnP).toHaveBeenCalledTimes(2));
    expect(api.browseUPnP).toHaveBeenLastCalledWith('v', '0', 'Kitchen');
  });

  it('shows no line under a server while its root is read or when that fails', async () => {
    vi.mocked(api.browseUPnP).mockResolvedValue(null);
    const { findByText } = renderWithI18n(<MediaServers speaker="" speakerName="Kitchen" />);
    const row = (await findByText('NAS')).closest('button');
    await vi.waitFor(() => expect(api.browseUPnP).toHaveBeenCalledTimes(1));
    expect(row?.textContent).toBe('NAS');
  });

  it('opens a server on a page of its own and goes deeper and back up', async () => {
    vi.mocked(api.browseUPnP).mockImplementation(async (_usn, id) =>
      id === '0' ? [folder('m', 'Music'), folder('a', 'Audiobooks')] : [track('t', 'So What')]
    );
    const { findByText, findByRole, queryByText } = renderWithI18n(
      <MediaServers speaker="" speakerName="Kitchen" />
    );
    fireEvent.click(await findByText('NAS'));
    fireEvent.click(await findByText('Music'));
    expect(await findByText('So What')).toBeTruthy();
    expect(api.browseUPnP).toHaveBeenLastCalledWith('u', 'm', '');
    fireEvent.click(await findByRole('button', { name: 'Back' }));
    expect(await findByText('Music')).toBeTruthy();
    expect(queryByText('So What')).toBeNull();
  });

  it('plays a track without a toast to say so', async () => {
    vi.mocked(api.browseUPnP).mockResolvedValue([track('t', 'So What')]);
    const { findByText, queryByText } = renderWithI18n(
      <MediaServers speaker="" speakerName="Kitchen" />
    );
    fireEvent.click(await findByText('NAS'));
    fireEvent.click(await findByText('So What'));
    await vi.waitFor(() => expect(api.playUPnP).toHaveBeenCalledTimes(1));
    expect(queryByText(/Playing/u)).toBeNull();
  });

  it('searches on "search again" instead of rereading the list', async () => {
    const { findByText, getByRole } = renderWithI18n(
      <MediaServers speaker="" speakerName="Kitchen" />
    );
    await findByText('NAS');
    fireEvent.click(getByRole('button', { name: 'Search again' }));
    expect(await findByText('New NAS')).toBeTruthy();
    expect(api.searchUPnPServers).toHaveBeenCalledTimes(1);
  });

  it('says the speaker did not answer, not that no server was found, and asks again', async () => {
    vi.mocked(api.getUPnPServers).mockResolvedValueOnce(null);
    vi.mocked(api.searchUPnPServers).mockResolvedValueOnce([]);
    const { findByText, getByRole, queryByText } = renderWithI18n(
      <MediaServers speaker="Kitchen" speakerName="Kitchen" />
    );
    expect(await findByText('Couldn’t reach Kitchen. Try again.')).toBeTruthy();
    expect(queryByText('No media servers found')).toBeNull();
    fireEvent.click(getByRole('button', { name: 'Search again' }));
    expect(await findByText('No media servers found')).toBeTruthy();
    expect(api.searchUPnPServers).toHaveBeenCalledWith('Kitchen');
  });
});
