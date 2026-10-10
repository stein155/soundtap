// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef } from 'react';
import { Button } from 'react-aria-components';
import styled from 'styled-components';
import { Icon } from '../../atoms/Icon';

// The bar on a sheet, above the list.
export const EntryRow = styled.div`
  margin: 0 calc(var(--page-gutter) * -1);
`;

type Props = { label: string } & ({ onPress: () => void; takeFocus?: boolean } | { href: string });

// The first row on a service's page, as Stockholm's Search Catalog: a tap opens the real
// search, or another app.
export function SearchEntry(props: Props) {
  const button = useRef<HTMLButtonElement>(null);
  const takeFocus = 'onPress' in props && props.takeFocus;
  useEffect(() => {
    if (takeFocus) button.current?.focus();
  }, [takeFocus]);
  const body = (
    <>
      <span className="search-mark">
        <Icon.search />
      </span>
      <span className="search-text">{props.label}</span>
    </>
  );
  if ('href' in props)
    return (
      <a className="search" href={props.href} target="_blank" rel="noreferrer">
        {body}
      </a>
    );
  return (
    <Button ref={button} className="search" onPress={props.onPress}>
      {body}
    </Button>
  );
}
