// The card beside a conversation (Views/WorkspacePanel.swift): where the
// agent runs, its folder in the editor or a terminal, its branch and git
// actions, its history, changes and files in the right panel, its plans, and
// the other agents in the same folder. Opened up, 540 wide, it lists the
// changes, files or plans, with the diff, file or plan picked over them.

import { useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, Diff, FileText, Folder, GitBranch, History, Monitor, SquareArrowOutUpRight, SquareTerminal } from "lucide-react";
import type { Session, WorkspaceChange } from "@shared/protocol";
import { reveal } from "./host";
import { openAgent } from "./nav";
import { useStore } from "./store";
import { AgentMark, Icon } from "./ui";
import { git, workspace, type GitState } from "./workspace/api";
import { CardRow } from "./workspace/CardRow";
import { GitRow } from "./workspace/GitRow";
import {
  ArtifactList, ArtifactView, ChangesList, DiffLoader, FileList, FileLoader, routeTitle, type CardRoute,
} from "./workspace/CardLists";
import { HistoryPanel } from "./panel/HistoryPanel";
import { showPanel } from "./panel/state";
import { nameOf } from "./panel/files";
import { fileManager, placeTitle } from "./workspace/place";
import "./styles/workspace.css";

import type { CardTab } from "./workspace/cardTab";
const TAB_TITLE: Record<CardTab, string> = { changes: "Changes", files: "Files", history: "History", plans: "Plans" };

// For comparisons, as the Mac's AGENTMAN_FILES_OPEN: the card open on a diff
// or a file (?filesOpen=diff:term.go, ?filesOpen=file:README.md).
const startOpen = new URLSearchParams(location.search).get("filesOpen") ?? "";

function dotColour(state: Session["state"]): string {
  if (state === "waiting_input") return "var(--needs-you)";
  if (state === "busy") return "var(--working)";
  return "var(--faint)";
}

