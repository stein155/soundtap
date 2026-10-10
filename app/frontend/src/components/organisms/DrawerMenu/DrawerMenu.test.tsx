// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, renderWithI18n } from '../../../test/render';
import { DrawerMenu } from '.';

// Only the latest drawer pick may go.
describe('DrawerMenu', () => {
  it('drops a pick still waiting when the menu reopens or another row is picked', () => {
    vi.useFakeTimers();
    const on = { onRecent: vi.fn(), onSpotify: vi.fn(), onTuneIn: vi.fn(), onSettings: vi.fn() };
    const view = renderWithI18n(<DrawerMenu open onClose={() => {}} {...on} />);
    fireEvent.click(view.getByText('Settings'));
    view.rerender(<DrawerMenu open={false} onClose={() => {}} {...on} />);
    view.rerender(<DrawerMenu open onClose={() => {}} {...on} />);
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
});
