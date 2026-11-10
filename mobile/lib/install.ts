/**
 * This install of the app, named once and kept: what it sends as its device
 * id when it pairs. The relay names a pairing by it, so pairing this phone
 * again is the same device on the Mac, not another; an id per pairing made
 * each re-pairing a new entry. Reinstalling the app starts a new one.
 *
 * Not a secret: a pairing still takes a code from the Mac. It only groups
 * this install's pairings in the Mac's list of devices.
 */

export const INSTALL_ID_KEY = "agentman.installId";

/** The prefix the relay recognises as an install's own id. */
export const INSTALL_PREFIX = "install-";

export interface Storage {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
}

export async function installId(storage: Storage, random: () => string): Promise<string> {
  const kept = await storage.getItem(INSTALL_ID_KEY).catch(() => null);
  if (kept && kept.startsWith(INSTALL_PREFIX) && kept.length > INSTALL_PREFIX.length + 8) return kept;
  const made = INSTALL_PREFIX + random().replace(/[^0-9a-zA-Z]/g, "").toLowerCase();
  await storage.setItem(INSTALL_ID_KEY, made).catch(() => {});
  return made;
}
