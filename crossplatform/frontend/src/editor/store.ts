// One folder in the editor (Editor/EditorStore.swift and TreeVim.swift): its
// tree, its files open in tabs, which one is in front, what git says has
// changed, the tree's Vim keys and their undo, and Search in Folder.

import { useSyncExternalStore } from "react";
import type { Session } from "@shared/protocol";
import { getPref, setPref } from "../prefs";
import { EditorDocument } from "./document";
import { copyText, files, paths, problemOf, type Entry, type SearchMatch, type SearchOptions, type SearchResults } from "./fs";
import { fuzzyFilter } from "./fuzzy";

export type GitMark = "modified" | "added" | "untracked" | "deleted" | "renamed" | "conflicted";

export const MARK_LETTER: Record<GitMark, string> = {
  modified: "M", added: "A", untracked: "U", deleted: "D", renamed: "R", conflicted: "!",
};

/** The CSS colour of a git mark (GitMark.tint). */
export const MARK_TINT: Record<GitMark, string> = {
  modified: "var(--git-modified)", renamed: "var(--git-modified)",
  added: "var(--ok)", untracked: "var(--ok)",
  deleted: "var(--error-text)", conflicted: "var(--error-text)",
};

/** What the agent panel shows. */
export type PanelRoute = { kind: "agents" } | { kind: "agentman" } | { kind: "agent"; id: string };

/** One thing a change in the tree did to the disk, enough to undo it. */
export type TreeStep = { kind: "moved"; from: string; to: string } | { kind: "made"; url: string };

export interface TreeChange {
  steps: TreeStep[];
  label: string;
}

export interface TreeClipboard {
  paths: string[];
  cut: boolean;
}

/** Asking for a name: a new file or folder, or a new name for one. */
export interface Naming {
  kind: "file" | "folder" | "rename";
  folder: string;
  entry?: Entry;
  name: string;
}

/** Folders that are heavy and rarely read, shown dimmed. */
export const QUIET_FOLDERS = new Set([
  "node_modules", ".build", "build", "dist", ".next", "DerivedData", "Pods", "target", "__pycache__", ".venv", "venv", ".expo",
]);

export const MAX_PANES = 4;

/** `git status --porcelain=v1 -z`, read. Paths come from the repository's
 *  root; prefix is where the folder sits in it. */
export function parseGitStatus(output: string, prefix: string) {
  const marks: Record<string, GitMark> = {};
  const untrackedFolders: string[] = [];
  const records = output.split("\0").filter((r) => r.length > 0);
  for (let i = 0; i < records.length; i++) {
    const record = records[i];
    if (record.length <= 3) continue;
    const x = record[0];
    const y = record[1];
    let path = record.slice(3);
    if (x === "R" || x === "C") i++;
    if (!path.startsWith(prefix)) continue;
    path = path.slice(prefix.length);
    let mark: GitMark;
    if (x === "?" && y === "?") {
      if (path.endsWith("/")) {
        untrackedFolders.push(path.slice(0, -1));
        continue;
      }
      mark = "untracked";
    } else if (x === "U" || y === "U" || (x === "A" && y === "A") || (x === "D" && y === "D")) {
      mark = "conflicted";
    } else if (x === "D" || y === "D") {
      mark = "deleted";
    } else if (x === "A") {
      mark = "added";
    } else if (x === "R" || y === "R") {
      mark = "renamed";
    } else {
      mark = "modified";
    }
    if (path) marks[path] = mark;
  }
  const changedFolders = new Set<string>();
  for (const path of [...Object.keys(marks), ...untrackedFolders]) {
    let folder = paths.dir(path);
    while (folder) {
      changedFolders.add(folder);
      folder = paths.dir(folder);
    }
  }
  for (const folder of untrackedFolders) changedFolders.add(folder);
  return { marks, untrackedFolders, changedFolders };
}

const sameEntries = (a: Entry[] | undefined, b: Entry[]) =>
  !!a && a.length === b.length && a.every((e, i) => e.path === b[i].path && e.directory === b[i].directory);

export class EditorStore {
  readonly root: string;
  readonly name: string;
  home = "";
  documents: EditorDocument[] = [];
  /** The splits, left to right, and the file each shows. */
  panes: (string | null)[] = [null];
  focusedPane = 0;
  /** Where the splits are across the editor, as the page measured them. */
  paneFrames: { x: number; width: number }[] = [];
  selection: string | null = null;
  expanded = new Set<string>();
  children: Record<string, Entry[]> = {};
  marks: Record<string, GitMark> = {};
  untrackedFolders: string[] = [];
  changedFolders = new Set<string>();
  branch: string | null = null;
  isRepository = false;
  quickOpen = false;
  searchOpen = false;
  searchQuery = "";
  searchOptions: SearchOptions = { caseSensitive: false, wholeWord: false, regex: false };
  searchResults: SearchResults | null = null;
  searchRunning = false;
  private searching: string | null = null;
  private searchID = 0;
  vimMode: string | null = null;
  vimKeys = "";
  showAgentPanel = getPref("agentPanelOpen", false);
  panelRoute: PanelRoute | null = null;
  showsFolderAgent = false;
  marked = new Set<string>();
  treeClipboard: TreeClipboard | null = null;
  creatingIn: string | null = null;
  renamingInTree: string | null = null;
  treeFilter: string | null = null;
  showTreeKeys = false;
  notice: string | null = null;
  trashing: Entry[] = [];
  treeKeys = "";
  movingInTree = false;
  treeUndo: TreeChange[] = [];
  treeRedo: TreeChange[] = [];
  private noticeTimer = 0;
  showTerminal = false;
  /** Bumped by New Terminal: a new shell in the panel. */
  terminalGeneration = 0;
  /** A tab asking whether to save its edits before it closes. */
  closing: EditorDocument | null = null;
  problem: string | null = null;
  naming: Naming | null = null;
  allFiles: string[] = [];
  private allFilesAt = 0;
  private indexFile: string | null = null;
  /** The tree has the keyboard. */
  treeFocused = false;
  /** Hooks the window sets: the page, and giving the tree the keyboard. */
  monaco: MonacoLike | null = null;
  focusTreeView: (() => void) | null = null;

