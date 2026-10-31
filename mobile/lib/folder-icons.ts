import AsyncStorage from "@react-native-async-storage/async-storage";
import * as ImageManipulator from "expo-image-manipulator";

/**
 * A folder's logo, beside its agents in place of its initials. It is
 * cosmetic, so each one crosses the relay once: fetched the first time it is
 * shown, shrunk to badge size, and kept on the phone for good. A different
 * file (the Mac found a new logo) is a different key, fetched once in turn.
 */

/** Pictures are kept this many pixels across: the header's 38pt tile on a
 *  3x screen. */
const KEPT_SIZE = 128;
const STORE_PREFIX = "agentman.folderIcon.v2:";

export function folderIconKey(cwd: string, path: string): string {
  return `${STORE_PREFIX}${cwd}\u0000${path}`;
}

const memory = new Map<string, string>();
const loading = new Map<string, Promise<string | null>>();

/** A logo already in memory, so a badge can draw it on its first frame. */
export function folderIconNow(key: string): string | null {
  return memory.get(key) ?? null;
}

/**
 * The logo as a data URI, or null when it cannot be had. Kept: one that was
 * shrunk and saved. Not kept: a failure, which may only mean the Mac was out
 * of reach, so the next launch tries again.
 */
export function loadFolderIcon(
  key: string,
  read: () => Promise<{ image?: string; mime?: string }>,
): Promise<string | null> {
  const known = memory.get(key);
  if (known) return Promise.resolve(known);
  const running = loading.get(key);
  if (running) return running;
  const task = (async () => {
    try {
      const stored = await AsyncStorage.getItem(key);
      if (stored) {
        memory.set(key, stored);
        return stored;
      }
    } catch {
      // Storage unavailable: fetch it, and keep it for this run only.
    }
    let picture: string | null = null;
    try {
      const result = await read();
      if (result.image && result.mime) picture = `data:${result.mime};base64,${result.image}`;
    } catch {
      return null;
    }
    if (!picture) return null;
    const small = await shrink(picture);
    memory.set(key, small ?? picture);
    if (small) {
      try {
        await AsyncStorage.setItem(key, small);
      } catch {
        // Kept in memory; fetched again next launch.
      }
    }
    return small ?? picture;
  })().finally(() => loading.delete(key));
  loading.set(key, task);
  return task;
}

async function shrink(uri: string): Promise<string | null> {
  try {
    const context = ImageManipulator.ImageManipulator.manipulate(uri);
    context.resize({ width: KEPT_SIZE, height: KEPT_SIZE });
    const rendered = await context.renderAsync();
    const saved = await rendered.saveAsync({ format: ImageManipulator.SaveFormat.PNG, base64: true });
    return saved.base64 ? `data:image/png;base64,${saved.base64}` : null;
  } catch {
    return null;
  }
}
