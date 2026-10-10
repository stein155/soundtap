// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Hint } from '../../../atoms/Hint';
import { Stack } from '../../../atoms/Stack';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { useI18n } from '../../../../lib/i18n';
import type { FirmwareReport } from '../../../../lib/types';

type Props = {
  report: FirmwareReport;
  busy: boolean;
  onInstall: () => void;
  onReboot: () => Promise<void>;
};

// What the speaker said about the picked package, and the next step it allows:
// install it, or once installed, reboot into it.
export function ReportCard({ report, busy, onInstall, onReboot }: Props) {
  const { t } = useI18n();
  // An older release or Bose's firmware says so before it asks.
  const warning =
    report.confirm === 'stock'
      ? t('firmwareStockWarn')
      : report.confirm === 'downgrade'
        ? t('firmwareDowngradeWarn', { version: report.version || '?' })
        : t('firmwareInstallWarn');
  const confirmLabel =
    report.confirm === 'stock'
      ? t('firmwareStockConfirm')
      : report.confirm === 'downgrade'
        ? t('firmwareDowngradeConfirm')
        : t('firmwareInstallConfirm');
  return (
    <Stack offset={8}>
      <FieldCard>
        <FieldRow label={t('firmwareKernel')} layout="value">
          {report.kernel}
        </FieldRow>
        <FieldRow label={t('firmwareModels')} layout="value">
          {report.models.join(', ')}
        </FieldRow>
        <FieldRow label={t('firmwareModules')} layout="value">
          {String(report.modules.length)}
        </FieldRow>
      </FieldCard>
      <Hint error={!report.installable}>
        {report.reason ||
          (report.installable ? t('firmwareCompatible') : t('firmwareNotInstallable'))}
      </Hint>
      {report.installable && !report.installed && (
        <ConfirmAction
          label={t('firmwareInstall')}
          warning={warning}
          confirmLabel={confirmLabel}
          disabled={busy}
          onConfirm={onInstall}
        />
      )}
      {report.installed && (
        <ConfirmAction
          label={t('firmwareReboot')}
          warning={t('firmwareRebootWarn')}
          confirmLabel={t('firmwareRebootConfirm')}
          onConfirm={onReboot}
        />
      )}
    </Stack>
  );
}
