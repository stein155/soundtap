// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Stack } from '../../../atoms/Stack';
import { OwnerKey } from '../UpdateSettings/OwnerKey';
import { CrashReportSwitch } from '../CrashReport';
import type { VersionInfo } from '../../../../lib/types';

// What only someone working on SoundTap needs, kept off the speaker's page: an own signing
// key, and offering crash reports (ADR 0126). Installing a file by hand, and the way back to
// Bose, live under Updates.
export function DeveloperSettings({
  speaker,
  version,
  crashReports,
  onCrashReports,
}: {
  speaker: string;
  version: VersionInfo | null;
  crashReports: boolean;
  onCrashReports: (on: boolean) => void;
}) {
  const firmware = version?.updateMode === 'firmware';
  return (
    <Stack gap={8}>
      {firmware && <OwnerKey key={speaker} speaker={speaker} />}
      <CrashReportSwitch speaker={speaker} on={crashReports} onChange={onCrashReports} />
    </Stack>
  );
}
