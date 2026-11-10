// An editor window (Editor/EditorWindow.swift): one per folder, opened by
// windows.go with ?view=editor&folder=…[&file=…][&terminal=1][&agent=here].
// A small editor for the folder: its files in a tree with what git says
// changed, the files open in tabs, Monaco, the terminal, the agent panel and
// a status bar.

import { useEffect, useMemo, useState } from "react";
import { Folder, PanelRight, Search, SquareTerminal } from "lucide-react";
import { Window } from "@wailsio/runtime";
import { EditorPanes } from "./Editor";
import { AgentPanel, ColumnDivider, PANEL_MIN } from "./editor/AgentPanel";
import { EditorAlerts } from "./editor/Alerts";
import { commandFor, onMenuCommand, runCommand } from "./editor/commands";
import { files, inShell } from "./editor/fs";
import { MonacoBridge } from "./editor/monaco";
import { GoToFile, GoToText } from "./editor/Palette";
import { StatusBar } from "./editor/StatusBar";
import { EditorStore, useEditor, type WorkspaceTab } from "./editor/store";
import { DiffPanel } from "./panel/DiffPanel";
import { HistoryPanel } from "./panel/HistoryPanel";
import type { Session } from "@shared/protocol";
import { DiskBanner, PathBar, TabStrip } from "./editor/Tabs";
import { FileTree } from "./editor/Tree";
import { EditorEmpty, MarkdownPreview, NonTextView } from "./editor/Views";
import { openInMain, platform, reveal } from "./host";
import { getPref, usePref } from "./prefs";
import { registerUnsaved } from "./shell/quitGuard";
import { useStore } from "./store";
import { Terminal } from "./Terminal";
import { AppIcon, ChromeButton, Icon } from "./ui";
import "./styles/editor.css";

const query = new URLSearchParams(location.search);

export function EditorWindow() {
  const { sessions } = useStore();
  const folder = query.get("folder") ?? sessions.find((s) => s.id === query.get("session"))?.cwd ?? null;
  if (!folder) return <div className="empty big">Connecting to am serve…</div>;
  return <FolderEditor folder={folder} />;
}

