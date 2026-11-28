// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// A version on a white card; one on offer is outlined in ink.
export const Card = styled.section<{ $offer?: boolean | undefined }>`
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  padding: 16px;
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: inset 0 0 0
    ${({ $offer }) => ($offer === true ? '2px var(--ink)' : '1px var(--border)')};

  & > button {
    margin-top: 8px;
  }
`;

export const Kicker = styled.p`
  margin: 0;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.5px;
  text-transform: uppercase;
  color: var(--ink-3);
`;

export const Title = styled.p`
  margin: 6px 0 0;
  font-size: 18px;
  font-weight: 600;
  overflow-wrap: anywhere;
`;

export const Meta = styled.p`
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--ink-3);
`;

export const Notes = styled.div`
  margin: 10px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--ink-2);

  p {
    margin: 6px 0 0;
  }
`;

// A heading inside the notes ("Changes", "Fixes").
export const NotesHeading = styled.h4`
  margin: 12px 0 2px;
  font-size: 13px;
  font-weight: 700;
  color: var(--ink);
`;

export const NotesList = styled.ul`
  margin: 2px 0 0;
  padding-left: 18px;

  li + li {
    margin-top: 2px;
  }
`;

// The card's buttons and what they report, full width under the rest.
export const Actions = styled.div`
  align-self: stretch;
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 14px;

  /* The space above is the row's own; a wide button adds none. */
  & > button:first-child {
    margin-top: 0;
  }
`;
