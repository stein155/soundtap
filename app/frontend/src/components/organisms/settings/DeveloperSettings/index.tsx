// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Stack } from '../../../atoms/Stack';
import { FirmwareUpload } from '../FirmwareUpload';
import { OwnerKey } from '../UpdateSettings/OwnerKey';
import { CrashReportSwitch } from '../CrashReport';
import type { VersionInfo } from '../../../../lib/types';

// What only someone working on SoundTap needs, kept off the speaker's page: installing a
// package by hand (also the way back to Bose's software), an own signing key, and offering
// crash reports (ADR 0126).
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
      {firmware && <FirmwareUpload speaker={speaker} />}
      {firmware && <OwnerKey key={speaker} speaker={speaker} />}
      <CrashReportSwitch speaker={speaker} on={crashReports} onChange={onCrashReports} />
    </Stack>
  );
}
