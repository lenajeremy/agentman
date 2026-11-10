// The editor page and the conversation with it (Editor/MonacoView.swift's
// MonacoController). The page is the Mac app's, unchanged, in an iframe;
// scripts/host-shim.js on its side turns WKWebView's message handlers into
// postMessage. The store keeps the files: it opens them, saves them and
// follows the disk. The page holds the live text and reports every edit, so
// a document's text is always what a save writes.

import { isDark, onAppearance } from "../terminal/theme";
import { daemon } from "../daemon";
import { editorTheme } from "../editorTheme";
import { popupMenu, type MenuEntry } from "../thread/menu";
import type { EditorDocument, LineEdit } from "./document";
import { copyText, files, onFilesChanged, readText } from "./fs";
import type { EditorStore, MonacoLike } from "./store";

/** A text file open in a tab, as the page needs it. */
interface Tab {
  id: string;
  path: string;
  revision: number;
  indent: string;
  readOnly: boolean;
  saves: number;
  base: number;
}

const tabOf = (d: EditorDocument): Tab => ({
  id: d.id, path: d.path, revision: d.revision, indent: d.indent, readOnly: d.readOnly, saves: d.saves, base: d.gitBaseVersion,
});

const tabSize = (indent: string) => (indent === "\t" ? 4 : Math.max(indent.length, 1));

const fileURI = (path: string) => "file://" + path.split("/").map(encodeURIComponent).join("/");

export class MonacoBridge implements MonacoLike {
  private frame: HTMLIFrameElement | null = null;
  private ready = false;
  private opened = new Map<string, Tab>();
  private layoutIDs: (string | null)[] = [];
  private layoutFront = 0;
  private pendingReveal: { id: string; line: number; column: number; length: number } | null = null;
  private fontSize = 0;
  private vim: boolean | null = null;
  private wanted = { fontSize: 14, vim: false };
  private calls = new Map<number, (value: unknown) => void>();
  private nextCall = 0;
  private languages = 0;
  private watching = 0;
  private stopWatching: (() => void) | null = null;
  // Light or dark as the app is: the Appearance setting, else the computer.
  private stopAppearance: () => void;

  constructor(private readonly store: EditorStore) {
    window.addEventListener("message", this.onMessage);
    this.stopAppearance = onAppearance(this.sendTheme);
  }

  dispose() {
    window.removeEventListener("message", this.onMessage);
    this.stopAppearance();
    window.clearTimeout(this.languages);
    window.clearInterval(this.watching);
    this.stopWatching?.();
  }

  attach(frame: HTMLIFrameElement | null) {
    this.frame = frame;
  }

  private get shown(): string | null {
    return this.layoutIDs[this.layoutFront] ?? null;
  }

  // MARK: - Calls into the page

  private call<T = unknown>(name: string, ...args: unknown[]): Promise<T | null> {
    const win = this.frame?.contentWindow;
    if (!win) return Promise.resolve(null);
    const id = ++this.nextCall;
    return new Promise((resolve) => {
      this.calls.set(id, resolve as (value: unknown) => void);
      win.postMessage({ channel: "editorCall", id, name, args }, "*");
    });
  }

  private sendTheme = () => {
    if (!this.ready) return;
    void this.call("configure", { theme: editorTheme(isDark()) });
  };

  // MARK: - Keeping the page in step

