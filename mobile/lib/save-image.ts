/**
 * The half of saving an image that touches the phone: a file to build the
 * download in, and the share sheet to hand it over with.
 *
 * Kept apart from `original.ts` because this imports native modules and that
 * is rules that want testing; neither should drag the other around.
 *
 * The share sheet rather than a save straight to Photos. It offers Save Image
 * first, which is what "download" usually means, and Save to Files and
 * AirDrop beside it — and a mockup or a diagram an agent drew is as likely to
 * be wanted in Files as in a camera roll. It also asks for no access up front:
 * the permission prompt appears only if Save Image is what gets chosen.
 */

import { Directory, File, Paths } from "expo-file-system";
import * as Sharing from "expo-sharing";

/** A folder of its own, so clearing it can never touch anything else. */
const FOLDER = "saved-images";

const UTI: Record<string, string> = {
  "image/png": "public.png",
  "image/jpeg": "public.jpeg",
  "image/gif": "com.compuserve.gif",
  "image/webp": "org.webmproject.webp",
};

/**
 * An empty file in the cache, named as the image will be saved.
 *
 * The cache because nothing here needs to outlive the share sheet: iOS may
 * clear it under pressure, and a download that is never shared should be the
 * first thing to go.
 */
export function scratchFile(name: string): File {
  const folder = new Directory(Paths.cache, FOLDER);
  if (!folder.exists) folder.create({ intermediates: true, idempotent: true });
  const file = new File(folder, name);
  if (file.exists) file.delete();
  return file;
}

/** Appends one base64 piece. `first` starts the file over. */
export function appendPiece(file: File, data: string, first: boolean): void {
  if (first) {
    if (file.exists) file.delete();
    file.create();
  }
  file.write(data, { encoding: "base64", append: true });
}

/** Removes a download, finished or not. Never throws: it is cleanup. */
export function discard(file: File | null): void {
  try {
    if (file?.exists) file.delete();
  } catch {
    // Left for iOS to clear with the rest of the cache.
  }
}

/**
 * Opens the share sheet on a file and resolves once it is dismissed, whatever
 * was chosen. The file is removed afterwards: by then it has been copied to
 * wherever it was sent, and keeping originals in the cache would quietly turn
 * the app into a second copy of every image it has shown.
 */
export async function offerFile(
  file: File,
  mime: string,
  /** Where the sheet points on an iPad, which shows it as a popover. Without
   *  one it opens from the top-left corner, nowhere near the button. */
  anchor?: { x: number; y: number; width: number; height: number },
): Promise<void> {
  try {
    if (!(await Sharing.isAvailableAsync())) {
      throw new Error("Sharing is not available on this device.");
    }
    await Sharing.shareAsync(file.uri, { mimeType: mime, UTI: UTI[mime], anchor });
  } finally {
    discard(file);
  }
}
