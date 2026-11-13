// The Files tab (RightPanel.swift's FilesPanel): the whole project as a
// tree, as the editor has it, with what git says changed; a search across
// every file; a file opens in the editor.

import { useCallback, useEffect, useState } from "react";
import { ChevronDown, ChevronRight, CircleX, FoldVertical, RotateCw, Search } from "lucide-react";
import type { Session, WorkspaceEntry } from "@shared/protocol";
import { openEditor, reveal } from "../host";
import { contextMenu } from "../thread/menu";
import { Icon } from "../ui";
import { ask, workspace } from "../workspace/api";
import { fileManager } from "../workspace/place";
import { copyText } from "./clipboard";
import { compareNames, FileTypeIcon, folderOf, nameOf } from "./files";
import { PanelButton } from "./PanelButton";
import { Sym } from "./Sym";

interface Entry {
  path: string;
  name: string;
  directory: boolean;
}

interface Mark {
  letter: string;
  tint: string;
}

/** git's word for a file, as the editor's tree marks it (EditorStore's GitMark). */
function markFor(status: string): Mark {
  if (status === "??" || status === "untracked") return { letter: "U", tint: "var(--ok)" };
  if (status.includes("U") || status === "AA" || status === "DD") return { letter: "!", tint: "var(--error-text)" };
  if (status.includes("D")) return { letter: "D", tint: "var(--error-text)" };
  if (status.includes("A")) return { letter: "A", tint: "var(--ok)" };
  if (status.includes("R")) return { letter: "R", tint: "var(--git-modified)" };
  return { letter: "M", tint: "var(--git-modified)" };
}

// What each folder's tree had open, kept while the app runs, as the editor
// keeps it.
const openFolders = new Map<string, Set<string>>();

export function FilesPanel({ session }: { session: Session }) {
  const root = session.cwd;
  const [children, setChildren] = useState<Record<string, Entry[]>>({});
  const [expanded, setExpanded] = useState<Set<string>>(() => openFolders.get(root) ?? new Set());
  const [marks, setMarks] = useState<Record<string, Mark>>({});
  const [query, setQuery] = useState("");
  const [matches, setMatches] = useState<string[]>([]);

  useEffect(() => {
    openFolders.set(root, expanded);
  }, [root, expanded]);

  const list = useCallback(async (folder: string): Promise<Entry[]> => {
    const result = await workspace("list_files", session.id, { path: folder }).catch(() => null);
    return (result?.entries ?? [])
      .filter((e: WorkspaceEntry) => e.name !== ".git" && e.name !== ".DS_Store")
      .map((e) => ({ path: folder ? `${folder}/${e.name}` : e.name, name: e.name, directory: e.directory }))
      .sort((a, b) => (a.directory !== b.directory ? (a.directory ? -1 : 1) : compareNames(a.name, b.name)));
  }, [session.id]);

  const reload = useCallback(async () => {
    const folders = ["", ...expanded];
    const listed = await Promise.all(folders.map(list));
    setChildren(Object.fromEntries(folders.map((f, i) => [f, listed[i]])));
    const changes = await workspace("list_changes", session.id).catch(() => null);
    const next: Record<string, Mark> = {};
    for (const change of changes?.changes ?? []) next[change.path.replace(/\/$/, "")] = markFor(change.status);
    setMarks(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list, session.id]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // The search: the daemon's best matches for the words typed.
  useEffect(() => {
    const filter = query.trim();
    if (!filter) {
      setMatches([]);
      return;
    }
    let live = true;
    const timer = window.setTimeout(async () => {
      const reply = await ask({ type: "find_files", sessionId: session.id, text: filter }).catch(() => null);
      if (live) setMatches(((reply?.files as string[] | undefined) ?? []).slice(0, 200));
    }, 120);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [query, session.id]);

  const changedFolders = new Set<string>();
  for (const path of Object.keys(marks)) {
    for (let folder = folderOf(path); folder; folder = folderOf(folder)) changedFolders.add(folder);
  }
  const markOf = (path: string): Mark | undefined => {
    if (marks[path]) return marks[path];
    // A file inside a folder git hasn't seen.
    for (let folder = folderOf(path); folder; folder = folderOf(folder)) {
      if (marks[folder]?.letter === "U") return marks[folder];
    }
    return undefined;
  };

  const toggle = async (entry: Entry) => {
    const next = new Set(expanded);
    if (next.has(entry.path)) next.delete(entry.path);
    else {
      next.add(entry.path);
      if (!children[entry.path]) {
        const listed = await list(entry.path);
        setChildren((c) => ({ ...c, [entry.path]: listed }));
      }
    }
    setExpanded(next);
  };

  const activate = (entry: Entry) => {
    if (entry.directory) void toggle(entry);
    else void openEditor({ folder: root, file: entry.path, sessionId: session.id });
  };

  const rows: { entry: Entry; depth: number }[] = [];
  const add = (folder: string, depth: number) => {
    for (const entry of children[folder] ?? []) {
      rows.push({ entry, depth });
      if (entry.directory && expanded.has(entry.path)) add(entry.path, depth + 1);
    }
  };
  add("", 0);

  const row = (entry: Entry, depth: number, folder?: string) => {
    const mark = entry.directory ? undefined : markOf(entry.path);
    const open = entry.directory && expanded.has(entry.path);
    return (
      <button
        key={entry.path}
        className="files-row"
        style={{ paddingLeft: depth * 14 + 4 }}
        title={entry.path}
        onClick={() => activate(entry)}
        onContextMenu={(e) => contextMenu(e, [
          { label: "Copy Path", action: () => void copyText(`${root}/${entry.path}`) },
          { label: "Copy Relative Path", action: () => void copyText(entry.path) },
          { label: `Show in ${fileManager()}`, action: () => void reveal(`${root}/${entry.path}`) },
        ])}
      >
        <span className="files-chevron">
          {entry.directory && <Icon icon={open ? ChevronDown : ChevronRight} sf={open ? "chevron.down" : "chevron.right"} size={8} weight="semibold" />}
        </span>
        <FileTypeIcon name={entry.name} directory={entry.directory} open={open} size={14} />
        <span className="files-name" style={mark ? { color: mark.tint } : undefined}>{entry.name}</span>
        {folder && <span className="files-folder"><bdi>{folder}</bdi></span>}
        <span className="list-spacer" />
        {mark ? (
          <span className="files-mark" style={{ color: mark.tint }}>{mark.letter}</span>
        ) : entry.directory && changedFolders.has(entry.path) ? (
          <span className="files-dot" />
        ) : null}
      </button>
    );
  };

  return (
    <div className="panel-pane">
      <div className="panel-toolbar files-toolbar">
        <span className="search-field">
          <Sym icon={Search} sf="magnifyingglass" size={11} />
          <input value={query} placeholder="Search files" onChange={(e) => setQuery(e.target.value)} spellCheck={false} />
          {query && (
            <button className="search-clear" title="Clear" onClick={() => setQuery("")}>
              <Sym icon={CircleX} sf="xmark.circle.fill" size={11} />
            </button>
          )}
        </span>
        <PanelButton sf="arrow.clockwise" icon={RotateCw} help="Refresh" onClick={() => void reload()} />
        <PanelButton sf="rectangle.compress.vertical" icon={FoldVertical} help="Collapse all folders" onClick={() => setExpanded(new Set())} />
      </div>
      <div className="panel-divider" />
      <div className="files-rows">
        {query.trim()
          ? matches.map((path) => row({ path, name: nameOf(path), directory: false }, 0, folderOf(path)))
          : rows.map(({ entry, depth }) => row(entry, depth))}
      </div>
    </div>
  );
}
