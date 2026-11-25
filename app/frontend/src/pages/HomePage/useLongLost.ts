// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';

// How long the speaker may be out of reach before the page says so in full: a restart or a
// short Wi-Fi gap stays a banner, a speaker that is really gone gets its own page.
export const LONG_LOST_MS = 15000;

// Whether the speaker has been unreachable for LONG_LOST_MS on end; false again as soon as
// it answers.
export function useLongLost(unreachable: boolean, after = LONG_LOST_MS) {
  const [long, setLong] = useState(false);
  useEffect(() => {
    if (!unreachable) return;
    const timer = setTimeout(() => setLong(true), after);
    // Back in reach: the next loss starts counting from nothing.
    return () => {
      clearTimeout(timer);
      setLong(false);
    };
  }, [unreachable, after]);
  return unreachable && long;
}
