// Searching agents and what was said (Stores/SearchStore.swift,
// Stores/BoardSearch.swift): by name, folder or branch, and the daemon's
// index of every conversation, running or ended.

import { useEffect, useState } from "react";
import type { SearchHit, SearchResults, Session } from "@shared/protocol";
import { daemon } from "../daemon";
import { openAgent } from "../nav";
import { ask, getShell, setShell } from "../shell/state";
import { setJump } from "../thread/find";

/** Worth asking the daemon: a word of three letters at least. */
export function searchable(query: string): boolean {
  return query.split(/\s+/).some((w) => w.length >= 3);
}

/** The best message of each conversation that has the query's words, once
 *  typing has paused. */
export function useMessageHits(query: string, limit = 50): SearchResults | null {
  const [hits, setHits] = useState<SearchResults | null>(null);
  useEffect(() => {
    const q = query.trim();
    if (!searchable(q)) {
      setHits(null);
      return;
    }
    let current = true;
    const timer = window.setTimeout(() => {
      daemon.request({ type: "search", text: q.slice(0, 200), limit }, 15_000)
        .then((reply) => current && reply.type === "search" && reply.search && setHits(reply.search))
        .catch(() => {});
    }, 150);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [query, limit]);
  return hits;
}

export interface BoardSearch {
  query: string;
  ids: Set<string>;
  /** What was said, for the agents found that way or both ways. */
  bySession: Record<string, SearchHit>;
  /** Conversations that ended and said it: not on the Board, but asked for. */
  past: SearchHit[];
  searching: boolean;
  indexing: boolean;
}

/** The sidebar's search, for the Board; nothing when nothing is searched for. */
export function boardSearch(query: string, sessions: Session[], branches: Record<string, string>, folder: string | null,
  hits: SearchResults | null): BoardSearch | null {
  const q = query.trim();
  if (!q) return null;
  const lower = q.toLowerCase();
  const inFolder = (cwd: string) => !folder || cwd === folder || cwd.startsWith(folder.endsWith("/") ? folder : folder + "/");
  const found: BoardSearch = { query: q, ids: new Set(), bySession: {}, past: [], searching: searchable(q) && hits?.query !== q, indexing: hits?.indexing === true };
  for (const s of sessions) {
    if (s.state === "ended" || !inFolder(s.cwd)) continue;
    if (s.name.toLowerCase().includes(lower) || s.cwd.toLowerCase().includes(lower) || (branches[s.cwd] ?? "").toLowerCase().includes(lower)) found.ids.add(s.id);
  }
  if (hits?.query === q) {
    for (const hit of hits.hits) {
      if (!hit.session || !inFolder(hit.session.cwd)) continue;
      const current = sessions.find((s) => s.id === hit.session!.id);
      if (current && current.state !== "ended") {
        found.ids.add(current.id);
        found.bySession[current.id] ??= hit;
      } else {
        found.past.push({ ...hit, session: current ?? hit.session });
      }
    }
  }
  return found;
}

export function searchHeadline(found: BoardSearch, shown: number): string {
  const q = `“${found.query}”`;
  if (shown === 0) return found.searching ? `Searching for ${q}` : `No agent matches ${q}`;
  return shown === 1 ? `1 agent matches ${q}` : `${shown} agents match ${q}`;
}

export function searchDetail(found: BoardSearch): string {
  if (found.searching) return found.indexing ? "Reading your conversations…" : "Searching messages…";
  if (found.past.length === 0) return " ";
  return found.past.length === 1 ? "Also said in 1 past conversation." : `Also said in ${found.past.length} past conversations.`;
}

/** Opens what a search found: its conversation, even one on no list, at
 *  the message, its words found. */
export function openHit(hit: SearchHit, query: string) {
  const session = hit.session;
  if (!session) return;
  setJump(session.id, hit.messageId, query);
  if (!getShell().pastSessions.some((s) => s.id === session.id)) {
    setShell({ foundSessions: { ...getShell().foundSessions, [session.id]: session } });
  }
  openAgent(session.id);
}

/** A message found from an editor's ⌘K, in this window: looked up again, as
 *  its conversation may have ended and be on no list here (openHit keeps it). */
export async function openFound(sessionId: string, messageId: string | null, query: string) {
  if (messageId && searchable(query)) {
    try {
      const reply = await ask({ type: "search", text: query.slice(0, 200), limit: 50 });
      const hits = (reply.search as SearchResults | undefined)?.hits ?? [];
      const hit = hits.find((h) => h.session?.id === sessionId && h.messageId === messageId) ?? hits.find((h) => h.session?.id === sessionId);
      if (hit) return openHit(hit, query);
    } catch {
      // Not found again: the agent, as it is.
    }
  }
  if (messageId) setJump(sessionId, messageId, query);
  openAgent(sessionId);
}

/** The query's words worth marking, longest first so "notarize" is marked
 *  whole rather than as "no" (SearchWords.of). */
export function searchWords(query: string): string[] {
  return query.split(/\s+/).filter((w) => w.length >= 2).sort((a, b) => b.length - a.length);
}

/** A snippet's parts, the query's words marked, ignoring case and accents. */
export function markedParts(text: string, query: string): { text: string; marked: boolean }[] {
  const fold = (t: string) => t.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase();
  const plain = fold(text);
  const found: [number, number][] = [];
  for (const word of searchWords(query).map(fold)) {
    let from = 0;
    for (let at = plain.indexOf(word, from); at >= 0; at = plain.indexOf(word, from)) {
      const end = at + word.length;
      if (!found.some(([a, b]) => at < b && end > a)) found.push([at, end]);
      from = end;
    }
  }
  found.sort((a, b) => a[0] - b[0]);
  const parts: { text: string; marked: boolean }[] = [];
  let last = 0;
  for (const [a, b] of found) {
    if (a > last) parts.push({ text: text.slice(last, a), marked: false });
    parts.push({ text: text.slice(a, b), marked: true });
    last = b;
  }
  if (last < text.length) parts.push({ text: text.slice(last), marked: false });
  return parts;
}

export interface SidebarHits {
  query: string;
  /** The best message of each agent listed. */
  bySession: Record<string, SearchHit>;
  /** Conversations that matched by what they said alone, running or long over. */
  others: SearchHit[];
  searching: boolean;
}

/** The sidebar's search, of what was said as well (SearchStore.sidebarHits):
 *  a listed agent shows its match; the rest are listed under it. */
export function sidebarHits(query: string, hits: SearchResults | null, listed: { pinned: Session[]; needsYou: Session[]; working: Session[]; active: Session[]; archived: Session[] },
  sessions: Session[], folder: string | null, stateFilter: string | null): SidebarHits {
  const q = query.trim();
  const found: SidebarHits = { query: q, bySession: {}, others: [], searching: !!q && searchable(q) && hits?.query !== q };
  if (!q || !hits || hits.query !== q) return found;
  const named = new Set([...listed.pinned, ...listed.needsYou, ...listed.working, ...listed.active, ...listed.archived].map((s) => s.id));
  const inFolder = (cwd: string) => !folder || cwd === folder || cwd.startsWith(folder.endsWith("/") ? folder : folder + "/");
  const stateOf = (s: Session) => (s.question || s.state === "waiting_input" ? "waiting_input" : s.state === "busy" ? "busy" : "idle");
  for (const hit of hits.hits) {
    const session = hit.session;
    if (!session || !inFolder(session.cwd)) continue;
    if (named.has(session.id)) {
      found.bySession[session.id] ??= hit;
      continue;
    }
    const current = sessions.find((s) => s.id === session.id) ?? session;
    if (stateFilter && (current.state === "ended" || stateOf(current) !== stateFilter)) continue;
    found.others.push({ ...hit, session: current });
  }
  return found;
}

