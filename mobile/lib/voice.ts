/**
 * Voice prompts. The phone records 16 kHz mono WAV, as Celery takes it, and
 * hands it to the Mac, whose daemon has Celery write it out on the user's
 * own sign-in. The relay carries at most 128 KiB a request from the phone,
 * so the recording goes in slices of its base64, which the Mac joins.
 */

import type { DaemonEvent, Request } from "./protocol";

/** Base64 per part: comfortably under the relay's limit with the envelope. */
export const VOICE_PART = 90_000;

/** The recording's base64 in parts, in order. */
export function voiceParts(base64: string, size = VOICE_PART): string[] {
  if (base64 === "") return [];
  const parts: string[] = [];
  for (let at = 0; at < base64.length; at += size) parts.push(base64.slice(at, at + size));
  return parts;
}

type Ask = (request: Request, timeoutMs?: number) => Promise<DaemonEvent>;

/**
 * Sends a recording to the Mac and returns what was said. Throws with the
 * Mac's own words when it can't: not signed in to Celery, nothing heard.
 */
export async function transcribe(ask: Ask, base64: string, id: string): Promise<string> {
  const parts = voiceParts(base64);
  if (parts.length === 0) throw new Error("Nothing was recorded.");
  for (let part = 0; part < parts.length; part++) {
    const last = part === parts.length - 1;
    // The last part waits for Celery to write it out.
    const reply = await ask(
      { type: "transcribe", voice: { id, part, parts: parts.length, mime: "audio/wav", data: parts[part] } },
      last ? 150_000 : 30_000,
    );
    if (reply.transcript?.done) return reply.transcript.text ?? "";
  }
  throw new Error("The recording didn't come back written out. Try again.");
}

/** What was said, added to what is already in the box. */
export function addSpoken(draft: string, spoken: string): string {
  const trimmed = draft.trim();
  return trimmed === "" ? spoken : `${trimmed} ${spoken}`;
}