  private listeners = new Set<() => void>();
  private version = 0;
  private scheduled = false;

  constructor(root: string) {
    this.root = root.replace(/\/+$/, "") || "/";
    this.name = paths.base(this.root) || this.root;
    void this.relist("");
    void files.home().then((home) => {
      this.home = home;
      this.changed();
    });
  }

  // MARK: - Watching

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getVersion = () => this.version;

  /** Something changed: the views draw again, once for many changes. */
  changed = () => {
    this.version += 1;
    if (this.scheduled) return;
    this.scheduled = true;
    queueMicrotask(() => {
      this.scheduled = false;
      for (const l of this.listeners) l();
    });
  };

  get active(): EditorDocument | undefined {
    const id = this.activeID;
    return this.documents.find((d) => d.id === id);
  }

  get activeID(): string | null {
    return this.panes[this.focusedPane] ?? null;
  }

  set activeID(id: string | null) {
    if (this.focusedPane >= this.panes.length) this.focusedPane = Math.max(this.panes.length - 1, 0);
    if (this.panes.length === 0) this.panes = [null];
    this.panes = this.panes.map((p, i) => (i === this.focusedPane ? id : p));
    this.changed();
  }

  get hasUnsaved() {
    return this.documents.some((d) => d.isDirty);
  }

  url(path: string) {
    return paths.join(this.root, path);
  }

  entries(folder: string): Entry[] {
    return this.children[folder] ?? [];
  }

  isExpanded(folder: string) {
    return this.expanded.has(folder);
  }

  setExpanded(folder: string, open: boolean) {
    if (open) {
      if (!this.children[folder]) void this.relist(folder);
      this.expanded.add(folder);
    } else {
      this.expanded.delete(folder);
    }
    this.changed();
  }

  /** Git's mark for a file, or for one inside a folder git hasn't seen. */
  mark(path: string): GitMark | undefined {
    const mark = this.marks[path];
    if (mark) return mark;
    return this.untrackedFolders.some((f) => path.startsWith(f + "/")) ? "untracked" : undefined;
  }

  // MARK: - Tabs

  /** Opens a file in a tab, or brings its tab to the front. */
  open(path: string) {
    this.activeID = this.openInBackground(path).id;
    this.reveal(path);
  }

  openInBackground(path: string): EditorDocument {
    const found = this.documents.find((d) => d.path === path && !d.readOnly);
    if (found) return found;
    const document = new EditorDocument(this.root, path, false, this.changed);
    this.documents = [...this.documents, document];
    if (this.activeID === null) this.activeID = document.id;
    void document.load().then(() => this.updateGitBases());
    this.changed();
    return document;
  }

  /** Opens any file, as Go to Definition does: one in the folder as usual,
   *  one from outside it read-only. */
  openFileAt(path: string) {
    const normal = paths.normalize(path);
    const inside = this.relativePath(normal);
    if (inside !== null) {
      this.open(inside);
      return;
    }
    const found = this.documents.find((d) => d.readOnly && d.url === normal);
    if (found) {
      this.activeID = found.id;
      return;
    }
    const document = new EditorDocument("/", normal.replace(/^\/+/, ""), true, this.changed);
    this.documents = [...this.documents, document];
    this.activeID = document.id;
    void document.load();
  }

  /** A file's path inside the folder, or null for one outside it. */
  relativePath(path: string): string | null {
    const normal = paths.normalize(path);
    if (!normal.startsWith(this.root + "/")) return null;
    return normal.slice(this.root.length + 1);
  }

  activate(document: EditorDocument) {
    this.activeID = document.id;
    if (!document.readOnly) this.reveal(document.path);
  }

  /** Shows a file in the tree: its folders open, its row selected. */
  reveal(path: string) {
    let folder = paths.dir(path);
    while (folder) {
      this.setExpanded(folder, true);
      folder = paths.dir(folder);
    }
    this.selection = path;
    this.changed();
  }

  /** Closes a tab, asking first when it has unsaved edits. */
  requestClose(document: EditorDocument) {
    if (document.isDirty) {
      this.closing = document;
      this.changed();
    } else {
      this.close(document);
    }
  }

  close(document: EditorDocument) {
    const index = this.documents.findIndex((d) => d.id === document.id);
    if (index < 0) return;
    this.documents = this.documents.filter((d) => d.id !== document.id);
    // A split that showed it shows another file no split shows, or closes.
    for (let pane = this.panes.length - 1; pane >= 0; pane--) {
      if (this.panes[pane] !== document.id) continue;
      const near = [...this.documents.keys()]
        .sort((a, b) => Math.abs(a - index) - Math.abs(b - index))
        .map((i) => this.documents[i].id)
        .find((id) => !this.panes.includes(id));
      if (near) {
        this.panes = this.panes.map((p, i) => (i === pane ? near : p));
      } else if (this.panes.length > 1) {
        this.closePane(pane);
      } else {
        const next = this.documents.length === 0 ? null : this.documents[Math.min(index, this.documents.length - 1)].id;
        this.panes = this.panes.map((p, i) => (i === pane ? next : p));
      }
    }
    if (this.closing?.id === document.id) this.closing = null;
    this.changed();
  }

  // MARK: - Splits

