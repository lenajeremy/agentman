// A card's git chip and the Board's commit sheet (BoardView.swift's
// BoardGitChip, BoardActions.swift's BoardCommitSheet). Commit opens the
// sheet; push and open a pull request are done from the card, where the
// chip says what it is doing, then what came of it. A push to a default
// branch asks once, on the chip itself.

import { useEffect, useState } from "react";
import { useKeyboardBack } from "../palette/PaletteView";
import { ArrowUpRight, CircleArrowUp, CircleCheck, CircleX, GitPullRequest, RotateCw, TriangleAlert, X } from "lucide-react";
import type { WorkspaceChange } from "@shared/protocol";
import { openURL } from "../host";
import { openAgent } from "../nav";
import { Loader } from "../shell/Loader";
import { git, workspace, type GitState } from "../workspace/api";
import { CommitSheet } from "../workspace/CommitSheet";
import { actionLabel, canPush, quickAction, type GitAction } from "../workspace/gitAction";
import { clearGitRun, noteGitChanged, performGit, pushesToProtected, useGitRun } from "./git";
import { BoardLink } from "./parts";

const ACTION_ICONS = { commit: CircleCheck, push: CircleArrowUp, createPR: GitPullRequest, idle: CircleCheck };
const ACTION_SF = { commit: "checkmark.circle", push: "arrow.up.circle", createPR: "arrow.triangle.pull", idle: "checkmark.circle" };

export function BoardGitChip({ sessionId, action, state, onCommit }: {
  sessionId: string;
  action: GitAction;
  state: GitState | null;
  onCommit: () => void;
}) {
  const run = useGitRun(sessionId);
  const [armed, setArmed] = useState(false);
  // Unconfirmed, it goes back to what it was.
  useEffect(() => {
    if (!armed) return;
    const timer = window.setTimeout(() => setArmed(false), 4000);
    return () => window.clearTimeout(timer);
  }, [armed]);

  if (run?.phase.kind === "running") {
    return <Status words={run.phase.words} help={run.phase.words} icon={<Loader size={10} />} />;
  }
  if (run?.phase.kind === "done") {
    const url = run.phase.url;
    return (
      <>
        <Status words={run.phase.words} help={run.phase.detail} tone="ok" icon={<CircleCheck size={10} className="ok" />} />
        {url && <BoardLink title="View PR" icon={ArrowUpRight} sf="arrow.up.right" onClick={() => void openURL(url)} />}
      </>
    );
  }
  if (run?.phase.kind === "failed") {
    const retry = run.phase.retry;
    return (
      <>
        <Status words={run.phase.words} help={run.phase.detail} tone="error" icon={<CircleX size={10} className="error" />} />
        <BoardLink title="Retry" icon={RotateCw} sf="arrow.clockwise" onClick={() => performGit(retry, sessionId)} />
        <BoardLink title="Open" icon={ArrowUpRight} sf="arrow.up.right" help="Do this from the agent's page instead" onClick={() => {
          clearGitRun(sessionId);
          openAgent(sessionId);
        }} />
        <button className="board-dismiss" title="Dismiss" aria-label="Dismiss" onClick={() => clearGitRun(sessionId)}><X size={8} strokeWidth={3} /></button>
      </>
    );
  }

  // Committing is always just that here: the sheet offers the push.
  const title = action.kind === "commit" ? "Commit" : armed && state?.branch ? `${actionLabel(action)} to ${state.branch}?` : actionLabel(action);
  const press = () => {
    if (action.kind === "commit") return onCommit();
    if (action.kind === "push" && pushesToProtected(state) && !armed) return setArmed(true);
    setArmed(false);
    performGit(action, sessionId);
  };
  return (
    <BoardLink
      title={title}
      icon={armed ? TriangleAlert : ACTION_ICONS[action.kind]}
      sf={armed ? "exclamationmark.triangle" : ACTION_SF[action.kind]}
      warns={armed}
      help={armed ? `Click again to push to ${state?.branch ?? "this branch"}` : title}
      onClick={press}
    />
  );
}

function Status({ words, help, icon, tone }: { words: string; help: string; icon: React.ReactNode; tone?: "ok" | "error" }) {
  return (
    <span className={`board-status${tone ? ` ${tone}` : ""}`} title={help}>
      {icon}
      <span>{words}</span>
    </span>
  );
}

type Stage = { kind: "loading" } | { kind: "form" } | { kind: "committed"; commit?: string; message?: string };

