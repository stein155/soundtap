// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { setUPnPRepeat, setUPnPShuffle, upnpModes } from '../../../lib/api/upnp';
import { ModesFor } from './parts';
import { must } from '../../../test/must';

vi.mock('../../../lib/api/upnp', () => ({
  upnpModes: vi.fn(),
  setUPnPShuffle: vi.fn().mockResolvedValue(null),
  setUPnPRepeat: vi.fn().mockResolvedValue(null),
}));

describe('a media-server folder', () => {
  it("sets the played folder's shuffle and repeat on the speaker", async () => {
    vi.mocked(upnpModes).mockResolvedValue({ shuffle: false, repeat: 'off' });
    render(
      <ModesFor now={{ standby: false, source: 'UPNP', canSkip: true }} speaker="a">
        {(m) =>
          m && (
            <>
              {m.shuffle}
              {m.repeat}
            </>
          )
        }
      </ModesFor>
    );
    await waitFor(() => expect(upnpModes).toHaveBeenCalledWith('a'));
    const [shuffle, repeat] = screen.getAllByRole('button');
    expect(screen.getAllByRole('button')).toHaveLength(2); // no + for a folder
    await userEvent.click(must(shuffle, 'shuffle'));
    expect(setUPnPShuffle).toHaveBeenCalledWith(true, 'a');
    await userEvent.click(must(repeat, 'repeat'));
    expect(setUPnPRepeat).toHaveBeenCalledWith('context', 'a');
  });
});
