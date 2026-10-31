// The editor's way to the disk: the shell's EditorFiles service
// (crossplatform/editorfs.go), as the Mac app's editor uses FileManager and
// git. Called by name, so the generated bindings stay as they are.

import { Call, Clipboard, Events } from "@wailsio/runtime";

export interface Entry {
  path: string;
  name: string;
  directory: boolean;
}

export type FileKind = "text" | "image" | "binary" | "tooLarge" | "missing";

export interface ReadFile {
  kind: FileKind;
  text: string;
  /** When it last changed, in nanoseconds, as a string; "" when missing. */
  modified: string;
  size: number;
}

export interface Stat {
  exists: boolean;
  directory: boolean;
  modified: string;
}

export interface GitResult {
  ok: boolean;
  output: string;
}

export interface SearchOptions {
  caseSensitive: boolean;
  wholeWord: boolean;
  regex: boolean;
}

export interface SearchRange {
  location: number;
  length: number;
}

export interface SearchMatch {
  path: string;
  line: number;
  column: number;
  length: number;
  preview: string;
  highlights: SearchRange[];
  count: number;
}

export interface SearchResults {
  query: string;
  options: SearchOptions;
  files: { path: string; matches: SearchMatch[] }[];
  total: number;
  truncated: boolean;
  problem: string;
  cancelled: boolean;
}

export const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

function call<T>(method: string, ...args: unknown[]): Promise<T> {
  if (!inShell) return Promise.reject(new Error("The editor needs the app's shell to reach the disk."));
  return Call.ByName(`main.EditorFiles.${method}`, ...args) as Promise<T>;
}

/** An error from Go, as the words an alert shows. */
export function problemOf(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error);
  try {
    const parsed = JSON.parse(text) as { message?: string };
    if (parsed.message) return parsed.message;
  } catch {
    // Plain words already.
  }
  return text;
}

export const files = {
  list: (root: string, folder: string) => call<Entry[]>("List", root, folder),
  listMany: (root: string, folders: string[]) => call<Record<string, Entry[]>>("ListMany", root, folders),
  read: (path: string) => call<ReadFile>("Read", path),
  stat: (paths: string[]) => call<Stat[]>("Stat", paths),
  write: (path: string, text: string) => call<string>("Write", path, text),
  createFile: (path: string) => call<void>("CreateFile", path),
  createFolder: (path: string, intermediate: boolean) => call<void>("CreateFolder", path, intermediate),
  move: (from: string, to: string) => call<void>("Move", from, to),
  copy: (from: string, to: string) => call<void>("Copy", from, to),
  trash: (path: string) => call<string>("Trash", path),
  inTrash: (path: string) => call<boolean>("InTrash", path),
  copyFiles: (paths: string[]) => call<boolean>("CopyFiles", paths),
  git: (root: string, args: string[]) => call<GitResult>("Git", root, args).catch(() => ({ ok: false, output: "" })),
  allFiles: (root: string) => call<string[]>("AllFiles", root),
  search: (root: string, query: string, options: SearchOptions, list: string[], edited: Record<string, string>, id: number) =>
    call<SearchResults>("Search", root, query, options, list, edited, id),
  home: () => call<string>("Home").catch(() => ""),
  watch: (root: string) => call<void>("Watch", root).catch(() => undefined),
};

/** Files changed under a folder, from the shell's watcher. */
export function onFilesChanged(root: string, handler: (changes: { path: string; type: number }[]) => void): () => void {
  if (!inShell) return () => {};
  return Events.On("agentman:editor-files", (event) => {
    const data = event.data as { root: string; changes: { path: string; type: number }[] };
    if (data?.root === root) handler(data.changes);
  });
}

/** Text on the clipboard, as Clipboard.copy does in the Mac app. */
export async function copyText(text: string): Promise<void> {
  if (inShell) await Clipboard.SetText(text);
  else await navigator.clipboard.writeText(text);
}

/** A picture on the disk, as the page can show it. */
export function fileURL(path: string): string {
  return `/__editor/file?path=${encodeURIComponent(path)}`;
}

/** Path helpers, for "/" paths inside a folder. */
export const paths = {
  dir(path: string): string {
    const i = path.lastIndexOf("/");
    return i < 0 ? "" : path.slice(0, i);
  },
  base(path: string): string {
    return path.slice(path.lastIndexOf("/") + 1);
  },
  ext(path: string): string {
    const name = paths.base(path);
    const i = name.lastIndexOf(".");
    return i <= 0 ? "" : name.slice(i + 1);
  },
  stem(name: string): string {
    const i = name.lastIndexOf(".");
    return i <= 0 ? name : name.slice(0, i);
  },
  join(root: string, path: string): string {
    if (!path) return root;
    return root.endsWith("/") ? root + path : `${root}/${path}`;
  },
  /** /a/b/../c as /a/c. */
  normalize(path: string): string {
    const out: string[] = [];
    for (const part of path.split("/")) {
      if (part === "" || part === ".") continue;
      if (part === "..") out.pop();
      else out.push(part);
    }
    return "/" + out.join("/");
  },
  /** ~ for the home folder, as abbreviatingWithTildeInPath. */
  tilde(path: string, home: string): string {
    if (home && (path === home || path.startsWith(home + "/"))) return "~" + path.slice(home.length);
    return path;
  },
};
