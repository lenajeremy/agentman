// What the workspace card's git row does (Views/GitActions.swift's
// GitAction), decided as T3 Code decides it: the action that fits where the
// branch stands on the row, every action in its menu.

import type { GitState } from "./api";

export type GitAction =
  | { kind: "commit"; push: boolean }
  | { kind: "push" }
  | { kind: "createPR" }
  | { kind: "idle"; label: string; hint: string };

export function actionLabel(action: GitAction): string {
  switch (action.kind) {
    case "commit": return action.push ? "Commit & push" : "Commit";
    case "push": return "Push";
    case "createPR": return "Create PR";
    case "idle": return action.label;
  }
}

export function actionSymbol(action: GitAction): string {
  switch (action.kind) {
    case "push": return "arrow.up.circle";
    case "createPR": return "arrow.triangle.pull";
    default: return "checkmark.circle";
  }
}

export function quickAction(state: GitState | null | undefined): GitAction {
  if (!state) return { kind: "idle", label: "Commit", hint: "Git status is unavailable." };
  if (!state.branch) return { kind: "idle", label: "Commit", hint: "Check out a branch to commit and push." };
  const canPushAnywhere = !!state.upstream || !!state.remote;
  if (state.changes > 0) return { kind: "commit", push: canPushAnywhere };
  if (state.upstream) {
    if (state.ahead > 0 && state.behind > 0) {
      return { kind: "idle", label: "Diverged", hint: "The branch has diverged from its upstream. Rebase or merge first." };
    }
    if (state.behind > 0) return { kind: "idle", label: "Behind", hint: "The upstream has commits this branch doesn't. Pull first." };
  }
  if (canPushAnywhere && state.ahead > 0) return { kind: "push" };
  if (canCreatePR(state)) return { kind: "createPR" };
  if (!canPushAnywhere) return { kind: "idle", label: "Commit", hint: "This repository has no remote to push to." };
  return { kind: "idle", label: "Up to date", hint: "Nothing to commit or push." };
}

export function canPush(state: GitState | null | undefined): boolean {
  if (!state?.branch) return false;
  return state.behind === 0 && state.ahead > 0 && (!!state.upstream || !!state.remote);
}

export function canCreatePR(state: GitState | null | undefined): boolean {
  if (!state?.branch || onDefaultBranch(state)) return false;
  return state.behind === 0 && state.aheadOfDefault > 0 && (!!state.upstream || !!state.remote);
}

export function onDefaultBranch(state: GitState): boolean {
  return !!state.branch && state.branch === state.defaultBranch;
}
