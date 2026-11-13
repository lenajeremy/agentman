// One shell on screen (Terminal/TerminalPanel.swift's TerminalHost and
// TerminalSession's emulator view): xterm.js attached to a shell am serve
// keeps. Attaching replays what the shell printed, so a shell moved between
// the panel, the bottom of the conversation and the editor keeps its screen.

import { useEffect, useRef } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { endpoint, platform } from "../host";
import { onAppearance, terminalTheme, TERMINAL_FONT, TERMINAL_SIZE } from "./theme";

/** Each shell's view: clearing from the bar reaches the one on screen. */
const views = new Map<string, { clear: () => void; focus: () => void; focused: () => boolean }>();

export function clearShell(id: string) {
  views.get(id)?.clear();
}

/** Of the shells showing, the one with the keyboard. */
export function focusedShell(ids: string[]): string | undefined {
  return ids.find((id) => views.get(id)?.focused());
}

export function ShellView({ id, visible }: { id: string; visible: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const shown = useRef(visible);
  const fitRef = useRef<() => void>(() => {});
  const focusRef = useRef<() => void>(() => {});
  const claimRef = useRef<() => void>(() => {});

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const term = new XTerm({
      fontFamily: TERMINAL_FONT,
      fontSize: TERMINAL_SIZE,
      lineHeight: 1,
      cursorBlink: false,
      allowProposedApi: true,
      // Option types characters, as it does everywhere else on a Mac.
      macOptionIsMeta: false,
      scrollback: 10_000,
      theme: terminalTheme(),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);

    let ws: WebSocket | undefined;
    let disposed = false;
    const encoder = new TextEncoder();
    // What xterm sends is either the user's (typed, pasted, a click) or its
    // own answer to a program's question (where the cursor is, what terminal
    // this is). It marks the user's (CoreService.onUserInput), so answers can
    // go apart: none for the scrollback replayed on attaching, whose
    // questions were answered when they were asked (tmux's, answered again,
    // would be typed into the shell as "1;2c0;276;0c"), and the rest as
    // replies, which am serve takes from one window when two show the shell.
    // Without the mark, everything goes as typed, as before.
    const core = (term as unknown as { _core?: { coreService?: { onUserInput?: (listener: () => void) => { dispose(): void } } } })._core?.coreService;
    let typed = false;
    const userInput = core?.onUserInput?.(() => (typed = true));
    let replaying = false;
    const send = (data: string | Uint8Array<ArrayBuffer>) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(data);
    };
    const clear = () => {
      term.clear();
      term.write("\x1b[2J\x1b[3J\x1b[H");
      send(JSON.stringify({ type: "clear" }));
    };

    // A size of nothing (hidden, or passing through a move) would cut every
    // line the shell holds; such sizes are passed over, as the Mac's view does.
    const refit = () => {
      if (!shown.current || el.clientWidth < 40 || el.clientHeight < 20) return;
      try {
        fit.fit();
      } catch {
        // Not laid out yet.
      }
    };
    fitRef.current = refit;
    focusRef.current = () => term.focus();
    // A shell shown in two windows (the editor and the agent's page) takes
    // the size of the one that was last sized; the window you come back to
    // takes it back, so what runs in it draws for this window again
    // (TerminalPanel.swift's TerminalContainer claim).
    const claim = () => {
      if (shown.current) send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    claimRef.current = claim;
    const cameForward = () => {
      if (document.visibilityState === "visible") claim();
    };
    window.addEventListener("focus", claim);
    document.addEventListener("visibilitychange", cameForward);
    views.set(id, {
      clear,
      focus: () => term.focus(),
      focused: () => !!el.contains(document.activeElement),
    });

    // ⌘K clears the screen and scrollback while the terminal has the keyboard.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type === "keydown" && e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
        clear();
        return false;
      }
      return true;
    });

    (async () => {
      // The cells are measured in the code font, which may still be loading.
      try {
        await document.fonts.load(`${TERMINAL_SIZE}px "Google Sans Code"`);
        if (!disposed) term.options.fontFamily = TERMINAL_FONT;
      } catch {
        // The fallback stands.
      }
      if ((await platform()) !== "linux" && new URLSearchParams(location.search).get("renderer") !== "dom") {
        try {
          const { WebglAddon } = await import("@xterm/addon-webgl");
          if (!disposed) term.loadAddon(new WebglAddon());
        } catch {
          // The DOM renderer stays.
        }
      }
      refit();
      const ep = await endpoint();
      if (disposed) return;
      const q = new URLSearchParams({ token: ep.token, id, cols: String(term.cols), rows: String(term.rows) });
      ws = new WebSocket(`${ep.terminal}?${q}`);
      ws.binaryType = "arraybuffer";
      ws.onopen = () => {
        send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      };
      // The first message is the scrollback, then what the shell says now.
      let scrollback = true;
      ws.onmessage = (e) => {
        if (!(e.data instanceof ArrayBuffer)) return;
        const data = new Uint8Array(e.data);
        if (scrollback) {
          scrollback = false;
          replaying = true;
          // Called once xterm has read it all, before what comes after.
          term.write(data, () => (replaying = false));
        } else {
          term.write(data);
        }
      };
    })().catch(() => {});

    const input = term.onData((data) => {
      const user = typed;
      typed = false;
      // Focus reports are the window's own and go as typed from each.
      if (!userInput || user || data === "\x1b[I" || data === "\x1b[O") send(encoder.encode(data));
      else if (!replaying) send(JSON.stringify({ type: "reply", data }));
    });
    const binary = term.onBinary((data) => send(Uint8Array.from(data, (c) => c.charCodeAt(0))));
    const resize = term.onResize(({ cols, rows }) => send(JSON.stringify({ type: "resize", cols, rows })));
    const observer = new ResizeObserver(refit);
    observer.observe(el);
    const offTheme = onAppearance(() => (term.options.theme = terminalTheme()));

    return () => {
      disposed = true;
      views.delete(id);
      window.removeEventListener("focus", claim);
      document.removeEventListener("visibilitychange", cameForward);
      observer.disconnect();
      offTheme();
      input.dispose();
      userInput?.dispose();
      binary.dispose();
      resize.dispose();
      ws?.close();
      term.dispose();
    };
  }, [id]);

  // Shown again: its size may have changed while hidden, and the keyboard
  // goes to it.
  useEffect(() => {
    shown.current = visible;
    if (visible) {
      fitRef.current();
      claimRef.current();
      requestAnimationFrame(() => focusRef.current());
    }
  }, [visible]);

  return <div className="shell-host" ref={host} style={{ visibility: visible ? "visible" : "hidden" }} />;
}
