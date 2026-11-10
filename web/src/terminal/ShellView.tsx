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
      ws.onmessage = (e) => {
        if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
      };
    })().catch(() => {});

    const input = term.onData((data) => send(encoder.encode(data)));
    const binary = term.onBinary((data) => send(Uint8Array.from(data, (c) => c.charCodeAt(0))));
    const resize = term.onResize(({ cols, rows }) => send(JSON.stringify({ type: "resize", cols, rows })));
    const observer = new ResizeObserver(refit);
    observer.observe(el);
    const offTheme = onAppearance(() => (term.options.theme = terminalTheme()));

    return () => {
      disposed = true;
      views.delete(id);
      observer.disconnect();
      offTheme();
      input.dispose();
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
      requestAnimationFrame(() => focusRef.current());
    }
  }, [visible]);

  return <div className="shell-host" ref={host} style={{ visibility: visible ? "visible" : "hidden" }} />;
}
