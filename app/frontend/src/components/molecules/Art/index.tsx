// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { Icon } from '../../atoms/Icon';
import { artColor } from '../../../lib/artColor';
import { initials } from '../../../lib/artwork';
import type { Artwork } from '../../../lib/playback';
import { resetThemeColor, setThemeColor, washColor } from '../../../lib/themeColor';

// A square of artwork: the picture when there is one and it loads, otherwise the name's
// initials on a plain tile. A square picture fills the box; a logo of another shape
// sits on a light plate so a transparent one stays readable. A picture that failed is
// tried again after ART_RETRY_MS, so a network blip does not leave initials for as long
// as the same station plays.
const ART_RETRY_MS = 30_000;

export function Art({
  src,
  name,
  className = '',
}: {
  src?: string | null | undefined;
  name: string;
  className?: string;
}): ReactElement {
  const [square, setSquare] = useState(true);
  const [failed, setFailed] = useState<string | null>(null);
  useEffect(() => {
    if (!failed) return;
    const retry = setTimeout(() => setFailed(null), ART_RETRY_MS);
    return () => clearTimeout(retry);
  }, [failed]);
  if (!src || failed === src) {
    return (
      <div className={`art art-empty ${className}`} aria-hidden="true">
        <span className="art-letters">{initials(name)}</span>
      </div>
    );
  }
  return (
    <div className={`art ${square ? 'art-cover' : 'art-plate'} ${className}`} aria-hidden="true">
      <img
        src={src}
        alt=""
        draggable={false}
        onLoad={(e) => {
          const { naturalWidth: w, naturalHeight: h } = e.currentTarget;
          setSquare(w > 0 && h > 0 && Math.abs(w / h - 1) < 0.05);
        }}
        onError={() => setFailed(src)}
      />
    </div>
  );
}

// The artwork blown up and blurred behind a bar or a sheet, for the colour of what plays.
export function Blur({ src }: { src?: string | null }): ReactElement {
  return (
    <div className="blur" aria-hidden="true">
      {src && <img src={src} alt="" />}
    </div>
  );
}

// The now-playing screen's backdrop as in Stockholm: the blurred artwork, darkened and
// washed with its own main colour. The wash stays black until that colour is known.
export function WashedBlur({ src }: { src?: string | null }): ReactElement {
  const [wash, setWash] = useState('0 0 0');
  useEffect(() => {
    let stale = false;
    if (src)
      void artColor(src).then((rgb) => {
        if (!stale) setWash(rgb ? rgb.join(' ') : '0 0 0');
      });
    return () => {
      stale = true;
    };
  }, [src]);
  // The phone's own bar above takes the colour at the top of this backdrop while it shows.
  useEffect(() => setThemeColor(washColor(wash)), [wash]);
  useEffect(() => resetThemeColor, []);
  return (
    <div className="blur" aria-hidden="true" style={{ '--wash': wash }}>
      {src && <img src={src} alt="" />}
    </div>
  );
}

// The artwork of what a room plays: a station's logo, or a glyph for an input. `name` is
// what plays, for the initials of a logo that does not load.
// The glyph for a source that has no picture of its own.
const SOURCE_GLYPH = {
  bluetooth: Icon.bluetooth,
  tv: Icon.tv,
  aux: Icon.volume,
  speaker: Icon.volume,
};

export function PlayArt({
  artwork,
  name,
  className = '',
}: {
  artwork: Artwork;
  name: string;
  className?: string;
}): ReactElement {
  if (artwork.kind === 'station')
    return <Art src={artwork.src} name={name} className={className} />;
  const Glyph = SOURCE_GLYPH[artwork.kind];
  return (
    <div className={`art art-empty ${className}`} aria-hidden="true">
      <Glyph />
    </div>
  );
}
