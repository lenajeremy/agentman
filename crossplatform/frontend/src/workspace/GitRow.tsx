// The card's git row (Views/GitActions.swift's GitActionsRow): the action
// that fits where the branch stands, a menu of the rest, then what the last
// one did or why it failed.

import { useEffect, useRef, useState } from "react";
import { ArrowUpCircle, ChevronDown, CircleCheck, GitPullRequestArrow, X } from "lucide-react";
import type { WorkspaceChange } from "@shared/protocol";
import type { Session } from "@shared/protocol";
import { openURL } from "../host";
import { confirmSheet, menuUnder } from "../thread/menu";
import { Icon } from "../ui";
import { git, type GitState, type GitType } from "./api";
import { actionLabel, actionSymbol, canCreatePR, canPush, onDefaultBranch, quickAction, type GitAction } from "./gitAction";
import { CardRow } from "./CardRow";
import { CommitSheet } from "./CommitSheet";
import { Spinner } from "./Loaded";

type Step = { kind: "commit"; paths: string[]; message: string } | { kind: "push" } | { kind: "createPR" };

interface Outcome {
  text: string;
  url?: string;
  failed?: boolean;
}

// For comparisons, as the Mac's AGENTMAN_TEST_GIT: ?git=sheet, running,
// outcome or failed shows that state.
const TEST = new URLSearchParams(location.search).get("git");
const TEST_OUTCOME: Outcome | null =
  TEST === "outcome" ? { text: "Committed 3f9c2a1, pushed to origin/main." }
  : TEST === "failed" ? { text: "Committed 3f9c2a1, but the push was rejected: fetch first.", failed: true }
  : null;

const SYMBOL_ICON ={ "checkmark.circle": CircleCheck, "arrow.up.circle": ArrowUpCircle, "arrow.triangle.pull": GitPullRequestArrow } as const;

export function GitRow({ session, state, changes, hidden, changed }: {
  session: Session;
  state: GitState | null;
  changes: WorkspaceChange[];
  hidden: number;
  /** Reads the branch and the changes again. */
  changed: () => void;
}) {
  const [committing, setCommitting] = useState<"commit" | "commitAndPush" | null>(TEST === "sheet" ? "commitAndPush" : null);
  const [running, setRunning] = useState<string | null>(TEST === "running" ? "Writing the message…" : null);
  const [outcome, setOutcome] = useState<Outcome | null>(TEST_OUTCOME);
  const menuButton = useRef<HTMLButtonElement>(null);
  const action = quickAction(state);
  const idle = action.kind === "idle";
  const symbol = actionSymbol(action) as keyof typeof SYMBOL_ICON;

  // Hides after 10 s unless it failed.
  useEffect(() => {
    if (!outcome || outcome.failed) return;
    const timer = window.setTimeout(() => setOutcome((now) => (now === outcome ? null : now)), 10_000);
    return () => clearTimeout(timer);
  }, [outcome]);

  const confirm = async (which: GitAction) => {
    if (!state?.branch) return;
    const branch = state.branch;
    const commits = `${state.ahead} commit${state.ahead === 1 ? "" : "s"}`;
    const target = state.upstream ?? `${state.remote ?? "origin"}/${branch}`;
    let title: string, message: string, button: string;
    if (which.kind === "createPR") {
      title = `Open a pull request from ${branch}?`;
      const pushed = !state.upstream || state.ahead > 0 ? ` Its ${commits} not pushed yet go first.` : "";
      message = `${branch} goes to ${state.defaultBranch ?? "the default branch"} on GitHub, titled and described from its commits.${pushed}`;
      button = "Create PR";
    } else {
      title = onDefaultBranch(state) ? `Push to ${branch}, the default branch?` : `Push ${branch}?`;
      message = `${commits} go to ${target}.`;
      button = "Push";
    }
    const answer = await confirmSheet(title, message, [button, "Cancel"]);
    if (answer === button) run([which.kind === "createPR" ? { kind: "createPR" } : { kind: "push" }]);
  };

  const start = (which: GitAction) => {
    if (which.kind === "commit") setCommitting(which.push ? "commitAndPush" : "commit");
    else if (which.kind === "push" || which.kind === "createPR") void confirm(which);
  };

  const run = async (steps: Step[]) => {
    setOutcome(null);
    const said: string[] = [];
    let url: string | undefined;
    for (const step of steps) {
      let type: GitType;
      let paths: string[] | undefined;
      let message: string | undefined;
      if (step.kind === "commit") {
        setRunning(step.message ? "Committing…" : "Writing the message…");
        type = "git_commit";
        paths = step.paths;
        message = step.message;
      } else if (step.kind === "push") {
        setRunning("Pushing…");
        type = "git_push";
      } else {
        setRunning("Opening the PR…");
        type = "git_create_pr";
      }
      try {
        const after = await git(type, session.id, message, paths);
        if (after.done === "committed") said.push(`Committed ${after.commit ?? ""}`);
        else if (after.done === "pushed") said.push(`pushed to ${after.upstream ?? "the remote"}`);
        else if (after.done === "pr_created") {
          said.push("opened the pull request");
          url = after.url;
        }
      } catch (err) {
        const before = said.length ? said.join(", ") + ", but " : "";
        setOutcome({ text: before + (err instanceof Error ? err.message : String(err)), failed: true });
        setRunning(null);
        changed();
        return;
      }
    }
    setRunning(null);
    const text = said.join(", ");
    setOutcome({ text: text.charAt(0).toUpperCase() + text.slice(1) + ".", url });
    changed();
  };

  return (
    <div className="git-row">
      <div className="git-row-line">
        <CardRow
          title={running ?? actionLabel(action)}
          icon={SYMBOL_ICON[symbol]}
          sf={symbol}
          onClick={idle || running ? undefined : () => start(action)}
          help={action.kind === "idle" ? action.hint : actionLabel(action)}
          trailing={<>{running && <span className="git-spinner"><Spinner /></span>}<span className="git-menu-room" /></>}
        />
        <button
          ref={menuButton}
          className="git-menu"
          title="Commit, push or open a pull request"
          disabled={!!running || !state}
          onClick={() => menuUnder(menuButton.current!, [
            { label: "Commit…", action: () => setCommitting("commit"), disabled: (state?.changes ?? 0) === 0 || !state?.branch },
            { label: "Push", action: () => void confirm({ kind: "push" }), disabled: !canPush(state) },
            { label: "Create PR", action: () => void confirm({ kind: "createPR" }), disabled: !canCreatePR(state) },
          ])}
        >
          <Icon icon={ChevronDown} sf="chevron.down" size={9} weight="semibold" />
        </button>
      </div>
      {outcome && (
        <div className="git-outcome">
          <span className={outcome.failed ? "failed" : ""}>{outcome.text}</span>
          <span className="card-spacer" />
          {outcome.url && <button className="link-button" onClick={() => void openURL(outcome.url!)}>View PR</button>}
          <button className="git-dismiss" title="Dismiss" onClick={() => setOutcome(null)}>
            <Icon icon={X} sf="xmark" size={8} weight="bold" />
          </button>
        </div>
      )}
      {committing && (
        <CommitSheet
          state={state}
          changes={changes}
          hidden={hidden}
          then={committing}
          confirm={(paths, message, push) => {
            setCommitting(null);
            void run([{ kind: "commit", paths, message }, ...(push ? [{ kind: "push" } as Step] : [])]);
          }}
          cancel={() => setCommitting(null)}
        />
      )}
    </div>
  );
}
