// The image lightbox (Design/ImagePreview.swift's lightbox): every area that
// shows a picture opens it through here, and Overlays draws it.

import { useSyncExternalStore } from "react";

export interface LightboxImage {
  /** Where to load it: a data: URL, or a URL the page can fetch. */
  src: string;
  title: string;
  /** For Copy Image and Show in Finder, when it is a file. */
  path?: string;
}

interface State {
  images: LightboxImage[];
  index: number;
}

let state: State | null = null;
const listeners = new Set<() => void>();

function set(next: State | null) {
  state = next;
  for (const l of listeners) l();
}

/** Opens the lightbox on images[index]; ← and → go through the rest. */
export function openLightbox(images: LightboxImage[], index = 0) {
  if (images.length) set({ images, index: Math.min(Math.max(index, 0), images.length - 1) });
}

export function closeLightbox() {
  set(null);
}

export function showLightboxImage(index: number) {
  if (state) set({ ...state, index });
}

export function useLightbox(): State | null {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => state,
  );
}
