// The editor's file tree (EditorWindow.swift's FileTree, FileRow, FilterRow
// and TreeVim.swift's bars): the folder's files with what git says, the
// filter, the bars at its foot for a new file and the Trash, and the keys.

import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { FilePlus, FolderPlus, ListFilter, CircleX, X, CircleCheck } from "lucide-react";
import { ChromeButton, Icon } from "../ui";
import { contextMenu, type MenuEntry } from "../thread/menu";
import { reveal } from "../host";
import { FileTypeIcon } from "./fileIcons";
import { copyText, paths, type Entry } from "./fs";
import { MARK_LETTER, MARK_TINT, QUIET_FOLDERS, type EditorStore } from "./store";

export function FileTree({ store }: { store: EditorStore }) {
  const list = useRef<HTMLDivElement>(null);
  const filter = store.treeFilter?.trim() ?? "";

  useEffect(() => {
    store.focusTreeView = () => list.current?.focus();
    return () => {
      store.focusTreeView = null;
    };
  }, [store]);

  const menuFor = (entry: Entry | null): MenuEntry[] => {
    const folder = entry ? (entry.directory ? entry.path : paths.dir(entry.path)) : "";
    const items: MenuEntry[] = [
      { label: "New File…", action: () => askName(store, { kind: "file", folder, name: "" }) },
      { label: "New Folder…", action: () => askName(store, { kind: "folder", folder, name: "" }) },
    ];
    if (entry) {
      items.push(
        { separator: true },
        { label: "Rename…", action: () => askName(store, { kind: "rename", folder, entry, name: entry.name }) },
        { label: "Show in Finder", action: () => void reveal(store.url(entry.path)) },
        { label: "Copy Path", action: () => void copyText(store.url(entry.path)) },
        { label: "Copy Relative Path", action: () => void copyText(entry.path) },
        { separator: true },
        {
          label: "Move to Trash…",
          action: () => {
            store.trashing = [entry];
            store.focusTree();
          },
        },
      );
    }
    return items;
  };

  const rows = filter ? null : flatten(store);

  return (
    <div className="tree">
      <div className="tree-top">
        <div className="tree-head">
          <span className="tree-title">Files</span>
          <span style={{ flex: 1 }} />
          <ChromeButton icon={FilePlus} sf="doc.badge.plus" title="New file" onClick={() => askName(store, { kind: "file", folder: "", name: "" })} />
          <ChromeButton icon={FolderPlus} sf="folder.badge.plus" title="New folder" onClick={() => askName(store, { kind: "folder", folder: "", name: "" })} />
        </div>
        {store.treeFilter !== null && <TreeFilterBar store={store} />}
      </div>
      <div
        ref={list}
        className="tree-list"
        tabIndex={0}
        onFocus={() => {
          store.treeFocused = true;
        }}
        onBlur={() => {
          store.treeFocused = false;
        }}
        onContextMenu={(e) => contextMenu(e, menuFor(null))}
        onKeyDown={(e) => treeArrowKeys(store, e)}
      >
        {rows ? (
          <>
            <div className="tree-section">{store.name}</div>
            {rows.map(({ entry, depth }) => (
              <FileRow key={entry.path} store={store} entry={entry} depth={depth} onMenu={(e) => contextMenu(e, menuFor(entry))} />
            ))}
          </>
        ) : (
          <>
            <div className="tree-section">MATCHING “{filter}”</div>
            {store.treeMatches(filter).map((path) => (
              <FilterRow key={path} store={store} path={path} onMenu={(e) => contextMenu(e, menuFor(store.treeEntry(path)))} />
            ))}
          </>
        )}
      </div>
      {store.trashing.length > 0 ? (
        <TreeTrashBar store={store} />
      ) : store.creatingIn !== null ? (
        <TreeCreateBar key={store.creatingIn} store={store} folder={store.creatingIn} />
      ) : null}
      {store.showTreeKeys && (
        <div className="tree-keys-wrap">
          <TreeKeysSheet
            close={() => {
              store.showTreeKeys = false;
              store.changed();
            }}
          />
        </div>
      )}
    </div>
  );
}

