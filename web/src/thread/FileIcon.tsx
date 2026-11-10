// A file's icon as the system shows it (NSWorkspace on macOS, from the
// shell's /fileicon.png route); elsewhere, or when there is none, a Lucide
// icon in its place.

import { useState } from "react";
import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";

export function FileIcon({ path, size, fallback }: { path: string; size: number; fallback: LucideIcon }) {
  const [failed, setFailed] = useState(false);
  if (failed || document.documentElement.dataset.platform !== "darwin") {
    return <span className="file-icon-fallback" style={{ width: size, height: size }}><Icon icon={fallback} size={size * 0.8} /></span>;
  }
  return (
    <img
      className="file-icon"
      src={`/fileicon.png?size=${size}&path=${encodeURIComponent(path)}`}
      width={size}
      height={size}
      alt=""
      onError={() => setFailed(true)}
    />
  );
}
