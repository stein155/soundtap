// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Stack } from '../../../atoms/Stack';
import { CrashReportSwitch } from '../CrashReport';

// What only someone working on SoundTap needs, kept off the speaker's page: offering crash
// reports (ADR 0126). Installing a file by hand, an own build among them, and the way back
// to Bose live under Updates.
export function DeveloperSettings({
  speaker,
  crashReports,
  onCrashReports,
}: {
  speaker: string;
  crashReports: boolean;
  onCrashReports: (on: boolean) => void;
}): ReactElement {
  return (
    <Stack gap={8}>
      <CrashReportSwitch speaker={speaker} on={crashReports} onChange={onCrashReports} />
    </Stack>
  );
}
