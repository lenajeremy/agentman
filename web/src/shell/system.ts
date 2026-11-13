// The system am serve runs on, for words that depend on it (where a key is
// kept, what the file manager is called), as a hook.

import { useEffect, useState } from "react";
import { knownSystem, serverSystem } from "../host";

/** "darwin", "linux" or "windows" once am serve has said; null until then,
 *  "" if it couldn't. Asked as the page starts (main.tsx), so it's mostly
 *  known by the time anything shows words that depend on it. */
export function useServerSystem(): string | null {
  const [system, setSystem] = useState<string | null>(knownSystem);
  useEffect(() => {
    void serverSystem().then(setSystem);
  }, []);
  return system;
}
