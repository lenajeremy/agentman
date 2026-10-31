// An agent in the detail area (AgentThreadView.swift's layout): its header,
// the conversation and message box, the workspace card beside them, the
// right panel at the window's edge, and the terminal under the conversation.

import { useState } from "react";
import type { Session } from "@shared/protocol";
import { AgentHeader } from "./AgentHeader";
import { Composer } from "./Composer";
import { openEditor } from "./host";
import { usePref } from "./prefs";
import { RightPanel } from "./RightPanel";
import { Terminal } from "./Terminal";
import { Thread } from "./Thread";
import { WorkspaceCard } from "./WorkspaceCard";

const startTerminal = new URLSearchParams(location.search).get("tab") === "terminal";

export function AgentView({ session, platform }: { session: Session; platform: string }) {
  const [terminal, setTerminal] = useState(startTerminal);
  const [panel, setPanel] = usePref("rightPanelOpen", false);
  const [card, setCard] = useState(true);
  const [sent, setSent] = useState(0);

  return (
    <>
      <AgentHeader
        session={session}
        terminal={terminal}
        panel={panel}
        card={card}
        onTerminal={() => setTerminal(!terminal)}
        onPanel={() => setPanel(!panel)}
        onCard={() => setCard(!card)}
      />
      <div className="detail-body">
        <div className="conversation">
          <Thread session={session} stickToEnd={sent} card={card} panel={panel} onShowChanges={() => setPanel(true)} onReview={() => setCard(true)} />
          <Composer session={session} onSent={() => setSent((n) => n + 1)} />
        </div>
        {card && (
          <WorkspaceCard
            session={session}
            platform={platform}
            onEditor={() => void openEditor({ folder: session.cwd, sessionId: session.id })}
            onTerminal={() => setTerminal(true)}
          />
        )}
        {panel && <RightPanel session={session} onClose={() => setPanel(false)} />}
      </div>
      {terminal && (
        <div className="terminal-panel">
          <Terminal target={{ session: session.id }} />
        </div>
      )}
    </>
  );
}