/** The rows the tree shows, with how deep each is. */
function flatten(store: EditorStore) {
  const rows: { entry: Entry; depth: number }[] = [];
  const walk = (folder: string, depth: number) => {
    for (const entry of store.entries(folder)) {
      rows.push({ entry, depth });
      if (entry.directory && store.isExpanded(entry.path)) walk(entry.path, depth + 1);
    }
  };
  walk("", 0);
  return rows;
}

/** The arrow keys move through the rows, as an outline view's do; Vim's
 *  keys are the window's (keys.ts). */
function treeArrowKeys(store: EditorStore, e: ReactKeyboardEvent) {
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  const entry = store.currentTreeEntry;
  switch (e.key) {
    case "ArrowDown":
      store.moveTree(1);
      break;
    case "ArrowUp":
      store.moveTree(-1);
      break;
    case "ArrowRight":
      if (entry?.directory) store.setExpanded(entry.path, true);
      break;
    case "ArrowLeft":
      if (entry?.directory && store.isExpanded(entry.path)) store.setExpanded(entry.path, false);
      else if (entry && paths.dir(entry.path)) store.selectInTree(paths.dir(entry.path));
      break;
    default:
      return;
  }
  e.preventDefault();
}

export function askName(store: EditorStore, naming: NonNullable<EditorStore["naming"]>) {
  store.naming = naming;
  store.changed();
}