export function WorkspaceCard({ session, tab, folderGone, onEditor, onTerminal }: {
  session: Session;
  platform: string;
  /** The list to open up on; a new n opens it again. */
  tab: { tab: CardTab; n: number };
  folderGone: boolean;
  onEditor: () => void;
  onTerminal: () => void;
}) {
  const { sessions } = useStore();
  const [shownTab, setShownTab] = useState<CardTab>(tab.tab);
  const [expanded, setExpanded] = useState(tab.n > 0);
  const [stack, setStack] = useState<CardRoute[]>(() => {
    if (startOpen.startsWith("diff:")) return [{ kind: "diff", path: startOpen.slice(5) }];
    if (startOpen.startsWith("file:")) return [{ kind: "file", path: startOpen.slice(5) }];
    return [];
  });
  const [branch, setBranch] = useState<string | undefined>();
  const [gitState, setGitState] = useState<GitState | null>(null);
  const [changes, setChanges] = useState<WorkspaceChange[]>([]);
  const [hidden, setHidden] = useState(0);
  const [reread, setReread] = useState(0);
  const gitReadFor = useRef("");
  const known = useRef({ changes: 0, branch: false });

  // A tab picked (the review bar's Plans) opens it up; another agent closes it.
  useEffect(() => {
    if (tab.n === 0) return;
    setShownTab(tab.tab);
    setStack([]);
    setExpanded(true);
  }, [tab.tab, tab.n]);
  const firstSession = useRef(session.id);
  useEffect(() => {
    if (firstSession.current === session.id) return;
    firstSession.current = session.id;
    setStack([]);
    setExpanded(false);
    setBranch(undefined);
    setGitState(null);
    setChanges([]);
    setHidden(0);
    gitReadFor.current = "";
  }, [session.id]);

  // Again as the agent works: a turn changes files long before it ends. A
  // burst of activity asks once, a second after it settles. The branch is
  // read again when a turn begins or ends, or after a git action.
  useEffect(() => {
    let live = true;
    const wait = known.current.changes > 0 || known.current.branch ? 1000 : 0;
    const timer = window.setTimeout(async () => {
      const read = `${session.id}|${session.state}|${reread}`;
      if (gitReadFor.current !== read) {
        gitReadFor.current = read;
        const state = await git("git_status", session.id).catch(() => null);
        if (!live) return;
        setGitState(state);
        // A daemon from before the git actions names the branch here.
        const name = state ? state.branch : await workspace("list_commits", session.id).then((r) => r.branch, () => undefined);
        if (!live) return;
        setBranch(name);
        known.current.branch = !!name;
      }
      const listed = await workspace("list_changes", session.id).catch(() => null);
      if (!live) return;
      setChanges((now) => (JSON.stringify(now) === JSON.stringify(listed?.changes ?? []) ? now : listed?.changes ?? []));
      setHidden(listed?.hidden ?? 0);
      known.current.changes = listed?.changes?.length ?? 0;
    }, wait);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [session.id, session.state, session.lastActivityAt, reread]);

  const open = expanded || stack.length > 0;
  const back = () => (stack.length ? setStack(stack.slice(0, -1)) : setExpanded(false));

  // ⌘[ goes back through what was opened.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "[") {
        e.preventDefault();
        back();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const push = (route: CardRoute) => setStack((s) => [...s, route]);
  const added = changes.reduce((n, c) => n + (c.added ?? 0), 0);
  const removed = changes.reduce((n, c) => n + (c.removed ?? 0), 0);
  const siblings = sessions.filter((s) => s.cwd === session.cwd && s.id !== session.id && s.state !== "ended");
  const route = stack[stack.length - 1];

  return (
    <aside className={`workspace-card ${open ? "open" : ""}`}>
      {route ? (
        <>
          <CardHeader title={routeTitle(route)} back={back} />
          <div className="card-divider" />
          <div className="card-body" key={JSON.stringify(route)}>
            {route.kind === "diff" && <DiffLoader session={session} path={route.path} />}
            {route.kind === "file" && <FileLoader session={session} path={route.path} />}
            {route.kind === "folder" && <FileList session={session} path={route.path} open={push} />}
            {route.kind === "artifact" && <ArtifactView session={session} artifact={route.artifact} />}
          </div>
        </>
      ) : expanded ? (
        <>
          <CardHeader title={TAB_TITLE[shownTab]} back={back} />
          <div className="card-divider" />
          <div className="card-body">
            {shownTab === "changes" && <ChangesList session={session} open={push} />}
            {shownTab === "files" && <FileList session={session} path="" open={push} />}
            {shownTab === "history" && <HistoryPanel session={session} />}
            {shownTab === "plans" && <ArtifactList session={session} open={push} />}
          </div>
        </>
      ) : (
        <>
          <section className="card-section">
            <CardRow
              title={placeTitle()}
              icon={Monitor}
              sf="desktopcomputer"
              trailing={
                <button className="card-trailing-button" title={`Show the folder in ${fileManager()}`} onClick={() => void reveal(session.cwd)}>
                  <Icon icon={Folder} sf="folder" size={12} />
                </button>
              }
            />
            {!folderGone && (
              <>
                <CardRow title="Open in editor" icon={SquareArrowOutUpRight} sf="arrow.up.right.square" onClick={onEditor} />
                <CardRow title="Open terminal" icon={SquareTerminal} sf="terminal" onClick={onTerminal} />
              </>
            )}
          </section>
          <div className="card-divider" />
          <section className="card-section">
            {branch && (
              <>
                <CardRow title={branch} icon={GitBranch} sf="arrow.triangle.branch" />
                {/* Not from a daemon too old to read where the branch stands. */}
                {gitState && (
                  <GitRow session={session} state={{ ...gitState, changes: changes.length + hidden }} changes={changes} hidden={hidden}
                    changed={() => setReread((n) => n + 1)} />
                )}
              </>
            )}
            <CardRow title="History" icon={History} sf="clock.arrow.circlepath" onClick={() => showPanel("history")} />
            <CardRow
              title="Changes"
              icon={Diff}
              sf="plusminus"
              onClick={() => showPanel("diff")}
              trailing={added + removed > 0 ? (
                <span className="card-counts"><span className="ok">+{added}</span><span className="error-text">−{removed}</span></span>
              ) : undefined}
            />
            <CardRow title="Files" icon={Folder} sf="folder" onClick={() => showPanel("files")} />
            {(session.artifacts ?? 0) > 0 && (
              <CardRow
                title="Plans"
                icon={FileText}
                sf="doc.text"
                onClick={() => {
                  setShownTab("plans");
                  setExpanded(true);
                }}
                trailing={<span className="card-chevron"><Icon icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" /></span>}
              />
            )}
          </section>
          {siblings.length > 0 && (
            <>
              <div className="card-divider" />
              <section className="card-section">
                <div className="card-heading">In this project · {siblings.length}</div>
                {siblings.slice(0, 6).map((sibling) => (
                  <CardRow
                    key={sibling.id}
                    title={sibling.name}
                    glyph={<AgentMark kind={sibling.kind} size={14} />}
                    onClick={() => openAgent(sibling.id)}
                    trailing={<span className="card-dot-box"><span className="card-dot" style={{ background: dotColour(sibling.state) }} /></span>}
                  />
                ))}
              </section>
            </>
          )}
        </>
      )}
    </aside>
  );
}

function CardHeader({ title, back }: { title: string; back: () => void }) {
  return (
    <div className="card-header">
      <button className="card-back" title="Back" onClick={back}>
        <Icon icon={ChevronLeft} sf="chevron.left" size={12} weight="semibold" />
      </button>
      <span className="card-header-title">{nameOf(title)}</span>
    </div>
  );
}
