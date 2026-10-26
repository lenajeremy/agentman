import type { ManagerCard, ManagerEntry, Question } from "./protocol";

/**
 * The conversation with Agentman, as the daemon tells it: entries in order,
 * a reply streaming in as a placeholder filled by deltas, and cards that come
 * again when they are settled. Kept free of React so the merging rules can be
 * tested on Node; the Mac follows the same ones.
 */

/** Adds an entry, or replaces the one with its id. A finished reply replaces
 *  its placeholder whole; a streaming one never empties text already shown. */
export function upsertEntry(entries: readonly ManagerEntry[], entry: ManagerEntry): ManagerEntry[] {
  const index = entries.findIndex((existing) => existing.id === entry.id);
  if (index === -1) return [...entries, entry];
  if (entry.streaming && entries[index].text) return entries as ManagerEntry[];
  const next = entries.slice();
  next[index] = entry;
  return next;
}

/** More text for a reply still being written. */
export function applyDelta(entries: readonly ManagerEntry[], id: string, delta: string): ManagerEntry[] {
  const index = entries.findIndex((existing) => existing.id === id);
  if (index === -1) return entries as ManagerEntry[];
  const next = entries.slice();
  next[index] = { ...next[index], text: next[index].text + delta };
  return next;
}

/** An older page joined in front of what is already here, without repeats. */
export function prependOlder(entries: readonly ManagerEntry[], older: readonly ManagerEntry[]): ManagerEntry[] {
  const known = new Set(entries.map((entry) => entry.id));
  return [...older.filter((entry) => !known.has(entry.id)), ...entries];
}

export const cardIsOpen = (card?: ManagerCard): boolean => card?.state === "open";
/** Settled or put away: the conversation no longer shows it. */
export const cardIsGone = (card?: ManagerCard): boolean => card?.state === "settled" || card?.state === "dismissed";

/** Something worth telling the user: a finished reply, news from an agent,
 *  or a card waiting on them. */
export function isNews(entry: ManagerEntry): boolean {
  switch (entry.role) {
    case "assistant": return !entry.streaming && entry.text.length > 0;
    case "brief":
    case "agent": return true;
    case "card": return cardIsOpen(entry.card);
    default: return false;
  }
}

/** What arrived since the user last looked. */
export function unreadCount(entries: readonly ManagerEntry[], lastRead: number): number {
  return entries.filter((entry) => entry.ts > lastRead && !entry.closed && isNews(entry)).length;
}

export function openCards(entries: readonly ManagerEntry[]): ManagerCard[] {
  return entries.flatMap((entry) => (entry.card && cardIsOpen(entry.card) ? [entry.card] : []));
}

/** The choices on a card: the agent's question, or whether to give a key. */
export function cardQuestion(card: ManagerCard): Question | null {
  if (card.kind === "question" && card.questionId) {
    return { id: card.questionId, prompt: "", options: card.options ?? [], multiple: card.multiple, custom: card.custom };
  }
  if (card.kind === "key") return { id: card.id, prompt: "", options: card.options ?? [] };
  return null;
}

/** What the conversation shows: entries, with Agentman's runs of activity
 *  folded into one card, and images it looked at shown on their own. */
export type ChatItem =
  | { kind: "entry"; key: string; entry: ManagerEntry }
  | { kind: "activities"; key: string; entries: ManagerEntry[] }
  | { kind: "image"; key: string; entry: ManagerEntry };

export function chatItems(entries: readonly ManagerEntry[]): ChatItem[] {
  const items: ChatItem[] = [];
  let run: ManagerEntry[] = [];
  const flush = () => {
    if (run.length > 0) items.push({ kind: "activities", key: `acts-${run[0].id}`, entries: run });
    run = [];
  };
  for (const entry of entries) {
    // A card answered in the agent itself, or put away, is gone, as is
    // anything the user closed.
    if (cardIsGone(entry.card) || entry.closed) continue;
    if (entry.role === "activity") {
      if (entry.path && entry.sessionId) {
        flush();
        items.push({ kind: "image", key: entry.id, entry });
      } else {
        run.push(entry);
      }
      continue;
    }
    flush();
    items.push({ kind: "entry", key: entry.id, entry });
  }
  flush();
  return items;
}
