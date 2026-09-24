/**
 * Rules for images on their way to an agent.
 *
 * The arithmetic lives here, apart from the pickers and the network, so it can
 * be tested on Node rather than discovered on a phone.
 */

/** What one message may carry. Matches the daemon's own maxUploadIDs. */
export const MAX_ATTACHMENTS = 4;

/** What the relay accepts for one image. */
export const MAX_UPLOAD_BYTES = 4 * 1024 * 1024;

/**
 * The longest edge an image is sent at.
 *
 * Not a size limit — downscaled screenshots come in far under the relay's cap
 * either way. It is about the wait: uploading five megabytes over cellular to
 * ask a question about a screenshot is a bad trade, and an agent reads nothing
 * at twelve megapixels that it cannot read at two.
 */
export const MAX_DIMENSION = 1600;

/** JPEG quality for the downscale. Text in a screenshot survives this. */
export const JPEG_QUALITY = 0.7;

export interface Size {
  width: number;
  height: number;
}

/**
 * The size an image should be resized to, or null when it already fits.
 *
 * Returns null rather than the original size so a caller can skip the
 * re-encode entirely: running a small screenshot through the manipulator costs
 * time and can only lose detail.
 */
export function fitWithin(size: Size, max = MAX_DIMENSION): Size | null {
  const { width, height } = size;
  // A picker that could not determine the dimensions reports zero. Resizing
  // from an unknown size would be guessing, so leave it alone.
  if (!(width > 0) || !(height > 0)) return null;
  const longest = Math.max(width, height);
  if (longest <= max) return null;

  const scale = max / longest;
  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
  };
}

/**
 * The base64 payload of a data URI, without its prefix.
 *
 * expo-clipboard hands back a whole data URI; the relay wants bytes. Returns
 * "" for anything that is not one, so a malformed clipboard cannot become a
 * request body.
 */
export function base64FromDataUri(uri: string): string {
  const match = /^data:image\/(?:png|jpe?g|gif|webp);base64,([A-Za-z0-9+/]+={0,2})$/.exec(uri);
  return match ? match[1] : "";
}

/** Roughly how many bytes a base64 string decodes to. */
export function decodedLength(base64: string): number {
  if (base64.length === 0) return 0;
  const padding = base64.endsWith("==") ? 2 : base64.endsWith("=") ? 1 : 0;
  return (base64.length * 3) / 4 - padding;
}

/**
 * Whether another image can be added to a message.
 *
 * Both limits are the daemon's and the relay's, restated here so the app can
 * say no before a round trip rather than after one.
 */
export function canAttachMore(current: number): boolean {
  return current < MAX_ATTACHMENTS;
}
