// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { InputHTMLAttributes, TextareaHTMLAttributes } from 'react';
import { cardShadow, useSurface } from '../../../hooks/useSurface';
import { Area, Input } from './styled';

// A free-standing text input in the design's field style.
export function TextField(props: InputHTMLAttributes<HTMLInputElement>) {
  return <Input $shadow={cardShadow(useSurface())} {...props} />;
}

// The multi-line version, for a description.
export function TextArea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <Area $shadow={cardShadow(useSurface())} {...props} />;
}
