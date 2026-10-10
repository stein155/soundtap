// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button, Slider, SliderThumb, SliderTrack } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';

interface Props {
  value: number;
  onChange: (volume: number) => void;
  label: string;
  // Taps on the speaker icon; without it the icon is only a picture.
  onMute?: () => void;
  // Drawn on a dark picture (now playing) instead of a card.
  light?: boolean;
}

// A volume slider with the speaker icon at its start, which mutes and unmutes.
export function VolumeSlider({ value, onChange, label, onMute, light }: Props) {
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
    </Slider>
  );
}
