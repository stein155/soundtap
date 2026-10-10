// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Icon, type IconName } from '../../atoms/Icon';
import { Glyph as PlainGlyph, IconBadge, Label, Meta, Row, Switch, Text } from './styled';

// The icon, label and current value of a settings row; shared by rows that open a page
// (SetItem, Accordion) and switch rows.
// art: a picture on its own instead of an icon tile (a speaker's drawing, as on its card).
function SettingLabel({
  icon,
  label,
  meta,
  art,
}: {
  icon?: IconName;
  label: string;
  meta?: string;
  art?: ReactNode;
}) {
  const Glyph = icon && Icon[icon];
  return (
    <>
      {art && <PlainGlyph aria-hidden="true">{art}</PlainGlyph>}
      {Glyph && !art && (
        <IconBadge aria-hidden="true">
          <Glyph width="18" height="18" />
        </IconBadge>
      )}
      <Text>
        <Label>{label}</Label>
        {meta && <Meta>{meta}</Meta>}
      </Text>
    </>
  );
}

// A settings row that opens a page of its own.
export function SetItem({
  icon,
  label,
  meta,
  art,
  onClick,
}: {
  icon?: IconName;
  label: string;
  meta?: string;
  art?: ReactNode;
  onClick: () => void;
}) {
  return (
    <Row type="button" onClick={onClick}>
      <SettingLabel icon={icon} label={label} meta={meta} art={art} />
    </Row>
  );
}

// A settings row that is on or off, with an iOS-style switch; the whole row is the target.
export function SetSwitch({
  icon,
  label,
  on,
  onToggle,
}: {
  icon?: IconName;
  label: string;
  on: boolean;
  onToggle: () => void;
}) {
  return (
    <Row type="button" role="switch" aria-checked={on} onClick={onToggle}>
      <SettingLabel icon={icon} label={label} />
      <Switch $on={on} aria-hidden="true" />
    </Row>
  );
}
