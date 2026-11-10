// The lightbox's look (Design/ImagePreview.swift:269). A placeholder until
// the thread's pictures are built: PARITY.md §6 "Lightbox".

import { closeLightbox, useLightbox } from "./lightbox";

export function Lightbox() {
  const box = useLightbox();
  if (!box) return null;
  const image = box.images[box.index];
  return (
    <div className="lightbox" onClick={closeLightbox} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.8)", display: "grid", placeItems: "center", zIndex: 100 }}>
      <img src={image.src} alt={image.title} style={{ maxWidth: "90%", maxHeight: "90%" }} />
    </div>
  );
}
