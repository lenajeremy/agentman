// The main window (Views/ContentView.swift): the sidebar, then the detail
// area — Agentman, New Agent, an agent, or "This agent has ended" — and the
// overlays over everything (toasts, palette, away summary, lightbox).

import { useStore } from "./store";
import { AgentView } from "./AgentView";
import { AgentmanView } from "./manager/AgentmanView";
import { NewAgentView } from "./NewAgentView";
import { Overlays } from "./Overlays";
import { Sidebar } from "./Sidebar";
import { openAgent, useRoute } from "./nav";

export function MainWindow({ platform }: { platform: string }) {
  const route = useRoute();
  const { sessions } = useStore();
  const selected = route.kind === "agent" ? sessions.find((s) => s.id === route.id) : undefined;

  return (
    <div className="app">
      <Sidebar selected={selected?.id} onSelect={openAgent} platform={platform} />
      <main className="detail">
        {route.kind === "agentman" && <AgentmanView />}
        {route.kind === "new" && <NewAgentView worktree={route.worktree} />}
        {route.kind === "agent" && (selected ? <AgentView session={selected} platform={platform} /> : <div className="empty big">Connecting to am serve…</div>)}
      </main>
      <Overlays />
    </div>
  );
}
