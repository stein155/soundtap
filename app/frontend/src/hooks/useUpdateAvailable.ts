// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { getReleases } from '../lib/api';
import type { VersionInfo } from '../lib/types';
import { updateAvailable } from '../lib/version';

// Whether a newer stable release is out for this speaker, for the Updates row's summary.
// The speaker caches its release list, so opening settings does not hit GitHub each time.
export function useUpdateAvailable(speaker: string, version: VersionInfo | null): boolean {
  const updatable = version?.updateMode === 'firmware' && version.updatable;
  const [found, setFound] = useState<string | null>(null);
  useEffect(() => {
    if (!updatable) return undefined;
    let current = true;
    void getReleases(speaker).then((r) => {
      if (current && r && updateAvailable(r.current, r.stable?.tag)) setFound(speaker);
    });
    return () => {
      current = false;
    };
  }, [speaker, updatable]);
  return updatable && found === speaker;
}
