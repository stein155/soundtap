// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Invitation = styled.div`
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 40px 16px;
  text-align: center;
`;

export const Heading = styled.h3`
  margin: 6px 0 0;
  font-size: 16px;
  font-weight: 700;
  letter-spacing: var(--tracking-title);
`;

export const Text = styled.p`
  max-width: 280px;
  margin: 0;
  font-size: 13.5px;
  line-height: 1.5;
  color: var(--ink-2);
  text-wrap: pretty;
`;

// A playlist's or album's header: its cover, name and "play all".
export const Hero = styled.div`
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px 0;
`;

export const Cover = styled.img`
  width: 124px;
  height: 124px;
  flex-shrink: 0;
  object-fit: cover;
  border-radius: var(--radius);
  box-shadow: 0 12px 28px -14px rgba(0, 0, 0, 0.5);
`;

export const HeroText = styled.div`
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;

  ${Heading} {
    margin-top: 0;
    font-size: 18px;
    letter-spacing: -0.2px;
    line-height: 1.25;
  }

  ${Text} {
    font-size: 12px;
    font-weight: 400;
    color: var(--ink-3);
  }

  /* Play: black with a white glyph, never black on Spotify's green. */
  & > button {
    margin-top: 12px;
    min-height: 44px;
    padding: 0 20px;
  }
`;

export const Account = styled.div`
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 48px;
  padding: 8px 0 16px;
  font-size: 12.5px;
  color: var(--ink-3);
`;

export const AccountName = styled.span`
  display: inline-flex;
  align-items: center;
  gap: 8px;
`;

export const LinkButton = styled.button`
  padding: 8px 0;
  font-weight: 600;
  color: var(--ink-3);
  text-decoration: underline;
  text-underline-offset: 3px;
`;
