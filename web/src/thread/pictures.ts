// Which reads in a thread were of a picture, and its path
// (ImageThumbnail.imagePath and imageFile, ThreadItem.picture).

import type { Message } from "@shared/protocol";

/** The image file a path names, if it is one (ImageThumbnail.imagePath). */
export function imagePath(text: string): string | null {
  return /\/[^\s"'`]+?\.(?:png|jpe?g|gif|webp|heic|bmp|tiff?)/i.exec(text)?.[0] ?? null;
}

const SHELL_CHARACTERS = /[\t"'`$&|;<>()*?[\]\\]/;
const IMAGE_ENDING = /^.+\.(?:png|jpe?g|gif|webp|heic|bmp|tiff?)$/i;

/**
 * The picture a file tool opened, from its summary, which names one file
 * and nothing else, so a space in it is part of the name: "09 Waitlist.png",
 * or macOS's "Screenshot 2026-10-05 at 10.00.00 AM.png"
 * (ImageThumbnail.imageFile). The whole first line, one absolute path with
 * nothing a shell would act on and no control characters, as the daemon
 * serves it and the phone shows it (lib/imagepath.ts): a summary that is a
 * path but not such a one is no picture, since the daemon would refuse it.
 * One that isn't a path at all is searched as before.
 */
export function imageFileInSummary(summary: string): string | null {
  const first = summary.split("\n", 1)[0].replace(/^[\p{Zs}\t]+|[\p{Zs}\t]+$/gu, "");
  if (!first.startsWith("/")) return imagePath(summary);
  const components = first.split("/").slice(1);
  if (first.length > 4096 || SHELL_CHARACTERS.test(first) || /[\p{Cc}\p{Cf}]/u.test(first) ||
      // As written, not needing cleaning: no "//", "." or "..".
      components.some((c) => c === "" || c === "." || c === "..") ||
      !IMAGE_ENDING.test(first)) return null;
  return first;
}

/** The picture a read opened (ThreadItem.picture): from its summary, which
 *  names what it read. Only a read with no summary is looked for in its
 *  text, since a file's text is what is in it: a README mentioning a
 *  screenshot is still a README read. */
export function pictureRead(message: Message): string | null {
  const summary = message.tool?.summary;
  if (summary !== undefined && summary !== null) return imageFileInSummary(summary);
  return imagePath(message.text ?? "");
}