function FolderEditor({ folder }: { folder: string }) {
  const store = useMemo(() => new EditorStore(folder), [folder]);
  const bridge = useMemo(() => new MonacoBridge(store), [store]);
  useEditor(store);
  const { sessions } = useStore();
  const [os, setOs] = useState(document.documentElement.dataset.platform ?? "");
  const [terminalHeight, setTerminalHeight] = usePref("terminalHeight", 260);
  const [panelWidth, setPanelWidth] = usePref("agentPanelWidth", 400);
  const [size, setSize] = useState({ w: window.innerWidth, h: window.innerHeight });
  const [vim] = usePref("editorVim", false);

  // Editor ▸ Vim Mode ticked as the setting is.
  useEffect(() => {
    void files.setVimMenu(vim);
  }, [vim]);

  useEffect(() => {
    store.monaco = bridge;
    return () => {
      store.monaco = null;
      bridge.dispose();
    };
  }, [store, bridge]);

  useEffect(() => {
    void platform().then(setOs);
    const resized = () => setSize({ w: window.innerWidth, h: window.innerHeight });
    window.addEventListener("resize", resized);
    return () => window.removeEventListener("resize", resized);
  }, []);

  // What the window was opened on, and for screenshots and tests the tree's
  // keys, a filter, marks or the box for a new file (AGENTMAN_TREE).
  useEffect(() => {
    document.title = store.name;
    if (inShell) void Window.SetTitle(store.name);
    const file = query.get("file");
    if (file) store.open(file);
    if (query.get("terminal") === "1") store.showTerminal = true;
    if (query.get("agent") === "here") store.showsFolderAgent = true;
    if (query.get("panel")) store.setShowAgentPanel(true);
    for (const part of (query.get("tree") ?? "").split(";")) {
      if (part === "keys") store.showTreeKeys = true;
      if (part === "create") store.creatingIn = "";
      if (part.startsWith("filter:")) {
        store.treeFilter = part.slice(7);
        void store.loadAllFiles();
      }
      if (part.startsWith("mark:")) store.marked = new Set(part.slice(5).split(",").filter(Boolean));
    }
    const preview = query.get("preview") === "1";
    if (preview && file) {
      const document = store.documents.find((d) => d.path === file);
      void document?.whenLoaded().then(() => {
        if (document.isMarkdown && !document.previewing) store.togglePreview();
      });
    }
    if (query.get("goto") === "1") store.quickOpen = true;
    const search = query.get("search");
    if (search !== null) {
      store.searchQuery = search;
      store.showSearch();
      if (search) store.runSearch(search);
    }
    store.changed();
  }, [store]);

  useEffect(() => {
    store.showFolderAgent(sessions, sessions.length > 0);
  }, [store, sessions]);

  // Agents change files while the editor is open: the tree, git's marks and
  // the open files follow them, every 2 s.
  useEffect(() => {
    let live = true;
    const loop = async () => {
      while (live) {
        await store.refresh().catch(() => undefined);
        await new Promise((r) => setTimeout(r, 2000));
      }
    };
    void loop();
    return () => {
      live = false;
    };
  }, [store]);

  // Quitting with unsaved files asks first (the shell's alert).
  useEffect(
    () =>
      registerUnsaved(`editor:${store.root}`, () =>
        store.documents.filter((d) => d.isDirty).map((d) => ({ name: d.name, path: d.path, save: () => d.save() })),
      ),
    [store],
  );

  // The menus' keys, the tree's Vim keys, ⌘W; and the same commands from
  // the menu bar and from inside the editor page.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const command = commandFor(e);
      if (store.treeFocused && !command) {
        const mod = /Mac/.test(navigator.platform) ? e.metaKey : e.ctrlKey;
        if (mod && !e.altKey && e.key.toLowerCase() === "z") {
          e.preventDefault();
          void (e.shiftKey ? store.redoTreeChange() : store.undoTreeChange());
          return;
        }
        const target = e.target as HTMLElement | null;
        const typing = target?.tagName === "INPUT" || target?.tagName === "TEXTAREA";
        if (!typing && getPref("editorVim", false) && store.handleTreeKey(e)) {
          e.preventDefault();
          e.stopPropagation();
          return;
        }
      }
      if (!command) return;
      e.preventDefault();
      e.stopPropagation();
      void runCommand(store, command);
    };
    const fromPage = (e: MessageEvent) => {
      if (e.data?.channel !== "editorKey") return;
      const command = commandFor(e.data);
      if (command) void runCommand(store, command);
    };
    const openMain = (e: Event) => openInMain((e as CustomEvent<string>).detail);
    // Asked to open this folder's editor from inside it (host.ts openEditor).
    const openHere = (e: Event) => {
      const asked = (e as CustomEvent<{ file: string; terminal: boolean }>).detail;
      if (asked.file) store.open(asked.file);
      if (asked.terminal && !store.showTerminal) store.toggleTerminal();
      store.changed();
    };
    window.addEventListener("agentman:editor-open", openHere);
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("message", fromPage);
    window.addEventListener("agentman:open-main", openMain);
    const offMenu = onMenuCommand((command) => {
      if (document.hasFocus()) void runCommand(store, command);
    });
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("message", fromPage);
      window.removeEventListener("agentman:open-main", openMain);
      window.removeEventListener("agentman:editor-open", openHere);
      offMenu();
    };
  }, [store]);

  const active = store.active;
  const leading = os === "darwin" ? 82 : 16;
  const panelMax = size.w * 0.6;
  const shownPanelWidth = Math.min(panelWidth, Math.max(panelMax, PANEL_MIN));

  return (
    <div className="editor-window">
      <header className="editor-header drag">
        <div className="editor-brand" style={{ paddingLeft: leading }}>
          <AppIcon size={20} />
          <span className="editor-brand-name">Agentman</span>
        </div>
        <span className="editor-folder-icon">
          <Icon icon={Folder} sf="folder" size={13} />
        </span>
        <span className="editor-folder-name">{store.name}</span>
        {store.branch && (
          <>
            <span className="editor-slash">/</span>
            <span className="editor-branch">{store.branch}</span>
          </>
        )}
        <span className="editor-header-spacer" />
        <span className="editor-header-buttons no-drag">
          <ChromeButton icon={Search} sf="magnifyingglass" title="Go to file (⌘P)" onClick={() => void runCommand(store, "goToFile")} />
          <ChromeButton icon={SquareTerminal} sf="terminal" title="Toggle terminal" active={store.showTerminal} onClick={() => store.toggleTerminal()} />
          <ChromeButton icon={Folder} sf="folder" title="Show in Finder" onClick={() => void reveal(store.active?.url ?? store.root)} />
          <ChromeButton icon={PanelRight} sf="sidebar.right" title="Toggle agent conversation" active={store.showAgentPanel} onClick={() => store.setShowAgentPanel(!store.showAgentPanel)} />
        </span>
      </header>
      <div className="editor-split">
        <FileTree store={store} />
        <div className="editor-main">
          <div className="editor-row">
            <div className="editor-column">
              {(store.documents.length > 0 || store.changesSession) && <TabStrip store={store} />}
              {active && !store.showingChanges && (
                <>
                  <PathBar store={store} document={active} />
                  <DiskBanner document={active} close={() => store.close(active)} />
                </>
              )}
              <div className="editor-stack">
                <EditorPanes store={store} bridge={bridge} />
                {active && active.loaded && active.kind !== "text" && (
                  <InFrontPane store={store}>
                    <NonTextView document={active} />
                  </InFrontPane>
                )}
                {active?.isMarkdown && active.previewing && (
                  <InFrontPane store={store}>
                    <MarkdownPreview store={store} document={active} />
                  </InFrontPane>
                )}
                {!active && store.panes.length === 1 && <EditorEmpty store={store} />}
                {store.workspaceTab && store.changesSession && (
                  <div className="in-front editor-changes">
                    <EditorChanges session={sessions.find((s) => s.id === store.changesSession)} tab={store.workspaceTab} />
                  </div>
                )}
              </div>
              {store.showTerminal && (
                <>
                  <RowDivider height={terminalHeight} setHeight={setTerminalHeight} maximum={size.h * 0.75} />
                  <div className="editor-terminal" style={{ height: terminalHeight }}>
                    <Terminal key={store.terminalGeneration} target={{ folder: store.root }} />
                  </div>
                </>
              )}
              {store.quickOpen ? <GoToFile store={store} /> : store.searchOpen ? <GoToText store={store} /> : null}
            </div>
            {store.showAgentPanel && (
              <>
                <ColumnDivider width={shownPanelWidth} setWidth={setPanelWidth} maximum={panelMax} />
                <div className="agent-panel-column" style={{ width: shownPanelWidth }}>
                  <AgentPanel store={store} />
                </div>
              </>
            )}
          </div>
          <StatusBar store={store} />
        </div>
      </div>
      <EditorAlerts store={store} />
    </div>
  );
}

