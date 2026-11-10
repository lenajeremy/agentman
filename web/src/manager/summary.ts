// What the sidebar's Agentman card and the dock badge show (Sidebar.swift's
// AgentmanCard, AgentsStore's unread count), from Agentman's conversation.

import { openCardsOf, unreadOf, useManager, viewingManager } from "./store";

export interface ManagerSummary {
  /** Agentman entries not read yet: the card's badge and the dock's. */
  unread: number;
  /** Cards (questions, keys, needs-you) waiting for an answer. */
  cardsWaiting: number;
}

export function useManagerSummary(): ManagerSummary {
  const s = useManager();
  return {
    // Nothing is unread while the conversation is in front of the user.
    unread: viewingManager(s) ? 0 : unreadOf(s).length,
    cardsWaiting: openCardsOf(s).length,
  };
}
