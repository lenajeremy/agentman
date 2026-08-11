/**
 * Parsing for the payload behind a pairing QR code.
 *
 * The daemon encodes `agentman://pair?relay=<url>&token=<secret>`. A URL rather
 * than bare JSON so the same string doubles as a deep link: tapping it opens
 * the app straight into pairing, which is what makes this usable over a remote
 * shell where pointing a camera at the screen is not an option.
 */
export interface ScannedPairing {
  relayUrl: string;
  token: string;
}

/**
 * Reads a scanned or deep-linked payload.
 *
 * Returns null for anything that is not one of ours, because a camera will
 * happily read every barcode it sees and most of them are not this.
 */
export function parsePairingPayload(raw: string): ScannedPairing | null {
  const text = raw.trim();
  if (!text.toLowerCase().startsWith("agentman://pair")) return null;

  // The URL class rejects unknown schemes on some engines, so read the query
  // directly rather than depending on that.
  const queryStart = text.indexOf("?");
  if (queryStart === -1) return null;

  const params = new URLSearchParams(text.slice(queryStart + 1));
  const relayUrl = (params.get("relay") ?? "").trim();
  const token = (params.get("token") ?? "").trim();
  if (!relayUrl || !token) return null;

  // Only http(s) is meaningful, and refusing anything else keeps a malicious
  // QR from pointing the app at a scheme it was never meant to speak.
  if (!/^https?:\/\//i.test(relayUrl)) return null;

  return { relayUrl, token };
}
