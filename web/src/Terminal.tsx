// A terminal: xterm.js here, the program on a pty in am serve
// (internal/term). A folder's is its whole terminal panel, its shells kept by
// am serve and shared by every window (RightPanel.swift's FolderTerminal); an
// agent's attaches to its tmux session on its own.

import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { endpoint, platform } from "./host";
import { thisComputer } from "./workspace/place";
import { shellCommand, useShells } from "./terminal/shells";
import { TerminalPanel, type TerminalMove } from "./terminal/TerminalPanel";
import { terminalTheme, TERMINAL_FONT, TERMINAL_SIZE } from "./terminal/theme";
import "./styles/terminal.css";

export type TerminalTarget = { session: string } | { folder: string };

/** A terminal: a folder's panel of shells, or an agent's tmux on its own.
 *  hide shows a Hide button (under the editor or the conversation); move, one
 *  that moves it between under the conversation and the side panel. */
export function Terminal({ target, hide, move }: { target: TerminalTarget; hide?: () => void; move?: TerminalMove }) {
  if ("folder" in target) return <FolderTerminal folder={target.folder} hide={hide} move={move} />;
  return <AgentTerminal session={target.session} />;
}

/** The folder's shells, or a way to start one. */
export function FolderTerminal({ folder, hide, move }: { folder: string; hide?: () => void; move?: TerminalMove }) {
  const state = useShells(folder);
  if (state.problem) {
    return <div className="term-note"><span>This folder isn't on {thisComputer()} any more.</span></div>;
  }
  if (!state.ready) return <div className="term-note" />;
  if (!state.shells.length) {
    return (
      <div className="term-note">
        <span>No terminal open in this folder.</span>
        <button className="push-button small" onClick={() => shellCommand(folder, { type: "new" })}>New Terminal</button>
      </div>
    );
  }
  return <TerminalPanel folder={folder} state={state} hide={hide} move={move} />;
}

/** An agent's tmux session, attached for as long as it is shown. */
function AgentTerminal({ session }: { session: string }) {
  const host = useRef<HTMLDivElement>(null);
  const [note, setNote] = useState("Connecting…");

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const term = new XTerm({
      fontFamily: TERMINAL_FONT,
      fontSize: TERMINAL_SIZE,
      allowProposedApi: true,
      macOptionIsMeta: false,
      theme: terminalTheme(),
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
      const q = new URLSearchParams({ token: ep.token, cols: String(term.cols), rows: String(term.rows), session });
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
  }, [session]);

  return (
    <div className="agent-term">
      <div className="agent-term-host" ref={host} />
      {note && <div className="agent-term-note">{note}</div>}
    </div>
  );
}
