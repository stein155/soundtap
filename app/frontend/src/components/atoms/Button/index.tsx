// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { AnchorHTMLAttributes, ButtonHTMLAttributes } from 'react';
import { ButtonEl, LinkEl, type ButtonVariant } from './styled';

export type { ButtonVariant } from './styled';

interface Look {
  variant?: ButtonVariant;
  // Full width, with the gap the design leaves above a stand-alone action.
  wide?: boolean;
  // The smaller size that fits beside other content (a playlist's header).
  small?: boolean;
}

export function Button({
  variant = 'primary',
  wide,
  small,
  type = 'button',
  ...rest
}: Look & ButtonHTMLAttributes<HTMLButtonElement>) {
  return <ButtonEl type={type} $variant={variant} $wide={wide} $small={small} {...rest} />;
}

// A link that looks like a button: a download, a page elsewhere.
export function ButtonLink({
  variant = 'primary',
  wide,
  small,
  ...rest
}: Look & AnchorHTMLAttributes<HTMLAnchorElement> & { href: string }) {
  return <LinkEl $variant={variant} $wide={wide} $small={small} {...rest} />;
}
