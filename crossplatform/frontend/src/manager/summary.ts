// What the sidebar's Agentman card and the dock badge show (Sidebar.swift's
// AgentmanCard, AgentsStore's unread count). Owned by the Agentman area,
// which fills it from Agentman's conversation; a stub until then.

export interface ManagerSummary {
  /** Agentman entries not read yet: the card's badge and the dock's. */
  unread: number;
  /** Cards (questions, keys, needs-you) waiting for an answer. */
  cardsWaiting: number;
}

export function useManagerSummary(): ManagerSummary {
  return { unread: 0, cardsWaiting: 0 };
}
