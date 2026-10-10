// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Letters = styled.span<{ $covered: boolean; $scale: number }>`
  max-width: 100%;
  white-space: nowrap;
  text-align: center;
  font-size: 14px;
  font-weight: 700;
  line-height: 1;
  transform: scale(${({ $scale }) => $scale});
  visibility: ${({ $covered }) => ($covered ? 'hidden' : 'visible')};
`;

// Laid over the lettermark in the (relatively positioned) artwork square. A square logo
// (TuneIn's usually are) fills the tile; any other shape sits whole on the tile's own
// plain background. The blurred wash of its colours behind it was too loud
// (2026-10-25).
export const Image = styled.img<{ $loaded: boolean; $square: boolean }>`
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  border-radius: inherit;
  object-fit: ${({ $square }) => ($square ? 'cover' : 'contain')};
  padding: ${({ $square }) => ($square ? '0' : '12%')};
  opacity: ${({ $loaded }) => ($loaded ? 1 : 0)};
  transition: opacity 260ms ease;
`;
