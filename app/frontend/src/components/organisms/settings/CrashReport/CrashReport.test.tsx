// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import type { CrashReport } from '../../../../lib/types';
import { CrashNotice, CrashReportSwitch } from '.';

vi.mock('../../../../lib/api', () => ({
  getCrashReport: vi.fn(),
  recordCrashReport: vi.fn(),
  saveSettings: vi.fn(),
}));

const crash: CrashReport = {
  id: 'panic-0123456789ab',
  kind: 'panic',
  title: 'Crash: soundtapd panic: boom',
  body: '**What happened:** soundtapd stopped with a panic.\n\n```text\ngoroutine 1 [running]:\n```\n',
  url: 'https://github.com/stein155/soundtap/issues/new?body=x&title=y',
};

describe('CrashNotice', () => {
  afterEach(() => vi.resetAllMocks());

  it('asks the speaker nothing and shows nothing while reports are off', () => {
    const { container } = renderWithI18n(<CrashNotice speaker="kitchen" enabled={false} />);
    expect(api.getCrashReport).not.toHaveBeenCalled();
    expect(container.textContent).toBe('');
  });

  it('shows nothing when the speaker has no crash to offer', async () => {
    vi.mocked(api.getCrashReport).mockResolvedValue(null);
    const { container } = renderWithI18n(<CrashNotice speaker="kitchen" enabled />);
    await waitFor(() => expect(api.getCrashReport).toHaveBeenCalledWith('kitchen'));
    expect(container.textContent).toBe('');
  });

  it('shows the exact report before it leaves, and records it once opened', async () => {
    vi.mocked(api.getCrashReport).mockResolvedValue(crash);
    vi.mocked(api.recordCrashReport).mockResolvedValue(undefined);
    const { findByRole, getByRole, getByTestId, queryByRole, container } = renderWithI18n(
      <CrashNotice speaker="kitchen" enabled />
    );
    fireEvent.click(await findByRole('button', { name: 'Report this crash' }));

    // Word for word what the link carries, and no link opened yet.
    expect(getByTestId('crash-title').textContent).toBe(crash.title);
    expect(getByTestId('crash-body').textContent).toBe(crash.body);
    expect(container.textContent).toContain('Maintenance › Help and support');
    expect(api.recordCrashReport).not.toHaveBeenCalled();

    const open = getByRole('link', { name: 'Open the report on GitHub' });
    expect(open.getAttribute('href')).toBe(crash.url);
    expect(open.getAttribute('target')).toBe('_blank');
    expect(open.getAttribute('rel')).toContain('noopener');
    open.addEventListener('click', (e) => e.preventDefault()); // jsdom does not navigate
    fireEvent.click(open);

    expect(api.recordCrashReport).toHaveBeenCalledWith(crash.id, 'reported', 'kitchen');
    expect(queryByRole('link', { name: 'Open the report on GitHub' })).toBeNull();
    expect(container.textContent).toBe('');
  });

  it('goes back from the preview without recording anything', async () => {
    vi.mocked(api.getCrashReport).mockResolvedValue(crash);
    const { findByRole, getByRole, queryByTestId } = renderWithI18n(
      <CrashNotice speaker="kitchen" enabled />
    );
    fireEvent.click(await findByRole('button', { name: 'Report this crash' }));
    fireEvent.click(getByRole('button', { name: 'Cancel' }));
    expect(queryByTestId('crash-body')).toBeNull();
    expect(getByRole('button', { name: 'Report this crash' })).toBeTruthy();
    expect(api.recordCrashReport).not.toHaveBeenCalled();
  });

  it('records a dismissed crash so it is not offered again', async () => {
    vi.mocked(api.getCrashReport).mockResolvedValue(crash);
    vi.mocked(api.recordCrashReport).mockRejectedValue(new Error('offline'));
    const { findByRole, container } = renderWithI18n(<CrashNotice speaker="" enabled />);
    fireEvent.click(await findByRole('button', { name: 'Dismiss' }));
    expect(api.recordCrashReport).toHaveBeenCalledWith(crash.id, 'dismissed', '');
    // A failed save only means the speaker offers it again next time.
    await waitFor(() => expect(container.textContent).toBe(''));
  });

  it('looks once reports are switched on', async () => {
    vi.mocked(api.getCrashReport).mockResolvedValue(crash);
    const { rerender, findByRole } = renderWithI18n(
      <CrashNotice speaker="kitchen" enabled={false} />
    );
    rerender(<CrashNotice speaker="kitchen" enabled />);
    expect(await findByRole('button', { name: 'Report this crash' })).toBeTruthy();
  });
});

describe('CrashReportSwitch', () => {
  afterEach(() => vi.resetAllMocks());

  it('saves the opt-in and says when it could not', async () => {
    const onChange = vi.fn();
    vi.mocked(api.saveSettings).mockResolvedValue(undefined);
    const { getByRole, rerender, findByRole } = renderWithI18n(
      <CrashReportSwitch speaker="kitchen" on={false} onChange={onChange} />
    );
    const row = getByRole('switch', { name: /Offer crash reports/ });
    expect(row.getAttribute('aria-checked')).toBe('false');
    fireEvent.click(row);
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(true));
    expect(api.saveSettings).toHaveBeenCalledWith({ crashReports: true }, 'kitchen');

    vi.mocked(api.saveSettings).mockRejectedValue(new Error('offline'));
    rerender(<CrashReportSwitch speaker="kitchen" on onChange={onChange} />);
    fireEvent.click(getByRole('switch', { name: /Offer crash reports/ }));
    expect((await findByRole('alert')).textContent).toBe('Crash reports could not be switched');
    expect(onChange).toHaveBeenCalledTimes(1);
  });
});
