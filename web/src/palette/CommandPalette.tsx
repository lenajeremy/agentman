// ⌘K (Views/CommandPalette.swift): every agent and everything the app does,
// by a few letters, in the manner of the command menus in Linear, Raycast
// and Slack; and whatever was typed can go to Agentman as a question. Over
// an editor, agents and Agentman open in the editor's own panel, Usage in
// its popover, and only what needs the main window goes there.

import { useEffect } from "react";
import {
  Bell, Book, Braces, Circle, Code, Contrast, History, Key, ListFilter, MessageSquareText, MessagesSquare, Pencil, Settings, ShieldCheck, Sparkles,
  SquarePen, SquareTerminal, ChartSpline, QrCode, Smartphone, type LucideIcon,
} from "lucide-react";
import type { Session } from "@shared/protocol";
import { showLastAway } from "../away/away";
import { daemon } from "../daemon";
import type { EditorStore, PanelRoute } from "../editor/store";
import { agentName } from "../labels";
import { openEditor, openInMain, openSettings, pickFolder } from "../host";
import { openAgent, openAgentman, openNewAgent, useRoute, openUsage } from "../nav";
import { setPref } from "../prefs";
import { askAgentman, useAgentmanOn } from "../shell/agentman";
import { cachedHome } from "../shell/native";
import { loadFolders, setShell, useShell, type FilterState } from "../shell/state";
import { useStore } from "../store";
import { fuzzyScore } from "./fuzzy";
import { PaletteView, type PaletteItem } from "./PaletteView";

interface Command {
  item: PaletteItem;
  /** Extra words it answers to, beyond its title. */
  keywords?: string;
  run: () => void;
}

export const SETTINGS_PAGES: { id: string; title: string; icon: string; lucide: LucideIcon }[] = [
  { id: "general", title: "General", icon: "gearshape", lucide: Settings },
  { id: "notifications", title: "Notifications", icon: "bell.badge", lucide: Bell },
  { id: "devices", title: "Devices", icon: "iphone", lucide: Smartphone },
  { id: "permissions", title: "Permissions", icon: "checkmark.shield", lucide: ShieldCheck },
  { id: "personality", title: "Personality", icon: "text.bubble", lucide: MessageSquareText },
  { id: "knowledge", title: "Knowledge", icon: "book", lucide: Book },
  { id: "keys", title: "Keys", icon: "key", lucide: Key },
  { id: "languages", title: "Languages", icon: "curlybraces", lucide: Braces },
];

const DOT: Record<string, string> = { waiting_input: "var(--needs-you)", busy: "var(--working)" };

function tilde(path: string): string {
  const home = cachedHome();
  return home && (path === home || path.startsWith(home + "/")) ? "~" + path.slice(home.length) : path;
}

function base(path: string): string {
  return path.replace(/\/+$/, "").split("/").pop() || path;
}

/** Shows a terminal for the folder in the main window, as RightPanel.showTerminal
 *  does; the right panel and terminal listen for it. */
function showTerminal(folder: string) {
  window.dispatchEvent(new CustomEvent("agentman:show-terminal", { detail: { folder } }));
}

export function CommandPalette() {
  const { showPalette } = useShell();
  if (!showPalette) return null;
  return <Palette />;
}

/** ⌘K over an editor (EditorWindow.swift's CommandPalette(editor:)). */
export function EditorCommandPalette({ store }: { store: EditorStore }) {
  if (!store.showPalette) return null;
  return <Palette editor={store} />;
}

