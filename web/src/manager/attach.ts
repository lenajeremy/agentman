// Images for Agentman (Design/Attachments.swift): at most four, each no
// larger than the model reads them, all within one message to am serve.
// Pasted and dropped images are read by the page itself; picked ones
// through the shell (menus.go's Files.Read), as a page can't read a file
// by its path.

import { Call } from "@wailsio/runtime";
import { pickFiles } from "../host";
import type { InlineImage } from "./store";

export interface Attachment {
  id: string;
  /** What it shows in the box: a data: URL. */
  src: string;
  mime: string;
  /** The image as sent, base64. */
  data: string;
  /** The picture itself, for squeezing several to fit. */
  bitmap: ImageBitmap;
}

export const MAX_COUNT = 4;
/** The longest edge Claude reads an image at. */
const MAX_EDGE = 1568;
/** What one message's images may come to, in base64. */
const BUDGET = 12_000_000;
/** The largest picked file read. */
const MAX_FILE = 40 << 20;

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

function blobBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

function encode(bitmap: ImageBitmap, edge: number, quality: number): Promise<{ mime: string; blob: Blob } | null> {
  const scale = Math.min(1, edge / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(bitmap.width * scale));
  canvas.height = Math.max(1, Math.round(bitmap.height * scale));
  const ctx = canvas.getContext("2d");
  if (!ctx) return Promise.resolve(null);
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  const blob = (type: string, q?: number) => new Promise<Blob | null>((r) => canvas.toBlob(r, type, q));
  return (async () => {
    // As PNG when that stays small (screenshots keep their text sharp), as
    // JPEG otherwise.
    if (quality >= 0.85) {
      const png = await blob("image/png");
      if (png && png.size <= 700_000) return { mime: "image/png", blob: png };
    }
    const jpeg = await blob("image/jpeg", quality);
    return jpeg ? { mime: "image/jpeg", blob: jpeg } : null;
  })();
}

/** An attachment from a picture, shrunk to fit. */
export async function fromBlob(blob: Blob): Promise<Attachment | null> {
  try {
    const bitmap = await createImageBitmap(blob);
    const made = await encode(bitmap, MAX_EDGE, 0.85);
    if (!made) return null;
    const data = await blobBase64(made.blob);
    return { id: crypto.randomUUID(), src: `data:${made.mime};base64,${data}`, mime: made.mime, data, bitmap };
  } catch {
    return null;
  }
}

/** The images among pasted or dropped files. */
export async function fromFiles(files: FileList | File[]): Promise<Attachment[]> {
  const found: Attachment[] = [];
  for (const file of Array.from(files)) {
    if (!file.type.startsWith("image/")) continue;
    const made = await fromBlob(file);
    if (made) found.push(made);
  }
  return found;
}

interface PickedFile {
  mime: string;
  data: string;
}

/** Images from the system's picker. */
export async function pick(): Promise<Attachment[]> {
  return fromPaths(await pickFiles("Choose images for Agentman"));
}

/** Pictures in files, by their paths, read through the shell. */
export async function fromPaths(paths: string[]): Promise<Attachment[]> {
  const found: Attachment[] = [];
  for (const path of paths) {
    if (!inShell) break;
    try {
      const file = (await Call.ByName("main.Files.Read", path, MAX_FILE)) as PickedFile;
      if (!file.data || !file.mime.startsWith("image/")) continue;
      const bytes = Uint8Array.from(atob(file.data), (c) => c.charCodeAt(0));
      const made = await fromBlob(new Blob([bytes], { type: file.mime }));
      if (made) found.push(made);
    } catch {
      // Not a picture this can read.
    }
  }
  return found;
}

/** The images as the daemon takes them, squeezed further if together they
 *  would be too large for one message. */
export async function encoded(attachments: Attachment[]): Promise<InlineImage[]> {
  let images = attachments.map((a) => ({ mime: a.mime, data: a.data }));
  for (const [edge, quality] of [[1200, 0.6], [1000, 0.5], [800, 0.45], [640, 0.4], [480, 0.35]]) {
    if (images.reduce((n, i) => n + i.data.length, 0) <= BUDGET) break;
    const next: InlineImage[] = [];
    for (const a of attachments) {
      const made = await encode(a.bitmap, edge, quality);
      if (made) next.push({ mime: made.mime, data: await blobBase64(made.blob) });
    }
    images = next;
  }
  return images;
}

/** Whether a drag carries files or images, without reading them. */
export function dragHoldsImages(e: DragEvent | React.DragEvent): boolean {
  const types = Array.from(e.dataTransfer?.types ?? []);
  return types.includes("Files");
}
