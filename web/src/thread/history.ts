// The message box's ↑ history for an agent (promptHistory.ts), read through
// the store: the thread as loaded and what waits to reach the agent, then
// older prompts from the daemon, a page at a time (fetch_prompts).

import { daemon } from "../daemon";
import { loadedThread } from "../store";
import { PromptHistory, promptsOf, type Prompts } from "./promptHistory";
import { queuedWords } from "./state";

/** What the user said to an agent, before a cursor, newest first; null from
 *  a daemon that can't say. */
async function olderPrompts(sessionId: string, cursor: string): Promise<Prompts | null> {
  try {
    const page = (await daemon.request({ type: "fetch_prompts", sessionId, before: cursor, limit: 20 })).page;
    if (!page) return null;
    return { prompts: promptsOf(page.messages), cursor: page.nextCursor, hasMore: page.hasMore };
  } catch {
    return null;
  }
}

/** A history reading the agent's prompts through the store. */
export function agentHistory(): PromptHistory {
  const history = new PromptHistory();
  history.loaded = (sessionId) => {
    const thread = loadedThread(sessionId);
    return { prompts: promptsOf(thread.messages, queuedWords(sessionId)), cursor: thread.cursor, hasMore: thread.hasMore };
  };
  history.fetch = olderPrompts;
  return history;
}
