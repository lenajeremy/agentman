import { useCallback, useEffect, useState } from "react";

import { BrowsedFolder, mergeCounts } from "./folders";
import { useStore, type Store } from "./store";

/**
 * Walking the Mac's folders.
 *
 * Shared by the New session screen and the folder filter so the two browsers
 * cannot drift: the same paths are reachable, the same ones are hidden, and
 * "up" means the same thing in both. Paths stay relative to the Mac user's
 * home, which is what the daemon accepts — it never takes an absolute path
 * from the phone for browsing.
 */
export function useDirectoryBrowser(overrideStore?: Store) {
  const activeStore = useStore();
  const store = overrideStore ?? activeStore;
  const [path, setPath] = useState("");
  const [folders, setFolders] = useState<BrowsedFolder[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const { daemonOnline, listDirectories, credentials } = store;

  useEffect(() => {
    setPath("");
    setFolders([]);
    setError("");
  }, [credentials]);

  useEffect(() => {
    if (!daemonOnline) return;
    let live = true;
    setLoading(true);
    setError("");
    void listDirectories(path)
      .then((listing) => {
        if (live) setFolders(mergeCounts(listing.names, listing.folders));
      })
      .catch((err: Error) => {
        if (live) setError(err.message);
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
    // Only a path, device, or connection change should re-read; store methods
    // change identity as session state updates while this screen is open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, daemonOnline, credentials]);

  const down = useCallback((name: string) => {
    setPath((current) => (current ? `${current}/${name}` : name));
  }, []);

  const up = useCallback(() => {
    setPath((current) => current.split("/").slice(0, -1).join("/"));
  }, []);

  return { path, setPath, folders, loading, error, down, up };
}