function Palette({ editor }: { editor?: EditorStore }) {
  const { sessions } = useStore();
  const { folders, pastSessions } = useShell();
  const route = useRoute();
  const keySet = useAgentmanOn();
  const initial = editor ? "" : new URLSearchParams(location.search).get("palette") ?? "";
  useEffect(() => {
    void loadFolders();
  }, []);
  const close = () => {
    if (!editor) {
      setShell({ showPalette: false });
      return;
    }
    editor.showPalette = false;
    editor.changed();
  };

  /** Over an editor, in its panel; else the main window's page. */
  const inPanel = (next: PanelRoute) => {
    if (!editor) return;
    editor.panelRoute = next;
    editor.setShowAgentPanel(true);
  };
  const showAgent = (id: string) => (editor ? inPanel({ kind: "agent", id }) : openAgent(id));
  const showAgentman = () => (editor ? inPanel({ kind: "agentman" }) : openAgentman());
  const ask = (question: string) => {
    if (!editor) {
      askAgentman(question);
      return;
    }
    inPanel({ kind: "agentman" });
    void daemon.request({ type: "manager_send", text: question, clientId: crypto.randomUUID() }).catch(() => {});
  };
  /** What needs the main window, there, from an editor. */
  const inMain = (link: string, here: () => void) => (editor ? openInMain(link) : here());

  function commands(query: string): Command[] {
    const list: Command[] = [];
    const trimmed = query.trim();
    if (trimmed && keySet) {
      // The question rides in the row's id, to be asked when chosen.
      list.push({ item: { id: "ask:" + trimmed, title: `Ask Agentman: “${trimmed}”`, icon: "sparkles", lucide: Sparkles, tint: "var(--working)" }, run: () => ask(trimmed) });
    }
    // The agent open now, first; not over an editor, which has its own.
    const open = editor ? undefined : route.kind === "agent" ? sessions.find((s) => s.id === route.id) ?? pastSessions.find((s) => s.id === route.id) : undefined;
    if (open) {
      list.push({ item: { id: "here-editor", title: `Open ${open.name}'s Folder in the Editor`, icon: "chevron.left.forwardslash.chevron.right", lucide: Code }, run: () => void openEditor({ folder: open.cwd }) });
      list.push({ item: { id: "here-terminal", title: `Open a Terminal for ${open.name}`, icon: "terminal", lucide: SquareTerminal }, run: () => showTerminal(open.cwd) });
      list.push({ item: { id: "here-rename", title: `Rename ${open.name}…`, icon: "pencil", lucide: Pencil }, run: () => setShell({ renaming: open.id }) });
    }
    // Agents, those that need the user first.
    const agents = [...sessions].sort((a: Session, b: Session) => {
      const wa = a.state === "waiting_input" ? 0 : 1;
      const wb = b.state === "waiting_input" ? 0 : 1;
      return wa - wb || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
    });
    for (const s of agents) {
      const state = s.state === "waiting_input" ? "needs you" : s.state === "busy" ? "working" : "idle";
      list.push({
        item: { id: "agent:" + s.id, title: s.name, detail: `${base(s.cwd)} · ${state}`, icon: "circle.fill", lucide: Circle, tint: DOT[s.state] ?? "var(--faint)" },
        keywords: `${s.cwd} ${agentName(s.kind)}`,
        run: () => showAgent(s.id),
      });
    }
    list.push({ item: { id: "new", title: "New Agent", icon: "square.and.pencil", lucide: SquarePen, shortcut: "⌘N" }, run: () => inMain("agentman://new", openNewAgent) });
    list.push({ item: { id: "agentman", title: "Agentman", detail: "the conversation", icon: "bubble.left.and.text.bubble.right", lucide: MessagesSquare }, run: showAgentman });
    list.push({
      item: { id: "usage", title: "Usage", detail: "what the agents spent", icon: "chart.xyaxis.line", lucide: ChartSpline },
      keywords: "cost tokens spend money limits",
      run: () => {
        if (!editor) return openUsage();
        editor.showUsage = true;
        editor.changed();
      },
    });
    list.push({ item: { id: "away", title: "What Happened While I Was Away", icon: "clock.arrow.circlepath", lucide: History }, keywords: "summary catch up", run: () => inMain("agentman://away", showLastAway) });
    for (const [state, title] of [["waiting_input", "Show Agents That Need You"], ["busy", "Show Working Agents"], ["idle", "Show Idle Agents"]] as [FilterState, string][]) {
      list.push({ item: { id: `filter:${state}`, title, icon: "line.3.horizontal.decrease", lucide: ListFilter }, keywords: "filter", run: () => inMain(`agentman://filter/${state}`, () => setShell({ stateFilter: state })) });
    }
    list.push({ item: { id: "filter:all", title: "Show All Agents", icon: "line.3.horizontal.decrease", lucide: ListFilter }, keywords: "filter clear", run: () => inMain("agentman://filter/all", () => setShell({ stateFilter: null, folderFilter: null })) });
    list.push({
      item: { id: "open-folder", title: "Open a Folder in the Editor…", icon: "chevron.left.forwardslash.chevron.right", lucide: Code, shortcut: "⌘O" },
      run: () => void pickFolder("Choose a folder to open in the editor", cachedHome()).then((folder) => {
        if (folder) void openEditor({ folder });
      }),
    });
    for (const folder of folders.slice(0, 8)) {
      const name = base(folder.path);
      list.push({ item: { id: "editor:" + folder.path, title: `Open ${name} in the Editor`, detail: tilde(folder.path), icon: "chevron.left.forwardslash.chevron.right", lucide: Code }, keywords: folder.path, run: () => void openEditor({ folder: folder.path }) });
      list.push({ item: { id: "terminal:" + folder.path, title: `Open a Terminal in ${name}`, detail: tilde(folder.path), icon: "terminal", lucide: SquareTerminal }, keywords: `${folder.path} shell`, run: () => void openEditor({ folder: folder.path, terminal: true }) });
    }
    list.push({ item: { id: "settings", title: "Settings", icon: "gearshape", lucide: Settings, shortcut: "⌘," }, run: () => void openSettings() });
    list.push({ item: { id: "pair", title: "Connect a Device…", detail: "a phone or tablet", icon: "qrcode", lucide: QrCode }, keywords: "pair phone iphone ipad qr code", run: () => void openSettings("pair") });
    for (const page of SETTINGS_PAGES) {
      list.push({ item: { id: "settings:" + page.id, title: `Settings: ${page.title}`, icon: page.icon, lucide: page.lucide }, keywords: "preferences", run: () => void openSettings(page.id) });
    }
    for (const [value, title] of [["light", "Use the Light Appearance"], ["dark", "Use the Dark Appearance"], ["system", "Match the Mac's Appearance"]]) {
      list.push({ item: { id: "appearance:" + value, title, icon: "circle.lefthalf.filled", lucide: Contrast }, keywords: "theme mode", run: () => setPref("appearance", value) });
    }
    return list;
  }

  function results(query: string): PaletteItem[] {
    const all = commands(query);
    const trimmed = query.trim();
    if (!trimmed) return all.slice(0, 40).map((c) => c.item);
    const ranked = all
      .filter((c) => !c.item.id.startsWith("ask:"))
      .map((c) => {
        const scores = [fuzzyScore(trimmed, c.item.title), fuzzyScore(trimmed, c.keywords ?? "")].filter((s): s is number => s !== undefined);
        return scores.length ? { item: c.item, score: Math.max(...scores) } : null;
      })
      .filter((r): r is { item: PaletteItem; score: number } => !!r)
      .sort((a, b) => b.score - a.score)
      .slice(0, 40)
      .map((r) => r.item);
    // A question, or nothing that matches, goes to Agentman first.
    const askItem = all.find((c) => c.item.id.startsWith("ask:"))?.item;
    if (askItem) {
      if (!ranked.length || trimmed.endsWith("?") || trimmed.split(" ").filter(Boolean).length >= 4) ranked.unshift(askItem);
      else ranked.push(askItem);
    }
    return ranked;
  }

  return (
    <PaletteView
      placeholder="Go to an agent, run a command, or ask Agentman"
      initialQuery={initial}
      results={results}
      dismiss={close}
      choose={(item) => {
        close();
        if (item.id.startsWith("ask:")) ask(item.id.slice(4));
        else commands("").find((c) => c.item.id === item.id)?.run();
      }}
    />
  );
}
