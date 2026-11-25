// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

// Mock the whole API client: no real network, deterministic data.
vi.mock('../lib/api');
import * as apiModule from '../lib/api';
import { loadLanguage } from '../lib/i18n';

// Every export is a vi mock at runtime; expose them as such for the type-checker.
const api = vi.mocked(apiModule);

import App from '../App';
import { settingsFixture } from '../test/settings';
import type { Settings } from '../lib/types';
import { version, multiroomClient, recentEntry } from '../hooks/testFixtures';
import { must } from '../test/must';

const PRESETS = [
  { slot: 1, name: 'Radio Foo', tuneInId: 's1', location: '', logo: '' },
  { slot: 2, name: 'Radio Bar', tuneInId: 's2', location: '', logo: '' },
  null,
  null,
  null,
  null,
];

// The page is ready once its top bar is there.
const home = async () => screen.findByRole('button', { name: 'Menu' });
const openPresets = async () => {
  await home();
  await userEvent.click(screen.getByRole('button', { name: 'Presets' }));
};
// Settings sit in the menu from the left.
const openSettings = async () => {
  await userEvent.click(await home());
  await userEvent.click(await screen.findByRole('button', { name: 'Settings' }));
};
// The speaker bar opens the speakers sheet: which speaker this page controls.
const openSpeakers = async () => {
  await userEvent.click(await screen.findByRole('button', { name: /^Speaker: / }));
  return screen.findByRole('dialog', { name: 'Speakers' });
};
// Now playing's Group opens who plays along.
const openGroup = async () => {
  await userEvent.click(await screen.findByRole('button', { name: /^Open player/ }));
  await userEvent.click(await screen.findByRole('button', { name: 'Group' }));
  return screen.findByRole('dialog', { name: 'Group' });
};

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState({}, '', '/');
  Object.defineProperty(window.navigator, 'languages', {
    configurable: true,
    value: ['en-US'],
  });
  // useSpeaker reads these on mount + poll.
  api.getNowPlaying.mockResolvedValue({ standby: true });
  api.getVolume.mockResolvedValue(30);
  api.getDevices.mockResolvedValue([]);
  api.getPresets.mockResolvedValue(PRESETS);
  // HomePage + SettingsSheet read settings.
  api.getSettings.mockResolvedValue(
    settingsFixture({
      name: 'Kitchen',
      model: 'ST20',
      language: 'en',
      host: 'kitchen.local',
      bass: { actual: 0, target: 0, min: -9, max: 0, default: 0 },
    })
  );
  api.getVersion.mockResolvedValue(
    version({ version: '1.2.3', os: '', app: '', updatable: false, updateMode: 'unmanaged' })
  );
  // SettingsSheet also fetches the releases on open.
  api.getReleases.mockResolvedValue({
    current: '1.2.3',
    updatable: false,
    stable: null,
  });
  api.scanWifi.mockResolvedValue([{ ssid: 'HomeNet', signal: 'good', secure: true }]);
  api.setWifi.mockResolvedValue(null);
  api.getWifiJoinFailed.mockResolvedValue('');
  api.getWifiFellBack.mockResolvedValue('');
  api.saveSettings.mockResolvedValue(null);
  // SearchSheet loads the browse tree when opened (empty query).
  api.browseTuneIn.mockResolvedValue({ categories: [], stations: [] });
  api.playPreset.mockResolvedValue(null);
  api.setVolume.mockResolvedValue(null);
  // The settings sheet asks whether this speaker has a Bluetooth radio, to decide
  // whether to offer the page at all. Without an answer the menu never renders.
  api.getBluetooth.mockResolvedValue({
    adapter: false,
    alias: '',
    powered: false,
    discoverable: false,
    devices: [],
  });
  api.getMultiroom.mockResolvedValue([
    multiroomClient({
      id: 'local',
      name: 'Kitchen',
      host: 'soundtap',
      ip: '192.0.2.135',
      percent: 10,
      muted: false,
      groupId: 'g1',
      current: true,
      member: true,
    }),
    multiroomClient({
      id: 'mac',
      name: 'Test MacBook Pro',
      host: 'macbook',
      ip: '192.0.2.127',
      percent: 10,
      muted: true,
      groupId: 'g2',
      current: false,
      member: false,
    }),
  ]);
  api.stopPlayback.mockResolvedValue(null);
  api.skipSpotify.mockResolvedValue(null);
  api.skipKey.mockResolvedValue(null);
  api.selectStation.mockResolvedValue(null);
  api.storePreset.mockResolvedValue(null);
  api.getRecent.mockResolvedValue([]);
  api.getFavorites.mockResolvedValue([]);
  api.removeRecent.mockResolvedValue(null);
  api.getUserSources.mockResolvedValue([]);
  api.getSleep.mockResolvedValue({ minutes: 0, endsAt: '' });
  api.setSleep.mockResolvedValue({
    minutes: 30,
    endsAt: new Date(Date.now() + 1800000).toISOString(),
  });
  api.setPower.mockResolvedValue(null);
  api.inviteSpeaker.mockResolvedValue(null);
  api.followSpeaker.mockResolvedValue(null);
  api.getMqttStatus.mockResolvedValue({ connected: false, lastError: '' });
  api.searchTuneIn.mockResolvedValue([
    {
      tuneInId: 's9',
      name: 'Jazz Live',
      tagline: 'smooth',
      genre: '',
      country: 'NL',
      logo: '',
    },
  ]);
});

