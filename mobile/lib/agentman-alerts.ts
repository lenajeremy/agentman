/**
 * What Agentman says that is worth an alert, when the phone raises its own
 * because push isn't working: the same things the Mac pushes. A card that
 * needs you rings like an agent needing you; a brief, word from an agent and
 * a finished reply are Agentman's own kind. The tap opens Agentman.
 */

import type { AlertKind } from "./notification-prefs";
import type { ManagerEntry } from "./protocol";

export interface AgentmanAlert {
  kind: AlertKind;
  title: string;
  body: string;
}

/** The alert a new entry makes, or null when it is not news on its own. */
export function agentmanAlert(entry: ManagerEntry): AgentmanAlert | null {
  const card = entry.card;
  if (entry.role === "card") {
    if (!card || card.state !== "open") return null;
    if (card.kind === "note") {
      return { kind: "agentman", title: "Agentman suggests a note", body: card.title };
    }
    return {
      kind: "needsYou",
      title: `${card.agentName || "An agent"} needs you`,
      body: card.title || "Agentman has a card for you.",
    };
  }
  if (entry.role === "brief" || entry.role === "agent") {
    return { kind: "agentman", title: "Agentman", body: entry.text || "Agentman has news." };
  }
  return null;
}

/** The alert for a reply Agentman just finished: its last words. */
export function replyAlert(entries: ManagerEntry[]): AgentmanAlert | null {
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    if (entry.role === "user") return null;
    if (entry.role === "assistant" && !entry.streaming && entry.text.trim()) {
      return { kind: "agentman", title: "Agentman", body: entry.text.trim() };
    }
  }
  return null;
}
