/** Credentials needed to isolate device-local drafts between paired daemons. */
export interface DraftCredentials {
  relayUrl: string;
  token: string;
}

export interface DraftStorage {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
}

const DRAFT_PREFIX = "agentman.draft.v2.";

/**
 * Stable, non-secret namespace for one relay account.
 *
 * Device tokens rotate when the phone pairs again, but their signed claims
 * carry the same account id. Reading that id is not an authentication decision
 * — the relay still verifies the token — it merely keeps the same Mac's drafts
 * together without putting the bearer itself into an AsyncStorage key.
 */
export function draftNamespace(credentials: DraftCredentials): string {
  const account = accountClaim(credentials.token);
  const identity = `${credentials.relayUrl}\u0000${account ?? `token:${credentials.token}`}`;
  return stableFingerprint(identity);
}

export function draftStorageKey(namespace: string, sessionId: string): string {
  return `${DRAFT_PREFIX}${namespace}.${encodeURIComponent(sessionId)}`;
}

/** Whitespace-only composers are intentional clears, not drafts. */
export function persistedDraftValue(text: string): string | null {
  return text.trim() === "" ? null : text;
}

/** Small injectable boundary so persistence behavior is testable without RN. */
export function createDraftPersistence(storage: DraftStorage) {
  return {
    async load(namespace: string, sessionId: string): Promise<string> {
      try {
        return (await storage.getItem(draftStorageKey(namespace, sessionId))) ?? "";
      } catch {
        return "";
      }
    },

    async save(namespace: string, sessionId: string, text: string): Promise<void> {
      try {
        const value = persistedDraftValue(text);
        const key = draftStorageKey(namespace, sessionId);
        if (value === null) {
          await storage.removeItem(key);
        } else {
          await storage.setItem(key, value);
        }
      } catch {
        // A draft persistence failure must never make the composer unusable.
      }
    },

    async clear(namespace: string, sessionId: string): Promise<void> {
      try {
        await storage.removeItem(draftStorageKey(namespace, sessionId));
      } catch {
        // Ignored for the same reason.
      }
    },
  };
}

function accountClaim(token: string): string | null {
  const encoded = token.split(".", 1)[0];
  if (!encoded) return null;
  try {
    const parsed = JSON.parse(decodeBase64Url(encoded)) as { acc?: unknown };
    return typeof parsed.acc === "string" && /^[a-zA-Z0-9_-]{1,128}$/.test(parsed.acc)
      ? parsed.acc
      : null;
  } catch {
    return null;
  }
}

function decodeBase64Url(input: string): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  let bits = 0;
  let buffer = 0;
  let output = "";
  for (const character of input) {
    const value = alphabet.indexOf(character);
    if (value < 0) throw new Error("invalid base64url");
    buffer = (buffer << 6) | value;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      output += String.fromCharCode((buffer >>> bits) & 0xff);
    }
  }
  return output;
}

/** Two independent 32-bit FNV streams give local keys a compact 64-bit id. */
function stableFingerprint(value: string): string {
  let first = 0x811c9dc5;
  let second = 0x9e3779b9;
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    first = Math.imul(first ^ code, 0x01000193);
    second = Math.imul(second ^ code, 0x85ebca6b);
  }
  return `${(first >>> 0).toString(16).padStart(8, "0")}${
    (second >>> 0).toString(16).padStart(8, "0")
  }`;
}
