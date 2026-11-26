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

type Props = { readonly label: string } & (
  { onPress: () => void; takeFocus?: boolean } | { href: string }
);

// Whether the field takes focus as it opens; a link never does.
const wantsFocus = (props: Props): boolean => 'onPress' in props && props.takeFocus === true;

// The first row on a service's page: a grey field in a white bar that opens the real
// search, or a row that opens another app.
export function SearchEntry(props: Props): ReactElement {
  const button = useRef<HTMLButtonElement>(null);
  const { label } = props;
  const takeFocus = wantsFocus(props);
  useEffect(() => {
    if (takeFocus) button.current?.focus();
  }, [takeFocus]);
  if ('href' in props) {
    const { href } = props;
    return (
      <a className="search-row" href={href} target="_blank" rel="noreferrer">
        <Icon.search aria-hidden="true" />
        <span className="search-text">{label}</span>
        <Icon.external className="search-go" aria-hidden="true" />
      </a>
    );
  }
  const { onPress } = props;
  return (
    <div className="search-bar">
      <Button ref={button} className="search" onPress={onPress}>
        <Icon.search aria-hidden="true" />
        <span className="search-text">{label}</span>
      </Button>
    </div>
  );
}
