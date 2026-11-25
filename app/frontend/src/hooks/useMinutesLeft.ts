// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';

const TICK_MS = 15000;
const MINUTE_MS = 60000;

// Minutes left until endsAt (ms since epoch), refreshed while shown; 0 without one.
export function useMinutesLeft(endsAt: number | null): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (endsAt === null || endsAt === 0) return;
    const tick = () => setNow(Date.now());
    tick();
    const id = setInterval(tick, TICK_MS);
    return () => clearInterval(id);
  }, [endsAt]);
  return endsAt === null || endsAt === 0 ? 0 : Math.max(1, Math.ceil((endsAt - now) / MINUTE_MS));
}
