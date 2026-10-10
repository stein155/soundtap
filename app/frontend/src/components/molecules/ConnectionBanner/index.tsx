// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Spinner } from '../../atoms/Spinner';
import { Bar } from './styled';

// Pinned to the top while the speaker that serves this page does not answer: what is on
// screen is its last known state, not live. Goes away by itself once it answers.
export function ConnectionBanner({ message }: { message: string }) {
  return (
    <Bar role="status">
      <Spinner small />
      {message}
    </Bar>
  );
}
