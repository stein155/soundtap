// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../../lib/api');
import * as apiModule from '../../lib/api';

const api = apiModule as unknown as Record<keyof typeof apiModule, Mock>;

import App from '../../App';
import { settingsFixture } from '../../test/settings';

const PRESETS = [{ slot: 1, name: 'Radio Foo', tuneInId: 's1', location: '', logo: '' }, null];

const PLAYING = {
  standby: false,
  source: 'TUNEIN',
  playStatus: 'PLAY_STATE',
  stationName: 'Radio Foo',
  tuneInId: 's1',
  track: '',
  artist: '',
  art: '',
};

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState({}, '', '/');
  Object.defineProperty(window.navigator, 'languages', { configurable: true, value: ['en-US'] });
  api.getNowPlaying.mockResolvedValue({ standby: true });
  api.getVolume.mockResolvedValue(30);
  api.getDevices.mockResolvedValue([]);
  api.getMultiroom.mockResolvedValue([]);
  api.getPresets.mockResolvedValue(PRESETS);
  api.getSettings.mockResolvedValue(
    settingsFixture({ name: 'Kitchen', model: 'ST20', language: 'en' })
  );
  api.getVersion.mockResolvedValue({ version: '1.2.3', updatable: false });
  api.getReleases.mockResolvedValue({
    current: '1.2.3',
    updatable: false,
    stable: null,
  });
  api.getBluetooth.mockResolvedValue({
    adapter: false,
    alias: '',
    powered: false,
    discoverable: false,
    devices: [],
  });
  api.getMqttStatus.mockResolvedValue({ connected: false, lastError: '' });
  api.browseTuneIn.mockResolvedValue({ categories: [], stations: [] });
  api.getRecent.mockResolvedValue([]);
  api.getFavorites.mockResolvedValue([]);
  api.getUserSources.mockResolvedValue(['AUX']);
  api.getSleep.mockResolvedValue({ minutes: 0, endsAt: '' });
  api.playPreset.mockResolvedValue(null);
  api.stopPlayback.mockResolvedValue(null);
  api.setPower.mockResolvedValue(null);
  api.selectUserSource.mockResolvedValue(null);
});

describe('HomePage', () => {
  it('opens the presets from the idle bar and plays the tapped slot', async () => {
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /Pick something to play/ }));
    const presets = await screen.findByRole('dialog', { name: 'PRESETS & FAVORITES' });
    await userEvent.click(await screen.findByText('Radio Foo'));
    await waitFor(() => expect(api.playPreset).toHaveBeenCalledTimes(1));
    expect(api.playPreset.mock.calls[0][0]).toBe(1);
    // Playing closes the sheet.
    await waitFor(() => expect(presets).not.toBeInTheDocument());
  }, 30000);

  it('stops what plays from the mini bar', async () => {
    api.getNowPlaying.mockResolvedValue(PLAYING);
    render(<App />);
    expect(await screen.findByRole('button', { name: 'Open player: Radio Foo' })).toBeVisible();
    await userEvent.click(await screen.findByRole('button', { name: /^Stop · / }));
    await waitFor(() => expect(api.stopPlayback).toHaveBeenCalled());
  }, 30000);

  it('powers the speaker and switches its source from the speaker card', async () => {
    api.getNowPlaying.mockResolvedValue(PLAYING);
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /^Speaker: / }));
    const card = await screen.findByRole('dialog', { name: /^Speaker / });
    const power = await screen.findByRole('button', { name: 'Power' });
    expect(card).toContainElement(power);
    await userEvent.click(power);
    await waitFor(() => expect(api.setPower).toHaveBeenCalledWith(false, ''));
    await userEvent.click(await screen.findByRole('radio', { name: 'AUX' }));
    await waitFor(() => expect(api.selectUserSource).toHaveBeenCalledWith('AUX', ''));
  }, 30000);

  it('reaches the settings through the menu', async () => {
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: 'Menu' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Settings' }));
    expect(await screen.findByRole('region', { name: 'Settings' })).toBeInTheDocument();
  }, 30000);

  it('the volume icon on the speaker bar opens the volume panel, not the card', async () => {
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /^Volume / }));
    const panel = await screen.findByRole('dialog', { name: /^Volume / });
    expect(panel.querySelector('input[type="range"]')).not.toBeNull();
    expect(screen.queryByRole('button', { name: /^Power/ })).toBeNull();
  });
});
