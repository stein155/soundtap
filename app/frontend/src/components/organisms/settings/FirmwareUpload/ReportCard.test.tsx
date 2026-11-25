// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderWithI18n } from '../../../../test/render';
import en from '../../../../lib/i18n/messages/en';
import type { FirmwareReport } from '../../../../lib/types';
import { ReportCard } from './ReportCard';

const report = (confirm?: NonNullable<FirmwareReport['confirm']>): FirmwareReport => {
  const base: FirmwareReport = {
    size: 1,
    modules: [],
    kernel: 'mainline',
    models: ['st10'],
    installable: true,
  };
  return confirm === undefined ? base : { ...base, confirm };
};

const noop = vi.fn().mockResolvedValue(undefined);

// ADR 0048: an unsigned build installs on the press alone, and the page says so before it
// asks; a signed package and Bose's own file, which has its own warning, do not get it.
describe('ReportCard', () => {
  it('tells an unsigned build that the press on the speaker installs it', () => {
    const { queryByText } = renderWithI18n(
      <ReportCard
        report={report('install')}
        signed={false}
        busy={false}
        onInstall={noop}
        onReboot={noop}
      />
    );
    expect(queryByText(en.firmwareUnsigned)).not.toBeNull();
  });

  it('says nothing of the kind for a signed package or Bose firmware', () => {
    for (const [r, signed] of [
      [report('install'), true],
      [report('stock'), false],
    ] as const) {
      const { queryByText, unmount } = renderWithI18n(
        <ReportCard report={r} signed={signed} busy={false} onInstall={noop} onReboot={noop} />
      );
      expect(queryByText(en.firmwareUnsigned)).toBeNull();
      unmount();
    }
  });
});