/** An agent's Diff or History, in place of the code. The code stays open
 *  behind it, with its scroll and undo, for when this closes. */
function EditorChanges({ session, tab }: { session?: Session; tab: WorkspaceTab }) {
  if (!session) return <div className="editor-changes-gone">This agent's changes are no longer here.</div>;
  return tab === "history" ? <HistoryPanel key={session.id} session={session} /> : <DiffPanel key={session.id} session={session} />;
}

/** A view of the file in front over its own split, below the split's
 *  title, when the window is split; over the whole editor otherwise. */
function InFrontPane({ store, children }: { store: EditorStore; children: React.ReactNode }) {
  const frame = store.panes.length > 1 ? store.paneFrames[store.focusedPane] : undefined;
  if (!frame) return <div className="in-front">{children}</div>;
  return (
    <div className="in-front" style={{ left: frame.x, width: frame.width, top: 26 }}>
      {children}
    </div>
  );
}

/** The edge above the terminal: drag it to resize the terminal. */
function RowDivider({ height, setHeight, maximum }: { height: number; setHeight: (h: number) => void; maximum: number }) {
  const [start, setStart] = useState<{ y: number; height: number } | null>(null);
  return (
    <div
      className="row-divider"
      onPointerDown={(e) => {
        setStart({ y: e.clientY, height });
        e.currentTarget.setPointerCapture(e.pointerId);
      }}
      onPointerMove={(e) => {
        if (!start) return;
        setHeight(Math.min(Math.max(start.height - (e.clientY - start.y), 120), Math.max(maximum, 120)));
      }}
      onPointerUp={() => setStart(null)}
    />
  );
}
