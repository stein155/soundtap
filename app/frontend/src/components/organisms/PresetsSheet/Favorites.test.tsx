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
  it('plays the favorite and closes the page', () => {
    const p = props();
    render(<Favorites {...p} />);
    fireEvent.click(screen.getByText('Radio 1'));
    expect(p.onPlay).toHaveBeenCalledWith(item);
    expect(p.close).toHaveBeenCalled();
  });

  it('shows only the first of a list saved before it offered one', () => {
    const p = { ...props(), items: [item, { ...item, key: 'tunein:s2', name: 'Radio 2' }] };
    render(<Favorites {...p} />);
    expect(screen.getByText('Radio 1')).toBeInTheDocument();
    expect(screen.queryByText('Radio 2')).not.toBeInTheDocument();
  });

  it('removes the favorite and stays on the page', () => {
    const p = props();
    render(<Favorites {...p} />);
    fireEvent.click(screen.getByRole('button', { name: /Radio 1$/u }));
    expect(p.onRemove).toHaveBeenCalledWith(item);
    expect(p.close).not.toHaveBeenCalled();
  });

  it('stores what a list holds in its place instead of playing', () => {
    const p = { ...props(), saving: true };
    render(<Favorites {...p} />);
    fireEvent.click(screen.getByText('Radio 1'));
    expect(p.onPlay).not.toHaveBeenCalled();
    expect(p.onAdd).toHaveBeenCalled();
    expect(p.close).toHaveBeenCalled();
  });

  it('shows an empty slot while there is no favorite', () => {
    const p = { ...props(), items: [] };
    render(<Favorites {...p} />);
    expect(screen.getByText('Empty')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Empty'));
    expect(p.onPlay).not.toHaveBeenCalled();
  });
});
