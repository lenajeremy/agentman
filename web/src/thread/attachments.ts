// What waits in the message box to go with a message (Design/Attachments.swift):
// pictures, shrunk to what the model reads, and other files, which the agent
// is given the path of. At most four. They come from the paperclip, a paste
// or a drop anywhere on the thread, so they are kept per agent here rather
// than in the box.

import { useSyncExternalStore } from "react";
import { readLocalFile } from "./state";

export interface Attachment {
  id: string;
  /** A picture to show: a data: URL. */
  preview: string;
  /** The picture's bytes as sent, base64; "" for a file. */
  data: string;
  mime: string;
  /** For a file that isn't a picture: its path, which the agent is given. */
  file?: string;
}

export const MAX_ATTACHMENTS = 4;
/** The longest edge Claude reads an image at. */
const MAX_EDGE = 1568;
/** What one message's images may come to, in base64. */
const BUDGET = 12_000_000;
/** The largest file sent to an agent, as the daemon keeps them. */
export const MAX_FILE_BYTES = 10 << 20;

const lists = new Map<string, Attachment[]>();
const listeners = new Map<string, Set<() => void>>();
const NONE: Attachment[] = [];

export function attachmentsOf(sessionId: string): Attachment[] {
  return lists.get(sessionId) ?? NONE;
}

export function setAttachments(sessionId: string, next: Attachment[]) {
  if (next.length) lists.set(sessionId, next);
  else lists.delete(sessionId);
  for (const l of listeners.get(sessionId) ?? []) l();
}

export function useAttachments(sessionId: string): Attachment[] {
  return useSyncExternalStore(
    (cb) => {
      if (!listeners.has(sessionId)) listeners.set(sessionId, new Set());
      listeners.get(sessionId)!.add(cb);
      return () => listeners.get(sessionId)?.delete(cb);
    },
    () => attachmentsOf(sessionId),
  );
}

/** Adds what fits of found: four at most. */
export function addAttachments(sessionId: string, found: Attachment[]) {
  const current = attachmentsOf(sessionId);
  const room = Math.max(0, MAX_ATTACHMENTS - current.length);
  if (room && found.length) setAttachments(sessionId, [...current, ...found.slice(0, room)]);
}

function loadImage(src: string): Promise<HTMLImageElement | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => resolve(null);
    img.src = src;
  });
}

/** A picture shrunk to fit, as PNG when that stays small (screenshots keep
 *  their text sharp), as JPEG otherwise. */
async function encode(img: HTMLImageElement, edge = MAX_EDGE, quality = 0.85): Promise<{ data: string; mime: string; preview: string } | null> {
  const scale = Math.min(1, edge / Math.max(img.naturalWidth, img.naturalHeight, 1));
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(img.naturalWidth * scale));
  canvas.height = Math.max(1, Math.round(img.naturalHeight * scale));
  const context = canvas.getContext("2d");
  if (!context) return null;
  context.imageSmoothingQuality = "high";
  context.drawImage(img, 0, 0, canvas.width, canvas.height);
  if (quality >= 0.85) {
    const png = canvas.toDataURL("image/png");
    const data = png.slice(png.indexOf(",") + 1);
    if ((data.length * 3) / 4 <= 700_000) return { data, mime: "image/png", preview: png };
  }
  const jpeg = canvas.toDataURL("image/jpeg", quality);
  return { data: jpeg.slice(jpeg.indexOf(",") + 1), mime: "image/jpeg", preview: jpeg };
}

/** An attachment from a picture's URL (data: or blob:). */
export async function fromImage(src: string): Promise<Attachment | null> {
  const img = await loadImage(src);
  if (!img) return null;
  const made = await encode(img);
  return made ? { id: crypto.randomUUID(), ...made } : null;
}

const IMAGE_EXT = /\.(png|jpe?g|gif|webp|heic|heif|bmp|tiff?)$/i;

/** A file the user picked, by its path: a picture as one, anything else as
 *  the file itself (10 MB at most). */
export async function fromPath(path: string): Promise<Attachment | null> {
  const picture = IMAGE_EXT.test(path);
  const file = await readLocalFile(path, picture ? 64 << 20 : 0);
  if (!file) return null;
  if (picture && file.data) {
    const made = await fromImage(`data:${file.mime};base64,${file.data}`);
    if (made) return made;
  }
  if (file.size > MAX_FILE_BYTES) return null;
  return { id: crypto.randomUUID(), preview: "", data: "", mime: "", file: path };
}

/** The pictures in a paste or a drop, read from their bytes. */
export async function fromFiles(files: FileList | File[]): Promise<Attachment[]> {
  const found: Attachment[] = [];
  for (const file of Array.from(files)) {
    if (!file.type.startsWith("image/")) continue;
    const url = URL.createObjectURL(file);
    const made = await fromImage(url);
    URL.revokeObjectURL(url);
    if (made) found.push(made);
  }
  return found;
}

/** Whether a drag carries something the box takes. */
export function dropAccepts(data: DataTransfer | null): boolean {
  return !!data && Array.from(data.types).some((t) => t === "Files" || t.startsWith("image/"));
}

/** What was dropped on the thread, waiting in the box. */
export async function addDropped(sessionId: string, data: DataTransfer | null) {
  if (!data) return;
  addAttachments(sessionId, await fromFiles(data.files));
}

/** The pictures as the daemon takes them, squeezed further if together they
 *  would be too large for one message. */
export async function encoded(list: Attachment[]): Promise<{ mime: string; data: string }[]> {
  const pictures = list.filter((a) => !a.file);
  let images = pictures.map((a) => ({ mime: a.mime, data: a.data }));
  for (const [edge, quality] of [[1200, 0.6], [1000, 0.5], [800, 0.45], [640, 0.4], [480, 0.35]]) {
    if (images.reduce((n, i) => n + i.data.length, 0) <= BUDGET) break;
    const next: { mime: string; data: string }[] = [];
    for (const a of pictures) {
      const img = await loadImage(a.preview);
      const made = img ? await encode(img, edge, quality) : null;
      if (made) next.push({ mime: made.mime, data: made.data });
    }
    images = next;
  }
  return images;
}
