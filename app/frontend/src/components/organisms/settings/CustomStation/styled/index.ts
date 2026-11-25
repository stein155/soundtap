// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Field, Label } from '../../../../molecules/LabeledField/styled';

// The design draws this one form plainer than the settings pages: labels in the weight of
// a word, white fields with a fine edge.
export const Form = styled.form`
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 0 0;

  ${Field} {
    gap: 6px;
  }

  ${Label} {
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0;
    text-transform: none;
    color: var(--ink);
  }

  input {
    border-radius: var(--radius);
    background: var(--surface);
    padding: 0 14px;
  }

  input:focus {
    box-shadow: inset 0 0 0 1.5px var(--ink);
  }
`;

export const Text = styled.p`
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--ink-3);
`;

export const Small = styled(Text)`
  font-size: 12px;
`;

export const Footer = styled.div`
  display: grid;
  gap: 12px;
  margin-top: 2px;
`;
