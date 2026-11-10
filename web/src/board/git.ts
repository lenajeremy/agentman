// Pushing and opening a pull request from a Board card (Stores/BoardGit.swift):
// each agent's run, while it works and for a moment after, kept here
// because a card can leave the screen meanwhile. Committing has its own
// sheet: there is a message to write.

import { useSyncExternalStore } from "react";
import type { Session, Verification } from "@shared/protocol";
import { git, type GitState } from "../workspace/api";
import type { GitAction } from "../workspace/gitAction";

export type GitRunPhase =
  | { kind: "running"; words: string }
  | { kind: "done"; words: string; detail: string; url?: string }
  | { kind: "failed"; words: string; detail: string; retry: GitAction };

export interface GitRun {
  id: number;
  phase: GitRunPhase;
}

let runs: Record<string, GitRun> = {};
/** Commits and pushes made from the Board's sheet, by agent, so its card
 *  reads its branch again. */
let changed: Record<string, number> = {};
let next = 0;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

export function useGitRun(sessionId: string): GitRun | undefined {
  return useSyncExternalStore(subscribe, () => runs[sessionId]);
}

export function useGitChanged(sessionId: string): number {
  return useSyncExternalStore(subscribe, () => changed[sessionId] ?? 0);
}

export function noteGitChanged(sessionId: string) {
  changed = { ...changed, [sessionId]: (changed[sessionId] ?? 0) + 1 };
  emit();
}

export function clearGitRun(sessionId: string) {
  const { [sessionId]: _, ...rest } = runs;
  runs = rest;
  emit();
}

function setRun(sessionId: string, run: GitRun) {
  runs = { ...runs, [sessionId]: run };
  emit();
}

/** Pushes or opens a pull request from the card, which says how it went. */
export function performGit(action: GitAction, sessionId: string) {
  if (action.kind !== "push" && action.kind !== "createPR") return;
  if (runs[sessionId]?.phase.kind === "running") return;
  const push = action.kind === "push";
  const run: GitRun = { id: ++next, phase: { kind: "running", words: push ? "Pushing…" : "Opening the PR…" } };
  setRun(sessionId, run);
  void (async () => {
    let failed = false;
    let detail = "";
    let url: string | undefined;
    try {
      const after = await git(push ? "git_push" : "git_create_pr", sessionId);
      detail = push ? `Pushed to ${after.upstream ?? "the remote"}.` : "Opened the pull request.";
      url = after.url;
    } catch (err) {
      failed = true;
      detail = err instanceof Error ? err.message : String(err);
    }
    if (runs[sessionId]?.id !== run.id) return;
    setRun(sessionId, {
      id: run.id,
      phase: failed
        ? { kind: "failed", words: push ? "Push failed" : "PR failed", detail, retry: action }
        : { kind: "done", words: push ? "Pushed" : "Pull request open", detail, url },
    });
    if (failed) return;
    // A failure waits to be read; this goes by itself, a link for longer.
    window.setTimeout(() => {
      if (runs[sessionId]?.id === run.id) clearGitRun(sessionId);
    }, url ? 30_000 : 10_000);
  })();
}

const PROTECTED = ["main", "master", "trunk", "develop", "production"];

function isProtected(branch: string, state: GitState): boolean {
  return branch === state.defaultBranch || PROTECTED.includes(branch);
}

/** Whether a push from here lands on a default branch, or one that goes by
 *  a default branch's name (BoardActions.swift's ShipCheck). */
export function pushesToProtected(state: GitState | null | undefined): boolean {
  if (!state?.branch) return false;
  if (isProtected(state.branch, state)) return true;
  const tracked = state.upstream?.split("/").slice(1).join("/");
  return !!tracked && isProtected(tracked, state);
}

// The agent whose changes the Board's commit sheet is open on.
let committing: string | null = null;
export function openCommitSheet(sessionId: string | null) {
  committing = sessionId;
  emit();
}
export function useCommitSheet(): string | null {
  return useSyncExternalStore(subscribe, () => committing);
}

/** Whether an agent's work can be shipped, and if not why, the same way the
 *  daemon decides it (BoardActions.swift's ShipCheck.reason). */
export function shipReason(session: Session, state: GitState | undefined, check: Verification | undefined): string | undefined {
  if (session.state !== "idle" || session.question) return "Still working";
  if (check?.state === "failed") return "Its tests fail";
  if (check?.state === "checking") return "Still being checked";
  if (!state) return "Not in a git repository";
  if (!state.branch) return "No branch checked out";
  if (isProtected(state.branch, state)) return `On ${state.branch}, which is yours to push`;
  const tracked = state.upstream?.split("/").slice(1).join("/");
  if (state.upstream && tracked && isProtected(tracked, state)) return `Tracks ${state.upstream}, which is yours to push`;
  if (state.behind > 0) return "Behind its upstream";
  if (!state.upstream && !state.remote) return "No remote to push to";
  if (state.changes === 0 && state.ahead === 0 && state.aheadOfDefault === 0) return "Nothing to ship";
  return undefined;
}

/** What will go out, in a few words. */
export function shipSummary(state: GitState): string {
  const parts = [state.branch ?? ""];
  if (state.changes > 0) parts.push(state.changes === 1 ? "1 changed file" : `${state.changes} changed files`);
  if (state.ahead > 0) parts.push(state.ahead === 1 ? "1 commit to push" : `${state.ahead} commits to push`);
  return parts.join(" · ");
}

