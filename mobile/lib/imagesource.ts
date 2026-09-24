import * as Clipboard from "expo-clipboard";
import * as ImageManipulator from "expo-image-manipulator";
import * as ImagePicker from "expo-image-picker";

import {
  base64FromDataUri,
  decodedLength,
  fitWithin,
  JPEG_QUALITY,
  MAX_ATTACHMENTS,
  MAX_UPLOAD_BYTES,
  type Size,
} from "./attachments";

/**
 * Getting a picture out of the phone and into a shape the relay will take.
 *
 * Everything here touches a native module, which is why the arithmetic it
 * depends on lives in attachments.ts instead.
 */

export interface PickedImage {
  /** A local uri, for the thumbnail in the composer. */
  uri: string;
  /** The bytes to upload, already downscaled. */
  base64: string;
  width: number;
  height: number;
}

/** Downscale and re-encode, or return the original when it already fits. */
async function prepare(uri: string, size: Size): Promise<PickedImage> {
  const target = fitWithin(size);
  const context = ImageManipulator.ImageManipulator.manipulate(uri);
  if (target) context.resize(target);
  const rendered = await context.renderAsync();
  const result = await rendered.saveAsync({
    format: ImageManipulator.SaveFormat.JPEG,
    compress: JPEG_QUALITY,
    base64: true,
  });
  return {
    uri: result.uri,
    base64: result.base64 ?? "",
    width: result.width,
    height: result.height,
  };
}

/**
 * Open the photo library.
 *
 * Permission is requested here rather than up front: asking on first launch,
 * before anyone has tried to send a picture, is how an app trains people to
 * decline.
 */
export async function pickImages(remaining: number): Promise<PickedImage[]> {
  const permission = await ImagePicker.requestMediaLibraryPermissionsAsync();
  if (!permission.granted) {
    throw new Error("Agentman needs access to your photos to send one.");
  }
  const result = await ImagePicker.launchImageLibraryAsync({
    mediaTypes: ["images"],
    allowsMultipleSelection: remaining > 1,
    selectionLimit: Math.max(1, Math.min(remaining, MAX_ATTACHMENTS)),
    // The picker's own quality is not used: everything goes through the same
    // downscale, so a photo and a pasted screenshot arrive the same size.
    quality: 1,
  });
  if (result.canceled) return [];

  const prepared: PickedImage[] = [];
  for (const asset of result.assets.slice(0, remaining)) {
    prepared.push(await prepare(asset.uri, { width: asset.width, height: asset.height }));
  }
  return prepared.filter(usable);
}

/** Whether the clipboard currently holds an image, for the paste affordance. */
export async function clipboardHasImage(): Promise<boolean> {
  try {
    return await Clipboard.hasImageAsync();
  } catch {
    // A platform without a clipboard image API is not an error worth showing.
    return false;
  }
}

/**
 * Take the image on the clipboard.
 *
 * It arrives as a data URI already, so the payload is unwrapped rather than
 * re-read from disk — but it still goes through the same downscale, because a
 * screenshot copied on a modern phone is three megapixels of mostly flat
 * colour.
 */
export async function pasteImage(): Promise<PickedImage | null> {
  const image = await Clipboard.getImageAsync({ format: "jpeg", jpegQuality: 1 });
  if (!image) return null;
  const raw = base64FromDataUri(image.data);
  if (!raw) return null;
  return prepare(image.data, image.size ?? { width: 0, height: 0 });
}

/**
 * Whether an image is small enough to send.
 *
 * The relay would refuse it anyway; refusing here means the wait happens
 * before the upload rather than after it.
 */
function usable(image: PickedImage): boolean {
  return image.base64.length > 0 && decodedLength(image.base64) <= MAX_UPLOAD_BYTES;
}