function Chevron({ open }: { open: boolean }) {
  return (
    <svg className={`tree-chevron ${open ? "open" : ""}`} width="9" height="9" viewBox="0 0 9 9" aria-hidden>
      <path d="M2.75 0.75 6.25 4.5 2.75 8.25" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function FileRow({ store, entry, depth, onMenu }: { store: EditorStore; entry: Entry; depth: number; onMenu: (e: React.MouseEvent) => void }) {
  const mark = entry.directory ? undefined : store.mark(entry.path);
  const quiet = entry.name.startsWith(".") || QUIET_FOLDERS.has(entry.name);
  const open = store.documents.find((d) => d.path === entry.path);
  const expanded = entry.directory && store.isExpanded(entry.path);
  const marked = store.marked.has(entry.path);
  const cut = !!store.treeClipboard?.cut && store.treeClipboard.paths.includes(entry.path);
  const color = marked ? "var(--working-text)" : mark ? MARK_TINT[mark] : quiet ? "var(--faint)" : "var(--text)";
  return (
    <div
      className={`tree-row ${store.selection === entry.path ? "selected" : ""}`}
      style={{ opacity: cut ? 0.45 : 1 }}
      onMouseDown={(e) => {
        if (e.button !== 0) return;
        store.select(entry.path);
      }}
      onClick={(e) => {
        // A folder opens or closes with a click; with the disclosure
        // chevron as well.
        if (entry.directory) store.setExpanded(entry.path, !store.isExpanded(entry.path));
        e.currentTarget.closest<HTMLElement>(".tree-list")?.focus();
      }}
      onContextMenu={(e) => {
        store.select(entry.path);
        onMenu(e);
      }}
    >
      <div className="tree-row-body" style={{ paddingLeft: 16 + depth * 12 }}>
        <span className="tree-disclosure">{entry.directory && <Chevron open={expanded} />}</span>
        <span className="tree-icon">
          <FileTypeIcon name={entry.name} directory={entry.directory} open={expanded} size={15} />
        </span>
        {store.renamingInTree === entry.path ? (
          <TreeRenameField store={store} entry={entry} />
        ) : (
          <span className="tree-name" style={{ color, textDecoration: mark === "deleted" ? "line-through" : undefined }}>
            {middleTruncated(entry.name)}
          </span>
        )}
        <span className="tree-gap" />
        {marked && (
          <span className="tree-marked" title="Marked: y, x and d act on every marked row">
            <Icon icon={CircleCheck} sf="checkmark.circle.fill" size={11} />
          </span>
        )}
        {open?.isDirty && <span className="tree-dirty" title="Unsaved changes" />}
        {mark ? (
          <span className="tree-mark" style={{ color: MARK_TINT[mark] }}>{MARK_LETTER[mark]}</span>
        ) : entry.directory && store.changedFolders.has(entry.path) ? (
          <span className="tree-folder-dot" title="Something inside changed" />
        ) : null}
      </div>
    </div>
  );
}

/** A name kept whole; CSS cuts it at the end when it doesn't fit. */
function middleTruncated(name: string) {
  return name;
}

/** A file the filter found: its name, and the folder it is in. */
function FilterRow({ store, path, onMenu }: { store: EditorStore; path: string; onMenu: (e: React.MouseEvent) => void }) {
  const folder = paths.dir(path);
  const name = paths.base(path);
  return (
    <div
      className={`tree-row ${store.selection === path ? "selected" : ""}`}
      title={path}
      onMouseDown={(e) => e.button === 0 && store.select(path)}
      onContextMenu={(e) => {
        store.select(path);
        onMenu(e);
      }}
    >
      <div className="tree-row-body" style={{ paddingLeft: 16 }}>
        <span className="tree-icon">
          <FileTypeIcon name={name} size={15} />
        </span>
        <span className="tree-name" style={{ color: store.marked.has(path) ? "var(--working-text)" : "var(--text)", flex: "none", maxWidth: "100%" }}>
          {name}
        </span>
        {folder && <span className="tree-filter-folder">{folder}</span>}
      </div>
    </div>
  );
}

/** / in the tree: a field above it that narrows it to the files that match. */
function TreeFilterBar({ store }: { store: EditorStore }) {
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  const down = () => {
    const first = store.visibleTreeRows()[0];
    if (first) store.selectInTree(first.path);
    store.focusTree();
  };
  const stop = () => {
    store.treeFilter = null;
    store.focusTree();
  };
  return (
    <div className="tree-filter">
      <span className="tree-filter-icon">
        <Icon icon={ListFilter} sf="line.3.horizontal.decrease" size={10.5} weight="semibold" />
      </span>
      <input
        ref={input}
        placeholder="Filter files"
        value={store.treeFilter ?? ""}
        spellCheck={false}
        onChange={(e) => {
          store.treeFilter = e.target.value;
          store.changed();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === "ArrowDown") {
            e.preventDefault();
            down();
          } else if (e.key === "Escape") {
            e.preventDefault();
            stop();
          }
          e.stopPropagation();
        }}
      />
      <button className="tree-filter-clear" title="Stop filtering (Escape)" onClick={stop}>
        <Icon icon={CircleX} sf="xmark.circle.fill" size={13} />
      </button>
    </div>
  );
}

/** a in the tree: the name of what to make, at the foot of the tree. */
function TreeCreateBar({ store, folder }: { store: EditorStore; folder: string }) {
  const [name, setName] = useState("");
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  return (
    <div className="tree-create">
      <div className="tree-create-title">New in {folder || store.name}</div>
      <input
        ref={input}
        className="tree-create-field"
        placeholder="name.ts, or folder/"
        value={name}
        spellCheck={false}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          e.stopPropagation();
          if (e.key === "Enter") {
            e.preventDefault();
            store.creatingIn = null;
            store.changed();
            void store.createInTree(name, folder).then((problem) => {
              if (problem) store.setProblem(problem);
              else if (name.trim().endsWith("/")) store.focusTree();
            });
          } else if (e.key === "Escape") {
            e.preventDefault();
            store.creatingIn = null;
            store.focusTree();
          }
        }}
      />
      <div className="tree-create-hint">End with / for a folder. Return makes it, Escape cancels.</div>
    </div>
  );
}