  /** Brings the page up to what the store has: the tabs, the splits, the
   *  text's size and Vim. */
  sync(fontSize: number, vim: boolean) {
    this.wanted = { fontSize, vim };
    if (!this.ready) return;
    const store = this.store;
    if (fontSize !== this.fontSize) {
      this.fontSize = fontSize;
      void this.call("configure", { fontSize });
    }
    if (vim !== this.vim) {
      this.vim = vim;
      void this.call("configure", { vim });
    }
    const documents = store.documents.filter((d) => d.loaded && d.kind === "text");
    const tabs = documents.map(tabOf);
    const ids = new Set(tabs.map((t) => t.id));
    for (const id of [...this.opened.keys()]) {
      if (ids.has(id)) continue;
      void this.call("close", id);
      this.opened.delete(id);
      this.layoutIDs = this.layoutIDs.map((l) => (l === id ? null : l));
    }
    for (const [i, tab] of tabs.entries()) {
      const document = documents[i];
      const was = this.opened.get(tab.id);
      if (was) {
        if (was.path !== tab.path) void this.call("rename", tab.id, fileURI(document.url));
        if (was.revision !== tab.revision) {
          const revision = tab.revision;
          void this.call<number>("setText", tab.id, document.text, revision).then((length) => this.check(document, length, revision));
        }
        if (was.indent !== tab.indent) void this.call("setOptions", tab.id, tabSize(tab.indent), tab.indent !== "\t");
        if (was.saves !== tab.saves) void this.call("saved", tab.id);
        if (was.base !== tab.base) this.sendBase(document);
      } else {
        const revision = tab.revision;
        const doc = {
          id: tab.id, uri: fileURI(document.url), text: document.text,
          tabSize: tabSize(tab.indent), insertSpaces: tab.indent !== "\t", gen: revision, readOnly: tab.readOnly,
        };
        void this.call<number>("open", doc).then((length) => this.check(document, length, revision));
        if (tab.base > 0) this.sendBase(document);
      }
      this.opened.set(tab.id, tab);
    }
    const layout = store.panes.map((pane) => (pane && ids.has(pane) ? pane : null));
    const front = Math.min(Math.max(store.focusedPane, 0), Math.max(layout.length - 1, 0));
    if (layout.join("|") !== this.layoutIDs.join("|") || front !== this.layoutFront) {
      const moved = (layout[front] ?? null) !== this.shown || front !== this.layoutFront;
      this.layoutIDs = layout;
      this.layoutFront = front;
      void this.call("layout", layout, front);
      // A tab brought to the front takes the keyboard.
      if (moved && this.shown) this.frame?.focus();
    }
    this.revealPending();
  }

  private sendBase(document: EditorDocument) {
    void this.call("setBase", document.id, document.gitBase ?? null);
  }

  /** The page answers an opened or replaced text with its length; another
   *  length means it evened out mixed line endings: its text is kept. */
  private check(document: EditorDocument, length: number | null, revision: number) {
    if (typeof length !== "number" || length < 0 || length === document.text.length) return;
    this.refetch(document, revision);
  }

  private refetch(document: EditorDocument, revision: number) {
    void this.call<string>("text", document.id).then((text) => {
      if (typeof text === "string" && document.revision === revision) document.adopt(text);
    });
  }

  // MARK: - Commands

  reveal(id: string, line: number, column: number, length: number) {
    this.pendingReveal = { id, line, column, length };
    this.revealPending();
  }

  private revealPending() {
    const pending = this.pendingReveal;
    if (!this.ready || !pending || this.shown !== pending.id || !this.opened.has(pending.id)) return;
    this.pendingReveal = null;
    void this.call("reveal", pending.id, pending.line, pending.column, pending.length);
    this.focus();
  }

  focus() {
    if (!this.ready) return;
    this.frame?.focus();
    void this.call("focus");
  }

  run(action: string) {
    if (!this.ready) return;
    this.frame?.focus();
    void this.call("run", action);
  }

  restartLanguage(id: string) {
    if (this.ready) void this.call("restartLanguage", id);
  }

  // MARK: - Language servers

  /** Asks am serve where the language servers are, and keeps asking every
   *  10 s while it isn't there. */
  private connectLanguages(delay = 0) {
    window.clearTimeout(this.languages);
    this.languages = window.setTimeout(async () => {
      try {
        const reply = await daemon.request({ type: "lsp_endpoint" });
        const endpoint = reply.lsp as { url?: string; token?: string } | undefined;
        if (endpoint?.url && endpoint.token && this.ready) {
          void this.call("connect", endpoint.url, endpoint.token, this.store.root);
          this.watch();
          return;
        }
      } catch {
        // am serve isn't there yet.
      }
      this.connectLanguages(10_000);
    }, delay);
  }

