// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, renderWithI18n } from '../../../test/render';
import { DrawerMenu } from '.';

// Only the latest drawer pick may go.
describe('DrawerMenu', () => {
  it('drops a pick still waiting when the menu reopens or another row is picked', () => {
    vi.useFakeTimers();
    const on = {
      onRecent: vi.fn(),
      onSpotify: vi.fn(),
      onTuneIn: vi.fn(),
      onMediaServers: vi.fn(),
      onSettings: vi.fn(),
    };
    const view = renderWithI18n(<DrawerMenu open onClose={vi.fn()} {...on} />);
    fireEvent.click(view.getByText('Settings'));
    view.rerender(<DrawerMenu open={false} onClose={vi.fn()} {...on} />);
    view.rerender(<DrawerMenu open onClose={vi.fn()} {...on} />);
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(on.onSettings).not.toHaveBeenCalled();

    fireEvent.click(view.getByText('Settings'));
    fireEvent.click(view.getByText('Spotify'));
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(on.onSettings).not.toHaveBeenCalled();
    expect(on.onSpotify).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it('marks the row of the page on screen, Settings and the media servers included', () => {
    const on = {
      onRecent: vi.fn(),
      onSpotify: vi.fn(),
      onTuneIn: vi.fn(),
      onMediaServers: vi.fn(),
      onSettings: vi.fn(),
    };
    const current = (view: ReturnType<typeof renderWithI18n>) =>
      view
        .getAllByRole('button')
        .filter((b) => b.getAttribute('aria-current') === 'page')
        .map((b) => b.textContent);
    const view = renderWithI18n(<DrawerMenu open onClose={vi.fn()} at="settings" {...on} />);
    expect(current(view)).toEqual(['Settings']);
    view.rerender(<DrawerMenu open onClose={vi.fn()} at="mediaservers" {...on} />);
    expect(current(view)).toEqual(['Media servers']);
    view.rerender(<DrawerMenu open onClose={vi.fn()} {...on} />);
    expect(current(view)).toEqual(['Just for You']);
  });
});
