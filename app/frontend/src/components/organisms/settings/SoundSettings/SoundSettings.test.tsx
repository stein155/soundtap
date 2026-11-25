// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import { SoundSettings } from '.';

const api = vi.hoisted(() => ({
  getSoundbarChoice: vi.fn(),
  setSoundbarChoice: vi.fn(),
  getTone: vi.fn(async () => null),
  setTone: vi.fn(),
  startAdaptiq: vi.fn(),
  getLipSync: vi.fn(async (): Promise<{ ms: number | null; max: number } | null> => null),
  setLipSync: vi.fn(),
}));
vi.mock('../../../../lib/api/settings', () => api);
vi.mock('../../../../lib/api/multiroom', () => ({
  getChannel: vi.fn(async (): Promise<'stereo' | 'mono' | null> => 'stereo'),
  setChannel: vi.fn(),
}));

const props = {
  bass: 0,
  caps: { actual: 0, target: 0, min: 0, max: 0, default: 0 },
  soundbar: null,
  onBass: vi.fn(),
};

const render = () =>
  renderWithI18n(
    <SoundSettings
      bass={0}
      caps={{ actual: 0, target: 0, min: 0, max: 0, default: 0 }}
      soundbar={null}
      onBass={vi.fn()}
    />
  );

describe('SoundSettings audio mode', () => {
  beforeEach(() => vi.clearAllMocks());

  it('sends the chosen mode and keeps the old one when the bar refuses', async () => {
    api.getSoundbarChoice.mockImplementation(async (c: string) =>
      c === 'audio-mode' ? 'normal' : null
    );
    api.setSoundbarChoice.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('no'));
    const view = render();
    const dialog = await view.findByRole('radio', { name: 'Dialogue' });
    fireEvent.click(dialog);
    await waitFor(() => expect(dialog.getAttribute('aria-checked')).toBe('true'));
    expect(api.setSoundbarChoice).toHaveBeenCalledWith('audio-mode', 'dialog', undefined);
    fireEvent.click(view.getByRole('radio', { name: 'Night' }));
    await waitFor(() => expect(dialog.getAttribute('aria-checked')).toBe('true'));
    expect(api.setSoundbarChoice).toHaveBeenLastCalledWith('audio-mode', 'night', undefined);
  });

  it('lets an older failed choice not undo a newer one', async () => {
    api.getSoundbarChoice.mockImplementation(async (c: string) =>
      c === 'audio-mode' ? 'normal' : null
    );
    let failFirst: (e: Error) => void = vi.fn();
    api.setSoundbarChoice
      .mockImplementationOnce(async () => new Promise((_, reject) => (failFirst = reject)))
      .mockResolvedValueOnce(undefined);
    const view = render();
    await view.findByRole('radiogroup', { name: 'Audio mode' });
    fireEvent.click(view.getByRole('radio', { name: 'Dialogue' }));
    fireEvent.click(view.getByRole('radio', { name: 'Night' }));
    await waitFor(() => expect(api.setSoundbarChoice).toHaveBeenCalledTimes(2));
    failFirst(new Error('late'));
    await new Promise((r) => setTimeout(r, 0));
    expect(view.getByRole('radio', { name: 'Night' })).toBeChecked();
  });

  it('shows nothing on a speaker without the setting', async () => {
    api.getSoundbarChoice.mockResolvedValue(null);
    const view = render();
    await waitFor(() => expect(api.getSoundbarChoice).toHaveBeenCalled());
    expect(view.queryByRole('radiogroup', { name: 'Audio mode' })).toBeNull();
  });

  it('offers a setting the bar has not reported yet, with nothing selected', async () => {
    api.getSoundbarChoice.mockImplementation(async (c: string) => (c === 'cec-mode' ? '' : null));
    const view = render();
    expect(
      await view.findByRole('radiogroup', { name: 'TV control (HDMI-CEC)' })
    ).toBeInTheDocument();
    expect(view.queryByRole('radio', { checked: true })).toBeNull();
  });

  it('sets the HDMI-CEC mode on its own route', async () => {
    api.getSoundbarChoice.mockImplementation(async (c: string) => (c === 'cec-mode' ? 'on' : null));
    api.setSoundbarChoice.mockResolvedValue(undefined);
    const view = render();
    expect(
      await view.findByRole('radiogroup', { name: 'TV control (HDMI-CEC)' })
    ).toBeInTheDocument();
    fireEvent.click(view.getByRole('radio', { name: 'Alternate' }));
    await waitFor(() =>
      expect(api.setSoundbarChoice).toHaveBeenCalledWith('cec-mode', 'alternate', undefined)
    );
  });

  it('sets the HDMI input button, numbers as they are', async () => {
    api.getSoundbarChoice.mockImplementation(async (c: string) =>
      c === 'hdmi-input' ? 'none' : null
    );
    api.setSoundbarChoice.mockResolvedValue(undefined);
    const view = render();
    expect(await view.findByRole('radiogroup', { name: 'HDMI input button' })).toBeInTheDocument();
    expect(view.getByRole('radio', { name: 'None' })).toBeChecked();
    fireEvent.click(view.getByRole('radio', { name: '2' }));
    await waitFor(() =>
      expect(api.setSoundbarChoice).toHaveBeenCalledWith('hdmi-input', '2', undefined)
    );
  });
});