  /** Tells the servers of files changed under the folder. */
  private watch() {
    if (this.stopWatching) return;
    void files.watch(this.store.root);
    this.watching = window.setInterval(() => void files.watch(this.store.root), 10_000);
    this.stopWatching = onFilesChanged(this.store.root, (changes) => {
      if (this.ready) void this.call("filesChanged", changes);
    });
  }

  // MARK: - The page's messages

  private onMessage = (e: MessageEvent) => {
    if (!this.frame || e.source !== this.frame.contentWindow) return;
    const data = e.data ?? {};
    if (data.channel === "editorReturn") {
      this.calls.get(data.id)?.(data.result);
      this.calls.delete(data.id);
    } else if (data.channel === "editor") {
      this.received(data.body ?? {});
    } else if (data.channel === "editorAsk") {
      void this.asked(data.body ?? {}).then((result) => {
        this.frame?.contentWindow?.postMessage({ channel: "editorAnswer", id: data.id, result }, "*");
      });
    }
  };

  private document(id: unknown) {
    return this.store.documents.find((d) => d.id === id);
  }

  private received(body: Record<string, unknown>) {
    const store = this.store;
    switch (body.type) {
      case "ready":
        this.ready = true;
        this.opened.clear();
        this.layoutIDs = [];
        this.layoutFront = 0;
        this.fontSize = 0;
        this.vim = null;
        this.sendTheme();
        this.sync(this.wanted.fontSize, this.wanted.vim);
        this.connectLanguages();
        store.changed();
        break;
      case "changes": {
        const document = this.document(body.id);
        const gen = body.gen as number;
        const raw = body.changes as { offset: number; length: number; text: string }[] | undefined;
        if (!document || typeof gen !== "number" || !Array.isArray(raw) || typeof body.length !== "number") return;
        // Edits made before the text was replaced from outside are gone from the page too.
        if (gen !== document.revision) return;
        if (!document.apply(raw, body.length)) this.refetch(document, gen);
        break;
      }
      case "pane":
        if (typeof body.index !== "number") return;
        this.layoutFront = body.index;
        store.focusPane(body.index);
        break;
      case "closePane":
        if (typeof body.index === "number") store.closePane(body.index);
        break;
      case "panes": {
        const frames = body.frames as { x: number; width: number }[] | undefined;
        if (Array.isArray(frames)) {
          store.paneFrames = frames;
          store.changed();
        }
        break;
      }
      case "cursor": {
        const document = this.document(body.id);
        if (!document || typeof body.line !== "number" || typeof body.column !== "number") return;
        document.line = body.line;
        document.column = body.column;
        store.changed();
        break;
      }
      case "menu":
        if (typeof body.x === "number" && typeof body.y === "number" && Array.isArray(body.items)) {
          this.showMenu(body.items as MenuItem[], body.x, body.y);
        }
        break;
      case "language": {
        const document = this.document(body.id);
        if (!document || typeof body.name !== "string" || typeof body.state !== "string") return;
        const status = { name: body.name, language: String(body.language ?? ""), state: body.state, detail: String(body.detail ?? "") };
        const was = document.language;
        if (!was || was.name !== status.name || was.state !== status.state || was.detail !== status.detail || was.language !== status.language) {
          document.language = status;
          store.changed();
        }
        break;
      }
      case "problems": {
        const document = this.document(body.id);
        if (!document) return;
        const errors = Number(body.errors ?? 0);
        const warnings = Number(body.warnings ?? 0);
        if (document.problems.errors !== errors || document.problems.warnings !== warnings) {
          document.problems = { errors, warnings };
          store.changed();
        }
        break;
      }
      case "open":
        // Go to Definition, into another file.
        if (typeof body.path === "string") store.openFileAt(body.path);
        break;
      case "vim":
        store.vimMode = typeof body.mode === "string" ? body.mode : null;
        store.vimKeys = "";
        store.changed();
        break;
      case "vimKeys":
        store.vimKeys = typeof body.keys === "string" ? body.keys : "";
        store.changed();
        break;
      case "command":
        if (typeof body.name === "string") void store.vimCommand(body.name, typeof body.path === "string" ? body.path : undefined);
        break;
      case "clipboard":
        // A Vim yank, on the system clipboard too.
        if (typeof body.text === "string") void copyText(body.text).catch(() => undefined);
        break;
      case "lspClosed":
        // am serve went away, or restarted: find it again.
        this.connectLanguages(2000);
        break;
      case "log":
        console.warn("[editor page]", body.message);
        break;
    }
  }

