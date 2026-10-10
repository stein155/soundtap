// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ButtonHTMLAttributes } from 'react';
import { Underlined } from './styled';

// Stockholm's underlined link inside a form; it acts, so it is a button.
export function TextButton(props: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <Underlined type="button" {...props} />;
}
