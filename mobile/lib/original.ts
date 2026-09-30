/**
 * Fetching the file behind an image preview.
 *
 * What the viewer shows is a copy the Mac made to fit: at most 2048 pixels on
 * its longest edge, re-encoded, small enough to travel as one message. That is
 * the right thing to look at and the wrong thing to keep. The file itself
 * comes a piece at a time, each asked for after the last arrived, so the phone
 * sets the pace and the whole image never has to sit in memory: a piece goes
 * to disk as it lands.
 *
 * Nothing here touches the network or the filesystem directly. Reading a
 * piece and writing one are both passed in, which is what lets the rules —
 * the ones that decide whether a saved file is the picture or a corruption of
 * it — be tested without a phone.
 */

import type { ImageSource, WorkspaceResult } from "./protocol";

/** Nothing larger is previewed, so nothing larger is offered. */
export const MAX_ORIGINAL_BYTES = 32 * 1024 * 1024;

/** The file changed on the Mac between two pieces. Worth retrying as is. */
export class OriginalChangedError extends Error {
  constructor() {
    super("The image changed on your Mac while it was downloading. Try again.");
    this.name = "OriginalChangedError";
  }
}

/** The length of what a base64 string decodes to, without decoding it. */
export function base64Bytes(data: string): number {
  if (data.length === 0 || data.length % 4 !== 0) return 0;
  const padding = data.endsWith("==") ? 2 : data.endsWith("=") ? 1 : 0;
  return (data.length / 4) * 3 - padding;
}

export interface OriginalFile {
  size: number;
  mime: string;
}

/**
 * Pulls a whole file through `read`, handing each piece to `write` in order.
 *
 * Every piece is checked against the first: same file, same length, starting
 * exactly where the last one ended. An agent can rewrite a screenshot in the
 * middle of a download, and the alternative to noticing is saving the top of
 * one picture on the bottom of another.
 */
export async function fetchOriginal(options: {
  /** Asks the Mac for the piece starting at `offset`. */
  read(offset: number): Promise<WorkspaceResult>;
  /** Appends one piece, still base64. `first` means start the file over. */
  write(data: string, first: boolean): void | Promise<void>;
  onProgress?(received: number, total: number): void;
  /** Checked between pieces, so leaving the screen stops the download. */
  cancelled?(): boolean;
}): Promise<OriginalFile | null> {
  let received = 0;
  let size = 0;
  let version = "";
  let mime = "";

  for (;;) {
    if (options.cancelled?.()) return null;
    const piece = await options.read(received);
    if (options.cancelled?.()) return null;

    const bytes = base64Bytes(piece.data ?? "");
    if (piece.kind !== "chunk" || bytes === 0 || !piece.size || !piece.version || !piece.mime) {
      throw new Error("Your Mac sent something that is not part of the image.");
    }
    if ((piece.offset ?? 0) !== received) {
      throw new Error("Your Mac sent the image out of order.");
    }
    if (received === 0) {
      if (piece.size > MAX_ORIGINAL_BYTES) {
        throw new Error("That image is too large to download.");
      }
      ({ size, version, mime } = { size: piece.size, version: piece.version, mime: piece.mime });
    } else if (piece.size !== size || piece.version !== version) {
      throw new OriginalChangedError();
    }
    // More than was promised is as wrong as a different file: the length is
    // what says when to stop, and a file that outgrew it is not that file.
    if (received + bytes > size) throw new OriginalChangedError();

    await options.write(piece.data as string, received === 0);
    received += bytes;
    options.onProgress?.(received, size);
    if (received === size) return { size, mime };
  }
}

/** "4.2 MB": short enough to sit on a button beside its label. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * What saving would bring, in words: "3024 × 1964 · 4.2 MB".
 *
 * Empty when the Mac is too old to describe the file, because then there is
 * nothing true to say about it.
 */
export function describeSource(source: ImageSource | undefined): string {
  if (!source) return "";
  const parts: string[] = [];
  if (source.width && source.height) parts.push(`${source.width} × ${source.height}`);
  parts.push(formatBytes(source.size));
  return parts.join(" · ");
}

const EXTENSIONS: Record<string, string> = {
  "image/png": "png",
  "image/jpeg": "jpg",
  "image/gif": "gif",
  "image/webp": "webp",
};

/**
 * The name to save an image under: the one it has on the Mac, with an
 * extension that matches the bytes.
 *
 * Those can disagree. A preview of `shot.png` is often a JPEG, and a file
 * saved as `shot.png` holding a JPEG opens in some apps and not others. And
 * the name arrives from a path on another machine, so anything that is not
 * plainly part of a filename is dropped rather than trusted.
 */
export function saveName(path: string, mime: string): string {
  const extension = EXTENSIONS[mime] ?? "png";
  const last = path.split("/").at(-1) ?? "";
  const stem = last
    .replace(/\.[A-Za-z0-9]{1,5}$/, "")
    .replace(/[^A-Za-z0-9 ._()-]/g, "_")
    .replace(/^[ .]+/, "")
    .slice(0, 80)
    .trim();
  return `${stem || "image"}.${extension}`;
}
