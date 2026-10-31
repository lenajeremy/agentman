// The main window, laid out as the Mac app's (Views/ContentView.swift and
// AgentThreadView.swift): the sidebar, then the agent's header, its
// conversation with the message box, and the workspace card beside it. The
// terminal opens under the conversation; the editor opens in a window of
// its own, as on the Mac.

import { useEffect, useState } from "react";
import { Ellipsis, PanelRight, SquareTerminal } from "lucide-react";
import type { Session } from "@shared/protocol";
import { Composer } from "./Composer";
import { Editor } from "./Editor";
import { openEditor, platform } from "./host";
import { folderLabel } from "./labels";
import { Sidebar } from "./Sidebar";
import { useStore } from "./store";
import { Terminal } from "./Terminal";
import { Thread } from "./Thread";
import { WorkspaceCard } from "./WorkspaceCard";
import { ChromeButton, Icon, PlusSlashMinus } from "./ui";

const query = new URLSearchParams(location.search);

function Header({ session, terminal, card, onTerminal, onCard }: { session: Session; terminal: boolean; card: boolean; onTerminal: () => void; onCard: () => void }) {
  return (
    <header className="detail-header drag">
      <div className="crumbs">
        <span className="crumb-folder">{folderLabel(session.cwd)}</span>
        <span className="crumb-slash">/</span>
        <span className="crumb-name no-drag">{session.name || session.kind}</span>
      </div>
      <div className="header-buttons no-drag">
        <ChromeButton icon={SquareTerminal} title="Terminal" active={terminal} onClick={onTerminal} />
        <ChromeButton glyph={<PlusSlashMinus />} title="Changes" />
        <ChromeButton icon={PanelRight} title="Workspace" active={card} onClick={onCard} />
        <button className="ellipsis" aria-label="More"><Icon icon={Ellipsis} size={15} /></button>
      </div>
    </header>
  );
}

function EditorWindow() {
  const { sessions } = useStore();
  const session = sessions.find((s) => s.id === query.get("session")) ?? sessions[0];
  if (!session) return <div className="empty big">Connecting to am serve…</div>;
  return (
    <div className="editor-window">
      <header className="detail-header drag" style={{ paddingLeft: document.documentElement.dataset.platform === "darwin" ? 82 : 18 }}>
        <div className="crumbs"><span className="crumb-name">{folderLabel(session.cwd)}</span></div>
      </header>
      <div className="editor-body"><Editor session={session} /></div>
    </div>
  );
}

export default function App() {
  const { sessions } = useStore();
  const [os, setOs] = useState("");
  const [selectedId, setSelectedId] = useState<string>();
  const [terminal, setTerminal] = useState(query.get("tab") === "terminal");
  const [card, setCard] = useState(true);
  const [sent, setSent] = useState(0);

  useEffect(() => {
    void platform().then(setOs);
  }, []);

  if (query.get("view") === "editor") return <EditorWindow />;

  const selected = sessions.find((s) => s.id === selectedId) ?? sessions[0];

  return (
    <div className="app">
      <Sidebar selected={selected?.id} onSelect={setSelectedId} platform={os} />
      <main className="detail">
        {selected ? (
          <>
            <Header session={selected} terminal={terminal} card={card} onTerminal={() => setTerminal(!terminal)} onCard={() => setCard(!card)} />
            <div className="detail-body">
              <div className="conversation">
                <div className="top-fade" />
                <Thread session={selected} stickToEnd={sent} />
                <Composer session={selected} onSent={() => setSent((n) => n + 1)} />
              </div>
              {card && (
                <WorkspaceCard
                  session={selected}
                  platform={os}
                  onEditor={() => void openEditor(selected.id)}
                  onTerminal={() => setTerminal(true)}
                />
              )}
            </div>
            {terminal && (
              <div className="terminal-panel">
                <Terminal target={{ session: selected.id }} />
              </div>
            )}
          </>
        ) : (
          <div className="empty big">Connecting to am serve…</div>
        )}
      </main>
    </div>
  );
}
