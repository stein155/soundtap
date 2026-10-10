// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { createContext, useContext } from 'react';

// A settings row's own page: its title in the sheet's header, and what the back
// button does while it is on screen.
export interface SubPage {
  title: string;
  back: () => void;
}

// What the settings sheet lends the rows below it: where a row's page is drawn, and how
// it takes over the header. Without a sheet (a component on its own, a test) layer is
// null and a row opens in place instead.
export const SubPageContext = createContext<{
  layer: HTMLElement | null;
  show: (page: SubPage | null) => void;
}>({ layer: null, show: () => {} });

export const useSubPageHost = () => useContext(SubPageContext);
