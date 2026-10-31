// A file open in the editor (Editor/EditorDocument.swift): its text as
// edited and as last saved, where the cursor is, and whether the file
// changed on disk under it, as it does whenever an agent edits it.

import { files, paths, problemOf, type FileKind } from "./fs";

/** A language server's state, as the status bar shows it. */
export interface LanguageStatus {
  name: string;
  language: string;
  /** starting, ready, restarting, failed or stopped. */
  state: string;
  detail: string;
}

export interface Problems {
  errors: number;
  warnings: number;
}

/** One edit from the editor: `length` UTF-16 units at `offset` replaced. */
export interface Change {
  offset: number;
  length: number;
  text: string;
}

/** A language server's edit, in lines and UTF-16 characters from 0. */
export interface LineEdit {
  startLine: number;
  startCharacter: number;
  endLine: number;
  endCharacter: number;
  text: string;
}

export const MARKDOWN = new Set(["md", "markdown", "mdown", "mkd", "mdx"]);

let nextID = 0;

export class EditorDocument {
  readonly id = `doc-${Date.now().toString(36)}-${++nextID}`;
  /** The file's path inside the folder; for one from outside it, its path from /. */
  path: string;
  /** A file from outside the folder (a dependency): shown, never edited. */
  readonly readOnly: boolean;
  /** Its absolute path. */
  url: string;
  kind: FileKind = "text";
  /** Read from the disk yet. */
  loaded = false;
  text = "";
  saved = "";
  /** Bumped whenever the text is replaced from outside the editor. */
  revision = 0;
  modified = "";
  changedOnDisk = false;
  deletedOnDisk = false;
  line = 1;
  column = 1;
  indent = "    ";
  crlf = false;
  problem: string | null = null;
  /** Bumped by each save, so the page can tell the language server. */
  saves = 0;
  language: LanguageStatus | null = null;
  problems: Problems = { errors: 0, warnings: 0 };
  /** The file as git's index has it: null for a file git doesn't follow,
   *  "" for one it hasn't seen. undefined until read. */
  gitBase: string | null | undefined = undefined;
  gitBaseVersion = 0;
  gitBaseKey: string | null = null;
  gitBaseRead: Promise<string | null> | null = null;
  previewing = false;

  constructor(
    root: string,
    path: string,
    readOnly: boolean,
    private readonly changed: () => void,
  ) {
    this.path = path;
    this.readOnly = readOnly;
    this.url = paths.join(root, path);
  }

  get isDirty() {
    return this.kind === "text" && this.text !== this.saved;
  }

  get name() {
    return paths.base(this.url);
  }

  get isMarkdown() {
    return this.kind === "text" && MARKDOWN.has(paths.ext(this.url).toLowerCase());
  }

  setGitBase(base: string | null) {
    if (base === this.gitBase && this.gitBaseVersion !== 0) return;
    this.gitBase = base;
    this.gitBaseVersion += 1;
    this.changed();
  }

  /** The file moved, by a rename in the tree. */
  moved(path: string, root: string) {
    this.path = path;
    this.url = paths.join(root, path);
    this.changed();
  }

  private loading: Promise<void> | null = null;

  /** Resolves once the file has been read the first time. */
  whenLoaded(): Promise<void> {
    if (this.loaded) return Promise.resolve();
    return this.loading ?? this.load();
  }

  load(): Promise<void> {
    this.loading = this.read();
    return this.loading;
  }

  private async read() {
    const file = await files.read(this.url).catch(() => null);
    this.loaded = true;
    if (!file || file.kind === "missing") {
      this.kind = "missing";
      this.changed();
      return;
    }
    this.modified = file.modified;
    this.changedOnDisk = false;
    this.deletedOnDisk = false;
    this.kind = file.kind;
    if (file.kind === "text") {
      this.text = file.text;
      this.saved = file.text;
      this.crlf = file.text.includes("\r\n");
      this.indent = indentUnit(file.text, this.url);
      this.revision += 1;
    }
    this.changed();
  }

  /** Writes the text, keeping the file's permissions and links. */
  async save(): Promise<boolean> {
    if (this.kind !== "text" || this.readOnly) return false;
    const text = this.text;
    try {
      const modified = await files.write(this.url, text);
      this.saved = text;
      this.modified = modified;
      this.changedOnDisk = false;
      this.deletedOnDisk = false;
      this.problem = null;
      this.saves += 1;
      this.changed();
      return true;
    } catch (error) {
      this.problem = `Couldn't save ${this.name}: ${problemOf(error)}`;
      this.changed();
      return false;
    }
  }

  /** Takes the editor's edits, last first. False, leaving the text as it
   *  was, when they don't fit it: then the editor's copy is fetched whole. */
  apply(changes: Change[], length: number): boolean {
    let edited = this.text;
    for (const change of changes) {
      if (change.offset < 0 || change.length < 0 || change.offset + change.length > edited.length) return false;
      edited = edited.slice(0, change.offset) + change.text + edited.slice(change.offset + change.length);
    }
    if (edited.length !== length) return false;
    this.text = edited;
    this.changed();
    return true;
  }