describe('App integration', () => {
  it('shows first-time Wi-Fi setup without the radio UI', async () => {
    await loadLanguage('de');
    Object.defineProperty(window.navigator, 'languages', {
      configurable: true,
      value: ['de-DE'],
    });
    window.history.replaceState({}, '', '/?setup=wifi');
    render(<App />);
    expect(await screen.findByText('Willkommen bei SoundTap')).toBeInTheDocument();
    expect(screen.getByText('Neues Leben für SoundTouch-Lautsprecher.')).toBeInTheDocument();
    expect(await screen.findByDisplayValue('Kitchen')).toBeInTheDocument();
    expect(screen.getByText('Schritt 1 von 2')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Weiter' }));
    expect(await screen.findByText('HomeNet')).toBeInTheDocument();
    expect(screen.getByText('Schritt 2 von 2')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Menü' })).not.toBeInTheDocument();
  });

  it('says why the last Wi-Fi join failed', async () => {
    api.getWifiJoinFailed.mockResolvedValue('wrong_password');
    window.history.replaceState({}, '', '/?setup=wifi');
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: 'Next' }));
    expect(await screen.findByText(/the password was not accepted/)).toBeInTheDocument();
  });

  it('saves setup name before switching Wi-Fi', async () => {
    window.history.replaceState({}, '', '/?setup=wifi');
    render(<App />);
    const name = await screen.findByDisplayValue('Kitchen');
    await userEvent.clear(name);
    await userEvent.type(name, 'Living room');
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    await userEvent.type(await screen.findByLabelText('Password for HomeNet'), 'secret123');
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }));

    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledWith({ name: 'Living room' }, ''));
    // Always the speaker serving the page, never a remembered remote room.
    expect(api.setWifi).toHaveBeenCalledWith(
      { ssid: 'HomeNet', security: 'wpa_or_wpa2', password: 'secret123' },
      ''
    );
    expect(api.saveSettings.mock.invocationCallOrder.at(-1)).toBeLessThan(
      must(api.setWifi.mock.invocationCallOrder[0], 'a setWifi call')
    );
  });

  // On the hotspot an unchanged name is not sent...
  it('does not resend an unchanged setup name', async () => {
    window.history.replaceState({}, '', '/?setup=wifi');
    render(<App />);
    await screen.findByDisplayValue('Kitchen');
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    await userEvent.type(await screen.findByLabelText('Password for HomeNet'), 'secret123');
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await waitFor(() => expect(api.setWifi).toHaveBeenCalled());
    expect(api.saveSettings).not.toHaveBeenCalled();
  });

  // ...and a name the speaker refuses does not stop the Wi-Fi hand-over.
  it('hands over Wi-Fi even when the speaker refuses the name', async () => {
    window.history.replaceState({}, '', '/?setup=wifi');
    api.saveSettings.mockRejectedValueOnce(
      Object.assign(new Error('pairing required'), { status: 401 })
    );
    render(<App />);
    await userEvent.type(await screen.findByDisplayValue('Kitchen'), ' 2');
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    await userEvent.type(await screen.findByLabelText('Password for HomeNet'), 'secret123');
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await waitFor(() => expect(api.setWifi).toHaveBeenCalled());
    expect(await screen.findByText(/The name could not be saved here/)).toBeInTheDocument();
  });

  // A name typed while the stored one is still loading is kept.
  it('keeps a setup name typed before the stored one arrives', async () => {
    window.history.replaceState({}, '', '/?setup=wifi');
    let answer: (v: Settings) => void = vi.fn();
    api.getSettings.mockImplementationOnce(async () => new Promise((r) => (answer = r)));
    render(<App />);
    const field = await screen.findByLabelText('Name');
    await userEvent.type(field, 'Typed name');
    answer(settingsFixture({ name: 'Stored name' }));
    await waitFor(() => expect(api.getSettings).toHaveBeenCalled());
    expect(field).toHaveValue('Typed name');
  });

  it('renders presets from the mocked API', async () => {
    render(<App />);
    await openPresets();
    expect(await screen.findByText('Radio Foo')).toBeInTheDocument();
    expect(screen.getByText('Radio Bar')).toBeInTheDocument();
    // Four open presets and the open favorite.
    expect((await screen.findAllByText('Open')).length).toBe(5);
  }, 30000);

  // <html lang> must follow the shown language, or a screen reader reads the whole UI
  // with the wrong pronunciation.
  it('puts the shown language on <html lang>', async () => {
    document.documentElement.lang = 'nl';
    render(<App />);
    await home();
    await waitFor(() => expect(document.documentElement.lang).toBe('en'));
  }, 30000);

  it('plays a preset when its row is tapped', async () => {
    render(<App />);
    await openPresets();
    const foo = await screen.findByText('Radio Foo');
    await userEvent.click(foo);
    await waitFor(() => expect(api.playPreset).toHaveBeenCalledTimes(1));
    // Called with the slot number (1) for Radio Foo.
    expect(api.playPreset.mock.calls[0]?.[0]).toBe(1);
  }, 30000);

  it('sets the volume from the volume panel', async () => {
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /^Volume / }));
    const slider = await screen.findByRole('slider', { name: 'Volume' });
    slider.focus();
    await userEvent.keyboard('{ArrowRight}');
    await waitFor(() =>
      expect(api.setVolume).toHaveBeenCalledWith(expect.any(Number), '', { group: false })
    );
  }, 30000);

  const openTuneIn = async () => {
    await userEvent.click(await screen.findByRole('button', { name: 'Menu' }));
    await userEvent.click(await screen.findByRole('button', { name: 'TuneIn' }));
  };

  const openTuneInSearch = async () =>
    userEvent.click(await screen.findByRole('button', { name: 'Search TuneIn' }));

  it('opens TuneIn from the menu and searches', async () => {
    render(<App />);
    await openTuneIn();
    await openTuneInSearch();
    await userEvent.type(screen.getByPlaceholderText('Stations, genres, countries…'), 'jazz');
    expect(await screen.findByText('Jazz Live')).toBeInTheDocument();
  }, 30000);

  it('plays a station from a link from the search, and says what is wrong with the form', async () => {
    render(<App />);
    await openTuneIn();
    await openTuneInSearch();
    await userEvent.click(await screen.findByRole('button', { name: /^Play from a link/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Play station' }));
    expect(screen.getAllByRole('alert').map((a) => a.textContent)).toEqual([
      'Required field',
      'Required field',
    ]);
    await userEvent.type(screen.getByLabelText('Link to the stream'), 'ftp://radio.example/live');
    await userEvent.type(screen.getByLabelText('Name'), 'My radio');
    await userEvent.click(screen.getByRole('button', { name: 'Play station' }));
    expect(screen.getByRole('alert').textContent).toBe(
      'Please use URLs that start with http(s)://'
    );
    expect(api.selectStation).not.toHaveBeenCalled();
    await userEvent.clear(screen.getByLabelText('Link to the stream'));
    await userEvent.type(screen.getByLabelText('Link to the stream'), 'https://radio.example/live');
    await userEvent.click(screen.getByRole('button', { name: 'Play station' }));
    await waitFor(() => expect(api.selectStation).toHaveBeenCalled());
    expect(api.selectStation.mock.calls[0]?.slice(0, 2)).toEqual([
      'https://radio.example/live',
      'My radio',
    ]);
    await waitFor(() => expect(screen.queryByLabelText('Link to the stream')).toBeNull());
  }, 30000);

  // An unreachable TuneIn offers a retry instead of a dead end.
  it('retries TuneIn after it could not be reached', async () => {
    api.browseTuneIn.mockResolvedValueOnce(null);
    render(<App />);
    await openTuneIn();
    await userEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByText(/Couldn't reach TuneIn/)).toBeNull());
    expect(api.browseTuneIn.mock.calls.length).toBeGreaterThanOrEqual(2);
  }, 30000);

  // As in stock: an empty slot says how to fill it, it opens no search.
  it('explains an empty slot when it is tapped', async () => {
    api.getPresets.mockResolvedValue([null, null, null, null, null, null]);
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /Pick something to play/ }));
    await userEvent.click(
      must((await screen.findAllByRole('button', { name: /^\d\s?Open$/ }))[0], 'an empty preset')
    );
    expect(await screen.findByText(/Press and hold an empty preset/)).toBeInTheDocument();
    expect(screen.queryByText(/for slot 1/i)).toBeNull();
    await userEvent.click(screen.getByRole('button', { name: 'OK' }));
    await waitFor(() => expect(screen.queryByText(/Press and hold an empty preset/)).toBeNull());
  }, 30000);

  it('opens a service on its own, without a switch to the other', async () => {
    render(<App />);
    await openTuneIn();
    await screen.findByRole('heading', { name: 'TuneIn' });
    expect(screen.queryByRole('tablist')).toBeNull();
    // The service's page comes first: no field, so no keyboard, until the bar is tapped.
    expect(screen.queryByPlaceholderText('Stations, genres, countries…')).toBeNull();
  }, 30000);

  // Settings open on the overview; each speaker is a page as Stockholm's Speaker Details,
  // its rows in groups, each opening a page of its own.
  async function openSpeakerSection(section: RegExp) {
    render(<App />);
    await openSettings();
    const sheet = await screen.findByRole('region', { name: 'Settings' });
    await userEvent.click(within(sheet).getByRole('button', { name: /Kitchen/ }));
    await userEvent.click(await screen.findByRole('button', { name: section }));
  }

  // Installing a file by hand, the way back to Bose included, lives under Updates.
  async function openUpload() {
    await openSpeakerSection(/^Updates/);
    await userEvent.click(await screen.findByRole('button', { name: /^Upload a file/ }));
  }

  it('offers firmware upload under Updates on a flashed speaker', async () => {
    api.getVersion.mockResolvedValue(
      version({ version: '1.2.3', os: '', app: '', updatable: false })
    );
    await openUpload();
    expect(
      await screen.findByText(
        'Choose a .stu file, with its .sig file if it has one. The speaker checks it first; you then confirm with one press on the speaker.'
      )
    ).toBeInTheDocument();
  }, 30000);

  it('validates, installs, then reboots firmware only after confirmation', async () => {
    api.getVersion.mockResolvedValue(
      version({ version: '1.2.3', os: '', app: '', updatable: false })
    );
    const report = {
      size: 1024,
      kernel: 'stock',
      models: ['ST10', 'ST20'],
      modules: [{ name: 'ubi.img', size: 512 }],
      installable: true,
    };
    api.uploadFirmware
      .mockResolvedValueOnce(report)
      .mockResolvedValueOnce({ ...report, installed: true, slot: 'Pong' });
    api.reboot.mockResolvedValue(null);
    await openUpload();
    fireEvent.change(must(document.querySelector('input[accept=".stu,.sig"]'), 'the file input'), {
      target: { files: [new File(['firmware'], 'Update_soundtap-firmware.stu')] },
    });
    expect(await screen.findByText('Compatible with this speaker.')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Install firmware' }));
    expect(api.uploadFirmware).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole('button', { name: 'Yes, install' }));
    await userEvent.click(
      await screen.findByRole('button', { name: 'Reboot into the new firmware' })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Yes, reboot' }));
    expect(api.reboot).toHaveBeenCalledOnce();
  }, 30000);

  // A newer Core installs after one confirmation, as stock asks: no press, no reboot button.
  // The speaker restarts on its own and the page over the app follows it (ADR 0048).
  it('updates SoundTap Core after one confirmation and follows the restart', async () => {
    api.getVersion.mockResolvedValue(
      version({ version: '1.2.3', os: '0.1.0', app: '', updatable: false })
    );
    api.getReleases.mockResolvedValue({
      current: '1.2.3',
      updatable: false,
      stable: null,
      core: {
        current: '0.1.0',
        latest: { tag: 'os-v0.2.0', name: '', notes: '', publishedAt: '' },
      },
    });
    api.startCoreUpdate.mockResolvedValue({
      size: 1,
      kernel: 'mainline',
      models: ['ST20'],
      modules: [],
      installable: true,
      installed: true,
      restarting: true,
    });
    await openSpeakerSection(/^Updates/);
    const card = await screen.findByRole('region', { name: 'SoundTap Core 0.2.0' });
    await userEvent.click(within(card).getByRole('button', { name: 'Update' }));
    expect(api.startCoreUpdate).not.toHaveBeenCalled();
    const ask = await screen.findByRole('dialog', { name: 'Update' });
    await userEvent.click(within(ask).getByRole('button', { name: 'Start' }));
    expect(api.startCoreUpdate).toHaveBeenCalledOnce();
    expect(
      await screen.findByRole('alertdialog', { name: 'The speaker is updating itself' })
    ).toBeInTheDocument();
    expect(api.reboot).not.toHaveBeenCalled();
  }, 30000);

  // The check sends the signature picked with the file, not the previous one.
  it('validates firmware with the signature picked alongside it', async () => {
    api.getVersion.mockResolvedValue(
      version({ version: '1.2.3', os: '', app: '', updatable: false })
    );
    api.uploadFirmware.mockResolvedValue({
      size: 1,
      kernel: '',
      models: ['ST10'],
      modules: [],
      installable: false,
    });
    await openUpload();
    fireEvent.change(must(document.querySelector('input[accept=".stu,.sig"]'), 'the file input'), {
      target: { files: [new File(['fw'], 'A.stu'), new File(['sigA'], 'A.stu.sig')] },
    });
    await waitFor(() => expect(api.uploadFirmware).toHaveBeenCalledTimes(1));
    expect(api.uploadFirmware).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ signature: 'sigA' })
    );
  }, 30000);

  it('keeps failed firmware named and never offers install', async () => {
    api.getVersion.mockResolvedValue(
      version({ version: '1.2.3', os: '', app: '', updatable: false })
    );
    api.uploadFirmware.mockRejectedValue(new Error('truncated package'));
    await openUpload();
    fireEvent.change(must(document.querySelector('input[accept=".stu,.sig"]'), 'the file input'), {
      target: { files: [new File(['broken'], 'broken.stu')] },
    });
    expect(await screen.findByText(/broken\.stu/)).toBeInTheDocument();
    expect(await screen.findByText(/Nothing was installed/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Install firmware' })).not.toBeInTheDocument();
  }, 30000);

  it('does not offer install when backend reports incompatible', async () => {
    api.getVersion.mockResolvedValue(
      version({ version: '1.2.3', os: '', app: '', updatable: false })
    );
    api.uploadFirmware.mockResolvedValue({
      size: 1024,
      kernel: 'mainline',
      models: ['ST10'],
      modules: [],
      installable: false,
      reason: 'This is the mainline ST10 package; this ST20 needs another image.',
    });
    await openUpload();
    fireEvent.change(must(document.querySelector('input[accept=".stu,.sig"]'), 'the file input'), {
      target: { files: [new File(['x'], 'st10.stu')] },
    });
    expect(await screen.findByText(/mainline ST10 package/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Install firmware' })).not.toBeInTheDocument();
  }, 30000);

  it('saves name, bass and the alarm together with Save', async () => {
    await openSpeakerSection(/^Alarm/);
    await userEvent.click(screen.getByRole('switch', { name: /Alarm on/ }));
    fireEvent.change(screen.getByLabelText('Time'), { target: { value: '06:45' } });
    await userEvent.selectOptions(screen.getByLabelText('Preset'), '2');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.saveSettings).toHaveBeenCalledWith(
        {
          alarm: {
            enabled: true,
            time: '06:45',
            slot: 2,
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          },
        },
        ''
      )
    );
  }, 30000);

  // Stock's Wireless Link has no bass control; it reports the range 0..0.
  it('shows only the standby switch on a speaker without bass', async () => {
    api.getSettings.mockResolvedValue(
      settingsFixture({
        name: 'Kitchen',
        model: 'WLA',
        language: 'en',
        host: 'kitchen.local',
        bass: { actual: 0, target: 0, min: 0, max: 0, default: 0 },
      })
    );
    render(<App />);
    await openSettings();
    const sheet = await screen.findByRole('region', { name: 'Settings' });
    await userEvent.click(within(sheet).getByRole('button', { name: /Kitchen/ }));
    expect(await screen.findByRole('switch', { name: /^Auto-off/ })).toBeInTheDocument();
    expect(screen.queryByRole('slider', { name: 'Bass' })).not.toBeInTheDocument();
  }, 30000);

  // Leaving a page with unsaved edits asks first; cancelling keeps them.
  it('asks before back throws away unsaved settings', async () => {
    await openSpeakerSection(/^Alarm/);
    await userEvent.click(screen.getByRole('switch', { name: /Alarm on/ }));
    const sheet = screen.getByRole('region', { name: 'Settings' });
    // Back from the alarm's page returns to the speaker's list: the edit is still there.
    await userEvent.click(within(sheet).getByRole('button', { name: 'Back' }));
    expect(screen.queryByRole('dialog', { name: 'Discard unsaved changes?' })).toBeNull();
    await userEvent.click(within(sheet).getByRole('button', { name: 'Back' }));
    // The app's own sheet asks, not the browser's dialog; Cancel stays.
    const ask = await screen.findByRole('dialog', { name: 'Discard unsaved changes?' });
    await userEvent.click(within(ask).getByRole('button', { name: 'Cancel' }));
    // Still on the speaker's page, with the edit waiting on the alarm's own Save.
    await userEvent.click(screen.getByRole('button', { name: /^Alarm/ }));
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  }, 30000);

  // Every way out of Settings asks too, not only Back.
  // A page below the overview has only Back, as in the design: leaving the speaker's page
  // asks first, and the overview's Close then leaves settings.
  it('asks before Back throws away unsaved settings', async () => {
    await openSpeakerSection(/^Alarm/);
    await userEvent.click(screen.getByRole('switch', { name: /Alarm on/ }));
    const sheet = screen.getByRole('region', { name: 'Settings' });
    expect(within(sheet).queryByRole('button', { name: 'Close' })).not.toBeInTheDocument();
    // The alarm's own page goes back to the speaker's without asking: the edit is the page's.
    await userEvent.click(within(sheet).getByRole('button', { name: 'Back' }));
    await userEvent.click(within(sheet).getByRole('button', { name: 'Back' }));
    const ask = await screen.findByRole('dialog', { name: 'Discard unsaved changes?' });
    await userEvent.click(within(ask).getByRole('button', { name: 'Cancel' }));
    // Still on the speaker's page.
    expect(within(sheet).queryByRole('button', { name: 'Close' })).not.toBeInTheDocument();
    await userEvent.click(within(sheet).getByRole('button', { name: 'Back' }));
    const again = await screen.findByRole('dialog', { name: 'Discard unsaved changes?' });
    await userEvent.click(within(again).getByRole('button', { name: 'Discard' }));
    // Back on the overview, a main page: the app's own bar, no Close of its own.
    expect(await within(sheet).findByRole('button', { name: 'Menu' })).toBeInTheDocument();
    expect(within(sheet).queryByRole('button', { name: 'Close' })).not.toBeInTheDocument();
  }, 30000);

  it('groups a second speaker by inviting it from the leader', async () => {
    api.getDevices.mockResolvedValue([
      { id: 'A', name: 'Kitchen', ip: '10.0.0.2', model: 'ST20', online: true, self: true },
      { id: 'B', name: 'Office', ip: '10.0.0.3', model: 'ST10', online: true, self: false },
    ]);
    api.getMultiroom.mockResolvedValue([]);
    // Grouping opens from now playing, so the leader plays something.
    api.getNowPlaying.mockResolvedValue({
      standby: false,
      source: 'TUNEIN',
      playStatus: 'PLAY_STATE',
      stationName: 'Radio',
      track: 'Song',
      artist: 'Artist',
      art: '',
    });
    render(<App />);
    const group = await openGroup();
    await userEvent.click(within(group).getByRole('switch', { name: /Office/ }));
    await waitFor(() => expect(api.inviteSpeaker).toHaveBeenCalledWith('10.0.0.3', ''));
  }, 30000);

  it("opens the selected speaker's own settings from the speakers sheet", async () => {
    render(<App />);
    const speakers = await openSpeakers();
    await userEvent.click(within(speakers).getByRole('button', { name: 'Settings' }));
    const sheet = await screen.findByRole('region', { name: 'Settings' });
    // Its page, not the overview: one level down, so there is a way back.
    expect(within(sheet).getByRole('button', { name: 'Back' })).toBeInTheDocument();
    expect(within(sheet).getByRole('heading', { name: 'Kitchen' })).toBeInTheDocument();
  }, 30000);

  // The speakers sheet lists every speaker; a tap makes it the one this page controls.
  it('switches to the speaker tapped in the speakers sheet', async () => {
    api.getDevices.mockResolvedValue([
      { id: 'A', name: 'Kitchen', ip: '10.0.0.2', model: 'ST20', online: true, self: true },
      { id: 'B', name: 'Office', ip: '10.0.0.3', model: 'ST10', online: true, self: false },
    ]);
    api.getMultiroom.mockResolvedValue([]);
    render(<App />);
    const speakers = await openSpeakers();
    expect(within(speakers).getByRole('radio', { name: /Kitchen/ })).toHaveAttribute(
      'aria-checked',
      'true'
    );
    await userEvent.click(within(speakers).getByRole('radio', { name: /Office/ }));
    expect(await screen.findByRole('button', { name: 'Speaker: Office' })).toBeInTheDocument();
  }, 30000);

  it('takes a station off the recent list from its sheet', async () => {
    api.getRecent.mockResolvedValue([
      recentEntry({ location: '/v1/playback/station/s1', stoppedAt: '' }),
    ]);
    render(<App />);
    await userEvent
      .setup({ pointerEventsCheck: 0 })
      .click(await screen.findByRole('button', { name: 'More for Jazz' }));
    await userEvent.click(
      await screen.findByRole('button', { name: 'Remove from Recently played' })
    );
    await waitFor(() =>
      expect(api.removeRecent).toHaveBeenCalledWith('TUNEIN', '/v1/playback/station/s1', '')
    );
  }, 30000);

  it('skips Spotify tracks from now playing', async () => {
    api.getNowPlaying.mockResolvedValue({
      standby: false,
      source: 'SPOTIFY',
      playStatus: 'PLAY_STATE',
      stationName: 'Song',
      track: 'Song',
      artist: 'Artist',
      art: '',
    });
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /^Open player/ }));
    await userEvent.click(await screen.findByRole('button', { name: 'Next track' }));
    await waitFor(() => expect(api.skipSpotify).toHaveBeenCalledWith('next', ''));
    await userEvent.click(screen.getByRole('button', { name: 'Previous track' }));
    await waitFor(() => expect(api.skipSpotify).toHaveBeenCalledWith('previous', ''));
  }, 30000);

  it("skips a media server's folder with the speaker's keys", async () => {
    api.getNowPlaying.mockResolvedValue({
      standby: false,
      source: 'UPNP',
      playStatus: 'PLAY_STATE',
      stationName: 'Song',
      track: 'Song',
      artist: 'Artist',
      art: '',
      canSkip: true,
    });
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /^Open player/ }));
    await userEvent.click(await screen.findByRole('button', { name: 'Next track' }));
    await waitFor(() => expect(api.skipKey).toHaveBeenCalledWith('next', ''));
    expect(api.skipSpotify).not.toHaveBeenCalled();
  }, 30000);

  it('starts a sleep timer from now playing', async () => {
    api.getNowPlaying.mockResolvedValue({
      standby: false,
      source: 'TUNEIN',
      playStatus: 'PLAY_STATE',
      stationName: 'Radio Foo',
      tuneInId: 's1',
      track: '',
      artist: '',
      art: '',
    });
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: 'Open player: Radio Foo' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Sleep timer' }));
    await userEvent.click(await screen.findByRole('menuitemradio', { name: '30 min' }));
    await waitFor(() => expect(api.setSleep).toHaveBeenCalledWith(30, ''));
    expect(await screen.findByRole('button', { name: 'Sleep timer' })).toHaveTextContent(/\d+ min/);
  }, 30000);

  it('says which version each speaker runs, under About in settings', async () => {
    render(<App />);
    await openSettings();
    await userEvent.click(await screen.findByRole('button', { name: /About/ }));
    expect(await screen.findByText('1.2.3')).toBeInTheDocument();
    expect(screen.getByText(/GPL-3\.0/)).toBeInTheDocument();
  }, 30000);

  it('writes MQTT settings to every speaker', async () => {
    api.getDevices.mockResolvedValue([
      { id: 'A', name: 'Kitchen', ip: '10.0.0.2', model: 'ST20', online: true, self: true },
      { id: 'B', name: 'Office', ip: '10.0.0.3', model: 'ST10', online: true, self: false },
    ]);
    api.getSettings.mockResolvedValue(
      settingsFixture({
        name: 'Kitchen',
        mqtt: { enabled: true, host: 'broker.local', port: 1883 },
      })
    );
    render(<App />);
    await openSettings();
    await userEvent.click(await screen.findByRole('button', { name: /Integrations/ }));
    await userEvent.click(await screen.findByRole('button', { name: /MQTT/ }));
    await userEvent.click(await screen.findByRole('switch', { name: /Connect MQTT/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledTimes(2));
    expect(api.saveSettings).toHaveBeenCalledWith(
      { mqtt: expect.objectContaining({ enabled: false }) },
      ''
    );
    expect(api.saveSettings).toHaveBeenCalledWith(
      { mqtt: expect.objectContaining({ enabled: false }) },
      'B'
    );
  }, 30000);

  it('refuses an MQTT port outside 1 to 65535 and takes an empty one', async () => {
    api.getSettings.mockResolvedValue(
      settingsFixture({
        name: 'Kitchen',
        mqtt: { enabled: true, host: 'broker.local', port: 1883 },
      })
    );
    render(<App />);
    await openSettings();
    await userEvent.click(await screen.findByRole('button', { name: /Integrations/ }));
    await userEvent.click(await screen.findByRole('button', { name: /MQTT/ }));
    await userEvent.click(await screen.findByRole('button', { name: 'More options' }));
    const port = await screen.findByLabelText('Port');
    await userEvent.clear(port);
    await userEvent.type(port, '70000');
    expect(screen.getByText(/Enter a port from 1 to 65535/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
    await userEvent.clear(port);
    expect(screen.queryByText(/Enter a port from 1 to 65535/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  }, 30000);

  // Escape with a typed search clears it instead of closing the sheet.
  it('clears the search on Escape before closing the sheet', async () => {
    render(<App />);
    await openTuneIn();
    await openTuneInSearch();
    await userEvent.type(screen.getByPlaceholderText('Stations, genres, countries…'), 'jazz');
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByPlaceholderText('Stations, genres, countries…')).toBeNull();
    expect(screen.getByRole('heading', { name: 'TuneIn' })).toBeInTheDocument();
    await openTuneInSearch();
    expect(screen.getByPlaceholderText('Stations, genres, countries…')).toHaveValue('');
  }, 30000);

  // The presets opened from now playing close back onto it, not onto home.
  it('returns to now playing when the presets close', async () => {
    api.getNowPlaying.mockResolvedValue({
      standby: false,
      source: 'TUNEIN',
      stationName: 'Radio Foo',
      playStatus: 'PLAY_STATE',
      tuneInId: 's1',
    });
    render(<App />);
    await userEvent.click(await screen.findByRole('button', { name: /Open player/ }));
    const playing = await screen.findByRole('dialog', { name: /Playing in/ });
    await userEvent.click(within(playing).getByRole('button', { name: 'Presets' }));
    await screen.findByRole('dialog', { name: 'Presets & favorites' });
    await userEvent.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Presets & favorites' })).not.toBeInTheDocument()
    );
    expect(screen.getByRole('dialog', { name: /Playing in/ })).toBeInTheDocument();
  }, 30000);
});
