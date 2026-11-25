// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { createContext, useContext } from 'react';
import type { IconName } from '../components/atoms/Icon';

// A button of the page's own in the header, beside the X (Wi-Fi's search again).
export interface SubPageAction {
  icon: IconName;
  label: string;
  onPress: () => void;
  disabled?: boolean | undefined;
}

// A settings row's own page: its title in the sheet's header, and what the back
// button does while it is on screen. eyebrow is the page it was opened from, when that
// is a page too; action is a header button of its own.
export interface SubPage {
  title: string;
  back: () => void;
  eyebrow?: string;
  action?: SubPageAction | undefined;
}

// What the settings sheet lends the rows below it: where a row's page is drawn, and how
// it takes over the header. Without a sheet (a component on its own, a test) layer is
// null and a row opens in place instead.
export interface SubPageHost {
  layer: HTMLElement | null;
  show: (page: SubPage | null) => void;
}

export const SubPageContext = createContext<SubPageHost>({
  layer: null,
  show: () => {
    // Without a sheet there is no header to take over.
  },
});

export const useSubPageHost = (): SubPageHost => useContext(SubPageContext);
