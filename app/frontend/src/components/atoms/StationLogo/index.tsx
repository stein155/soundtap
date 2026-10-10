// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { Image, Letters } from './styled';

interface Props {
  src: string | null;
  // The lettermark shown until (and unless) the image loads.
  initials: string;
  // The lettermark's size relative to its 14px base, for larger artwork squares.
  scale?: number;
}

// A station's logo: the lettermark, with the image laid over it once it has loaded.
// The lettermark hides then, because many logos are transparent and it would show
// through. A broken image leaves the lettermark.
export function StationLogo({ src, initials, scale = 1 }: Props) {
  const [state, setState] = useState<'loading' | 'loaded' | 'error'>('loading');
  const [square, setSquare] = useState(false);
  // A new image starts over.
  const [shownSrc, setShownSrc] = useState(src);
  if (src !== shownSrc) {
    setShownSrc(src);
    setState('loading');
  }
  const showImage = !!src && state !== 'error';
  return (
    <>
      <Letters $covered={showImage && state === 'loaded'} $scale={scale} aria-hidden="true">
        {initials}
      </Letters>
      {showImage && (
        <Image
          $loaded={state === 'loaded'}
          $square={square}
          src={src}
          alt=""
          loading="lazy"
          onLoad={(e) => {
            const { naturalWidth: w, naturalHeight: h } = e.currentTarget;
            setSquare(w > 0 && h > 0 && Math.abs(w / h - 1) < 0.05);
            setState('loaded');
          }}
          onError={() => setState('error')}
        />
      )}
    </>
  );
}
