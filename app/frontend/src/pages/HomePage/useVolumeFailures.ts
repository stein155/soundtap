// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useEffectEvent } from 'react';
import type { ReportFailure } from '../../hooks/useErrorReporter';

// A volume change the speaker refused or never got: said once per failure. A new name for
// the room (a rename, discovery catching up) must not say it again.
export function useVolumeFailures(failures: number, failed: ReportFailure, leadName: string): void {
  const report = useEffectEvent(() => failed(leadName)());
  useEffect(() => {
    if (failures > 0) report();
  }, [failures]);
}