describe('SoundSettings lip-sync', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows the delay the bar reports and sends a moved one', async () => {
    api.getSoundbarChoice.mockResolvedValue(null);
    api.getLipSync.mockResolvedValue({ ms: 40, max: 300 });
    api.setLipSync.mockResolvedValue(undefined);
    const view = render();
    const slider = await view.findByRole('slider', { name: 'Lip sync (sound delay)' });
    expect(slider.getAttribute('aria-valuetext')).toBe('40 ms');
    fireEvent.keyDown(slider, { key: 'ArrowRight' });
    await waitFor(() => expect(api.setLipSync).toHaveBeenCalledWith(50, undefined));
  });

  it('is not shown on a speaker without one', async () => {
    api.getSoundbarChoice.mockResolvedValue(null);
    api.getLipSync.mockResolvedValue(null);
    const view = render();
    await waitFor(() => expect(api.getLipSync).toHaveBeenCalled());
    expect(view.queryByRole('slider', { name: 'Lip sync (sound delay)' })).toBeNull();
  });
});

describe('SoundSettings switching soundbars', () => {
  beforeEach(() => vi.clearAllMocks());

  it("does not show bar A's values on bar B while B's read is on the way", async () => {
    api.getSoundbarChoice.mockImplementation(async (c: string, sp?: string) => {
      if (sp !== 'A') return new Promise(vi.fn());
      return c === 'audio-mode' ? 'normal' : null;
    });
    api.getLipSync.mockImplementation(async (sp?: string) =>
      sp === 'A' ? { ms: 40, max: 300 } : new Promise(vi.fn())
    );
    const view = renderWithI18n(<SoundSettings {...props} speaker="A" />);
    await view.findByRole('slider', { name: 'Lip sync (sound delay)' });
    await view.findByRole('radio', { name: 'Dialogue' });
    view.rerender(<SoundSettings {...props} speaker="B" />);
    expect(view.queryByRole('slider', { name: 'Lip sync (sound delay)' })).toBeNull();
    expect(view.queryByRole('radio', { name: 'Dialogue' })).toBeNull();
  });
});

describe('SoundSettings group', () => {
  it('shows nothing on a speaker without bass, soundbar or stereo choice', () => {
    const { container } = renderWithI18n(
      <SoundSettings {...props} soundbar={undefined} caps={props.caps} />
    );
    expect(container.textContent).toBe('');
  });

  it('puts bass under the Sound title, with the mono note only where stock has the switch', async () => {
    const caps = { actual: 0, target: 0, min: -9, max: 0, default: 0 };
    const plain = renderWithI18n(<SoundSettings {...props} soundbar={undefined} caps={caps} />);
    expect(plain.getByText('Sound')).toBeTruthy();
    expect(plain.queryByText('Turn off for mono')).toBeNull();
    plain.unmount();
    const st10 = renderWithI18n(<SoundSettings {...props} soundbar={undefined} caps={caps} mono />);
    expect(await st10.findByText('Turn off for mono')).toBeTruthy();
  });
});
