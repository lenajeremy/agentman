// The editor's text (Editor/MonacoView.swift): the Mac app's Monaco page
// (desktop/editor-web), unchanged, in an iframe, with a model for each file
// open in a tab. src/editor/monaco.ts is the conversation with it.

import { useEffect, useRef } from "react";
import { usePref } from "./prefs";
import type { MonacoBridge } from "./editor/monaco";
import { useEditor, type EditorStore } from "./editor/store";

export function EditorPanes({ store, bridge }: { store: EditorStore; bridge: MonacoBridge }) {
  useEditor(store);
  const frame = useRef<HTMLIFrameElement>(null);
  const [fontSize] = usePref("editorFontSize", 14);
  const [vim] = usePref("editorVim", false);

  useEffect(() => {
    bridge.attach(frame.current);
    return () => bridge.attach(null);
  }, [bridge]);

  // Every change to the tabs, the splits or the settings reaches the page.
  useEffect(() => {
    bridge.sync(fontSize, vim);
  });

  return <iframe ref={frame} className="monaco" src="/editor/index.html" title="Editor" />;
}
