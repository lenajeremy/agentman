// A picture full window (Design/ImagePreview.swift's LightboxView): the
// picture, zoomable, over the page darkened; its name, where it is in the
// run, and a spinner while the next is read; ← and → (and the arrows) go
// through the run, Escape, the close button or a click beside it closes it;
// a right-click copies it. Every area that shows a picture opens it through
// lightbox.ts.

import { useEffect, useState } from "react";
import { ChevronLeft, ChevronRight, X } from "lucide-react";
import { ZoomableImage } from "./editor/ZoomableImage";
import { reveal } from "./host";
import { closeLightbox, showLightboxImage, useLightbox, type LightboxImage } from "./lightbox";
import { useKeyboardBack } from "./palette/PaletteView";
import { copyImage } from "./thread/clipboard";
import { contextMenu } from "./thread/menu";
import { Spinner } from "./thread/Loader";
import { fileManager } from "./workspace/place";
import { Icon } from "./ui";
import "./styles/lightbox.css";

export function Lightbox() {
  const box = useLightbox();
  if (!box) return null;
  return <LightboxPage images={box.images} index={box.index} />;
}

function LightboxPage({ images, index }: { images: LightboxImage[]; index: number }) {
  const count = images.length;
  const image = images[index];
  // The picture showing, and which it is: the last one stays up while the
  // next is read.
  const [loaded, setLoaded] = useState<{ index: number; src: string | null } | null>(null);
  useKeyboardBack();

  useEffect(() => {
    let live = true;
    if (image.src) {
      setLoaded({ index, src: image.src });
      return;
    }
    void (image.load?.() ?? Promise.resolve(null)).then((src) => live && setLoaded({ index, src }));
    return () => {
      live = false;
    };
  }, [image, index]);

  const move = (by: number) => {
    const next = index + by;
    if (next >= 0 && next < count) showLightboxImage(next);
  };

  // Escape, ← and →, wherever the keyboard is, and before anything else
  // takes them (a thread's find, the editor).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeLightbox();
      else if (e.key === "ArrowLeft" && !e.metaKey && !e.altKey) move(-1);
      else if (e.key === "ArrowRight" && !e.metaKey && !e.altKey) move(1);
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });

  const shown = loaded?.index === index;
  const src = loaded?.src;
  const menu = (e: React.MouseEvent) => {
    if (!src) return;
    contextMenu(e, [
      { label: "Copy Image", action: () => void copyImage(src) },
      ...(image.path ? [{ label: `Show in ${fileManager()}`, action: () => void reveal(image.path!) }] : []),
    ]);
  };

  return (
    <div className="lightbox" role="dialog" aria-label={image.title}>
      {/* A click beside the picture closes it; on it, it zooms. */}
      <div
        className={`lightbox-stage${count > 1 ? " run" : ""}`}
        onClick={(e) => (e.target as HTMLElement).tagName !== "IMG" && closeLightbox()}
        onContextMenu={menu}
      >
        {src ? (
          <ZoomableImage src={src} alt={image.title} padding={0} />
        ) : shown ? (
          <div className="lightbox-unreadable">This image can't be read.</div>
        ) : null}
      </div>
      <div className="lightbox-caption">
        <span className="lightbox-title">{image.title}</span>
        {count > 1 && (
          <span className="lightbox-count tabular">
            {index + 1} of {count}
          </span>
        )}
        {!shown && <Spinner size={10} light />}
      </div>
      {count > 1 && (
        <>
          <button className="lightbox-arrow left" title="Previous Image (←)" aria-label="Previous Image" disabled={index === 0} onClick={() => move(-1)}>
            <Icon icon={ChevronLeft} sf="chevron.left" size={15} weight="semibold" />
          </button>
          <button className="lightbox-arrow right" title="Next Image (→)" aria-label="Next Image" disabled={index === count - 1} onClick={() => move(1)}>
            <Icon icon={ChevronRight} sf="chevron.right" size={15} weight="semibold" />
          </button>
        </>
      )}
      <button className="lightbox-close" title="Close (Esc)" aria-label="Close" onClick={closeLightbox}>
        <Icon icon={X} sf="xmark" size={13} weight="semibold" />
      </button>
    </div>
  );
}
