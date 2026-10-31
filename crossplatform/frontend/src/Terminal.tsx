// A terminal: xterm.js here, the program on a pty in am serve
// (internal/term). An agent's terminal attaches to its tmux session; a
// folder's starts the user's shell there.

import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { endpoint, platform } from "./host";
import "./styles/terminal.css";

export type TerminalTarget = { session: string } | { folder: string };

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

export function Terminal({ target }: { target: TerminalTarget }) {
  const host = useRef<HTMLDivElement>(null);
  const [note, setNote] = useState("Connecting…");
  const key = "session" in target ? `s:${target.session}` : `f:${target.folder}`;

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const term = new XTerm({
      fontFamily: '"Google Sans Code", ui-monospace, Menlo, Consolas, monospace',
      fontSize: 13,
      cursorBlink: true,
      allowProposedApi: true,
      theme: {
        background: cssVar("--paper"),
        foreground: cssVar("--text"),
        cursor: cssVar("--text"),
        selectionBackground: cssVar("--selection"),
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    let ws: WebSocket | undefined;
    let disposed = false;
    const encoder = new TextEncoder();

    (async () => {
      // WebGL draws faster where the web view has it on the GPU. WebKitGTK
      // can fall back to software for it, so Linux keeps the DOM renderer.
      if ((await platform()) !== "linux" && new URLSearchParams(location.search).get("renderer") !== "dom") {
        try {
          const { WebglAddon } = await import("@xterm/addon-webgl");
          term.loadAddon(new WebglAddon());
        } catch {
          // The DOM renderer stays.
        }
      }
      const ep = await endpoint();
      if (disposed) return;
      const q = new URLSearchParams({ token: ep.token, cols: String(term.cols), rows: String(term.rows) });
      if ("session" in target) q.set("session", target.session);
      else q.set("folder", target.folder);
      ws = new WebSocket(`${ep.terminal}?${q}`);
      ws.binaryType = "arraybuffer";
      ws.onopen = () => setNote("");
      ws.onmessage = (e) => {
        if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
      };
      ws.onclose = (e) => setNote(e.reason === "exited" ? "The program ended" : "Disconnected");
    })().catch((err) => setNote(String(err?.message ?? err)));

    const input = term.onData((data) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(data));
    });
    const resize = term.onResize(({ cols, rows }) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
    });
    const observer = new ResizeObserver(() => fit.fit());
    observer.observe(el);
    term.focus();

    return () => {
      disposed = true;
      observer.disconnect();
      input.dispose();
      resize.dispose();
      ws?.close();
      term.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  return (
    <div className="terminal">
      <div className="terminal-host" ref={host} />
      {note && <div className="terminal-note">{note}</div>}
    </div>
  );
}
