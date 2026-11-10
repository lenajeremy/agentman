// The system am serve runs on, for words that depend on it (where a key is
// kept, what the file manager is called), as a hook.

import { useEffect, useState } from "react";
import { serverSystem } from "../host";

/** "darwin", "linux" or "windows" once am serve has said; "" until then. */
export function useServerSystem(): string {
  const [system, setSystem] = useState("");
  useEffect(() => {
    void serverSystem().then(setSystem);
  }, []);
  return system;
}