  /** A language server's edits to a file not in front, as a rename across
   *  files makes; it is then unsaved, for the user to look over. */
  applyLineEdits(edits: LineEdit[]): boolean {
    if (this.kind !== "text" || this.readOnly) return false;
    const text = this.text;
    const starts = [0];
    for (let i = 0; i < text.length; i++) if (text.charCodeAt(i) === 10) starts.push(i + 1);
    const offset = (line: number, character: number): number | null => {
      if (line < 0 || character < 0 || line >= starts.length) return null;
      let end = line + 1 < starts.length ? starts[line + 1] - 1 : text.length;
      if (end > starts[line] && text.charCodeAt(end - 1) === 13 && line + 1 < starts.length) end -= 1;
      return Math.min(starts[line] + character, end);
    };
    const changes: { index: number; change: Change }[] = [];
    for (const [index, edit] of edits.entries()) {
      const start = offset(edit.startLine, edit.startCharacter);
      const end = offset(edit.endLine, edit.endCharacter);
      if (start === null || end === null || end < start) return false;
      changes.push({ index, change: { offset: start, length: end - start, text: edit.text } });
    }
    changes.sort((a, b) => (a.change.offset !== b.change.offset ? b.change.offset - a.change.offset : b.index - a.index));
    const length = changes.reduce((n, c) => n + c.change.text.length - c.change.length, text.length);
    return this.apply(changes.map((c) => c.change), length);
  }

  /** The editor's whole text, when its edits and this copy fell apart. */
  adopt(shown: string) {
    if (this.text === this.saved) this.saved = shown;
    this.text = shown;
    this.changed();
  }

  /** Back to the text as last saved. */
  revert() {
    if (!this.isDirty) return;
    this.text = this.saved;
    this.revision += 1;
    this.changed();
  }

  /** Takes the version on disk, dropping unsaved edits. */
  reloadFromDisk() {
    void this.load();
  }

  /** Keeps the edits, so a save writes over what changed on disk. */
  async keepMine() {
    this.changedOnDisk = false;
    const [stat] = await files.stat([this.url]).catch(() => [undefined]);
    if (stat?.exists) this.modified = stat.modified;
    this.changed();
  }

  /** Looks at the file on disk: one without unsaved edits follows it, one
   *  with them says it changed. `stat` is the disk's answer, asked for all
   *  open files at once. */
  async checkDisk(stat: { exists: boolean; modified: string } | undefined) {
    if (!this.loaded || this.kind === "missing" || !stat) return;
    if (!stat.exists) {
      if (!this.deletedOnDisk) {
        this.deletedOnDisk = true;
        this.changed();
      }
      return;
    }
    if (this.deletedOnDisk) {
      this.deletedOnDisk = false;
      this.changed();
    }
    if (stat.modified === this.modified) return;
    if (this.isDirty) {
      if (!this.changedOnDisk) {
        this.changedOnDisk = true;
        this.changed();
      }
      return;
    }
    if (this.kind === "text") {
      const file = await files.read(this.url).catch(() => null);
      if (file?.kind === "text" && file.text === this.text) {
        this.modified = stat.modified;
        return;
      }
    }
    await this.load();
  }

  /** The language's name, for the status bar. */
  get languageName(): string {
    switch (paths.ext(this.url).toLowerCase()) {
      case "swift": return "Swift";
      case "go": return "Go";
      case "ts": case "mts": case "cts": return "TypeScript";
      case "tsx": return "TypeScript React";
      case "js": case "mjs": case "cjs": return "JavaScript";
      case "jsx": return "JavaScript React";
      case "py": return "Python";
      case "rs": return "Rust";
      case "rb": return "Ruby";
      case "java": return "Java";
      case "kt": case "kts": return "Kotlin";
      case "c": case "h": return "C";
      case "cpp": case "cc": case "hpp": return "C++";
      case "m": case "mm": return "Objective-C";
      case "md": case "markdown": return "Markdown";
      case "json": return "JSON";
      case "yaml": case "yml": return "YAML";
      case "toml": return "TOML";
      case "sh": case "bash": case "zsh": return "Shell";
      case "css": return "CSS";
      case "scss": return "SCSS";
      case "html": case "htm": return "HTML";
      case "xml": case "plist": return "XML";
      case "sql": return "SQL";
      default: {
        const name = this.name.toLowerCase();
        if (name === "dockerfile") return "Dockerfile";
        if (name === "makefile") return "Makefile";
        return "Plain Text";
      }
    }
  }
}

/** What a file's lines are indented with: tabs, or the smallest step of
 *  spaces, 2 or 4; for a file with no indented lines, its language's. */
export function indentUnit(text: string, path: string): string {
  let tabs = 0;
  const steps: Record<number, number> = {};
  let count = 0;
  for (const line of text.split("\n")) {
    if (line.length === 0) continue;
    count += 1;
    if (count > 2000) break;
    if (line.startsWith("\t")) {
      tabs += 1;
    } else if (line.startsWith(" ")) {
      let spaces = 0;
      while (line[spaces] === " ") spaces++;
      if (line.length <= spaces) continue;
      if (spaces % 4 === 0) steps[4] = (steps[4] ?? 0) + 1;
      if (spaces % 2 === 0 && spaces % 4 !== 0) steps[2] = (steps[2] ?? 0) + 1;
    }
  }
  const spaced = (steps[2] ?? 0) + (steps[4] ?? 0);
  if (tabs > spaced) return "\t";
  if (spaced > 0) return (steps[2] ?? 0) > 0 ? "  " : "    ";
  switch (paths.ext(path).toLowerCase()) {
    case "js": case "jsx": case "ts": case "tsx": case "mjs": case "cjs": case "json": case "yaml": case "yml":
    case "css": case "scss": case "html": case "rb":
      return "  ";
    case "go": case "make":
      return "\t";
    default:
      return "    ";
  }
}
