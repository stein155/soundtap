// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button, Slider, SliderOutput, SliderThumb, SliderTrack } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';
import type { ReactElement } from 'react';

interface Props {
  readonly value: number;
  readonly onChange: (volume: number) => void;
  readonly label: string;
  // Taps on the speaker icon; without it the icon is only a picture.
  readonly onMute?: (() => void) | undefined;
  // Drawn on a dark picture (now playing) instead of a card.
  readonly light?: boolean | undefined;
  // The level as a number at the slider's end, as on now playing.
  readonly showValue?: boolean;
}

// A volume slider with the speaker icon at its start, which mutes and unmutes.
export function VolumeSlider({
  value,
  onChange,
  label,
  onMute,
  light = false,
  showValue = false,
}: Props): ReactElement {
  const { t } = useI18n();
  const muted = value === 0;
  const icon = muted ? <Icon.mute /> : <Icon.volume />;
  return (
    <Slider
      className={`vol${light ? ' on-dark' : ''}`}
      value={value}
      onChange={onChange}
      minValue={0}
      maxValue={100}
      step={2}
      aria-label={label}
    >
      {onMute ? (
        <Button className="vol-ico" aria-label={t('mute')} aria-pressed={muted} onPress={onMute}>
          {icon}
        </Button>
      ) : (
        <span className="vol-ico" aria-hidden="true">
          {icon}
        </span>
      )}
      <SliderTrack className="vtrack">
        {({ state }) => (
          <>
            <div className="vfill" style={{ width: `${state.getThumbPercent(0) * 100}%` }} />
            <SliderThumb className="vthumb" />
          </>
        )}
      </SliderTrack>
      {showValue && <SliderOutput className="vval" />}
    </Slider>
  );
}
