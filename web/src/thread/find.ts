// Find in one conversation (Views/FindBar.swift): the daemon finds the
// words in the whole conversation, not just what has been scrolled to, and
// the thread marks them, the shown match more strongly. Opening a search's
// hit lands here too, at its message (ThreadJump).

import { useSyncExternalStore } from "react";
import type { SearchHit } from "@shared/protocol";
import { daemon } from "../daemon";
import { searchable, searchWords } from "../board/search";

export interface Finding {
  query: string;
  /** The query the matches are for, which trails the one typed. */
  searched?: string;
  /** Every message that matched, oldest first. */
  matches: SearchHit[];
  /** The match shown. */
  index?: number;
}

/** Every message in one conversation that has the query's words, oldest first. */
export async function searchThread(sessionId: string, query: string): Promise<SearchHit[]> {
  if (!searchable(query)) return [];
  try {
    const reply = await daemon.request({ type: "search", sessionId, text: query.slice(0, 200) }, 15_000);
    return reply.type === "search" ? reply.search?.hits ?? [] : [];
  } catch {
    return [];
  }
}

/** "2/5", or "0/0" when nothing matched; a dash while the first answer is
 *  on its way. */
export function findCount(f: Finding): string {
  if (f.searched === undefined) return "–";
  if (!f.matches.length) return "0/0";
  return `${f.index !== undefined ? f.index + 1 : "–"}/${f.matches.length}`;
}

export const findMissing = (f: Finding) => f.searched !== undefined && f.matches.length === 0 && searchable(f.query);

// A message to open a conversation at, from a search; new each time, so the
// same one can be gone back to.
export interface Jump {
  sessionId: string;
  messageId: string;
  query: string;
  serial: number;
}
let jump: Jump | null = null;
let serial = 0;
const listeners = new Set<() => void>();

export function setJump(sessionId: string, messageId: string, query: string) {
  jump = { sessionId, messageId, query, serial: ++serial };
  listeners.forEach((l) => l());
}

export function takeJump(sessionId: string): Jump | null {
  if (!jump || jump.sessionId !== sessionId) return null;
  const taken = jump;
  jump = null;
  return taken;
}

export function useJump(): Jump | null {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => jump,
  );
}

/** Marks the words in a thread's messages with the CSS Highlight API, the
 *  shown match's message more strongly; nothing to take down by hand. */
export function markFound(root: HTMLElement | null, f: Finding | null, itemOf: (messageId: string) => string | undefined) {
  const registry = (CSS as unknown as { highlights?: Map<string, unknown> }).highlights;
  const Highlight = (window as unknown as { Highlight?: new (...ranges: Range[]) => unknown }).Highlight;
  if (!registry || !Highlight) return;
  registry.delete("find");
  registry.delete("find-current");
  if (!root || !f?.searched || !f.matches.length) return;
  const words = searchWords(f.searched).map((w) => w.toLowerCase());
  if (!words.length) return;
  const current = f.index !== undefined ? itemOf(f.matches[f.index].messageId) : undefined;
  const all: Range[] = [];
  const strong: Range[] = [];
  const seen = new Set<string>();
  for (const hit of f.matches) {
    const item = itemOf(hit.messageId);
    if (!item || seen.has(item)) continue;
    seen.add(item);
    const el = root.querySelector(`[data-item="${CSS.escape(item)}"]`);
    if (!el) continue;
    // Words of the page's own (a code block's Copy) aren't what was said.
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, {
      acceptNode: (node) => (node.parentElement?.closest("[data-nofind]") ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT),
    });
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const text = (node.textContent ?? "").toLowerCase();
      for (const word of words) {
        for (let at = text.indexOf(word); at >= 0; at = text.indexOf(word, at + word.length)) {
          const range = new Range();
          range.setStart(node, at);
          range.setEnd(node, at + word.length);
          (item === current ? strong : all).push(range);
        }
      }
    }
  }
  registry.set("find", new Highlight(...all));
  registry.set("find-current", new Highlight(...strong));
}
