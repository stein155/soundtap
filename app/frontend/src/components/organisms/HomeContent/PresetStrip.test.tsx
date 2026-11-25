// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { PresetStrip } from './PresetStrip';
import type { Player, Preset } from '../../../lib/types';

const preset = (slot: number, name: string): Preset => ({
  slot,
  source: 'TUNEIN',
  name,
  tuneInId: `s${slot}`,
  location: '',
  logo: '',
});

const strip = (player: Player = { status: 'idle', station: null }) => {
  const onPlay = vi.fn();
  const onOpen = vi.fn();
  render(
    <PresetStrip
      presets={[preset(1, 'Radio 1'), preset(2, 'Radio 2'), null, null, null, null]}
      player={player}
      speaker=""
      loading={false}
      onPlay={onPlay}
      onOpen={onOpen}
    />
  );
  return { onPlay, onOpen };
};

// No I18nProvider here, so the labels are the default language's (English).
describe('PresetStrip', () => {
  it('shows all six slots, each named with its number', () => {
    strip();
    expect(screen.getAllByRole('button')).toHaveLength(6);
    expect(screen.getByRole('button', { name: 'Preset 2: Radio 2' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Preset 3: Open' })).toBeInTheDocument();
  });

  it('plays a filled slot and opens the presets page for an open one', () => {
    const { onPlay, onOpen } = strip();
    fireEvent.click(screen.getByRole('button', { name: 'Preset 2: Radio 2' }));
    expect(onPlay).toHaveBeenCalledWith(2);
    fireEvent.click(screen.getByRole('button', { name: 'Preset 5: Open' }));
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onPlay).toHaveBeenCalledTimes(1);
  });

  it('rings only the slot that plays', () => {
    strip({ status: 'playing', station: { name: 'Radio 1', tuneInId: 's1' } } as Player);
    const active = screen.getAllByRole('button').filter((b) => b.hasAttribute('data-active'));
    expect(active.map((b) => b.getAttribute('aria-label'))).toEqual(['Preset 1: Radio 1']);
  });
});
