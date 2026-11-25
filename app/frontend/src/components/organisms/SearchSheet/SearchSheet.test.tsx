// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { beforeEach, describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../../../lib/api');
import * as api from '../../../lib/api';
import { SearchSheet } from '.';

beforeEach(() => {
  vi.mocked(api.browseTuneIn).mockResolvedValue(null);
});

const renderSearch = (onPick = vi.fn(), onClose = vi.fn()) =>
  render(
    <SearchSheet
      mode={{ search: true }}
      speaker=""
      onMenu={vi.fn()}
      onPresets={vi.fn()}
      onClose={onClose}
      onPick={onPick}
      onSpotifyPlayed={vi.fn()}
    />
  );

// No I18nProvider here, so the labels are English.
describe('SearchSheet', () => {
  it('names the search in its bar, with the field under it', () => {
    renderSearch();
    expect(screen.getByRole('heading', { level: 1, name: 'Search' })).toBeInTheDocument();
    expect(screen.getByRole('searchbox')).toBeInTheDocument();
  });

  it('plays a station from a link, on a page of its own', async () => {
    const onPick = vi.fn();
    renderSearch(onPick);
    await userEvent.click(screen.getByRole('button', { name: /^Play from a link/ }));
    const page = await screen.findByRole('dialog', { name: 'Play from a link' });
    expect(page).toBeInTheDocument();
    await userEvent.type(
      screen.getByLabelText('Link to the stream'),
      'https://radio.test/live.mp3'
    );
    await userEvent.type(screen.getByLabelText('Name'), 'My radio');
    await userEvent.click(screen.getByRole('button', { name: 'Play station' }));
    expect(onPick).toHaveBeenCalledWith(
      expect.objectContaining({ tuneInId: 'https://radio.test/live.mp3', name: 'My radio' })
    );
  });

  it('folds the help for a stream that does not play away until asked for', async () => {
    renderSearch();
    await userEvent.click(screen.getByRole('button', { name: /^Play from a link/ }));
    const help = (await screen.findByText('Stream not working?')).closest('details');
    expect(help?.open).toBe(false);
    await userEvent.click(screen.getByText('Stream not working?'));
    expect(help?.open).toBe(true);
    expect(screen.getByText('Wrong link')).toBeVisible();
  });
});
