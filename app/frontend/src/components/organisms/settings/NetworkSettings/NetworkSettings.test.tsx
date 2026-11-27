// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { beforeEach, describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import {
  dismissWifiFellBack,
  forgetWifi,
  getSavedWifi,
  getWifiFellBack,
  scanWifi,
  setWifi,
} from '../../../../lib/api';
import type { NetworkInfo } from '../../../../lib/types';
import { NetworkSettings, WifiPicker } from '.';
import { must } from '../../../../test/must';

vi.mock('../../../../lib/api');

beforeEach(() => {
  vi.mocked(getWifiFellBack).mockResolvedValue('');
});

describe('WifiPicker', () => {
  it('sends the SSID with its edge spaces intact', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: ' Home ', secure: false, signal: 'good' }]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { findByRole } = renderWithI18n(<WifiPicker network={null} speaker="Kitchen" />);
    fireEvent.click(await findByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(setWifi).toHaveBeenCalledWith(
        { ssid: ' Home ', security: 'none', password: '' },
        'Kitchen'
      )
    );
  });

  it('joins the network chosen from the list with its password', async () => {
    vi.mocked(scanWifi).mockResolvedValue([
      { ssid: 'NetA', secure: true, signal: 'good' },
      { ssid: 'NetB', secure: true, signal: 'fair' },
    ]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { findByRole, getByLabelText, getByRole } = renderWithI18n(
      <WifiPicker network={null} speaker="Kitchen" />
    );
    fireEvent.click(await findByRole('radio', { name: /NetB/u }));
    fireEvent.change(getByLabelText('Password for NetB'), { target: { value: 'abcdefgh' } });
    fireEvent.click(getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(setWifi).toHaveBeenCalledWith(
        { ssid: 'NetB', security: 'wpa_or_wpa2', password: 'abcdefgh' },
        'Kitchen'
      )
    );
  });

  it('drops the password when a refresh moves the selection to another network', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Trusted', secure: true, signal: 'good' }]);
    vi.mocked(setWifi).mockReset();
    const { findByLabelText, getByLabelText, getByRole } = renderWithI18n(
      <WifiPicker network={null} speaker="Kitchen" />
    );
    fireEvent.change(await findByLabelText('Password for Trusted'), {
      target: { value: 'abcdefgh' },
    });
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Other', secure: true, signal: 'good' }]);
    fireEvent.click(getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(getByRole('radio', { name: /Other/u, checked: true })).toBeTruthy());
    expect(getByLabelText('Password for Other')).toHaveProperty('value', '');
    expect(getByRole('button', { name: 'Connect' })).toHaveProperty('disabled', true);
  });

  it('shows the password unless asked not to, as Stockholm does', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: true, signal: 'good' }]);
    const { findByLabelText, getByRole } = renderWithI18n(<WifiPicker network={null} />);
    const pw = await findByLabelText('Password for Home');
    expect(pw.getAttribute('type')).toBe('text');
    fireEvent.click(getByRole('checkbox', { name: 'Show password' }));
    expect(pw.getAttribute('type')).toBe('password');
  });

  // The first-run page, as drawn: an eye in the field, the password hidden to start.
  it('shows and hides the password with the eye in the field on the first-run page', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: true, signal: 'good' }]);
    const { findByLabelText, getByRole, queryByRole } = renderWithI18n(
      <WifiPicker network={null} fixedSurvey docked />
    );
    const pw = await findByLabelText('Password for Home');
    expect(pw.getAttribute('type')).toBe('password');
    expect(queryByRole('checkbox', { name: 'Show password' })).toBeNull();
    const eye = getByRole('button', { name: 'Show password' });
    expect(eye.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(eye);
    expect(pw.getAttribute('type')).toBe('text');
    expect(eye.getAttribute('aria-pressed')).toBe('true');
  });

  // Docked, Connect ends the form and how the join goes reads above it.
  it('puts how the join goes above Connect on the first-run page', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: false, signal: 'good' }]);
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { findByRole, findByText } = renderWithI18n(
      <WifiPicker network={null} fixedSurvey docked />
    );
    const connect = await findByRole('button', { name: 'Connect' });
    fireEvent.click(connect);
    const saved = await findByText('Network saved. The speaker is switching over.');
    expect(saved.closest('form')).toBe(connect.closest('form'));
    // eslint-disable-next-line no-bitwise -- compareDocumentPosition answers with a bit mask
    expect(saved.compareDocumentPosition(connect) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  // The network the speaker is on is no choice, so an idle speaker's list can come up empty.
  it('offers Refresh and Manual Setup when nothing else is around', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: true, signal: 'good' }]);
    const network = { type: 'wifi', ssid: 'Home', ip: '10.0.0.2' } as NetworkInfo;
    const { findByText, getByRole, queryByRole } = renderWithI18n(<WifiPicker network={network} />);
    await findByText('No Networks Found');
    expect(queryByRole('button', { name: 'Connect' })).toBeNull();
    expect(getByRole('button', { name: 'Refresh' })).toBeTruthy();
    expect(getByRole('button', { name: 'Manual Setup' })).toBeTruthy();
  });

  it('says it is searching while a survey runs', async () => {
    vi.mocked(scanWifi).mockImplementation(async () => new Promise(vi.fn()));
    const { findByText } = renderWithI18n(<WifiPicker network={null} />);
    expect(await findByText('Searching for Wi-Fi Networks')).toBeTruthy();
  });

  it('joins a typed network with the security type chosen', async () => {
    vi.mocked(scanWifi).mockResolvedValue([]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { findByRole, getByLabelText, getByRole, queryByLabelText } = renderWithI18n(
      <WifiPicker network={null} speaker="Kitchen" />
    );
    fireEvent.click(await findByRole('button', { name: 'Manual Setup' }));
    fireEvent.change(getByLabelText('Wireless Network'), { target: { value: 'Hidden' } });
    expect(getByRole('button', { name: 'Connect' })).toBeDisabled();
    fireEvent.change(getByLabelText('Security Type'), { target: { value: 'none' } });
    expect(queryByLabelText('Enter Password')).toBeNull();
    fireEvent.click(getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(setWifi).toHaveBeenCalledWith(
        { ssid: 'Hidden', security: 'none', password: '' },
        'Kitchen'
      )
    );
  });

  it('opens the form for a network that is not in the list from its last row', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: true, signal: 'good' }]);
    const { findByRole, getByLabelText } = renderWithI18n(
      <WifiPicker network={null} fixedSurvey />
    );
    fireEvent.click(await findByRole('radio', { name: 'Other network' }));
    expect(getByLabelText('Wireless Network')).toBeTruthy();
  });

  it('offers no Refresh on the setup AP, whose survey is fixed', async () => {
    vi.mocked(scanWifi).mockResolvedValue([]);
    const { findByText, findByRole, queryByRole, rerender } = renderWithI18n(
      <WifiPicker network={null} fixedSurvey />
    );
    await findByText('No Networks Found');
    expect(queryByRole('button', { name: 'Refresh' })).toBeNull();
    rerender(<WifiPicker network={null} />);
    expect(await findByRole('button', { name: 'Refresh' })).toBeTruthy();
  });

  // Many Android and Windows devices cannot open .local names: name the router's list too.
  it("ends setup with the speaker's own address and its router name", async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: false, signal: 'good' }]);
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { findByRole, findByText } = renderWithI18n(
      <WifiPicker network={null} fixedSurvey host="soundtap-st10-a1b2c3.local" />
    );
    fireEvent.click(await findByRole('button', { name: 'Connect' }));
    expect(await findByText(/open http:\/\/soundtap-st10-a1b2c3\.local\./u)).toBeTruthy();
    expect(await findByText(/look for soundtap-st10-a1b2c3 in your router/u)).toBeTruthy();
  });

  // A survey that times out is not an empty one: say it failed and offer to retry.
  it('tells a failed survey from an empty one', async () => {
    vi.mocked(scanWifi).mockResolvedValue(null);
    const { findByText, getByRole, findByRole } = renderWithI18n(
      <WifiPicker network={null} speaker="Kitchen" />
    );
    expect(await findByText(/Couldn.t search for networks/u)).toBeTruthy();
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: true, signal: 'good' }]);
    fireEvent.click(getByRole('button', { name: 'Refresh' }));
    expect(await findByRole('radio', { name: /Home/u, checked: true })).toBeTruthy();
  });

  // WPA needs 8..63 bytes: say so before the speaker refuses it; and no autocorrect.
  it('holds Connect back for a too-short WPA password', async () => {
    vi.mocked(scanWifi).mockResolvedValue([{ ssid: 'Home', secure: true, signal: 'good' }]);
    const { findByLabelText, getByText, getByRole } = renderWithI18n(
      <WifiPicker network={null} speaker="Kitchen" />
    );
    const pw = await findByLabelText('Password for Home');
    expect(pw.getAttribute('autocapitalize')).toBe('none');
    fireEvent.change(pw, { target: { value: 'abc' } });
    expect(getByText('A Wi-Fi password is 8 to 63 characters.')).toBeTruthy();
    expect(getByRole('button', { name: 'Connect' })).toBeDisabled();
    fireEvent.change(pw, { target: { value: 'abcdefgh' } });
    expect(getByRole('button', { name: 'Connect' })).toBeEnabled();
  });

  it('joins a WEP network from the list with its key (ADR 0136)', async () => {
    vi.mocked(scanWifi).mockResolvedValue([
      { ssid: 'Old', secure: true, wep: true, signal: 'good' },
    ]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { findByLabelText, getByText, getByRole } = renderWithI18n(
      <WifiPicker network={null} speaker="Kitchen" />
    );
    const pw = await findByLabelText('Password for Old');
    fireEvent.change(pw, { target: { value: 'abcdef' } });
    expect(
      getByText('A WEP key is 5 or 13 characters, or 10 or 26 hexadecimal digits.')
    ).toBeTruthy();
    expect(getByRole('button', { name: 'Connect' })).toBeDisabled();
    fireEvent.change(pw, { target: { value: 'abcde' } });
    fireEvent.click(getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(setWifi).toHaveBeenCalledWith(
        { ssid: 'Old', security: 'wep', password: 'abcde' },
        'Kitchen'
      )
    );
  });

  it('joins a typed WEP network with a hex key', async () => {
    vi.mocked(scanWifi).mockResolvedValue([]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { findByRole, getByLabelText, getByRole } = renderWithI18n(
      <WifiPicker network={null} speaker="Kitchen" />
    );
    fireEvent.click(await findByRole('button', { name: 'Manual Setup' }));
    fireEvent.change(getByLabelText('Wireless Network'), { target: { value: 'Old' } });
    fireEvent.change(getByLabelText('Security Type'), { target: { value: 'wep' } });
    fireEvent.change(getByLabelText('Enter Password'), {
      target: { value: '0123456789ABCDEF0123456789' },
    });
    fireEvent.click(getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(setWifi).toHaveBeenCalledWith(
        { ssid: 'Old', security: 'wep', password: '0123456789ABCDEF0123456789' },
        'Kitchen'
      )
    );
  });

  // No second network can be sent while the first is still being joined.
  it('locks the network list while a connection is being made', async () => {
    vi.mocked(scanWifi).mockResolvedValue([
      { ssid: 'NetA', secure: false, signal: 'good' },
      { ssid: 'NetB', secure: false, signal: 'good' },
    ]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockImplementation(async () => new Promise(vi.fn()));
    const { findByRole, getByRole } = renderWithI18n(<WifiPicker network={null} speaker="" />);
    fireEvent.click(await findByRole('button', { name: 'Connect' }));
    await waitFor(() => expect(setWifi).toHaveBeenCalledTimes(1));
    fireEvent.submit(
      must(getByRole('button', { name: 'Connect' }).closest('form'), 'the join form')
    );
    await new Promise((r) => {
      setTimeout(r, 0);
    });
    expect(setWifi).toHaveBeenCalledTimes(1);
    expect(getByRole('radio', { name: /NetB/u })).toBeDisabled();
  });
});

describe('saved networks', () => {
  it('says once why new settings did not take, until dismissed', async () => {
    vi.mocked(getSavedWifi).mockResolvedValue([]);
    vi.mocked(getWifiFellBack).mockResolvedValue('wrong_password');
    vi.mocked(dismissWifiFellBack).mockResolvedValue(undefined);
    const net = { type: 'wifi', ssid: 'Home', ip: '10.0.0.2' } as NetworkInfo;
    const { findByRole, getByRole, queryByRole } = renderWithI18n(
      <NetworkSettings network={net} speaker="Kitchen" speakerName="Kitchen" />
    );
    expect(await findByRole('status')).toHaveTextContent('went back to its previous network');
    expect(getByRole('status')).toHaveTextContent('password was not accepted');
    fireEvent.click(getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(dismissWifiFellBack).toHaveBeenCalledWith('Kitchen'));
    expect(queryByRole('status')).toBeNull();
  });

  it('lists the others by name and forgets one', async () => {
    vi.mocked(getSavedWifi).mockResolvedValue([
      { id: 'aG9tZQ==', ssid: 'Home' },
      { id: 'Y2FmZQ==', ssid: 'Cafe' },
    ]);
    vi.mocked(forgetWifi).mockResolvedValue(undefined);
    const net = { type: 'wifi', ssid: 'Home', ip: '10.0.0.2' } as NetworkInfo;
    const { findByRole, queryByRole } = renderWithI18n(
      <NetworkSettings network={net} speaker="Kitchen" speakerName="Kitchen" />
    );
    fireEvent.click(await findByRole('button', { name: 'Forget Cafe' }));
    await waitFor(() => expect(forgetWifi).toHaveBeenCalledWith('Y2FmZQ==', 'Kitchen'));
    await waitFor(() => expect(queryByRole('button', { name: 'Forget Cafe' })).toBeNull());
    expect(queryByRole('button', { name: 'Forget Home' })).toBeNull();
  });

  it('says so when the speaker refuses to forget, and keeps the network', async () => {
    vi.mocked(getSavedWifi).mockResolvedValue([{ id: 'Y2FmZQ==', ssid: 'Cafe' }]);
    vi.mocked(forgetWifi).mockRejectedValue(Object.assign(new Error('forbidden'), { status: 403 }));
    const net = { type: 'wifi', ssid: 'Home', ip: '10.0.0.2' } as NetworkInfo;
    const { findByRole, getByRole } = renderWithI18n(
      <NetworkSettings network={net} speaker="Kitchen" speakerName="Kitchen" />
    );
    fireEvent.click(await findByRole('button', { name: 'Forget Cafe' }));
    expect(await findByRole('alert')).toHaveTextContent('Kitchen refused that');
    expect(getByRole('button', { name: 'Forget Cafe' })).toBeInTheDocument();
  });

  it('says what it is connected to: the network, its signal and address', async () => {
    vi.mocked(getSavedWifi).mockResolvedValue([]);
    const net = { type: 'wifi', ssid: 'Home', ip: '10.0.0.2', signal: 'good' } as NetworkInfo;
    const { findByText, getByText } = renderWithI18n(
      <NetworkSettings network={net} speaker="Kitchen" speakerName="Kitchen" />
    );
    expect(await findByText('Signal good · 10.0.0.2')).toBeInTheDocument();
    expect(getByText('Home')).toBeInTheDocument();
  });
});

describe('finding a network', () => {
  it('lists the networks nearby without the one in use, and joins one from its sheet', async () => {
    vi.mocked(getSavedWifi).mockResolvedValue([]);
    vi.mocked(scanWifi).mockResolvedValue([
      { ssid: 'Home', secure: true, signal: 'good' },
      { ssid: 'Guest', secure: true, signal: 'fair' },
    ]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const net = { type: 'wifi', ssid: 'Home', ip: '10.0.0.2' } as NetworkInfo;
    const { getByRole, findByRole, queryByRole, findByLabelText } = renderWithI18n(
      <NetworkSettings network={net} speaker="Kitchen" speakerName="Kitchen" />
    );
    fireEvent.click(getByRole('button', { name: 'Find a network' }));
    fireEvent.click(await findByRole('button', { name: /Guest/u }));
    expect(queryByRole('button', { name: /^Home/u })).toBeNull();
    const dialog = await findByRole('dialog', { name: 'Connect to Guest' });
    expect(dialog).toBeInTheDocument();
    fireEvent.change(await findByLabelText('Password'), { target: { value: 'abcdefgh' } });
    fireEvent.click(getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(setWifi).toHaveBeenCalledWith(
        { ssid: 'Guest', security: 'wpa_or_wpa2', password: 'abcdefgh' },
        'Kitchen'
      )
    );
  });

  it('joins a network that does not show up by its typed name', async () => {
    vi.mocked(getSavedWifi).mockResolvedValue([]);
    vi.mocked(scanWifi).mockResolvedValue([]);
    vi.mocked(setWifi).mockReset();
    vi.mocked(setWifi).mockResolvedValue(undefined);
    const { getByRole, findByRole, findByLabelText, getByLabelText } = renderWithI18n(
      <NetworkSettings network={null} speaker="Kitchen" speakerName="Kitchen" />
    );
    fireEvent.click(getByRole('button', { name: 'Find a network' }));
    fireEvent.click(await findByRole('button', { name: 'Other network' }));
    fireEvent.change(await findByLabelText('Wireless Network'), { target: { value: 'Hidden' } });
    fireEvent.change(getByLabelText('Password'), { target: { value: 'abcdefgh' } });
    fireEvent.click(getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(setWifi).toHaveBeenCalledWith(
        { ssid: 'Hidden', security: 'wpa_or_wpa2', password: 'abcdefgh' },
        'Kitchen'
      )
    );
  });
});
