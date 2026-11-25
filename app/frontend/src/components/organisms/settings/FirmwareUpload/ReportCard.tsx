// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Hint } from '../../../atoms/Hint';
import { Stack } from '../../../atoms/Stack';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { useI18n } from '../../../../lib/i18n';
import type { FirmwareReport } from '../../../../lib/types';

interface Props {
  report: FirmwareReport;
  // The package comes with its .sig; a release the speaker fetches always does.
  signed?: boolean;
  busy: boolean;
  onInstall: () => void;
  onReboot: () => Promise<void>;
}

// An older release or Bose's firmware says so before it asks.
function installWording(report: FirmwareReport, t: ReturnType<typeof useI18n>['t']) {
  if (report.confirm === 'stock')
    return { warning: t('firmwareStockWarn'), confirmLabel: t('firmwareStockConfirm') };
  if (report.confirm === 'downgrade')
    return {
      warning: t('firmwareDowngradeWarn', { version: report.version || '?' }),
      confirmLabel: t('firmwareDowngradeConfirm'),
    };
  return { warning: t('firmwareInstallWarn'), confirmLabel: t('firmwareInstallConfirm') };
}

// What the speaker said about the picked package, and the next step it allows:
// install it, or once installed, reboot into it. An unsigned build (ADR 0048) says that the
// press on the speaker is what installs it.
export function ReportCard({
  report,
  signed = true,
  busy,
  onInstall,
  onReboot,
}: Props): ReactElement {
  const { t } = useI18n();
  const { warning, confirmLabel } = installWording(report, t);
  const installable = report.installable && report.installed !== true;
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
        {report.bootloaders && report.bootloaders.length > 0 && (
          <FieldRow label={t('firmwareBootloaders')} layout="value">
            {report.bootloaders.join(', ')}
          </FieldRow>
        )}
      </FieldCard>
      <Hint error={!report.installable}>
        {report.reason ||
          (report.installable ? t('firmwareCompatible') : t('firmwareNotInstallable'))}
      </Hint>
      {installable && !signed && report.confirm !== 'stock' && <Hint>{t('firmwareUnsigned')}</Hint>}
      {installable && (
        <ConfirmAction
          label={t('firmwareInstall')}
          warning={warning}
          confirmLabel={confirmLabel}
          disabled={busy}
          onConfirm={onInstall}
        />
      )}
      {report.installed === true && (
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