  split() {
    if (this.panes.length >= MAX_PANES) return;
    const panes = [...this.panes];
    panes.splice(this.focusedPane + 1, 0, this.activeID);
    this.panes = panes;
    this.focusedPane += 1;
    this.changed();
  }

  closePane(index: number) {
    if (this.panes.length <= 1 || index < 0 || index >= this.panes.length) return;
    this.panes = this.panes.filter((_, i) => i !== index);
    if (this.focusedPane >= index) this.focusedPane = Math.max(Math.min(this.focusedPane - 1, this.panes.length - 1), 0);
    this.changed();
  }

  focusPane(index: number) {
    if (index < 0 || index >= this.panes.length || index === this.focusedPane) return;
    this.focusedPane = index;
    this.changed();
  }

  closeOthers(document: EditorDocument) {
    for (const other of this.documents) if (other.id !== document.id && !other.isDirty) this.close(other);
    this.activeID = document.id;
  }

  closeSaved() {
    for (const document of this.documents) if (!document.isDirty) this.close(document);
  }

  /** Every tab: the saved ones at once, then each with unsaved edits asks. */
  closeAll() {
    this.closeSaved();
    const dirty = this.documents.find((d) => d.isDirty);
    if (dirty) {
      this.activeID = dirty.id;
      this.requestClose(dirty);
    }
  }

  openToSide(document: EditorDocument) {
    if (this.panes.length >= MAX_PANES) {
      this.activate(document);
      return;
    }
    this.split();
    this.activeID = document.id;
  }

  async saveActive() {
    await this.active?.save();
  }

  async saveAll() {
    for (const document of this.documents) if (document.isDirty) await document.save();
  }

  /** Vim's `:` commands that are the editor's. */
  async vimCommand(name: string, path?: string) {
    const active = this.active;
    switch (name) {
      case "save":
        await this.saveActive();
        break;
      case "close":
        if (active) this.requestClose(active);
        break;
      case "discard":
        if (active) this.close(active);
        break;
      case "saveClose":
        if (active && (!active.isDirty || (await active.save()))) this.close(active);
        break;
      case "saveAll":
        await this.saveAll();
        break;
      case "closeAll":
        this.closeAll();
        break;
      case "split":
        this.split();
        break;
      case "saveCloseAll":
        await this.saveAll();
        this.closeSaved();
        break;
      case "open": {
        if (!path) {
          this.quickOpen = true;
          this.changed();
          return;
        }
        const full = path.startsWith("/") ? path : path.startsWith("~") ? this.home + path.slice(1) : this.url(path);
        const [stat] = await files.stat([full]).catch(() => [undefined]);
        if (stat?.exists) this.openFileAt(full);
        else this.setProblem(`There's no file at ${path}.`);
        break;
      }
      case "goToFile":
        this.quickOpen = true;
        this.changed();
        break;
      case "tree":
        this.focusTree();
        break;
    }
  }

  // MARK: - Focus

  /** Puts the keyboard in the file tree, at the file in front. */
  focusTree() {
    if (this.selection === null || !this.entryExists(this.selection)) {
      this.selection = (this.active && !this.active.readOnly ? this.active.path : null) ?? this.entries("")[0]?.path ?? null;
    }
    this.focusTreeView?.();
    this.changed();
  }

  focusEditor() {
    this.monaco?.focus();
  }

