// Whether an agent's folder is still there (the Mac asks FileManager): am
// serve is asked to list it, once per folder while the app runs, and again
// when it couldn't be reached.

import { useEffect, useState } from "react";
import { workspace } from "./api";

const known = new Map<string, boolean>();

export function useFolderExists(sessionId: string, folder: string): boolean {
  const [exists, setExists] = useState(known.get(folder) ?? true);
  useEffect(() => {
    if (known.has(folder)) {
      setExists(known.get(folder)!);
      return;
    }
    let live = true;
    workspace("list_files", sessionId).then(
      () => {
        known.set(folder, true);
        if (live) setExists(true);
      },
      (err) => {
        // Only a folder that isn't there counts; am serve not answering doesn't.
        const gone = /no such|not exist|isn't there|not found/i.test(String(err?.message ?? err));
        if (gone) known.set(folder, false);
        if (live) setExists(!gone);
      },
    );
    return () => {
      live = false;
    };
  }, [sessionId, folder]);
  return exists;
}
