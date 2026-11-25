// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, type ReactElement } from 'react';
import { Button } from 'react-aria-components';
import styled from 'styled-components';
import { Icon } from '../../atoms/Icon';

// The bar on a sheet, above the list.
export const EntryRow = styled.div`
  margin: 0 calc(var(--page-gutter) * -1);
`;

type Props = { label: string } & ({ onPress: () => void; takeFocus?: boolean } | { href: string });

// The first row on a service's page: a grey field in a white bar that opens the real
// search, or a row that opens another app.
export function SearchEntry(props: Props): ReactElement {
  const button = useRef<HTMLButtonElement>(null);
  const takeFocus = 'onPress' in props && props.takeFocus === true;
  useEffect(() => {
    if (takeFocus) button.current?.focus();
  }, [takeFocus]);
  if ('href' in props)
    return (
      <a className="search-row" href={props.href} target="_blank" rel="noreferrer">
        <Icon.search aria-hidden="true" />
        <span className="search-text">{props.label}</span>
        <Icon.external className="search-go" aria-hidden="true" />
      </a>
    );
  return (
    <div className="search-bar">
      <Button ref={button} className="search" onPress={props.onPress}>
        <Icon.search aria-hidden="true" />
        <span className="search-text">{props.label}</span>
      </Button>
    </div>
  );
}