  /** A link followed in a Markdown preview: a folder file opens in its tab,
   *  a folder shows in the tree; anything else opens outside. Answers
   *  whether it was handled here. */
  async followLink(href: string, from: EditorDocument): Promise<boolean> {
    const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(href)?.[1]?.toLowerCase();
    if (scheme && scheme !== "file") return false;
    let path = href.replace(/^file:\/\//i, "");
    path = path.split("#")[0].split("?")[0];
    try {
      path = decodeURIComponent(path);
    } catch {
      // Kept as written.
    }
    if (!path) return true;
    const target = scheme === "file" ? path : path.startsWith("/") ? this.url(path.slice(1)) : paths.join(paths.dir(from.url), path);
    const relative = this.relativePath(target);
    const [stat] = relative !== null ? await files.stat([paths.normalize(target)]).catch(() => [undefined]) : [undefined];
    if (relative === null || !stat?.exists) return true;
    if (stat.directory) {
      this.reveal(relative);
      this.setExpanded(relative, true);
      this.focusTree();
      return true;
    }
    this.open(relative);
    const opened = this.active;
    if (opened && !opened.previewing) {
      const showPreview = () => {
        if (opened.isMarkdown && !opened.previewing) this.togglePreview();
      };
      if (opened.loaded) showPreview();
      else void opened.load().then(showPreview);
    }
    return true;
  }

  /** Shows the Markdown file in front rendered, or its text again. */
  togglePreview() {
    const document = this.active;
    if (!document?.isMarkdown) return;
    document.previewing = !document.previewing;
    if (!document.previewing) this.focusEditor();
    this.changed();
  }

  private entryExists(path: string) {
    return this.entries(paths.dir(path)).some((e) => e.path === path);
  }

  // MARK: - Terminal

  newTerminal() {
    this.terminalGeneration += 1;
    this.showTerminal = true;
    this.changed();
  }

  toggleTerminal() {
    this.showTerminal = !this.showTerminal;
    this.changed();
  }

  setShowAgentPanel(open: boolean) {
    this.showAgentPanel = open;
    setPref("agentPanelOpen", open);
    this.changed();
  }

  /** Opens the panel on the agent working on the folder, when asked and the
   *  agents are known. */
  showFolderAgent(sessions: Session[], loaded: boolean) {
    if (!this.showsFolderAgent || !loaded) return;
    this.showsFolderAgent = false;
    const session = folderAgent(this.root, sessions);
    if (!session) return;
    this.panelRoute = { kind: "agent", id: session.id };
    this.setShowAgentPanel(true);
  }

  // MARK: - Alerts

  setProblem(problem: string | null) {
    this.problem = problem;
    this.changed();
  }

  // MARK: - Files

  async newFile(folder: string, name: string): Promise<string | null> {
    const path = this.childPath(folder, name);
    if (!path) return "That isn't a file name.";
    const [stat] = await files.stat([this.url(path)]);
    if (stat.exists) return `${name.trim()} is already there.`;
    try {
      await files.createFile(this.url(path));
    } catch {
      return `Couldn't create ${name}.`;
    }
    this.record({ steps: [{ kind: "made", url: this.url(path) }], label: `new ${name.trim()}` });
    await this.relist(folder);
    this.open(path);
    return null;
  }

  async newFolder(folder: string, name: string): Promise<string | null> {
    const path = this.childPath(folder, name);
    if (!path) return "That isn't a folder name.";
    try {
      await files.createFolder(this.url(path), false);
    } catch (error) {
      return problemOf(error);
    }
    this.record({ steps: [{ kind: "made", url: this.url(path) }], label: `new ${name.trim()}` });
    await this.relist(folder);
    this.setExpanded(path, true);
    this.selection = path;
    this.changed();
    return null;
  }

  async rename(entry: Entry, name: string): Promise<string | null> {
    const folder = paths.dir(entry.path);
    const path = this.childPath(folder, name);
    if (!path) return "That isn't a name.";
    if (path === entry.path) return null;
    try {
      await files.move(this.url(entry.path), this.url(path));
    } catch (error) {
      return problemOf(error);
    }
    this.record({ steps: [{ kind: "moved", from: this.url(entry.path), to: this.url(path) }], label: `rename ${entry.name}` });
    for (const document of this.documents) {
      if (document.path === entry.path || document.path.startsWith(entry.path + "/")) {
        document.moved(path + document.path.slice(entry.path.length), this.root);
      }
    }
    if (entry.directory && this.expanded.delete(entry.path)) this.expanded.add(path);
    await this.relist(folder);
    this.selection = path;
    this.changed();
    return null;
  }

  /** Moves one to the Trash, closing what was open from it, and says where
   *  it went, so undo can bring it back. */
  async moveToTrash(entry: Entry): Promise<TreeStep> {
    const url = this.url(entry.path);
    const trashed = await files.trash(url);
    for (const document of this.documents) {
      if (document.path === entry.path || document.path.startsWith(entry.path + "/")) this.close(document);
    }
    await this.relist(paths.dir(entry.path));
    return { kind: "moved", from: url, to: trashed || url };
  }

  /** Says what just happened in the status bar, for a moment. */
  flash(text: string) {
    this.notice = text;
    window.clearTimeout(this.noticeTimer);
    this.noticeTimer = window.setTimeout(() => {
      this.notice = null;
      this.changed();
    }, 2500);
    this.changed();
  }

  childPath(folder: string, name: string): string | null {
    const trimmed = name.trim();
    if (!trimmed || trimmed === "." || trimmed === ".." || trimmed.includes("/") || trimmed.includes(":")) return null;
    return folder ? `${folder}/${trimmed}` : trimmed;
  }

  async relist(folder: string) {
    const entries = await files.list(this.root, folder).catch(() => [] as Entry[]);
    if (!sameEntries(this.children[folder], entries)) {
      this.children = { ...this.children, [folder]: entries };
      this.changed();
    }
  }

  // MARK: - Keeping up with the disk

  /** Lists open folders again, asks git what changed, and checks each open
   *  file against the disk: agents change all three while it is open. */
  async refresh() {
    const folders = ["", ...[...this.expanded].sort()];
    const listed = await files.listMany(this.root, folders).catch(() => null);
    if (listed) {
      let children = this.children;
      for (const [folder, entries] of Object.entries(listed)) {
        if (!sameEntries(children[folder], entries)) children = { ...children, [folder]: entries };
      }
      if (children !== this.children) {
        this.children = children;
        this.changed();
      }
    }

    const prefix = await files.git(this.root, ["rev-parse", "--show-prefix"]);
    if (prefix.ok) {
      this.isRepository = true;
      const status = await files.git(this.root, ["status", "--porcelain=v1", "-z", "--untracked-files=normal"]);
      const parsed = parseGitStatus(status.ok ? status.output : "", prefix.output.trim());
      if (JSON.stringify(parsed.marks) !== JSON.stringify(this.marks)) this.marks = parsed.marks;
      if (parsed.untrackedFolders.join("\0") !== this.untrackedFolders.join("\0")) this.untrackedFolders = parsed.untrackedFolders;
      if ([...parsed.changedFolders].sort().join("\0") !== [...this.changedFolders].sort().join("\0")) this.changedFolders = parsed.changedFolders;
      const head = await files.git(this.root, ["rev-parse", "--abbrev-ref", "HEAD"]);
      const branch = head.ok ? head.output.trim() : null;
      if (branch !== this.branch) this.branch = branch;
      this.changed();
    } else if (this.isRepository) {
      this.isRepository = false;
      this.marks = {};
      this.branch = null;
      this.changed();
    }

    const open = this.documents.filter((d) => d.loaded);
    if (open.length) {
      const stats = await files.stat(open.map((d) => d.url)).catch(() => [] as undefined[]);
      await Promise.all(open.map((d, i) => d.checkDisk(stats[i])));
    }
    await this.updateGitBases();
  }

  /** Reads each open file as git's index has it, for the bars beside the
   *  line numbers, when the index, the file's name or its mark changed. */
  async updateGitBases() {
    const open = this.documents.filter((d) => d.loaded && d.kind === "text" && !d.readOnly);
    if (!open.length) return;
    if (!this.isRepository) {
      for (const document of open) {
        document.gitBaseKey = "none";
        document.setGitBase(null);
      }
      return;
    }
    if (this.indexFile === null) {
      const index = await files.git(this.root, ["rev-parse", "--git-path", "index"]);
      const path = index.output.trim();
      if (index.ok && path) this.indexFile = path.startsWith("/") ? path : paths.normalize(this.url(path));
    }
    const [stat] = this.indexFile ? await files.stat([this.indexFile]).catch(() => [undefined]) : [undefined];
    const changed = stat?.modified ?? "0";
    for (const document of open) {
      const mark = this.mark(document.path);
      const key = `${changed}|${document.path}|${mark ? MARK_LETTER[mark] : ""}`;
      let read: Promise<string | null>;
      if (document.gitBaseKey === key) {
        if (!document.gitBaseRead) continue;
        read = document.gitBaseRead;
      } else {
        document.gitBaseKey = key;
        const path = document.path;
        read = mark === "untracked"
          ? Promise.resolve("")
          : files.git(this.root, ["show", ":./" + path]).then((r) => (r.ok ? r.output : null));
        document.gitBaseRead = read;
      }
      const base = await read;
      if (document.gitBaseKey !== key) continue;
      document.setGitBase(base);
      if (document.gitBaseRead === read) document.gitBaseRead = null;
    }
  }

  /** Every file in the folder, for Go to File, the filter and search. */
  async loadAllFiles() {
    if (Date.now() - this.allFilesAt < 10_000 && this.allFiles.length) return;
    this.allFiles = await files.allFiles(this.root).catch(() => []);
    this.allFilesAt = Date.now();
    this.changed();
  }

  // MARK: - Search in Folder

  showSearch() {
    this.quickOpen = false;
    this.searchOpen = true;
    this.changed();
  }

  /** Searches the folder, unless that search is done or under way. */
  runSearch(query: string) {
    const options = { ...this.searchOptions };
    const key = JSON.stringify([query, options]);
    this.searchQuery = query;
    const results = this.searchResults;
    if (results && JSON.stringify([results.query, results.options]) === key) return;
    if (this.searchRunning && this.searching === key) return;
    if (!query) {
      this.searchResults = null;
      this.searchRunning = false;
      this.searching = null;
      this.changed();
      return;
    }
    this.searchRunning = true;
    this.searching = key;
    const id = ++this.searchID;
    const edited: Record<string, string> = {};
    for (const document of this.documents) if (document.isDirty && !document.readOnly) edited[document.path] = document.text;
    this.changed();
    void (async () => {
      await this.loadAllFiles();
      if (this.searching !== key || id !== this.searchID) return;
      const found = await files.search(this.root, query, options, this.allFiles, edited, id).catch((error) => {
        return { query, options, files: [], total: 0, truncated: false, problem: problemOf(error), cancelled: false } as SearchResults;
      });
      if (this.searching !== key || id !== this.searchID || found.cancelled) return;
      this.searchResults = found;
      this.searchRunning = false;
      this.changed();
    })();
  }

  setSearchOptions(options: SearchOptions) {
    this.searchOptions = options;
    this.changed();
    this.runSearch(this.searchQuery);
  }

  /** Opens a match's file with the match selected. */
  openMatch(match: SearchMatch) {
    this.searchOpen = false;
    this.open(match.path);
    const document = this.active;
    if (document) this.monaco?.reveal(document.id, match.line, match.column, match.length);
    this.changed();
  }

  // MARK: - The tree's rows

  /** The rows the tree shows, top to bottom, or what the filter found. */
  visibleTreeRows(): Entry[] {
    const filter = this.treeFilter?.trim();
    if (filter) return this.treeMatches(filter).map((path) => ({ path, name: paths.base(path), directory: false }));
    const rows: Entry[] = [];
    const walk = (folder: string) => {
      for (const entry of this.entries(folder)) {
        rows.push(entry);
        if (entry.directory && this.isExpanded(entry.path)) walk(entry.path);
      }
    };
    walk("");
    return rows;
  }

  treeMatches(filter: string): string[] {
    return fuzzyFilter(filter, this.allFiles, 200);
  }

  /** The entry for a row's path, whether or not its folder is listed. */
  async treeEntryOnDisk(path: string): Promise<Entry | null> {
    const listed = this.treeEntry(path);
    if (listed) return listed;
    const [stat] = await files.stat([this.url(path)]).catch(() => [undefined]);
    return stat?.exists ? { path, name: paths.base(path), directory: stat.directory } : null;
  }

  /** The entry for a row's path among the listed ones; a filter's match
   *  that isn't listed is a file. */
  treeEntry(path: string): Entry | null {
    const found = this.entries(paths.dir(path)).find((e) => e.path === path);
    if (found) return found;
    if (this.allFiles.includes(path)) return { path, name: paths.base(path), directory: false };
    return null;
  }

  /** Selecting a row: a file opens, unless the keys moved over it. */
  select(path: string | null) {
    if (this.selection === path) return;
    this.selection = path;
    this.changed();
    if (this.movingInTree) {
      this.movingInTree = false;
      return;
    }
    if (!path) return;
    const entry = this.treeEntry(path);
    if (!entry || entry.directory) return;
    if (this.active?.path !== path) this.open(path);
  }

  // MARK: - The tree's Vim keys

  /** Acts on a key pressed in the tree; false leaves it to the tree. */
  handleTreeKey(e: KeyboardEvent): boolean {
    const ctrl = e.ctrlKey && !e.metaKey && !e.altKey;
    const plain = !e.ctrlKey && !e.metaKey && !e.altKey;
    const key = e.key;
    const pending = this.treeKeys;
    this.treeKeys = "";
    if (this.trashing.length) {
      if (plain) {
        if (key === "y" || key === "Enter") void this.trashConfirmed().then((p) => p && this.setProblem(p));
        else if (key === "n" || key === "Escape") {
          this.trashing = [];
          this.changed();
        }
      }
      return true;
    }
    if (ctrl && !e.shiftKey) {
      const k = key.toLowerCase();
      if (k === "b") {
        this.focusEditor();
        return true;
      }
      if (k === "w") {
        this.treeKeys = "<C-w>";
        return true;
      }
      if (k === "r") {
        void this.redoTreeChange();
        return true;
      }
      return false;
    }
    if (!plain) return false;
    if (pending === "<C-w>") {
      if (key === "l") this.focusEditor();
      return true;
    }
    if (pending === "g") {
      if (key === "g") this.moveTreeTo(0);
      else if (key === "y") void this.copyTreePaths(false);
      return true;
    }
    if (key === "Enter") {
      this.toggleTreeRow();
      return true;
    }
    if (key === "Escape") return this.clearTreeState();
    switch (key) {
      case "j": this.moveTree(1); break;
      case "k": this.moveTree(-1); break;
      case "G": this.moveTreeTo(Number.MAX_SAFE_INTEGER); break;
      case "g": this.treeKeys = "g"; break;
      case "l": case "o": this.openTreeRow(true); break;
      case "h": this.closeTreeRow(); break;
      case "/": this.treeFilter = this.treeFilter ?? ""; void this.loadAllFiles(); break;
      case "a": this.creatingIn = this.targetFolder; break;
      case "r": {
        const entry = this.currentTreeEntry;
        if (entry) this.renamingInTree = entry.path;
        break;
      }
      case "d": void this.askToTrash(); break;
      case "y": case "c": void this.takeForTree(false); break;
      case "x": void this.takeForTree(true); break;
      case "p": void this.pasteInTree().then((p) => p && this.setProblem(p)); break;
      case "Y": void this.copyTreePaths(true); break;
      case " ": case "v": this.toggleMark(); break;
      case "u": void this.undoTreeChange(); break;
      case "?": this.showTreeKeys = !this.showTreeKeys; break;
      default: return false;
    }
    this.changed();
    return true;
  }

  get currentTreeEntry(): Entry | null {
    return this.selection ? this.treeEntry(this.selection) : null;
  }

  /** Where a new file or a paste goes: the folder under the cursor, or the
   *  one around the file under it. */
  get targetFolder(): string {
    const entry = this.currentTreeEntry;
    if (!entry) return "";
    return entry.directory ? entry.path : paths.dir(entry.path);
  }

  /** Moves the cursor without opening what it lands on. */
  selectInTree(path: string | null) {
    if (this.selection === path) return;
    this.movingInTree = true;
    this.select(path);
    this.movingInTree = false;
    requestAnimationFrame(() => document.querySelector(".tree-row.selected")?.scrollIntoView({ block: "nearest" }));
  }

  moveTree(step: number) {
    const rows = this.visibleTreeRows();
    if (!rows.length) return;
    const index = this.selection ? rows.findIndex((r) => r.path === this.selection) : -1;
    const next = index >= 0 ? Math.min(Math.max(index + step, 0), rows.length - 1) : step > 0 ? 0 : rows.length - 1;
    this.selectInTree(rows[next].path);
  }

  moveTreeTo(index: number) {
    const rows = this.visibleTreeRows();
    if (!rows.length) return;
    this.selectInTree(rows[Math.min(index, rows.length - 1)].path);
  }

  /** l and o: a folder opens (or the cursor goes into it); a file opens in
   *  a tab, which takes the keyboard. */
  openTreeRow(focus: boolean) {
    const entry = this.currentTreeEntry;
    if (!entry) return this.moveTreeTo(0);
    if (entry.directory) {
      const first = this.entries(entry.path)[0];
      if (this.isExpanded(entry.path) && first) this.selectInTree(first.path);
      else this.setExpanded(entry.path, true);
      return;
    }
    this.open(entry.path);
    if (this.treeFilter !== null) this.treeFilter = null;
    if (focus) setTimeout(() => this.focusEditor(), 0);
  }

  toggleTreeRow() {
    const entry = this.currentTreeEntry;
    if (!entry || !entry.directory) return this.openTreeRow(true);
    this.setExpanded(entry.path, !this.isExpanded(entry.path));
  }

  closeTreeRow() {
    const entry = this.currentTreeEntry;
    if (!entry) return;
    if (entry.directory && this.isExpanded(entry.path)) {
      this.setExpanded(entry.path, false);
    } else {
      const parent = paths.dir(entry.path);
      if (parent) this.selectInTree(parent);
    }
  }

  /** Escape: the keys sheet, the filter, the marks, then what was taken. */
  clearTreeState(): boolean {
    if (this.showTreeKeys) this.showTreeKeys = false;
    else if (this.treeFilter !== null) this.treeFilter = null;
    else if (this.marked.size) this.marked = new Set();
    else if (this.treeClipboard) this.treeClipboard = null;
    else return false;
    this.changed();
    return true;
  }

  toggleMark() {
    const path = this.selection;
    if (!path) return;
    const marked = new Set(this.marked);
    if (!marked.delete(path)) marked.add(path);
    this.marked = marked;
    this.moveTree(1);
  }

  /** The marked rows, or the one under the cursor. */
  get treeTargets(): string[] {
    return this.marked.size === 0 ? (this.selection ? [this.selection] : []) : [...this.marked].sort();
  }

  /** a: makes what the name says. A name ending in / is a folder; one with
   *  / inside makes the folders on the way. */
  async createInTree(input: string, folder: string): Promise<string | null> {
    const name = input.trim();
    if (!name) return null;
    const isFolder = name.endsWith("/");
    const parts = name.split("/").filter(Boolean);
    if (!parts.length || parts.some((p) => p === "." || p === ".." || p.includes(":"))) return "That isn't a name.";
    const relative = [folder, ...parts].filter(Boolean).join("/");
    const url = this.url(relative);
    const [here] = await files.stat([url]);
    if (here.exists) return `${parts.join("/")} is already there.`;
    // The first folder on the way that isn't there yet: undo takes it away.
    let made = url;
    for (;;) {
      const parent = paths.dir(made);
      if (parent === this.root) break;
      const [stat] = await files.stat([parent]);
      if (stat.exists) break;
      made = parent;
    }
    try {
      if (isFolder) {
        await files.createFolder(url, true);
      } else {
        await files.createFolder(paths.dir(url), true);
        await files.createFile(url);
      }
    } catch (error) {
      return problemOf(error);
    }
    this.record({ steps: [{ kind: "made", url: made }], label: `new ${parts[parts.length - 1] ?? name}` });
    let opened = folder;
    await this.relist(folder);
    for (const part of parts.slice(0, -1)) {
      opened = opened ? `${opened}/${part}` : part;
      await this.relist(opened);
      this.setExpanded(opened, true);
    }
    if (folder) this.setExpanded(folder, true);
    if (isFolder) {
      this.setExpanded(relative, true);
      this.selectInTree(relative);
    } else {
      this.selectInTree(relative);
      this.open(relative);
    }
    return null;
  }

  /** d: asks at the foot of the tree. */
  async askToTrash() {
    const entries = await Promise.all(this.treeTargets.map((p) => this.treeEntryOnDisk(p)));
    this.trashing = entries.filter((e): e is Entry => !!e);
    this.changed();
  }

  async trashConfirmed(): Promise<string | null> {
    const entries = this.trashing;
    this.trashing = [];
    this.changed();
    const problems: string[] = [];
    const steps: TreeStep[] = [];
    for (const entry of entries) {
      try {
        steps.push(await this.moveToTrash(entry));
      } catch (error) {
        problems.push(problemOf(error));
      }
    }
    if (steps.length) {
      this.record({ steps, label: entries.length === 1 ? `trash ${entries[0].name}` : `trash ${entries.length} items` });
    }
    const marked = new Set(this.marked);
    for (const entry of entries) marked.delete(entry.path);
    this.marked = marked;
    if (!problems.length && entries.length > 1) this.flash(`Moved ${entries.length} items to the Trash`);
    this.changed();
    return problems[0] ?? null;
  }

  /** y, c and x: takes the marked rows, or the one under the cursor, and
   *  puts them on the system clipboard too, as Finder would. */
  async takeForTree(cut: boolean) {
    const targets = this.treeTargets;
    if (!targets.length) return;
    this.treeClipboard = { paths: targets, cut };
    this.marked = new Set();
    const urls = targets.map((p) => this.url(p));
    if (!(await files.copyFiles(urls).catch(() => false))) await copyText(urls.join("\n")).catch(() => undefined);
    const what = targets.length === 1 ? paths.base(targets[0]) : `${targets.length} items`;
    this.flash(cut ? `Cut ${what}: p moves it` : `Copied ${what}: p pastes it`);
  }

  /** p: puts what was taken into the folder under the cursor. */
  async pasteInTree(): Promise<string | null> {
    const clipboard = this.treeClipboard;
    if (!clipboard) return null;
    const folder = this.targetFolder;
    let landed: string | null = null;
    const steps: TreeStep[] = [];
    const count = clipboard.paths.length;
    const finish = () => {
      if (steps.length) {
        const what = count === 1 ? paths.base(clipboard.paths[0]) : `${count} items`;
        this.record({ steps, label: (clipboard.cut ? "move " : "paste ") + what });
      }
    };
    for (const path of clipboard.paths) {
      const source = this.url(path);
      const [exists] = await files.stat([source]);
      if (!exists.exists) continue;
      const name = paths.base(path);
      if (clipboard.cut) {
        const destination = folder ? `${folder}/${name}` : name;
        if (destination === path) continue;
        if (destination.startsWith(path + "/")) {
          finish();
          return `${name} can't go inside itself.`;
        }
        const [there] = await files.stat([this.url(destination)]);
        if (there.exists) {
          finish();
          return `${name} is already in ${folder || this.name}.`;
        }
        try {
          await files.move(source, this.url(destination));
        } catch (error) {
          finish();
          return problemOf(error);
        }
        steps.push({ kind: "moved", from: source, to: this.url(destination) });
        for (const document of this.documents) {
          if (document.path === path || document.path.startsWith(path + "/")) {
            document.moved(destination + document.path.slice(path.length), this.root);
          }
        }
        if (this.expanded.has(path)) {
          this.setExpanded(path, false);
          this.setExpanded(destination, true);
        }
        await this.relist(paths.dir(path));
        landed = destination;
      } else {
        const destination = await this.freeName(name, folder);
        try {
          await files.copy(source, this.url(destination));
        } catch (error) {
          finish();
          return problemOf(error);
        }
        steps.push({ kind: "made", url: this.url(destination) });
        landed = destination;
      }
    }
    finish();
    await this.relist(folder);
    if (folder) this.setExpanded(folder, true);
    if (landed) this.selectInTree(landed);
    const items = count === 1 ? "1 item" : `${count} items`;
    this.flash(clipboard.cut ? `Moved ${items}` : `Pasted ${items}`);
    if (clipboard.cut) this.treeClipboard = null;
    this.changed();
    return null;
  }

  /** A name for a copy that is free in the folder: "name copy.ext", then
   *  "name copy 2.ext". */
  async freeName(name: string, folder: string): Promise<string> {
    const join = (file: string) => (folder ? `${folder}/${file}` : file);
    const exists = async (file: string) => (await files.stat([this.url(join(file))]))[0].exists;
    if (!(await exists(name))) return join(name);
    const ext = paths.ext(name);
    const dotfile = name.startsWith(".") && !name.slice(1).includes(".");
    const base = !ext || dotfile ? name : name.slice(0, name.length - ext.length - 1);
    const suffix = base === name ? "" : `.${ext}`;
    for (let number = 1; ; number++) {
      const candidate = base + (number === 1 ? " copy" : ` copy ${number}`) + suffix;
      if (!(await exists(candidate))) return join(candidate);
    }
  }

  /** Y and gy: the path of the marked rows or the one under the cursor. */
  async copyTreePaths(relative: boolean) {
    const targets = this.treeTargets;
    if (!targets.length) return;
    await copyText(targets.map((p) => (relative ? p : this.url(p))).join("\n")).catch(() => undefined);
    this.flash(targets.length === 1 ? `Copied ${relative ? "relative " : ""}path` : `Copied ${targets.length} paths`);
  }

  // MARK: - Undo

  record(change: TreeChange) {
    this.treeUndo.push(change);
    if (this.treeUndo.length > 100) this.treeUndo.shift();
    this.treeRedo = [];
  }

  async undoTreeChange() {
    const change = this.treeUndo.pop();
    if (!change) return this.flash("Nothing to undo");
    try {
      this.treeRedo.push(await this.reverse(change));
      this.flash(`Undid ${change.label}`);
    } catch (error) {
      this.setProblem(`Couldn't undo ${change.label}: ${problemOf(error)}`);
    }
  }

  async redoTreeChange() {
    const change = this.treeRedo.pop();
    if (!change) return this.flash("Nothing to redo");
    try {
      this.treeUndo.push(await this.reverse(change));
      this.flash(`Redid ${change.label}`);
    } catch (error) {
      this.setProblem(`Couldn't redo ${change.label}: ${problemOf(error)}`);
    }
  }

  /** Undoes a change's steps, last first, and returns what that did: a move
   *  goes back; something made goes to the Trash, from where a redo brings
   *  it back. */
  private async reverse(change: TreeChange): Promise<TreeChange> {
    const done: TreeStep[] = [];
    let landed: string | null = null;
    for (const step of [...change.steps].reverse()) {
      if (step.kind === "moved") {
        if (await files.inTrash(step.from)) {
          const into = (await files.trash(step.to)) || step.from;
          this.followMove(step.to, into);
          done.push({ kind: "moved", from: step.to, to: into });
          continue;
        }
        const [there] = await files.stat([step.from]);
        if (there.exists) throw new Error(`“${paths.base(step.from)}” couldn’t be moved because an item with the same name already exists.`);
        await files.createFolder(paths.dir(step.from), true);
        await files.move(step.to, step.from);
        this.followMove(step.to, step.from);
        done.push({ kind: "moved", from: step.to, to: step.from });
        landed = step.from;
      } else {
        const to = (await files.trash(step.url)) || step.url;
        this.followMove(step.url, to);
        done.push({ kind: "moved", from: step.url, to });
      }
    }
    await Promise.all(Object.keys(this.children).map((folder) => this.relist(folder)));
    const relative = landed ? this.relativePath(landed) : null;
    if (relative) {
      let folder = paths.dir(relative);
      while (folder) {
        this.setExpanded(folder, true);
        folder = paths.dir(folder);
      }
      this.selectInTree(relative);
    }
    return { steps: done, label: change.label };
  }

  /** Open tabs follow their files: to the new place, or closed when they
   *  leave the folder. */
  private followMove(from: string, to: string) {
    const old = this.relativePath(from);
    if (old === null) return;
    const next = this.relativePath(to);
    for (const document of this.documents) {
      if (document.path === old || document.path.startsWith(old + "/")) {
        if (next !== null) document.moved(next + document.path.slice(old.length), this.root);
        else this.close(document);
      }
    }
  }
}

/** What the page offers the store: EditorStore.monaco's calls. */
export interface MonacoLike {
  focus(): void;
  run(action: string): void;
  reveal(id: string, line: number, column: number, length: number): void;
  restartLanguage(id: string): void;
}

/** The agent working on a folder (AgentPanel.agent(for:in:)): one running
 *  in it, inside it or around it; one that needs the user first, then one
 *  at work, then the folder's own, then the latest to do anything. */
export function folderAgent(folder: string, sessions: Session[]): Session | undefined {
  const here = paths.normalize(folder);
  const related = sessions.filter((s) => {
    const cwd = paths.normalize(s.cwd);
    return s.state !== "ended" && (cwd === here || cwd.startsWith(here + "/") || here.startsWith(cwd + "/"));
  });
  const rank = (state: string) => (state === "waiting_input" ? 0 : state === "busy" ? 1 : state === "idle" ? 2 : 3);
  const time = (s: Session) => s.lastActivityAt ?? 0;
  return [...related].sort((a, b) => {
    if (rank(a.state) !== rank(b.state)) return rank(a.state) - rank(b.state);
    const aHere = paths.normalize(a.cwd) === here;
    const bHere = paths.normalize(b.cwd) === here;
    if (aHere !== bHere) return aHere ? -1 : 1;
    return time(b) - time(a);
  })[0];
}

/** Draws again whenever the store changes. */
export function useEditor(store: EditorStore): number {
  return useSyncExternalStore(store.subscribe, store.getVersion);
}
