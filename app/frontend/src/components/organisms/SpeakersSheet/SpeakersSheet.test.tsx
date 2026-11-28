// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, within } from '@testing-library/react';
import { renderWithI18n } from '../../../test/render';
import { room, selectedRoom } from '../../../hooks/testFixtures';
import { setRun } from '../../../hooks/updates';
import type { Playback } from '../../../hooks/usePlayback';
import { SpeakersSheet } from '.';

const kitchen = room({ id: 'kitchen', name: 'Kitchen' });
const office = room({ id: 'office', name: 'Office' });

const sheet = (lead = kitchen) => {
  const selected = selectedRoom({ rooms: [kitchen, office], lead });
  const playback: Playback = {
    playSlot: vi.fn(),
    pick: vi.fn(),
    playRecent: vi.fn(),
    transport: vi.fn(),
    skip: vi.fn(),
    canStore: false,
    storeCurrent: vi.fn(),
    storeSpotify: vi.fn(),
    setSource: vi.fn(),
    power: vi.fn(),
    roomTransport: vi.fn(),
  };
  const view = renderWithI18n(
    <SpeakersSheet
      open
      onClose={vi.fn()}
      selected={selected}
      playback={playback}
      onSettings={vi.fn()}
    />
  );
  return { ...view, selected, playback };
};

describe('SpeakersSheet', () => {
  // A speaker that updates says how far it is in its row; the others are as they were and
  // can be picked, to control one of them meanwhile.
  it('shows the update on the speaker that runs it, and lets another be picked', () => {
    act(() => setRun('kitchen', { phase: 'busy', stage: 'download', progress: 42, text: '' }));
    const { getByRole, selected } = sheet();
    const rows = getByRole('radiogroup');
    expect(within(rows).getByRole('radio', { name: /Kitchen/u })).toHaveTextContent(
      'Updating… 42% done'
    );
    expect(within(rows).getByRole('radio', { name: /Office/u })).not.toHaveTextContent('Updating');
    fireEvent.click(within(rows).getByRole('radio', { name: /Office/u }));
    expect(selected.select).toHaveBeenCalledWith('office');
  });

  it('holds the power of the selected speaker while it updates', () => {
    act(() => setRun('kitchen', { phase: 'busy', stage: 'restart', progress: null, text: '' }));
    const { getByRole, unmount } = sheet();
    expect(getByRole('button', { name: 'Turn Kitchen on' })).toBeDisabled();
    unmount();
    const other = sheet(office);
    expect(other.getByRole('button', { name: 'Turn Office on' })).toBeEnabled();
  });
});
