// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { PresetsSheet } from '.';
import type { Preset } from '../../../lib/types';

const preset = (slot: number, name: string): Preset => ({
  slot,
  source: 'TUNEIN',
  name,
  tuneInId: `s${slot}`,
  location: '',
  logo: '',
});

const renderSheet = (onPlay = vi.fn()) =>
  render(
    <PresetsSheet
      open
      onClose={vi.fn()}
      presets={[preset(1, 'Radio 1'), preset(2, 'Radio 2'), null]}
      player={{ status: 'idle', station: null }}
      loading={false}
      speaker=""
      canStore
      onPlay={onPlay}
      saving={null}
      onSave={vi.fn()}
      onStore={vi.fn()}
      favorites={{ items: [], onAdd: vi.fn(), onPlay: vi.fn(), onRemove: vi.fn() }}
    />
  );

// No I18nProvider here, so the labels are the default language's (English).
describe('PresetsSheet', () => {
  it('shows each preset as one plain row, as in Stockholm', () => {
    renderSheet();
    expect(screen.getByText('Radio 1')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Edit Radio/u })).not.toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});
