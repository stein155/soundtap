// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { setUPnPRepeat, setUPnPShuffle, upnpModes } from '../../../lib/api/upnp';
import { FolderModeButtons } from './parts';

vi.mock('../../../lib/api/upnp', () => ({
  upnpModes: vi.fn(),
  setUPnPShuffle: vi.fn().mockResolvedValue(null),
  setUPnPRepeat: vi.fn().mockResolvedValue(null),
}));

describe('FolderModeButtons', () => {
  it("sets the played folder's shuffle and repeat on the speaker", async () => {
    vi.mocked(upnpModes).mockResolvedValue({ shuffle: false, repeat: 'off' });
    render(<FolderModeButtons speaker="a" />);
    await waitFor(() => expect(upnpModes).toHaveBeenCalledWith('a'));
    const buttons = screen.getAllByRole('button');
    expect(buttons).toHaveLength(2); // no + for a folder
    await userEvent.click(buttons[0]);
    expect(setUPnPShuffle).toHaveBeenCalledWith(true, 'a');
    await userEvent.click(buttons[1]);
    expect(setUPnPRepeat).toHaveBeenCalledWith('context', 'a');
  });
});
