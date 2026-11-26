// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { VersionInfo } from './types';

// "SoundTap Core 0.1.0 · app 0.1.0": the two release lanes (ADR 0035) the way the releases
// name them. Falls back to the raw version for a soundtapd that does not report them.
export function versionLabel(v: VersionInfo | null | undefined): string {
  if (!v) return '';
  const parts = [v.os && `SoundTap Core ${v.os}`, v.app && `app ${v.app}`].filter(Boolean);
  return parts.length > 0 ? parts.join(' · ') : v.version || '';
}

// "0.1.0 · app 0.1.0": the same, short, where the line already says it is SoundTap (the
// version a newer one would replace).
export function shortVersionLabel(v: VersionInfo | null | undefined): string {
  if (!v) return '';
  const parts = [v.os, v.app && `app ${v.app}`].filter(Boolean);
  return parts.length > 0 ? parts.join(' · ') : v.version || '';
}

// The numbers of "app-v1.2.3" (or "1.2.3"), with a fourth that orders an alpha
// ("1.2.3-alpha.4", ADR 0035) before its release; null for a dev, beta or other build.
const appNumbers = (tag: string): number[] | null => {
  const v =
    /^(?:app-v)?(?<major>\d+)\.(?<minor>\d+)\.(?<patch>\d+)(?:-alpha\.(?<alpha>[1-9]\d*))?$/u.exec(
      tag.trim()
    )?.groups;
  if (!v) return null;
  const { major, minor, patch, alpha } = v;
  return [Number(major), Number(minor), Number(patch), alpha ? Number(alpha) : Infinity];
};

// Whether the release the speaker is offered ("stable" in /api/releases: a release, or on
// an alpha also a newer alpha) is newer than what it runs. A build that is not a release or
// an alpha (dev, beta) is never told to "update" to an older release.
export function updateAvailable(current: string, stable: string | undefined): boolean {
  const from = appNumbers(current);
  const to = stable ? appNumbers(stable) : null;
  if (!from || !to) return false;
  for (const [i, next] of to.entries()) {
    const running = from[i];
    if (running === undefined) return false; // both have four numbers
    if (next !== running) return next > running;
  }
  return false;
}
