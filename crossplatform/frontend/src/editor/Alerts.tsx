// The editor's alerts (EditorWindow.swift's confirmationDialog and alerts):
// "Save the changes to X?", a problem's OK, and the name of a new file or
// folder, or a new name for one. The first two are the system's own
// (menus.go's Confirm); the name sheet is drawn here, as the shell's alert
// has no text field.

import { useEffect, useRef, useState } from "react";
import { confirmSheet } from "../thread/menu";
import { AppIcon } from "../ui";
import type { EditorStore } from "./store";

export function EditorAlerts({ store }: { store: EditorStore }) {
  const asking = useRef<string | null>(null);

  // Closing a tab with unsaved edits.
  useEffect(() => {
    const document = store.closing;
    if (!document || asking.current === document.id) return;
    asking.current = document.id;
    void confirmSheet(`Save the changes to ${document.name}?`, "Your changes are lost if you don't save them.", ["Save", "Don't Save", "Cancel"]).then(async (answer) => {
      asking.current = null;
      store.closing = null;
      if (answer === "Save") {
        if (await document.save()) store.close(document);
      } else if (answer === "Don't Save") {
        store.close(document);
      }
      store.changed();
    });
  });

  // A problem, or a save that failed.
  const failed = store.documents.find((d) => d.problem);
  const problem = store.problem ?? failed?.problem ?? null;
  useEffect(() => {
    if (!problem || asking.current === `problem:${problem}`) return;
    asking.current = `problem:${problem}`;
    void confirmSheet(problem, "", ["OK"]).then(() => {
      asking.current = null;
      if (store.problem === problem) store.problem = null;
      if (failed?.problem === problem) failed.problem = null;
      store.changed();
    });
  });

  return store.naming ? <NameSheet store={store} /> : null;
}

/** New File, New Folder or Rename: a name, then Create or Rename. */
function NameSheet({ store }: { store: EditorStore }) {
  const naming = store.naming!;
  const [name, setName] = useState(naming.name);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);
  const title = naming.kind === "file" ? "New File" : naming.kind === "folder" ? "New Folder" : `Rename ${naming.entry?.name ?? ""}`;
  const action = naming.kind === "rename" ? "Rename" : "Create";
  const cancel = () => {
    store.naming = null;
    store.changed();
  };
  const apply = async () => {
    store.naming = null;
    store.changed();
    const problem =
      naming.kind === "file" ? await store.newFile(naming.folder, name)
        : naming.kind === "folder" ? await store.newFolder(naming.folder, name)
          : naming.entry ? await store.rename(naming.entry, name) : null;
    store.setProblem(problem);
  };
  return (
    <div className="alert-layer" onMouseDown={(e) => e.target === e.currentTarget && e.preventDefault()}>
      <div className="alert" role="alertdialog" aria-label={title}>
        <AppIcon size={64} />
        <div className="alert-title">{title}</div>
        <input
          ref={input}
          className="alert-field"
          placeholder="Name"
          value={name}
          spellCheck={false}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            e.stopPropagation();
            if (e.key === "Enter") {
              e.preventDefault();
              void apply();
            } else if (e.key === "Escape") {
              e.preventDefault();
              cancel();
            }
          }}
        />
        <div className="alert-buttons">
          <button className="alert-button" onClick={cancel}>Cancel</button>
          <button className="alert-button default" onClick={() => void apply()}>{action}</button>
        </div>
      </div>
    </div>
  );
}
