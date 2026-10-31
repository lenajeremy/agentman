// The editor: the Mac app's Monaco page (desktop/editor-web), unchanged, in
// an iframe. This component answers it the way Swift does in the Mac app,
// over postMessage (scripts/host-shim.js on the page's side). Files come
// from am serve, through the agent's folder; language servers connect to
// am serve directly, as on the Mac.
//
// The spike opens files read-only from the page's point of view: edits stay
// in Monaco and are not saved.

import { useCallback, useEffect, useRef, useState } from "react";
import type { Session, WorkspaceEntry } from "@shared/protocol";
import { daemon } from "./daemon";
import { editorTheme } from "./editorTheme";

type Pending = Map<number, (value: unknown) => void>;

export function Editor({ session }: { session: Session }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const pending = useRef<Pending>(new Map());
  const nextCall = useRef(0);
  const [ready, setReady] = useState(false);
  const [dir, setDir] = useState("");
  const [entries, setEntries] = useState<WorkspaceEntry[]>([]);
  const [open, setOpen] = useState<string | null>(null);
  const [lsp, setLsp] = useState("");

  const call = useCallback((name: string, ...args: unknown[]) => {
    const win = frame.current?.contentWindow;
    if (!win) return Promise.resolve(null);
    const id = ++nextCall.current;
    return new Promise<unknown>((resolve) => {
      pending.current.set(id, resolve);
      win.postMessage({ channel: "editorCall", id, name, args }, "*");
    });
  }, []);

  const readFile = useCallback(
    async (path: string): Promise<string | null> => {
      const event = await daemon.request({ type: "read_file", sessionId: session.id, path });
      return event.workspace?.kind === "file" ? (event.workspace.text ?? null) : null;
    },
    [session.id],
  );

  // What the page says and asks.
  useEffect(() => {
    const onMessage = async (e: MessageEvent) => {
      if (e.source !== frame.current?.contentWindow) return;
      const data = e.data ?? {};
      if (data.channel === "editorReturn") {
        pending.current.get(data.id)?.(data.result);
        pending.current.delete(data.id);
      } else if (data.channel === "editor") {
        const body = data.body ?? {};
        if (body.type === "ready") setReady(true);
        if (body.type === "log") console.log("[editor]", body.message);
      } else if (data.channel === "editorAsk") {
        const body = data.body ?? {};
        let result: unknown = null;
        if (body.type === "read" && typeof body.path === "string") {
          const prefix = session.cwd.endsWith("/") ? session.cwd : `${session.cwd}/`;
          if (body.path.startsWith(prefix)) result = await readFile(body.path.slice(prefix.length));
        }
        frame.current?.contentWindow?.postMessage({ channel: "editorAnswer", id: data.id, result }, "*");
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [readFile, session.cwd]);

  // Once the page is up: the app's colours, then the language servers.
  useEffect(() => {
    if (!ready) return;
    const dark = matchMedia("(prefers-color-scheme: dark)");
    const apply = () => void call("configure", { theme: editorTheme(dark.matches), fontSize: 13, vim: false });
    apply();
    dark.addEventListener("change", apply);
    daemon
      .request({ type: "lsp_endpoint" })
      .then((event) => {
        const ep = event.lsp as { url?: string; token?: string } | undefined;
        if (ep?.url && ep.token) {
          void call("connect", ep.url, ep.token, session.cwd);
          setLsp("Language servers connected");
        }
      })
      .catch((err) => setLsp(`No language servers: ${err.message}`));
    return () => dark.removeEventListener("change", apply);
  }, [ready, call, session.cwd]);

  // The folder's files.
  useEffect(() => {
    let live = true;
    daemon
      .request({ type: "list_files", sessionId: session.id, path: dir })
      .then((event) => {
        if (live) setEntries(event.workspace?.entries ?? []);
      })
      .catch(() => live && setEntries([]));
    return () => {
      live = false;
    };
  }, [session.id, dir]);

  useEffect(() => {
    setDir("");
    setOpen(null);
  }, [session.id]);

  // A file to open at once, from the window's query (AGENTMAN_OPEN=file=…).
  useEffect(() => {
    const asked = new URLSearchParams(location.search).get("file");
    if (ready && asked) void openFile(asked);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready]);

  const openFile = async (path: string) => {
    const text = await readFile(path);
    if (text === null) return;
    const uri = `file://${session.cwd.replace(/\/$/, "")}/${path}`;
    await call("open", { id: path, uri, text, tabSize: 4, insertSpaces: true, gen: 1 });
    await call("show", path, true);
    setOpen(path);
  };

  const sorted = [...entries].sort((a, b) => Number(b.directory) - Number(a.directory) || a.name.localeCompare(b.name));

  return (
    <div className="editor">
      <aside className="files">
        <div className="files-path">{dir || session.cwd.split("/").pop()}</div>
        {dir && (
          <button className="file" onClick={() => setDir(dir.split("/").slice(0, -1).join("/"))}>
            ..
          </button>
        )}
        {sorted.map((entry) => {
          const path = dir ? `${dir}/${entry.name}` : entry.name;
          return (
            <button
              key={path}
              className={`file ${entry.directory ? "dir" : ""} ${open === path ? "open" : ""}`}
              onClick={() => (entry.directory ? setDir(path) : void openFile(path))}
            >
              {entry.directory ? "▸ " : ""}
              {entry.name}
            </button>
          );
        })}
        {lsp && <div className="files-note">{lsp}</div>}
      </aside>
      <iframe ref={frame} className="monaco" src="/editor/index.html" title="Editor" />
    </div>
  );
}
