// The website's stickers (Design/Sticker.swift): animated WebP, which a
// browser plays as it is, and a still frame under Reduce Motion. On a patch
// of the site's dots fading out from the middle, as the empty screens of
// the New theme show them; on a dark page on a cream disc, since the
// sticker's black outline would vanish on black.

import "../styles/sticker.css";

export type StickerName = "terminal" | "mascot";

export function StickerOnDots({ name, size }: { name: StickerName; size: number }) {
  return (
    <span className="sticker-on-dots" style={{ width: size * 1.14, height: size * 1.14 }} aria-hidden>
      <span className="sticker-dots" />
      <span className="sticker-disc" style={{ width: size * 0.84, height: size * 0.84 }} />
      <picture className="sticker">
        <source srcSet={`/assets/stickers/${name}.png`} media="(prefers-reduced-motion: reduce)" />
        <img src={`/assets/stickers/${name}.webp`} width={size} height={size} alt="" draggable={false} />
      </picture>
    </span>
  );
}
