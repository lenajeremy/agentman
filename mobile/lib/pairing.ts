/**
 * Parsing for the payload behind a pairing QR code.
 *
 * The daemon encodes `agentman://pair?relay=<url>&token=<secret>`. A URL rather
 * than bare JSON so the same string doubles as a deep link: tapping it opens
 * the app straight into pairing, which is what makes this usable over a remote
 * shell where pointing a camera at the screen is not an option.
 */
/**
 * The relay a pairing uses when its QR code does not name one.
 *
 * Must match DefaultRelay in the daemon: the daemon omits the address when it
 * is this one, so a mismatch here would send a scanned pairing somewhere the
 * daemon is not.
 */
export const DEFAULT_RELAY = "https://agentman-production.up.railway.app";

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
  const token = (params.get("token") ?? "").trim();
  if (!token) return null;

  // The relay is omitted when it is the default one, which is what keeps the
  // printed QR small enough to sit alongside the prompt. Absent therefore
  // means "the public relay", and a self-hosted one is always spelled out.
  const relayUrl = (params.get("relay") ?? "").trim() || DEFAULT_RELAY;

  // Only http(s) is meaningful, and refusing anything else keeps a malicious
  // QR from pointing the app at a scheme it was never meant to speak.
  if (!/^https?:\/\//i.test(relayUrl)) return null;

  return { relayUrl, token };
}
