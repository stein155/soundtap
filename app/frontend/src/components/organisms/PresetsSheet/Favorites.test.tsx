// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { Favorites } from './Favorites';
import type { RecentItem } from '../../../lib/recent';

const item: RecentItem = {
  key: 'tunein:s1',
  name: 'Radio 1',
  art: '',
  tuneInId: 's1',
  spotifyUri: null,
  source: 'TUNEIN',
  stoppedAt: '',
  locations: ['s1'],
};

const props = () => ({
  items: [item],
  speaker: '',
  canAdd: true,
  saving: false,
  close: vi.fn(),
  onAdd: vi.fn(),
  onPlay: vi.fn(),
  onRemove: vi.fn(),
});

describe('Favorites', () => {
  it('plays a favorite and closes the page', () => {
    const p = props();
    render(<Favorites {...p} />);
    fireEvent.click(screen.getByText('Radio 1'));
    expect(p.onPlay).toHaveBeenCalledWith(item);
    expect(p.close).toHaveBeenCalled();
  });

  it('removes a favorite and stays on the page', () => {
    const p = props();
    render(<Favorites {...p} />);
    const buttons = screen.getAllByRole('button');
    fireEvent.click(buttons[buttons.length - 1]);
    expect(p.onRemove).toHaveBeenCalledWith(item);
    expect(p.close).not.toHaveBeenCalled();
  });

  it('does not play while something is held on its way to a slot', () => {
    const p = { ...props(), saving: true };
    render(<Favorites {...p} />);
    fireEvent.click(screen.getByText('Radio 1'));
    expect(p.onPlay).not.toHaveBeenCalled();
  });
});
