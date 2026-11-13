// Whether an agent's folder is there and in a repository, which the page
// can't see: the shell looks (menus.go Files.Folder). Asked once a folder,
// again when the window comes back to the front.

import { useEffect, useState } from "react";
import { hostCall } from "../host";

export interface FolderInfo {
  exists: boolean;
  repository: boolean;
}

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";
const known = new Map<string, FolderInfo>();

async function look(path: string): Promise<FolderInfo> {
  if (!inShell) return { exists: true, repository: true };
  try {
    return (await hostCall("Files.Folder", path)) as FolderInfo;
  } catch {
    return { exists: false, repository: false };
  }
}

export function useFolder(path: string): FolderInfo {
  const [info, setInfo] = useState<FolderInfo>(() => known.get(path) ?? { exists: true, repository: false });
  useEffect(() => {
    let cancelled = false;
    const refresh = () =>
      void look(path).then((found) => {
        known.set(path, found);
        if (!cancelled) setInfo(found);
      });
    if (known.has(path)) setInfo(known.get(path)!);
    refresh();
    window.addEventListener("focus", refresh);
    return () => {
      cancelled = true;
      window.removeEventListener("focus", refresh);
    };
  }, [path]);
  return info;
}
