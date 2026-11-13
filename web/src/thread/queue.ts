// Messages the daemon queued for an agent that couldn't take them yet (the
// Mac's QueuedMessage): shown until the agent's own copy arrives.

import type { Message } from "@shared/protocol";

/** What was written, and how many images went with it. */
export interface QueuedMessage {
  words: string;
  images: number;
}

/** As the thread shows it: its words, after how many images go with it. */
export function queuedLabel({ words, images }: QueuedMessage): string {
  if (images === 0) return words;
  const note = images === 1 ? "1 image" : `${images} images`;
  return words ? `${note} · ${words}` : note;
}

/** What still waits once these messages arrive: each of the user's takes
 *  the first found by its words, not by its label ("1 image · …" is nowhere
 *  in the agent's copy); images alone, the first. */
export function stillQueued(waiting: readonly QueuedMessage[], arrived: readonly Message[]): QueuedMessage[] {
  const left = [...waiting];
  for (const message of arrived) {
    if (message.role !== "user") continue;
    const text = (message.text ?? "").trim();
    const index = left.findIndex((queued) => {
      const words = queued.words.trim();
      return !words || text.includes(words);
    });
    if (index >= 0) left.splice(index, 1);
  }
  return left;
}
