// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Icon, type IconName } from '../../atoms/Icon';
import {
  Chevron,
  Desc,
  Glyph as PlainGlyph,
  IconBadge,
  Label,
  Meta,
  Row,
  Switch,
  Text,
} from './styled';

// The icon, label and current value of a settings row; shared by rows that open a page
// (SetItem, Accordion) and switch rows.
// art: a picture on its own instead of an icon tile (a speaker's drawing, as on its card).
function SettingLabel({
  icon,
  label,
  meta,
  desc,
  art,
}: {
  icon?: IconName;
  label: string;
  meta?: string;
  desc?: string;
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
        {desc && <Desc>{desc}</Desc>}
      </Text>
    </>
  );
}

// A settings row that opens a page of its own, with a chevron that says so. `danger` is a
// row that acts instead (a factory reset): red, and no chevron.
export function SetItem({
  icon,
  label,
  meta,
  art,
  danger,
  onClick,
}: {
  icon?: IconName;
  label: string;
  meta?: string;
  art?: ReactNode;
  danger?: boolean;
  onClick: () => void;
}) {
  return (
    <Row type="button" onClick={onClick} $danger={danger}>
      <SettingLabel icon={icon} label={label} meta={meta} art={art} />
      {!danger && (
        <Chevron aria-hidden="true">
          <Icon.chevron width="16" height="16" />
        </Chevron>
      )}
    </Row>
  );
}

// A settings row that is on or off, with an iOS-style switch; the whole row is the target.
// desc: what the switch does, in a line under its label.
export function SetSwitch({
  icon,
  label,
  desc,
  on,
  disabled,
  onToggle,
}: {
  icon?: IconName;
  label: string;
  desc?: string;
  on: boolean;
  disabled?: boolean;
  onToggle: () => void;
}) {
  return (
    <Row type="button" role="switch" aria-checked={on} disabled={disabled} onClick={onToggle}>
      <SettingLabel icon={icon} label={label} desc={desc} />
      <Switch $on={on} aria-hidden="true" />
    </Row>
  );
}
