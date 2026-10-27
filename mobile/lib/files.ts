/**
 * Files sent to an agent from the phone, other than pictures: a PDF, a log, a
 * spreadsheet. The relay's upload locker takes pictures only, so a file goes
 * to the Mac in slices of its base64 over the connection the phone already
 * has, as a voice recording does; the Mac keeps it in Agentman's own folder,
 * and the message names it as @path, which the agent reads. Claude's own app
 * hands files to Claude Code the same way.
 */

import type { DaemonEvent, Request } from "./protocol";

type Ask = (request: Request, timeoutMs?: number) => Promise<DaemonEvent>;

/** Base64 per part: comfortably under the relay's limit with the envelope. */
export const FILE_PART = 90_000;

/** The largest file the Mac keeps. */
export const MAX_FILE_BYTES = 10 * 1024 * 1024;

/** Sends a file to the Mac and resolves with where it now is there. */
export async function sendFile(ask: Ask, sessionId: string, id: string, name: string, base64: string): Promise<string> {
  if (base64 === "") throw new Error(`${name} is empty.`);
  const parts = Math.ceil(base64.length / FILE_PART);
  for (let part = 0; part < parts; part++) {
    const reply = await ask(
      { type: "attach_file", sessionId, file: { id, part, parts, name, data: base64.slice(part * FILE_PART, (part + 1) * FILE_PART) } },
      part === parts - 1 ? 60_000 : 30_000,
    );
    if (reply.attached?.done) {
      if (!reply.attached.path) break;
      return reply.attached.path;
    }
  }
  throw new Error(`${name} didn't reach your Mac. Try again.`);
}

/** The message as sent: the files named first, then the words. */
export function withFiles(text: string, paths: readonly string[]): string {
  if (paths.length === 0) return text;
  const refs = paths.map((path) => `@${path}`).join(" ");
  return text ? `${refs}\n\n${text}` : refs;
}

/** How big a file is, for a chip: "2.4 MB". */
export function fileSize(bytes: number | undefined): string {
  if (bytes === undefined) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
