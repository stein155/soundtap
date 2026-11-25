// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { scanWifi } from '../../../../lib/api';
import type { WifiNetwork } from '../../../../lib/types';

// What the speaker hears around it. `networks` is null until the first survey answers, and
// a failed survey keeps whatever the last one found.
export interface WifiSurvey {
  networks: WifiNetwork[] | null;
  scanning: boolean;
  failed: boolean;
  refresh: () => void;
}

export function useWifiSurvey(speaker?: string): WifiSurvey {
  const [networks, setNetworks] = useState<WifiNetwork[] | null>(null);
  const [scanning, setScanning] = useState(true);
  const [failed, setFailed] = useState(false);
  // Bumped by Refresh; each value is one survey.
  const [round, setRound] = useState(0);
  useEffect(() => {
    let current = true;
    void scanWifi(speaker).then((found) => {
      if (!current) return;
      setFailed(found === null);
      setNetworks((prev) => found ?? prev ?? []);
      setScanning(false);
    });
    return () => {
      current = false;
    };
  }, [speaker, round]);
  const refresh = () => {
    setScanning(true);
    setRound((r) => r + 1);
  };
  return { networks, scanning, failed, refresh };
}
