// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import type { Tone } from '../../../../lib/types';
import { SoundSettings } from '.';

const api = vi.hoisted(() => ({
  getSoundbarChoice: vi.fn(),
  setSoundbarChoice: vi.fn(),
  getTone: vi.fn(async (): Promise<Tone | null> => null),
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
      .mockImplementationOnce(
        async () =>
          new Promise((_, reject) => {
            failFirst = reject;
          })
      )
      .mockResolvedValueOnce(undefined);
    const view = render();
    await view.findByRole('radiogroup', { name: 'Audio mode' });
    fireEvent.click(view.getByRole('radio', { name: 'Dialogue' }));
    fireEvent.click(view.getByRole('radio', { name: 'Night' }));
    await waitFor(() => expect(api.setSoundbarChoice).toHaveBeenCalledTimes(2));
    failFirst(new Error('late'));
    await new Promise((r) => {
      setTimeout(r, 0);
    });
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

  it('still sends a move made just before the page closes', async () => {
    api.getSoundbarChoice.mockResolvedValue(null);
    api.getLipSync.mockResolvedValue({ ms: 40, max: 300 });
    api.setLipSync.mockResolvedValue(undefined);
    const view = render();
    const slider = await view.findByRole('slider', { name: 'Lip sync (sound delay)' });
    fireEvent.keyDown(slider, { key: 'ArrowRight' });
    expect(api.setLipSync).not.toHaveBeenCalled();
    view.unmount();
    expect(api.setLipSync).toHaveBeenCalledExactlyOnceWith(50, undefined);
  });

  it('is not shown on a speaker without one', async () => {
    api.getSoundbarChoice.mockResolvedValue(null);
    api.getLipSync.mockResolvedValue(null);
    const view = render();
    await waitFor(() => expect(api.getLipSync).toHaveBeenCalled());
    expect(view.queryByRole('slider', { name: 'Lip sync (sound delay)' })).toBeNull();
  });
});

describe('SoundSettings ADAPTiQ', () => {
  beforeEach(() => vi.clearAllMocks());

  const start = async (view: ReturnType<typeof renderWithI18n>) => {
    fireEvent.click(view.getByRole('button', { name: 'Run ADAPTiQ' }));
    await view.findByRole('dialog');
    fireEvent.click(view.getByRole('button', { name: 'Start' }));
    return view.findByRole('alert');
  };

  it('says the bar did not answer when the start never reached it', async () => {
    api.getSoundbarChoice.mockResolvedValue(null);
    api.startAdaptiq.mockRejectedValue(new TypeError('Failed to fetch'));
    const view = renderWithI18n(<SoundSettings {...props} speaker="TV" name="TV" />);
    expect((await start(view)).textContent).toBe('Couldn’t reach TV');
  });

  it('says the bar refused, and why, when it said no', async () => {
    api.getSoundbarChoice.mockResolvedValue(null);
    api.startAdaptiq.mockRejectedValue(
      Object.assign(new Error('busy'), { status: 409, code: 'busy' })
    );
    const view = renderWithI18n(<SoundSettings {...props} speaker="TV" name="TV" />);
    expect((await start(view)).textContent).toMatch(/^TV refused that \(.+\)$/u);
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

  it("sends a tone move to the bar it was made on, and does not show A's tone on B", async () => {
    const level = (value: number) => ({ value, min: -10, max: 10, step: 1 });
    api.getSoundbarChoice.mockResolvedValue(null);
    api.getTone.mockImplementation(async (sp?: string) =>
      sp === 'A'
        ? { bass: level(0), treble: level(0), centre: level(0), rear: level(0) }
        : new Promise(vi.fn())
    );
    api.setTone.mockResolvedValue(undefined);
    const view = renderWithI18n(<SoundSettings {...props} speaker="A" />);
    const treble = await view.findByRole('slider', { name: 'Treble' });
    fireEvent.keyDown(treble, { key: 'ArrowRight' });
    view.rerender(<SoundSettings {...props} speaker="B" />);
    expect(api.setTone).toHaveBeenCalledExactlyOnceWith(
      { bass: 0, treble: 1, centre: 0, rear: 0 },
      'A'
    );
    expect(view.queryByRole('slider', { name: 'Treble' })).toBeNull();
  });
});

describe('SoundSettings group', () => {
  it('shows nothing on a speaker without bass, soundbar or stereo choice', () => {
    const { container } = renderWithI18n(
      <SoundSettings {...props} soundbar={undefined} caps={props.caps} />
    );
    expect(container.textContent).toBe('');
  });

  it('puts bass under Tone, with the Output switch only where stock has it', async () => {
    const caps = { actual: 0, target: 0, min: -9, max: 0, default: 0 };
    const plain = renderWithI18n(<SoundSettings {...props} soundbar={undefined} caps={caps} />);
    expect(plain.getByText('Tone')).toBeTruthy();
    expect(plain.queryByText('Output')).toBeNull();
    expect(plain.queryByText('Turn off for mono')).toBeNull();
    plain.unmount();
    const st10 = renderWithI18n(<SoundSettings {...props} soundbar={undefined} caps={caps} mono />);
    expect(await st10.findByText('Turn off for mono')).toBeTruthy();
  });
});
