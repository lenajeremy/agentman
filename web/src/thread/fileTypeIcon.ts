// A file's type icon, as the Mac's FileIcon.asset picks it: by its exact
// name first, then its longest extension ("d.ts" before "ts"), then the
// plain file. The icons are public/assets/files/<name>.svg.

import { EXTENSIONS, FILE_NAMES } from "./fileIconMap";

export function fileTypeIcon(fileName: string): string {
  const name = (fileName.split("/").pop() ?? fileName).toLowerCase();
  const exact = FILE_NAMES[name];
  if (exact) return exact;
  let rest = name;
  for (let dot = rest.indexOf("."); dot >= 0; dot = rest.indexOf(".")) {
    rest = rest.slice(dot + 1);
    const icon = EXTENSIONS[rest];
    if (icon) return icon;
  }
  return "file";
}
