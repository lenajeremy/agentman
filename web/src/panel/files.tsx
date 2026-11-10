// Files as the Mac app shows them: the theme's icon for each
// (WorkspacePanel.swift's FileIcon and FileTypeIcon), git's letter and colour
// for a change (RightPanel.swift's ChangeStatus), and paths in a tree's order.

import { extensions, fileNames, folderNames, openFolderNames } from "./fileIconMap";

const base = (path: string) => path.slice(path.lastIndexOf("/") + 1);

/** The theme's icon for a file or folder: by its exact name first, then its
 *  longest extension ("d.ts" before "ts"), then the plain file or folder. */
export function fileIcon(name: string, directory = false, open = false): string {
  const lower = base(name).toLowerCase();
  if (directory) return open ? openFolderNames[lower] ?? "folder-open" : folderNames[lower] ?? "folder";
  if (fileNames[lower]) return fileNames[lower];
  let rest = lower;
  let dot: number;
  while ((dot = rest.indexOf(".")) >= 0) {
    rest = rest.slice(dot + 1);
    if (extensions[rest]) return extensions[rest];
  }
  return "file";
}

export function FileTypeIcon({ name, directory = false, open = false, size = 14 }: { name: string; directory?: boolean; open?: boolean; size?: number }) {
  return <img className="file-icon" src={`/assets/files/${fileIcon(name, directory, open)}.svg`} width={size} height={size} alt="" draggable={false} />;
}

/** git's "XY" status as a letter: M modified, A added, D deleted, R renamed, U new. */
export function changeLetter(status: string): string {
  if (status === "??" || status === "untracked") return "U";
  if (status.includes("D") || status === "deleted") return "D";
  if (status.includes("A") || status === "added") return "A";
  if (status.includes("R") || status === "renamed") return "R";
  return "M";
}

/** The letter's colour, as a CSS colour. */
export function changeTint(status: string): string {
  switch (changeLetter(status)) {
    case "U": case "A": return "var(--ok)";
    case "D": return "var(--error-text)";
    default: return "var(--working)";
  }
}

export function changeSymbol(status: string): string {
  switch (changeLetter(status)) {
    case "U": case "A": return "plus.circle";
    case "D": return "minus.circle";
    case "R": return "arrow.right.circle";
    default: return "circle.inset.filled";
  }
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

/** Finder's order for names ("file 2" before "file 10"). */
export function compareNames(a: string, b: string): number {
  return collator.compare(a, b);
}

/** Paths as a tree reads them: a folder's files together, folders before the
 *  files beside them, each level by name. */
export function treeOrder(a: string, b: string): number {
  const x = a.split("/").filter(Boolean), y = b.split("/").filter(Boolean);
  for (let i = 0; i < Math.min(x.length, y.length); i++) {
    if (x[i] === y[i]) continue;
    const xFolder = i < x.length - 1, yFolder = i < y.length - 1;
    if (xFolder !== yFolder) return xFolder ? -1 : 1;
    return compareNames(x[i], y[i]);
  }
  return x.length - y.length;
}

/** "12 KB", as Finder (ByteCountFormatter, .file) says a size. */
export function byteCount(bytes: number): string {
  if (bytes === 0) return "Zero KB";
  if (bytes < 1000) return `${bytes} byte${bytes === 1 ? "" : "s"}`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1000;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit++;
  }
  const digits = unit === 0 ? 0 : unit === 1 ? 1 : 2;
  return `${value.toLocaleString("en-US", { maximumFractionDigits: digits, minimumFractionDigits: 0 })} ${units[unit]}`;
}

export function folderOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
}

export function nameOf(path: string): string {
  return base(path);
}
