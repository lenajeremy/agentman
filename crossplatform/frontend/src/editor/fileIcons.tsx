// The file icon theme (WorkspacePanel.swift's FileIcon and FileTypeIcon):
// a file's icon by its exact name first, then its longest extension ("d.ts"
// before "ts"), then the theme's plain file or folder. The rules come from
// the Mac app's FileIconMap.swift (scripts/editor.mjs), the pictures from
// its asset catalogue (public/assets/files).

import map from "./fileIconMap.json";

const tables = map as Record<"fileNames" | "extensions" | "folderNames" | "openFolderNames", Record<string, string>>;

export function fileIconName(name: string, directory = false, open = false): string {
  const lower = name.slice(name.lastIndexOf("/") + 1).toLowerCase();
  if (directory) return open ? (tables.openFolderNames[lower] ?? "folder-open") : (tables.folderNames[lower] ?? "folder");
  const exact = tables.fileNames[lower];
  if (exact) return exact;
  let rest = lower;
  for (let dot = rest.indexOf("."); dot >= 0; dot = rest.indexOf(".")) {
    rest = rest.slice(dot + 1);
    const icon = tables.extensions[rest];
    if (icon) return icon;
  }
  return "file";
}

export function fileIconURL(name: string, directory = false, open = false): string {
  return `/assets/files/${fileIconName(name, directory, open)}.svg`;
}

export function FileTypeIcon({ name, directory = false, open = false, size = 14 }: { name: string; directory?: boolean; open?: boolean; size?: number }) {
  return <img className="file-type-icon" src={fileIconURL(name, directory, open)} width={size} height={size} alt="" draggable={false} />;
}
