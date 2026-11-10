// What other areas open in the workspace's panels: a terminal in an agent's
// folder (the header's terminal button, the card's Open terminal), the
// agent's own terminal (the header's More menu), a tab of the right panel.

import type { Session } from "@shared/protocol";
import { shellCommand } from "../terminal/shells";
import { showTerminalWhereKept } from "./state";

export { showPanel, closePanel, dockTerminal } from "./state";

/** Opens a terminal in the folder where the user keeps it, starting a shell
 *  there if it has none (RightPanel.showTerminal(in:)). */
export function openTerminal(folder: string) {
  shellCommand(folder, { type: "ensure" });
  showTerminalWhereKept();
}

/** The agent's own terminal, in a tab of its folder's panel, reused if it is
 *  still open; closing it leaves the agent running. */
export function openAgentTerminal(session: Session) {
  shellCommand(session.cwd, { type: "agent", session: session.id, title: session.name });
  showTerminalWhereKept();
}
