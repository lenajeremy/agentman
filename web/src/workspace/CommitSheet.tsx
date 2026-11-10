// T3 Code's "Commit changes" (Views/GitActions.swift's CommitSheet): the
// branch, the files going in, which can be left out, and a message, which
// Agentman writes when it is left empty. A sheet at the top of the window.

import { useEffect, useRef, useState } from "react";
import type { WorkspaceChange } from "@shared/protocol";
import type { GitState } from "./api";
import { onDefaultBranch } from "./gitAction";

export function CommitSheet({ state, changes, hidden, then, busy = null, problem = null, confirm, cancel }: {
  state: GitState | null;
  changes: WorkspaceChange[];
  hidden: number;
  then: "commit" | "commitAndPush";
  /** While it commits with the sheet still up (the Board's): what it is
   *  doing, the buttons off until it is done. */
  busy?: string | null;
  /** What went wrong, under the message. */
  problem?: string | null;
  confirm: (paths: string[], message: string, push: boolean) => void;
  cancel: () => void;
}) {
  const [message, setMessage] = useState("");
  const [excluded, setExcluded] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState(false);
  const included = changes.filter((c) => !excluded.has(c.path));
  const added = included.reduce((n, c) => n + (c.added ?? 0), 0);
  const removed = included.reduce((n, c) => n + (c.removed ?? 0), 0);
  const primary = () => {
    if (included.length && !busy) confirm(included.map((c) => c.path), message, then === "commitAndPush");
  };
  const sheet = useRef<HTMLDivElement>(null);

  // Esc cancels, Return commits, as the sheet's buttons take them.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        cancel();
      } else if (e.key === "Enter" && !(e.target instanceof HTMLTextAreaElement)) {
        e.preventDefault();
        primary();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });

  const toggle = (path: string, on: boolean) => {
    const next = new Set(excluded);
    if (on) next.delete(path);
    else next.add(path);
    setExcluded(next);
  };

  return (
    <div className="sheet-backdrop" onMouseDown={(e) => e.stopPropagation()}>
      <div className="sheet commit-sheet" ref={sheet} role="dialog" aria-label="Commit changes">
        <div className="commit-head">
          <div className="commit-title">Commit changes</div>
          <div className="commit-subtitle">Review and confirm your commit. Leave the message blank to auto-generate one.</div>
        </div>
        <div className="commit-box">
          <div className="commit-branch">
            <span className="muted">Branch</span>
            <span className="commit-branch-name">{state?.branch ?? "(detached HEAD)"}</span>
            <span className="card-spacer" />
            {state && onDefaultBranch(state) && <span className="needs-you">Default branch</span>}
          </div>
          <div className="commit-files">
            <div className="commit-files-head">
              {editing && (
                <input type="checkbox" className="checkbox" checked={excluded.size === 0}
                  onChange={(e) => setExcluded(e.target.checked ? new Set() : new Set(changes.map((c) => c.path)))} />
              )}
              <span className="muted">Files</span>
              {excluded.size > 0 && !editing && <span className="muted">({included.length} of {changes.length})</span>}
              <span className="card-spacer" />
              <button className="plain-button" onClick={() => setEditing(!editing)}>{editing ? "Done" : "Edit"}</button>
            </div>
            <div className="commit-list">
              {changes.map((change) => {
                const out = excluded.has(change.path);
                return (
                  <div key={change.path} className="commit-file">
                    {editing && <input type="checkbox" className="checkbox" checked={!out} onChange={(e) => toggle(change.path, e.target.checked)} />}
                    <span className={`commit-path ${out ? "out" : ""}`} title={change.path}><bdi>{change.path}</bdi></span>
                    <span className="card-spacer" />
                    {out ? (
                      <span className="faint">Excluded</span>
                    ) : (
                      <>
                        <span className="ok">+{change.added ?? 0}</span>
                        <span className="faint">/</span>
                        <span className="error-text">−{change.removed ?? 0}</span>
                      </>
                    )}
                  </div>
                );
              })}
            </div>
            <div className="commit-totals">
              {hidden > 0 && <span className="faint">{hidden} file{hidden === 1 ? "" : "s"} holding secrets left out</span>}
              <span className="card-spacer" />
              <span className="ok">+{added}</span>
              <span className="muted">{" / "}</span>
              <span className="error-text">−{removed}</span>
            </div>
          </div>
        </div>
        <div className="commit-message">
          <div className="commit-message-label">Commit message (optional)</div>
          <div className="commit-editor">
            <textarea value={message} onChange={(e) => setMessage(e.target.value)} autoFocus spellCheck={false} />
            {!message && <span className="commit-placeholder">Leave empty to auto-generate</span>}
          </div>
        </div>
        {problem && <div className="commit-problem error-text">{problem}</div>}
        <div className="commit-buttons">
          {busy && <span className="commit-busy"><span className="git-spinner" />{busy}</span>}
          <span className="card-spacer" />
          <button className="push-button large" disabled={!!busy} onClick={cancel}>Cancel</button>
          {then === "commitAndPush" && (
            <button className="push-button large" disabled={!included.length || !!busy} onClick={() => confirm(included.map((c) => c.path), message, false)}>Commit</button>
          )}
          <button className="push-button large default" disabled={!included.length || !!busy} onClick={primary}>
            {then === "commitAndPush" ? "Commit & push" : "Commit"}
          </button>
        </div>
      </div>
    </div>
  );
}
