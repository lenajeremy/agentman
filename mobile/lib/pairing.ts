/**
 * Parsing for the payload behind a pairing QR code.
 *
 * The daemon encodes a compact `agentman://pair/v2/<payload>` link. A URL rather
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

const MAX_RELAY_URL_LENGTH = 2_048;
const MAX_TOKEN_LENGTH = 16_384;

/**
 * Canonical relay base URL and transport policy shared by manual pairing,
 * scanned links, stored credentials, and websocket setup.
 *
 * Bearer credentials must never cross a cleartext network. HTTP remains useful
 * for a developer running the relay on this same device, so only real loopback
 * names and addresses get that exception.
 */
export function normalizeRelayUrl(raw: string): string {
  const trimmed = raw.trim().replace(/\/+$/, "");
  if (!trimmed || trimmed.length > MAX_RELAY_URL_LENGTH) {
    throw new Error("Enter a valid relay address.");
  }

  const withScheme = /^[a-z][a-z\d+.-]*:\/\//i.test(trimmed)
    ? trimmed
    : `https://${trimmed}`;
  let url: URL;
  try {
    url = new URL(withScheme);
  } catch {
    throw new Error("Enter a valid relay address.");
  }

  if (url.protocol === "wss:") url.protocol = "https:";
  if (url.protocol === "ws:") url.protocol = "http:";
  if (url.protocol !== "https:" && url.protocol !== "http:") {
    throw new Error("Relay addresses must use HTTPS.");
  }
  if (url.username || url.password || url.search || url.hash) {
    throw new Error("Relay addresses cannot contain credentials, a query, or a fragment.");
  }
  if (url.protocol === "http:" && !isLoopbackHost(url.hostname)) {
    throw new Error("HTTPS is required unless the relay is running on loopback.");
  }

  const path = url.pathname.replace(/\/+$/, "");
  return `${url.origin}${path === "/" ? "" : path}`;
}

/** Validate the credential shape before anything can connect with it. */
export function normalizeCredentials(
  value: unknown,
): ScannedPairing | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const candidate = value as { relayUrl?: unknown; token?: unknown };
  if (
    typeof candidate.relayUrl !== "string" ||
    typeof candidate.token !== "string" ||
    candidate.token.length === 0 ||
    candidate.token.length > MAX_TOKEN_LENGTH ||
    /[\u0000-\u0020\u007f]/.test(candidate.token)
  ) {
    return null;
  }
  try {
    return {
      relayUrl: normalizeRelayUrl(candidate.relayUrl),
      token: candidate.token,
    };
  } catch {
    return null;
  }
}

function isLoopbackHost(raw: string): boolean {
  const hostname = raw.toLowerCase().replace(/^\[|\]$/g, "");
  if (hostname === "localhost" || hostname.endsWith(".localhost")) return true;
  if (hostname === "::1") return true;
  const octets = hostname.split(".");
  return octets.length === 4 && octets.every((part) => /^\d{1,3}$/.test(part)) &&
    octets.every((part) => Number(part) <= 255) && Number(octets[0]) === 127;
}

/**
 * Reads a scanned or deep-linked payload.
 *
 * Returns null for anything that is not one of ours, because a camera will
 * happily read every barcode it sees and most of them are not this.
 */
export function parsePairingPayload(raw: string): ScannedPairing | null {
  const text = raw.trim();
  const compact = /^agentman:\/\/pair\/v2\/([a-z\d_-]+)$/i.exec(text);
  if (compact) {
    try {
      // Some camera vendors return mixed-case QR payloads inconsistently, so
      // canonicalize before decoding and credential validation.
      const encoded = compact[1].toLowerCase();
      const padded = encoded.replace(/-/g, "+").replace(/_/g, "/")
        .padEnd(Math.ceil(encoded.length / 4) * 4, "=");
      const binary = globalThis.atob(padded);
      const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
      const payload = JSON.parse(new TextDecoder().decode(bytes)) as {
        r?: unknown;
        t?: unknown;
      };
      return normalizeCredentials({
        relayUrl: typeof payload.r === "string" && payload.r ? payload.r : DEFAULT_RELAY,
        token: payload.t,
      });
    } catch {
      return null;
    }
  }
  if (!/^agentman:\/\/pair(?:\?|$)/i.test(text)) return null;

  // The URL class rejects unknown schemes on some engines, so read the query
  // directly rather than depending on that.
  const queryStart = text.indexOf("?");
  if (queryStart === -1) return null;

  const params = new URLSearchParams(text.slice(queryStart + 1));
  const token = params.get("token") ?? "";
  if (
    !token ||
    token.length > MAX_TOKEN_LENGTH ||
    /[\u0000-\u0020\u007f]/.test(token)
  ) return null;

  // The relay is omitted when it is the default one, which is what keeps the
  // printed QR small enough to sit alongside the prompt. Absent therefore
  // means "the public relay", and a self-hosted one is always spelled out.
  const relayUrl = (params.get("relay") ?? "").trim() || DEFAULT_RELAY;
  try {
    return { relayUrl: normalizeRelayUrl(relayUrl), token };
  } catch {
    return null;
  }
}