/** Commit from the Board: the agent page's sheet, then a way to push what
 *  was just committed, without leaving the Board. */
export function BoardCommitSheet({ sessionId, close }: { sessionId: string; close: () => void }) {
  // Closed, the keyboard goes back to the button that opened it.
  useKeyboardBack();
  const [stage, setStage] = useState<Stage>({ kind: "loading" });
  const [state, setState] = useState<GitState | null>(null);
  const [changes, setChanges] = useState<WorkspaceChange[]>([]);
  const [hidden, setHidden] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [pushedTo, setPushedTo] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const now = await git("git_status", sessionId);
        const listed = await workspace("list_changes", sessionId);
        setState(now);
        setChanges(listed.changes ?? []);
        setHidden(listed.hidden ?? 0);
        setStage(listed.changes?.length ? { kind: "form" } : { kind: "committed" });
      } catch (err) {
        setProblem(err instanceof Error ? err.message : String(err));
        setStage({ kind: "form" });
      }
    })();
  }, [sessionId]);

  const commit = async (paths: string[], message: string) => {
    setProblem(null);
    setBusy(message ? "Committing…" : "Writing the message…");
    try {
      const after = await git("git_commit", sessionId, message, paths);
      setState(after);
      setStage({ kind: "committed", commit: after.commit, message: after.message });
      noteGitChanged(sessionId);
    } catch (err) {
      setProblem(err instanceof Error ? err.message : String(err));
    }
    setBusy(null);
  };

  const push = async () => {
    setProblem(null);
    setBusy("Pushing…");
    try {
      const after = await git("git_push", sessionId);
      setState(after);
      setPushedTo(after.upstream ?? "the remote");
      noteGitChanged(sessionId);
    } catch (err) {
      setProblem(err instanceof Error ? err.message : String(err));
    }
    setBusy(null);
  };

  if (stage.kind === "loading") {
    return (
      <div className="sheet-backdrop">
        <div className="sheet board-sheet-loading"><Loader size={11} /><span>Reading the changes</span></div>
      </div>
    );
  }
  if (stage.kind === "form") {
    return <CommitSheet state={state} changes={changes} hidden={hidden} then="commit" busy={busy} problem={problem} confirm={(paths, message) => void commit(paths, message)} cancel={close} />;
  }

  const pushable = !pushedTo && canPush(state);
  const pushTitle = state && pushesToProtected(state) && state.branch ? `Push to ${state.branch}` : "Push";
  const branch = state?.branch ?? "this branch";
  let summary: string;
  if (pushedTo) summary = `Everything on ${branch} is on ${pushedTo}.`;
  else if (pushable && state) {
    const commits = state.ahead === 1 ? "1 commit" : `${state.ahead} commits`;
    summary = `${commits} on ${branch} to push to ${state.upstream ?? `${state.remote ?? "origin"}/${state.branch ?? ""}`}.`;
  } else if (!stage.commit) {
    summary = hidden > 0 ? "Only files holding secrets are left, and those aren't committed from here." : "Everything here is already committed.";
  } else {
    const next = quickAction(state);
    summary = next.kind === "idle" ? next.hint : `Saved on ${branch}.`;
  }
  return (
    <div className="sheet-backdrop" onKeyDown={(e) => e.key === "Escape" && !busy && close()}>
      <div className="sheet board-committed" role="dialog" aria-label="Committed">
        <div className="board-committed-head">
          <div className="commit-title">{pushedTo ? "Pushed" : stage.commit ? "Committed" : "Nothing to commit"}</div>
          <div className="commit-subtitle">{summary}</div>
        </div>
        {stage.commit && (
          <div className="board-committed-commit">
            <CircleCheck size={11} className="ok" />
            <span className="board-committed-hash">{stage.commit}</span>
            <span className="board-committed-message">{stage.message ?? ""}</span>
          </div>
        )}
        {problem && <div className="commit-problem error-text">{problem}</div>}
        <div className="commit-buttons">
          {busy && <span className="commit-busy"><Loader size={11} />{busy}</span>}
          <span className="card-spacer" />
          <button className="push-button large" disabled={!!busy} autoFocus onClick={close}>Done</button>
          {/* Never on Return: a push is asked for, not fallen into. */}
          {pushable && <button className="push-button large default" disabled={!!busy} onClick={() => void push()}>{pushTitle}</button>}
        </div>
      </div>
    </div>
  );
}
