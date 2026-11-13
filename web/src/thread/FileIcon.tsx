// A file's icon as the system shows it (NSWorkspace on macOS, from the
// shell's /fileicon.png route). A browser can't ask the system for one, so
// there it is the file's type icon from the editor's set (the Mac's
// FileIcon.asset), the nearest to a document's own; where neither can be
// drawn, a Lucide icon in its place.

import { useState } from "react";
import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";
import { fileTypeIcon } from "./fileTypeIcon";

export function FileIcon({ path, size, fallback }: { path: string; size: number; fallback: LucideIcon }) {
  const [failed, setFailed] = useState(false);
  const shell = document.documentElement.dataset.platform === "darwin";
  if (failed) {
    return <span className="file-icon-fallback" style={{ width: size, height: size }}><Icon icon={fallback} size={size * 0.8} /></span>;
  }
  return (
    <img
      className="file-icon"
      src={shell ? `/fileicon.png?size=${size}&path=${encodeURIComponent(path)}` : `/assets/files/${fileTypeIcon(path)}.svg`}
      width={size}
      height={size}
      alt=""
      onError={() => setFailed(true)}
    />
  );
}
