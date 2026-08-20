import * as Crypto from "expo-crypto";

/**
 * Account-wide replies are broadcast to every paired device, so correlation
 * identifiers must be unpredictable and globally collision-resistant rather
 * than a timestamp plus a process-local counter.
 */
export function newFrameId(): string {
  return Crypto.randomUUID();
}
