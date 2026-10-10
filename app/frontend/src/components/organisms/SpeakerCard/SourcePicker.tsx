// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { ToggleButton, ToggleButtonGroup } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';
import type { Source } from '../../../lib/rooms';

const SOURCE_ICON = { wifi: Icon.wifi, bluetooth: Icon.bluetooth, tv: Icon.tv } as const;

interface Props {
  sources: Source[];
  current: Source;
  onChange: (source: Source) => void;
}

// Wi-Fi, Bluetooth, AUX or a soundbar's TV and HDMI inputs: shown only when the speaker has more than one.
export function SourcePicker({ sources, current, onChange }: Props) {
  const { t } = useI18n();
  if (sources.length < 2) return null;
  return (
    <div className="line row">
      <span className="lbl">{t('source')}</span>
      <ToggleButtonGroup
        className="srcs"
        aria-label={t('source')}
        selectionMode="single"
        disallowEmptySelection
        selectedKeys={[current]}
        onSelectionChange={(keys) => {
          const [source] = [...keys];
          if (source) onChange(source as Source);
        }}
      >
        {sources.map((source) => {
          const Glyph = source === 'aux' || source === 'hdmi' ? null : SOURCE_ICON[source];
          return (
            <ToggleButton key={source} id={source} className="src" aria-label={t(`src_${source}`)}>
              {Glyph ? <Glyph /> : <b>{source === 'hdmi' ? 'HDMI' : 'AUX'}</b>}
            </ToggleButton>
          );
        })}
      </ToggleButtonGroup>
    </div>
  );
}