/** d in the tree: what goes to the Trash, answered with y or n. */
function TreeTrashBar({ store }: { store: EditorStore }) {
  const items = store.trashing;
  const answer = (key: string, title: string, destructive: boolean, action: () => void) => (
    <button className={`trash-answer ${destructive ? "destructive" : ""}`} onClick={action} aria-label={title}>
      <span className="trash-key">{key}</span>
      <span className="trash-title">{title}</span>
    </button>
  );
  return (
    <div className="tree-trash">
      <div className="tree-trash-question">
        {items.length === 1 ? `Move ${items[0].name} to the Trash?` : `Move ${items.length} items to the Trash?`}
      </div>
      {items.length > 1 && (
        <div className="tree-trash-list">
          {items.slice(0, 4).map((i) => i.name).join(", ") + (items.length > 4 ? ` and ${items.length - 4} more` : "")}
        </div>
      )}
      <div className="tree-trash-answers">
        {answer("y", "Move to Trash", true, () => {
          void store.trashConfirmed().then((p) => p && store.setProblem(p));
          store.focusTree();
        })}
        {answer("n", "Cancel", false, () => {
          store.trashing = [];
          store.focusTree();
        })}
      </div>
    </div>
  );
}

/** r in the tree: the row's name, editable where it is. */
function TreeRenameField({ store, entry }: { store: EditorStore; entry: Entry }) {
  const [name, setName] = useState(entry.name);
  const input = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => {
    const el = input.current;
    if (!el) return;
    el.focus();
    // The name without its extension selected, as Finder does.
    const stem = entry.directory ? entry.name : paths.stem(entry.name);
    el.setSelectionRange(0, (stem || entry.name).length);
  }, [entry]);
  const leave = () => {
    if (store.renamingInTree === entry.path) {
      store.renamingInTree = null;
      store.changed();
    }
  };
  return (
    <input
      ref={input}
      className="tree-rename"
      value={name}
      spellCheck={false}
      onChange={(e) => setName(e.target.value)}
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
      onBlur={leave}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Enter") {
          e.preventDefault();
          store.renamingInTree = null;
          void store.rename(entry, name).then((p) => {
            store.setProblem(p);
            store.focusTree();
          });
        } else if (e.key === "Escape") {
          e.preventDefault();
          store.renamingInTree = null;
          store.focusTree();
        }
      }}
    />
  );
}

const KEY_GROUPS: [string, [string, string][]][] = [
  ["Move", [
    ["j  k", "Down, up"],
    ["gg  G", "Top, bottom"],
    ["l  o", "Open the file, or the folder"],
    ["⏎", "Open the file; open or close the folder"],
    ["h", "Close the folder, or go to it"],
    ["/", "Filter the files"],
    ["⌃B  ⌃W l", "To the editor (⌃B, ⌃W h back)"],
  ]],
  ["Files", [
    ["a", "New file; end with / for a folder"],
    ["r", "Rename"],
    ["d", "Move to the Trash (y to confirm)"],
    ["y  c", "Copy"],
    ["x", "Cut, to move"],
    ["p", "Paste into the folder"],
    ["Y  gy", "Copy the relative, full path"],
    ["u  ⌃R", "Undo, redo (⌘Z, ⇧⌘Z too)"],
    ["␣  v", "Mark, for several at once"],
    ["Esc", "Clear the filter, marks, clipboard"],
  ]],
];

/** ?: the tree's keys, over it. */
function TreeKeysSheet({ close }: { close: () => void }) {
  return (
    <div className="tree-keys">
      <div className="tree-keys-head">
        <span>Tree keys</span>
        <button title="Close (? or Escape)" onClick={close}>
          <Icon icon={X} sf="xmark" size={10} weight="bold" />
        </button>
      </div>
      {KEY_GROUPS.map(([title, keys]) => (
        <div key={title} className="tree-keys-group">
          <div className="tree-keys-title">{title.toUpperCase()}</div>
          {keys.map(([key, what]) => (
            <div key={key} className="tree-keys-row">
              <span className="tree-keys-key">{key}</span>
              <span className="tree-keys-what">{what}</span>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}
