// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement, ReactNode } from 'react';
import { Notes, NotesHeading, NotesList } from './styled';

// A release's notes as GitHub keeps them, drawn without Markdown syntax. On the speaker only the
// part from "What changed" on counts: the download steps above it are for the release page. A
// commit's "type(scope): " prefix is left out and its first letter raised. No HTML is built from
// the text: every piece becomes a React text node.
const changedFrom = (md: string): string => {
  const at = md.search(/^#{2,3}\s+What changed\s*$/mu);
  return at < 0 ? md : md.slice(at).replace(/^#{2,3}\s+What changed\s*\n/u, '');
};

const commitPrefix = /^[a-z]+(?:\([^)]*\))?!?:\s+/u;

const plainItem = (s: string): string => {
  const t = s.replace(commitPrefix, '');
  return t.charAt(0).toUpperCase() + t.slice(1);
};

// **bold**, `code` and [text](link) inside a line; the link shows as its text.
function inline(s: string): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /\*\*(?<bold>[^*]+)\*\*|`(?<code>[^`]+)`|\[(?<link>[^\]]+)\]\([^)]*\)/gu;
  let last = 0;
  for (let m = re.exec(s); m; m = re.exec(s)) {
    if (m.index > last) out.push(s.slice(last, m.index));
    const { bold, code, link } = m.groups ?? {};
    if (bold) out.push(<strong key={m.index}>{bold}</strong>);
    else if (code) out.push(<code key={m.index}>{code}</code>);
    else out.push(link);
    last = re.lastIndex;
  }
  if (last < s.length) out.push(s.slice(last));
  return out;
}

export function ReleaseNotes({
  id,
  md,
}: {
  readonly id: string;
  readonly md: string;
}): ReactElement {
  const blocks: ReactElement[] = [];
  let items: string[] = [];
  let para: string[] = [];
  const flush = (): void => {
    if (items.length > 0) {
      blocks.push(
        <NotesList key={`l${blocks.length}`}>
          {items.map((it) => (
            <li key={it}>{inline(plainItem(it))}</li>
          ))}
        </NotesList>
      );
      items = [];
    }
    if (para.length > 0) {
      blocks.push(<p key={`p${blocks.length}`}>{inline(para.join(' '))}</p>);
      para = [];
    }
  };
  for (const raw of changedFrom(md).split('\n')) {
    const line = raw.trim();
    const heading = (/^#{1,6}\s+(?<t>.*)$/u.exec(line) ?? /^\*\*(?<t>[^*]+)\*\*$/u.exec(line))
      ?.groups?.['t'];
    const item = (/^[-*]\s+(?<t>.*)$/u.exec(line) ?? /^\d+\.\s+(?<t>.*)$/u.exec(line))?.groups?.[
      't'
    ];
    if (line === '') flush();
    else if (heading !== undefined) {
      flush();
      blocks.push(<NotesHeading key={`h${blocks.length}`}>{heading}</NotesHeading>);
    } else if (item === undefined) {
      if (items.length > 0) flush();
      para.push(line);
    } else {
      if (para.length > 0) flush();
      items.push(item);
    }
  }
  flush();
  return <Notes id={id}>{blocks}</Notes>;
}