  /** What the page asks and waits for: a file's text, or edits to files
   *  that aren't open. */
  private async asked(body: Record<string, unknown>): Promise<unknown> {
    if (body.type === "read" && typeof body.path === "string") return this.read(body.path);
    if (body.type === "edit" && Array.isArray(body.files)) return this.edit(body.files as EditFile[]);
    return null;
  }

  /** A file's text, as the editor has it when it's open, else from disk. */
  private async read(path: string): Promise<string | null> {
    const open = this.store.documents.find((d) => d.url === path && d.kind === "text" && d.loaded);
    if (open) return open.text;
    const file = await files.read(path).catch(() => null);
    return file?.kind === "text" ? file.text : null;
  }

  /** A language server's edits to files that aren't open, a rename's: they
   *  open in tabs behind, unsaved, for the user to save. */
  private async edit(list: EditFile[]): Promise<boolean> {
    const targets: [EditorDocument, LineEdit[]][] = [];
    for (const file of list) {
      const inside = typeof file.path === "string" ? this.store.relativePath(file.path) : null;
      if (inside === null || !Array.isArray(file.edits)) return false;
      const edits: LineEdit[] = [];
      for (const edit of file.edits) {
        const { start, end } = edit.range ?? {};
        if (!start || !end || typeof edit.newText !== "string") return false;
        edits.push({ startLine: start.line, startCharacter: start.character, endLine: end.line, endCharacter: end.character, text: edit.newText });
      }
      targets.push([this.store.openInBackground(inside), edits]);
    }
    await Promise.all(targets.map(([d]) => d.whenLoaded()));
    return targets.every(([d, edits]) => d.applyLineEdits(edits));
  }

  /** The right-click menu, the system's, from what the page says applies. */
  private showMenu(items: MenuItem[], x: number, y: number) {
    const entries: MenuEntry[] = [];
    for (const item of items) {
      if (item.id === "-") {
        entries.push({ separator: true });
        continue;
      }
      const id = item.id;
      const action = id === "cut" || id === "copy" || id === "paste"
        ? () => void this.clipboardAction(id)
        : () => this.run(id);
      entries.push({ label: item.title ?? id, action, disabled: item.enabled === false });
    }
    if (!entries.length) return;
    const box = this.frame?.getBoundingClientRect();
    popupMenu(entries, (box?.left ?? 0) + x, (box?.top ?? 0) + y);
  }

  /** Cut, Copy and Paste from the menu, as the Edit menu's would do them:
   *  the selection through the system's clipboard. */
  private async clipboardAction(id: "cut" | "copy" | "paste") {
    this.frame?.focus();
    if (id === "paste") {
      const text = await readText();
      if (text) void this.call("replaceSelection", text);
      return;
    }
    const selected = await this.call<string>("selection");
    if (!selected) return;
    await copyText(selected).catch(() => undefined);
    if (id === "cut") void this.call("replaceSelection", "");
  }
}

interface MenuItem {
  id: string;
  title?: string;
  enabled?: boolean;
}

interface EditFile {
  path?: string;
  edits?: { range?: { start?: { line: number; character: number }; end?: { line: number; character: number } }; newText?: string }[];
}
