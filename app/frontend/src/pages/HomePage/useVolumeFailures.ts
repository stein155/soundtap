// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect } from 'react';
import type { ReportFailure } from '../../hooks/useErrorReporter';

// A volume change the speaker refused or never got: said once per failure.
export function useVolumeFailures(failures: number, failed: ReportFailure, leadName: string): void {
  useEffect(() => {
    if (failures) failed(leadName)();
  }, [failures, failed, leadName]);
}
