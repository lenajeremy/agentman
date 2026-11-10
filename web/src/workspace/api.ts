// Reading an agent's folder and acting on its git, through am serve
// (Stores/Workspace.swift, Stores/Worktrees.swift). Each call is one
// request; the daemon keeps every read inside the agent's own folder.

import type { Artifact, WorkspaceResult } from "@shared/protocol";
import { daemon, type AppEvent, type AppRequest } from "../daemon";

/** Where a session's branch stands, as git_status and the git actions answer
 *  (internal/protocol GitState). */
export interface GitState {
  sessionId: string;
  branch?: string;
  defaultBranch?: string;
  upstream?: string;
  ahead: number;
  behind: number;
  aheadOfDefault: number;
  remote?: string;
  changes: number;
  done?: string;
  commit?: string;
  message?: string;
  url?: string;
}

export interface Worktree {
  path: string;
  name: string;
  repo: string;
  branch?: string;
  main?: boolean;
  agentman?: boolean;
  copied?: string[];
}

/** What the phone's protocol doesn't name: the git actions, worktrees, and
 *  the fields they take. */
type Extra = {
  type: string;
  sessionId?: string;
  path?: string;
  text?: string;
  offset?: number;
  commit?: string;
  ref?: string;
  paths?: string[];
  ignoreWhitespace?: boolean;
  withFile?: boolean;
  force?: boolean;
  approve?: boolean;
  clientId?: string;
};

/** A request and its answer, waiting as long as the request can take: a push
 *  or a pull request is slow, and daemon.request gives up after 30 s. */
export function ask(req: Extra, timeout = 30_000): Promise<AppEvent> {
  const id = daemon.send(req as unknown as AppRequest);
  if (!id) return Promise.reject(new Error("Couldn't reach am serve."));
  return new Promise((resolve, reject) => {
    const off = daemon.on((event, replyTo) => {
      if (replyTo !== id) return;
      off();
      clearTimeout(timer);
      resolve(event);
    });
    const timer = window.setTimeout(() => {
      off();
      reject(new Error("am serve took too long to answer."));
    }, timeout);
  });
}

/** The daemon's words, as the Mac shows them: its prefix gone, a capital first. */
function said(error: unknown, fallback: string, prefixes: RegExp): string {
  const text = String(error || fallback).replace(prefixes, "");
  return text.charAt(0).toUpperCase() + text.slice(1);
}

export type WorkspaceType =
  | "list_files" | "read_file" | "list_changes" | "file_diff" | "list_commits"
  | "commit_files" | "commit_diff" | "list_branches" | "read_artifact";

export interface WorkspaceOptions {
  path?: string;
  offset?: number;
  commit?: string;
  ref?: string;
  ignoreWhitespace?: boolean;
  withFile?: boolean;
}

export async function workspace(type: WorkspaceType, sessionId: string, opts: WorkspaceOptions = {}): Promise<WorkspaceResult> {
  const reply = await ask({
    type,
    sessionId,
    path: opts.path ?? "",
    offset: opts.offset,
    commit: opts.commit,
    ref: opts.ref,
    ignoreWhitespace: opts.ignoreWhitespace || undefined,
    withFile: opts.withFile || undefined,
  });
  if (reply.type === "error") throw new Error(said(reply.error, "That couldn't be read.", /^(daemon|source|workspace): /));
  if (!reply.workspace) throw new Error("That couldn't be read.");
  return reply.workspace;
}

export type GitType = "git_status" | "git_commit" | "git_push" | "git_create_pr";

/** A git action in an agent's folder, or with git_status, where its branch stands. */
export async function git(type: GitType, sessionId: string, message?: string, paths?: string[]): Promise<GitState> {
  const reply = await ask({ type, sessionId, text: message, paths }, type === "git_status" ? 20_000 : 150_000);
  if (reply.type === "error") throw new Error(said(reply.error, "Git couldn't do that.", /^(daemon|workspace): /));
  const state = reply.git as GitState | undefined;
  if (!state) throw new Error("Update am serve to commit and push from here.");
  return state;
}

/** The documents a session's agent wrote, newest first. */
export async function artifacts(sessionId: string): Promise<Artifact[]> {
  const reply = await ask({ type: "list_artifacts", sessionId });
  if (reply.type === "error") throw new Error(reply.error ?? "They couldn't be listed.");
  return [...(reply.artifacts ?? [])].sort((a, b) => b.updatedAt - a.updatedAt);
}

/** Approves a document, or asks for changes to it. The problem, if any. */
export async function review(sessionId: string, artifact: string, approve: boolean, comment: string): Promise<string | undefined> {
  try {
    const reply = await ask({ type: "review_artifact", sessionId, path: artifact, approve, text: comment, clientId: `app-review-${crypto.randomUUID()}` });
    if (reply.type === "error" || reply.status === "failed") {
      return String(reply.error ?? "It couldn't be sent.").replace(/^(daemon|source): /, "");
    }
    return undefined;
  } catch (err) {
    return err instanceof Error ? err.message : String(err);
  }
}

// Worktrees: ~/Agentman/Worktrees/<repo>/<name>, made, listed and removed by
// the daemon. Paths go as relative to home.

function homeRelative(folder: string, home: string): string {
  if (!folder.startsWith(home + "/")) throw new Error("Worktrees are made from a folder inside your home folder.");
  return folder.slice(home.length + 1);
}

async function worktreeReply(req: Extra): Promise<AppEvent> {
  const reply = await ask(req, 120_000);
  if (reply.type === "error") throw new Error(said(reply.error, "Git couldn't do that.", /^(daemon|workspace): /));
  return reply;
}

export async function createWorktree(folder: string, name: string, home: string): Promise<Worktree> {
  const reply = await worktreeReply({ type: "create_worktree", path: homeRelative(folder, home), text: name });
  const worktree = reply.worktree as Worktree | undefined;
  if (!worktree) throw new Error("Update am serve to make worktrees from here.");
  return worktree;
}

export async function listWorktrees(folder: string, home: string): Promise<Worktree[]> {
  const reply = await worktreeReply({ type: "list_worktrees", path: homeRelative(folder, home) });
  return (reply.worktrees as Worktree[] | undefined) ?? [];
}

export async function removeWorktree(path: string, force: boolean, home: string): Promise<void> {
  await worktreeReply({ type: "remove_worktree", path: homeRelative(path, home), force: force || undefined });
}
