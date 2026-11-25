// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Icon, type IconName } from '../../atoms/Icon';
import {
  Badge,
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
// strong: the label in the weight of a name (a speaker), with its desc under it.
// disc: the icon on a grey disc, the label bold with its desc as a sentence under it (a
// route to take, as on the Updates page).
function SettingLabel({
  icon,
  label,
  meta,
  desc,
  art,
  strong = false,
  disc = false,
}: {
  icon?: IconName | undefined;
  label: string;
  meta?: string | undefined;
  desc?: string | undefined;
  art?: ReactNode | undefined;
  strong?: boolean | undefined;
  disc?: boolean | undefined;
}) {
  const Glyph = icon && Icon[icon];
  const hasArt = Boolean(art);
  return (
    <>
      {hasArt && <PlainGlyph aria-hidden="true">{art}</PlainGlyph>}
      {Glyph && !hasArt && (
        <IconBadge aria-hidden="true" $disc={disc}>
          <Glyph width="22" height="22" />
        </IconBadge>
      )}
      <Text>
        <Label $strong={strong || disc}>{label}</Label>
        {meta && <Meta>{meta}</Meta>}
        {desc && <Desc $close={strong && !disc}>{desc}</Desc>}
      </Text>
    </>
  );
}

// A settings row that opens a page of its own, with a chevron that says so. `danger` is a
// row that acts instead (a factory reset): red, and no chevron. A speaker's row names it in
// bold with its model under it (desc), and a badge before the chevron says what waits.
export function SetItem({
  icon,
  label,
  meta,
  desc,
  art,
  badge,
  inkBadge,
  danger = false,
  disc = false,
  onClick,
}: {
  icon?: IconName | undefined;
  label: string;
  meta?: string | undefined;
  desc?: string | undefined;
  art?: ReactNode | undefined;
  badge?: string | undefined;
  // The badge in ink instead of amber.
  inkBadge?: boolean;
  danger?: boolean;
  disc?: boolean | undefined;
  onClick: () => void;
}): ReactElement {
  return (
    <Row type="button" onClick={onClick} $danger={danger}>
      <SettingLabel
        icon={icon}
        label={label}
        meta={meta}
        desc={desc}
        art={art}
        strong={!!desc && !disc}
        disc={disc}
      />
      {badge && <Badge $ink={inkBadge}>{badge}</Badge>}
      {!danger && (
        <Chevron aria-hidden="true">
          <Icon.chevron width="18" height="18" />
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
  icon?: IconName | undefined;
  label: string;
  desc?: string | undefined;
  on: boolean;
  disabled?: boolean;
  onToggle: () => void;
}): ReactElement {
  return (
    <Row
      type="button"
      role="switch"
      aria-checked={on}
      disabled={disabled}
      onClick={onToggle}
      $roomy={!!desc}
    >
      <SettingLabel icon={icon} label={label} desc={desc} />
      <Switch $on={on} aria-hidden="true" />
    </Row>
  );
}
