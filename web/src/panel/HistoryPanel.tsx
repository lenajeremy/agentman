// The History tab (Views/HistoryPanel.swift): the branch's commits, newest
// first, any branch browsed without checking it out; a commit opens its diff.
// It keeps up as the agent works: a commit it makes appears on top.

import { useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronRight, GitCommitHorizontal, RotateCw } from "lucide-react";
import type { Session, WorkspaceBranch, WorkspaceCommit } from "@shared/protocol";
import { menuUnder } from "../thread/menu";

import { workspace } from "../workspace/api";
import { Spinner } from "../workspace/Loaded";
import { dateNumeric, relativeShort } from "./dates";
import { DiffPanel } from "./DiffPanel";
import { PanelButton } from "./PanelButton";
import { Sym } from "./Sym";

export function HistoryPanel({ session }: { session: Session }) {
  const [commits, setCommits] = useState<WorkspaceCommit[]>([]);
  const [branches, setBranches] = useState<WorkspaceBranch[]>([]);
  const [branch, setBranch] = useState<string | undefined>();
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [ref, setRef] = useState<string | undefined>();
  const [selected, setSelected] = useState<WorkspaceCommit | null>(null);
  const lastSelected = useRef<string | null>(null);
  // A page belongs to the branch it was asked for, even if the user switches
  // branches while the daemon is answering.
  const generation = useRef(0);
  const loadingRef = useRef(false);
  const commitsRef = useRef<WorkspaceCommit[]>([]);
  commitsRef.current = commits;
  const menuButton = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);

  const page = async (gen: number, offset: number) => {
    if (loadingRef.current && gen === generation.current && offset > 0) return;
    loadingRef.current = true;
    setLoading(true);
    try {
      const result = await workspace("list_commits", session.id, { offset, ref });
      if (gen !== generation.current) return;
      setCommits((now) => {
        const known = new Set(now.map((c) => c.hash));
        return [...now, ...(result.commits ?? []).filter((c) => !known.has(c.hash) && known.add(c.hash))];
      });
      setBranch(result.branch);
      setMore(!!result.truncated);
      setProblem(null);
    } catch (err) {
      if (gen !== generation.current) return;
      setProblem(err instanceof Error ? err.message : String(err));
    } finally {
      if (gen === generation.current) {
        loadingRef.current = false;
        setLoading(false);
      }
    }
  };

  const reload = () => {
    const gen = ++generation.current;
    loadingRef.current = false;
    setCommits([]);
    commitsRef.current = [];
    setMore(false);
    setProblem(null);
    return page(gen, 0);
  };

  const readBranches = () =>
    workspace("list_branches", session.id).then((r) => setBranches(r.branches ?? []), () => {});

  // The newest commits on top of those already read; history rewritten under
  // the list is read again from the start.
  const update = async () => {
    if (loadingRef.current) return;
    const top = commitsRef.current[0];
    if (!top) return reload();
    const gen = generation.current;
    const result = await workspace("list_commits", session.id, { offset: 0, ref }).catch(() => null);
    if (!result || gen !== generation.current) return;
    const fresh = result.commits ?? [];
    const index = fresh.findIndex((c) => c.hash === top.hash);
    if (index > 0) setCommits((now) => [...fresh.slice(0, index), ...now]);
    else if (index < 0) {
      setCommits(fresh);
      setMore(!!result.truncated);
    }
    setBranch(result.branch);
    setProblem(null);
  };

  useEffect(() => {
    void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ref, session.id]);
  useEffect(() => {
    void readBranches();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.id]);

  // Once the agent has been quiet for two seconds, or its turn begins or
  // ends; not as the tab opens, which reads the list anyway.
  const watching = useRef(false);
  useEffect(() => {
    if (!watching.current) {
      watching.current = true;
      return;
    }
    const timer = window.setTimeout(() => void update(), 2000);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.state, session.lastActivityAt]);

  // Back from a commit: the list scrolled to it.
  useEffect(() => {
    if (selected || !lastSelected.current) return;
    list.current?.querySelector(`[data-hash="${lastSelected.current}"]`)?.scrollIntoView({ block: "center" });
  }, [selected]);

  if (selected) {
    return <DiffPanel session={session} commit={selected} back={() => setSelected(null)} />;
  }

  const shown = branches.find((b) => b.ref === ref)?.name ?? branch ?? "Current branch";
  return (
    <div className="panel-pane">
      <div className="panel-toolbar">
        <button
          ref={menuButton}
          className="toolbar-menu"
          title="Browse a branch’s history without checking it out"
          onClick={() => menuUnder(menuButton.current!, [
            { label: "Checked-out branch", checked: ref === undefined, action: () => setRef(undefined) },
            ...branches.map((b) => ({
              label: b.name + (b.current ? " (checked out)" : ""),
              checked: ref === b.ref,
              action: () => setRef(b.current ? undefined : b.ref),
            })),
          ])}
        >
          <Sym icon={GitCommitHorizontal} sf="point.3.connected.trianglepath.dotted" size={12} weight="medium" />
          <span className="toolbar-menu-title history-branch">{shown}</span>
          <Sym icon={ChevronDown} sf="chevron.down" size={8} weight="semibold" />
        </button>
        <span className="list-spacer" />
        <PanelButton sf="arrow.clockwise" icon={RotateCw} help="Refresh history" disabled={loading}
          onClick={() => {
            void reload();
            void readBranches();
          }} />
      </div>
      <div className="panel-divider" />
      {!commits.length && loading ? (
        <div className="loaded-wait"><Spinner /></div>
      ) : !commits.length && !problem ? (
        <div className="empty-note">No commits here yet.</div>
      ) : (
        <div className="history-list" ref={list}>
          {commits.map((commit) => (
            <div key={commit.hash} data-hash={commit.hash}>
              <CommitRow commit={commit} open={() => {
                lastSelected.current = commit.hash;
                setSelected(commit);
              }} />
              <div className="panel-divider" />
            </div>
          ))}
          {problem && <div className="history-problem">{problem}</div>}
          {(more || problem) && (
            <button className="history-more" disabled={loading} onClick={() => void page(generation.current, commitsRef.current.length)}>
              {loading ? "Loading…" : problem ? "Try again" : "Load older commits"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function CommitRow({ commit, open }: { commit: WorkspaceCommit; open: () => void }) {
  return (
    <button className="commit-row" onClick={open} title={`${commit.subject}\n${dateNumeric(commit.authoredAt)}`}>
      <span className="commit-glyph" aria-hidden><span /><i /><span /></span>
      <span className="commit-text">
        <span className="commit-subject">{commit.subject}</span>
        <span className="commit-meta">
          <span className="commit-hash">{commit.hash.slice(0, 7)}</span>
          <span className="faint">·</span>
          <span className="commit-author">{commit.author}</span>
          <span className="faint">·</span>
          <span className="commit-when">{relativeShort(commit.authoredAt)}</span>
          <span className="list-spacer" />
          <span className="commit-counts">
            {(commit.added ?? 0) > 0 && <span className="ok">+{commit.added!.toLocaleString("en-US")}</span>}
            {(commit.removed ?? 0) > 0 && <span className="error-text">−{commit.removed!.toLocaleString("en-US")}</span>}
          </span>
        </span>
      </span>
      <span className="commit-chevron"><Sym icon={ChevronRight} sf="chevron.right" size={8} weight="semibold" /></span>
    </button>
  );
}
