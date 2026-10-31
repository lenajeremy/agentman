// The Diff tab (RightPanel.swift's DiffPanel and DiffPanelModel): everything
// the agent's folder has changed since its last commit, or what a commit
// changed, file by file, stacked or split, with the changed files in a tree
// beside them. Each file's diff is read when it comes into view and again
// only when its entry in the list changes or a turn begins or ends.

import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDownUp, Check, ChevronDown, ChevronLeft, ChevronRight, CircleDot, CircleMinus, CirclePlus, CircleArrowRight, Copy,
  CornerDownLeft, FoldVertical, ListTree, Pilcrow, Rows2, Columns2, UnfoldVertical, X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { Session, WorkspaceChange, WorkspaceCommit } from "@shared/protocol";
import { openEditor, reveal } from "../host";
import { usePref } from "../prefs";
import { contextMenu, menuUnder } from "../thread/menu";
import { Icon } from "../ui";
import { workspace } from "../workspace/api";
import { Spinner } from "../workspace/Loaded";
import { fileManager } from "../workspace/place";
import { copyText } from "./clipboard";
import { dateAndTime } from "./dates";
import {
  digitsOf, foldLabel, layoutItems, layoutSplit, parseRows, type Fold, type Item, type Line, type SplitItem,
} from "./diffLayout";
import { changeLetter, changeSymbol, changeTint, FileTypeIcon, treeOrder } from "./files";
import { languageFor, runs } from "./highlight";
import { PanelButton } from "./PanelButton";
import { Sym } from "./Sym";

interface FileDiff {
  text: string;
  items: Item[];
  split: SplitItem[];
  digits: number;
  truncated: boolean;
}

const TREE_WIDTH = 196;
/** How often the list is read while the agent works. */
const EVERY = 2500;

const STATUS_ICON: Record<string, LucideIcon> = {
  "plus.circle": CirclePlus, "minus.circle": CircleMinus, "arrow.right.circle": CircleArrowRight, "circle.inset.filled": CircleDot,
};

function layout(text: string, truncated: boolean, opened: Set<number>, file?: string[]): FileDiff {
  const rows = parseRows(text);
  const items = layoutItems(rows, opened, file);
  return { text, items, split: layoutSplit(items), digits: digitsOf(rows), truncated };
}

export function DiffPanel({ session, commit, back }: { session: Session; commit?: WorkspaceCommit; back?: () => void }) {
  const [split, setSplit] = usePref("diffSplit", false);
  const [wrap, setWrap] = usePref("diffWrap", true);
  const [ignoreWhitespace, setIgnoreWhitespace] = usePref("diffIgnoreWhitespace", false);
  const [showTree, setShowTree] = usePref("diffFileTree", true);

  const [changes, setChanges] = useState<WorkspaceChange[]>([]);
  const [hidden, setHidden] = useState(0);
  const [problem, setProblem] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [detail, setDetail] = useState<WorkspaceCommit | undefined>();
  const [diffs, setDiffs] = useState<Record<string, FileDiff>>({});
  const [failures, setFailures] = useState<Record<string, string>>({});
  const [versions, setVersions] = useState<Record<string, number>>({});
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [picked, setPicked] = useState<string | null>(null);

  // What the model keeps between reads, outside React's state.
  const m = useRef({
    readAt: 0,
    seenState: undefined as string | undefined,
    whitespace: ignoreWhitespace,
    changes: [] as WorkspaceChange[],
    versions: {} as Record<string, number>,
    readVersions: {} as Record<string, number>,
    loading: new Set<string>(),
    opened: {} as Record<string, Set<number>>,
    files: {} as Record<string, string[]>,
    diffs: {} as Record<string, FileDiff>,
  });

  const refresh = useCallback(async (everything: boolean) => {
    const model = m.current;
    model.readAt = Date.now();
    try {
      const result = await workspace(commit ? "commit_files" : "list_changes", session.id, { commit: commit?.hash });
      setDetail(result.commit);
      const listed = [...(result.changes ?? [])].sort((a, b) => treeOrder(a.path, b.path));
      const whitespace = ignoreWhitespace !== model.whitespace;
      if (whitespace) {
        model.whitespace = ignoreWhitespace;
        model.opened = {};
      }
      const before = new Map(model.changes.map((c) => [c.path, c]));
      const nextVersions = { ...model.versions };
      for (const change of listed) {
        const was = before.get(change.path);
        const same = was && was.status === change.status && was.added === change.added && was.removed === change.removed;
        if (everything || whitespace || !same) nextVersions[change.path] = (nextVersions[change.path] ?? 0) + 1;
      }
      model.versions = nextVersions;
      setVersions(nextVersions);
      if (JSON.stringify(listed) !== JSON.stringify(model.changes)) {
        model.changes = listed;
        setChanges(listed);
      }
      setHidden(result.hidden ?? 0);
      setProblem(null);
      const paths = new Set(listed.map((c) => c.path));
      if (Object.keys(model.diffs).some((p) => !paths.has(p))) {
        model.diffs = Object.fromEntries(Object.entries(model.diffs).filter(([p]) => paths.has(p)));
        setDiffs(model.diffs);
      }
    } catch (err) {
      setProblem(err instanceof Error ? err.message : String(err));
    }
    setLoaded(true);
  }, [commit, session.id, ignoreWhitespace]);

  // Reads a file's diff, unless it was read since it last changed.
  const load = useCallback(async (path: string) => {
    const model = m.current;
    const version = model.versions[path] ?? 0;
    if (model.readVersions[path] === version || model.loading.has(path)) return;
    model.loading.add(path);
    try {
      const result = await workspace(commit ? "commit_diff" : "file_diff", session.id, { path, commit: commit?.hash, ignoreWhitespace: model.whitespace });
      const text = result.diff ?? "";
      const old = model.diffs[path];
      if (!old || old.text !== text || old.truncated !== !!result.truncated) {
        delete model.opened[path];
        delete model.files[path];
        model.diffs = { ...model.diffs, [path]: layout(text, !!result.truncated, new Set()) };
        setDiffs(model.diffs);
      }
      setFailures((f) => {
        if (!(path in f)) return f;
        const { [path]: _, ...rest } = f;
        return rest;
      });
    } catch (err) {
      setFailures((f) => ({ ...f, [path]: err instanceof Error ? err.message : String(err) }));
    }
    model.readVersions[path] = version;
    model.loading.delete(path);
    // Changed again while it was being read.
    if ((model.versions[path] ?? 0) !== version) void load(path);
  }, [commit, session.id]);

  // Shows the unchanged lines a fold stands for, read from the file: as it
  // is now, or as the commit left it.
  const openFold = useCallback(async (fold: Fold, path: string) => {
    const model = m.current;
    if (!model.files[path]) {
      const read = commit
        ? await workspace("commit_diff", session.id, { path, commit: commit.hash, ignoreWhitespace: model.whitespace, withFile: true }).catch(() => null)
        : await workspace("read_file", session.id, { path }).catch(() => null);
      if (read?.text === undefined) return;
      model.files[path] = read.text.split("\n");
    }
    const opened = new Set(model.opened[path] ?? []);
    opened.add(fold.id);
    model.opened[path] = opened;
    const diff = model.diffs[path];
    if (diff) {
      model.diffs = { ...model.diffs, [path]: layout(diff.text, diff.truncated, opened, model.files[path]) };
      setDiffs(model.diffs);
    }
  }, [commit, session.id]);

  // While the agent works, every few seconds; when a turn begins or ends,
  // everything again, at once; at once for the whitespace button.
  const state = commit ? "idle" : session.state;
  const activity = commit ? 0 : session.lastActivityAt;
  useEffect(() => {
    const model = m.current;
    const turned = loaded && model.seenState !== state;
    let wait = 0;
    if (loaded && model.whitespace === ignoreWhitespace) {
      wait = turned ? 300 : Math.max(400, EVERY - (Date.now() - model.readAt));
    }
    const timer = window.setTimeout(() => {
      model.seenState = state;
      void refresh(turned);
    }, wait);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state, activity, ignoreWhitespace]);

  const added = changes.reduce((n, c) => n + (c.added ?? 0), 0);
  const removed = changes.reduce((n, c) => n + (c.removed ?? 0), 0);
  const allFolded = collapsed.size === changes.length;
  const scopeButton = useRef<HTMLButtonElement>(null);

  const toggle = (path: string) => setCollapsed((c) => {
    const next = new Set(c);
    if (next.has(path)) next.delete(path);
    else next.add(path);
    return next;
  });

  const body = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(540);
  useLayoutEffect(() => {
    const el = body.current;
    if (!el) return;
    const read = () => setWidth(el.clientWidth);
    read();
    const observer = new ResizeObserver(read);
    observer.observe(el);
    return () => observer.disconnect();
  }, [loaded]);
  const tree = showTree && width >= 480;
  const sectionsWidth = width - (tree ? TREE_WIDTH + 1 : 0);
  const scroller = useRef<HTMLDivElement>(null);
  const shownCommit = detail ?? commit;

  return (
    <div className="panel-pane">
      <div className="panel-toolbar diff-toolbar">
        {commit ? (
          <>
            <PanelButton sf="chevron.left" icon={ChevronLeft} help="Back to history" onClick={() => back?.()} />
            <span className="diff-hash" title={commit.subject}>{commit.hash.slice(0, 7)}</span>
          </>
        ) : (
          <button ref={scopeButton} className="toolbar-menu" title="Which changes to show"
            onClick={() => menuUnder(scopeButton.current!, [{ label: "Uncommitted", checked: true, action: () => {} }])}>
            <span>Uncommitted</span>
            <Sym icon={ChevronDown} sf="chevron.down" size={8} weight="semibold" />
          </button>
        )}
        <span className="list-spacer" />
        {changes.length > 0 && (
          <>
            <span className="diff-totals"><span className="ok">+{added}</span><span className="error-text">−{removed}</span></span>
            <PanelButton
              sf={allFolded ? "rectangle.expand.vertical" : "rectangle.compress.vertical"}
              icon={allFolded ? UnfoldVertical : FoldVertical}
              help={allFolded ? "Expand all files" : "Collapse all files"}
              onClick={() => setCollapsed(allFolded ? new Set() : new Set(changes.map((c) => c.path)))}
            />
          </>
        )}
        <span className="segmented">
          <PanelButton sf="rectangle.split.1x2" icon={Rows2} help="Stacked diff view" on={!split} onClick={() => setSplit(false)} />
          <PanelButton sf="rectangle.split.2x1" icon={Columns2} help="Split diff view" on={split} onClick={() => setSplit(true)} />
        </span>
        <PanelButton sf="arrow.turn.down.left" icon={CornerDownLeft} help={wrap ? "Don't wrap long lines" : "Wrap long lines"} on={wrap} onClick={() => setWrap(!wrap)} />
        <PanelButton sf="paragraphsign" icon={Pilcrow} help={ignoreWhitespace ? "Show whitespace changes" : "Hide whitespace changes"}
          on={ignoreWhitespace} onClick={() => setIgnoreWhitespace(!ignoreWhitespace)} />
        <PanelButton sf="list.bullet.indent" icon={ListTree} help={showTree ? "Hide file tree" : "Show file tree"} on={showTree} onClick={() => setShowTree(!showTree)} />
      </div>
      <div className="panel-divider" />
      {shownCommit && (
        <>
          <div className="commit-detail selectable">
            <div className="commit-detail-subject">{shownCommit.subject}</div>
            <div className="commit-detail-meta">{shownCommit.author} · {dateAndTime(shownCommit.authoredAt)}</div>
            {shownCommit.body?.trim() && <CommitBody key={shownCommit.hash} text={shownCommit.body.trim()} />}
          </div>
          <div className="panel-divider" />
        </>
      )}
      {!loaded ? (
        <div className="loaded-wait"><Spinner /></div>
      ) : problem ? (
        <div className="empty-note">{problem}</div>
      ) : !changes.length ? (
        <div className="empty-note">
          {hidden > 0 ? "Only files holding secrets have changed; they aren't shown." : commit ? "No file changes in this commit." : "No uncommitted changes."}
        </div>
      ) : (
        <div className="diff-body" ref={body}>
          <div className={`diff-sections ${wrap || split ? "" : "sideways"}`} ref={scroller} style={{ width: sectionsWidth }}>
            {changes.map((change) => {
              const folded = collapsed.has(change.path);
              return (
                <section key={change.path} className="diff-section" data-path={change.path}>
                  <DiffFileHeader cwd={session.cwd} change={change} collapsed={folded} width={sectionsWidth} toggle={() => toggle(change.path)}
                    onVisible={folded ? undefined : () => void load(change.path)} version={versions[change.path] ?? 0} />
                  {!folded && (
                    <FileSection diff={diffs[change.path]} failure={failures[change.path]} path={change.path} split={split} wrap={wrap}
                      ignoreWhitespace={ignoreWhitespace} width={sectionsWidth} openFold={(fold) => void openFold(fold, change.path)} />
                  )}
                </section>
              );
            })}
            <div style={{ height: 28 }} />
          </div>
          {tree && (
            <>
              <div className="diff-tree-line" />
              <ChangedFilesTree changes={changes} picked={picked} hide={() => setShowTree(false)} pick={(path) => {
                setPicked(path);
                setCollapsed((c) => {
                  const next = new Set(c);
                  next.delete(path);
                  return next;
                });
                requestAnimationFrame(() => {
                  const section = scroller.current?.querySelector<HTMLElement>(`[data-path="${CSS.escape(path)}"]`);
                  if (section && scroller.current) scroller.current.scrollTop = section.offsetTop;
                });
              }} />
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** The rest of a commit's message, over its diff: four lines, and all of it
 *  a click away, scrolling when it is longer than the panel can spare. */
function CommitBody({ text }: { text: string }) {
  const [full, setFull] = useState(false);
  const [cut, setCut] = useState(false);
  const el = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const node = el.current;
    if (node && !full) setCut(node.scrollHeight > node.clientHeight + 1);
  }, [text, full]);
  return (
    <div className="commit-body">
      <div ref={el} className={`commit-body-text ${full ? "full" : "clamped"}`}>{text}</div>
      {(full || cut) && <button className="commit-body-more" onClick={() => setFull(!full)}>{full ? "Show less" : "Show more"}</button>}
    </div>
  );
}

function FileSection({ diff, failure, path, split, wrap, ignoreWhitespace, width, openFold }: {
  diff?: FileDiff; failure?: string; path: string; split: boolean; wrap: boolean; ignoreWhitespace: boolean; width: number; openFold: (fold: Fold) => void;
}) {
  if (!diff) {
    if (failure) return <SectionNote text={failure} width={width} />;
    return <div className="section-wait" style={{ width }}><Spinner /></div>;
  }
  if (!diff.items.length) {
    return <SectionNote width={width} text={diff.truncated ? "This diff is too large to show." : ignoreWhitespace && !diff.text ? "No changes with whitespace hidden." : "No line changes: a binary file, or only its mode changed."} />;
  }
  return (
    <>
      {split
        ? diff.split.map((item) => "fold" in item
          ? <FoldRow key={`f${item.fold.id}`} fold={item.fold} width={width} open={() => openFold(item.fold)} />
          : <PairRow key={item.pair.key} old={item.pair.old} now={item.pair.new} path={path} digits={diff.digits} wrap={wrap} width={width} />)
        : diff.items.map((item) => "fold" in item
          ? <FoldRow key={`f${item.fold.id}`} fold={item.fold} width={width} open={() => openFold(item.fold)} />
          : <LineRow key={item.line.key} line={item.line} path={path} digits={diff.digits} wrap={wrap} width={width} />)}
      {diff.truncated && <SectionNote text="The rest of this diff is too large to show." width={width} />}
    </>
  );
}

function SectionNote({ text, width }: { text: string; width: number }) {
  return <div className="section-note" style={{ width }}>{text}</div>;
}

/** Text that may wrap after a slash, as the Mac's text breaks a path. */
function breakable(text: string): React.ReactNode {
  if (!text.includes("/")) return text;
  const parts = text.split("/");
  return parts.map((part, i) => (i < parts.length - 1 ? <span key={i}>{part}/<wbr /></span> : part));
}

/** The line-number column, for numbers this many digits long. */
function gutter(digits: number): number {
  return digits * Math.ceil(12 * 0.61) + 14;
}

/** A line's text coloured as code, the words that changed marked. Its indent
 *  is in spaces that don't break, a tab four of them: a wrapped line broke at
 *  its indent, leaving a blank line over its code. */
const LineText = memo(function LineText({ line, path }: { line: Line; path: string }) {
  const source = line.row.text || " ";
  const indent = /^[ \t]*/.exec(source)![0];
  const shown = indent.replace(/\t/g, "    ").replace(/ /g, " ");
  const raw = shown + source.slice(indent.length);
  const shift = shown.length - indent.length;
  const language = raw.length <= 1500 ? languageFor(path) : null;
  // Where the changed words are, in the shown text.
  const marks = line.changed.map(([a, b]) => {
    const start = a < indent.length ? a : a + shift;
    return [start, start + (b - a)] as const;
  }).filter(([, end]) => end <= raw.length);
  const markClass = line.row.kind === "add" ? "word-add" : "word-remove";
  const pieces: React.ReactNode[] = [];
  let at = 0;
  let key = 0;
  for (const run of runs(raw, language)) {
    const start = at, end = at + run.text.length;
    at = end;
    // Cut the run where a mark begins or ends.
    const cuts = new Set([start, end]);
    for (const [a, b] of marks) {
      if (a > start && a < end) cuts.add(a);
      if (b > start && b < end) cuts.add(b);
    }
    const points = [...cuts].sort((x, y) => x - y);
    for (let i = 0; i < points.length - 1; i++) {
      const a = points[i], b = points[i + 1];
      const marked = marks.some(([x, y]) => a >= x && b <= y);
      const cls = [run.token ? `tok-${run.token}` : "", marked ? markClass : ""].filter(Boolean).join(" ");
      const text = raw.slice(a, b);
      pieces.push(<span key={key++} className={cls || undefined}>{breakable(text)}</span>);
    }
  }
  return <>{pieces}</>;
});

const LineRow = memo(function LineRow({ line, path, digits, wrap, width }: { line: Line; path: string; digits: number; wrap: boolean; width: number }) {
  const number = line.row.kind === "remove" ? line.row.oldNumber : line.row.newNumber;
  return (
    <div className={`diff-line ${line.row.kind} ${wrap ? "wrap" : "nowrap"}`} style={wrap ? { width } : { minWidth: width }}>
      <span className="diff-number" style={{ width: gutter(digits) }}>{number ?? ""}</span>
      <span className="diff-text"><LineText line={line} path={path} /></span>
    </div>
  );
});

const PairRow = memo(function PairRow({ old, now, path, digits, wrap, width }: {
  old?: Line; now?: Line; path: string; digits: number; wrap: boolean; width: number;
}) {
  const half = Math.max((width - 1) / 2, 0);
  const side = (line: Line | undefined, isOld: boolean) => {
    if (!line) return <div className="pair-side blank" style={{ width: half }} />;
    const number = isOld ? line.row.oldNumber : line.row.newNumber;
    return (
      <div className={`pair-side ${line.row.kind}`} style={{ width: half }}>
        <span className="diff-number split" style={{ width: gutter(digits) }}>{number ?? ""}</span>
        <span className={`diff-text ${wrap ? "wrap" : "clip"}`}><LineText line={line} path={path} /></span>
      </div>
    );
  };
  return (
    <div className="diff-pair" style={{ width }}>
      {side(old, true)}
      <div className="pair-line" />
      {side(now, false)}
    </div>
  );
});

/** Unchanged lines left out of a diff, as one line saying how many; a click
 *  shows them. */
function FoldRow({ fold, width, open }: { fold: Fold; width: number; open: () => void }) {
  return (
    <div className="fold-row" style={{ width }}>
      <button className="fold-button" title="Show the unchanged lines" onClick={open}>
        <Sym icon={ArrowDownUp} sf="arrow.up.and.down" size={9} weight="medium" />
        <span>{foldLabel(fold)}</span>
      </button>
    </div>
  );
}

/** A file's bar over its diff, kept at the top while its diff scrolls: a
 *  click folds the diff away or opens it. */
function DiffFileHeader({ cwd, change, collapsed, width, toggle, onVisible, version }: {
  cwd: string; change: WorkspaceChange; collapsed: boolean; width: number; toggle: () => void; onVisible?: () => void; version: number;
}) {
  const [copied, setCopied] = useState(false);
  const el = useRef<HTMLDivElement>(null);
  // Read when it comes into view, and again when its entry changes.
  useEffect(() => {
    const node = el.current;
    if (!node || !onVisible) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) onVisible();
    }, { rootMargin: "600px 0px" });
    observer.observe(node.parentElement ?? node);
    return () => observer.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version, collapsed]);
  const symbol = changeSymbol(change.status);
  return (
    <div
      ref={el}
      className="diff-file-header"
      style={{ width }}
      onClick={toggle}
      onContextMenu={(e) => contextMenu(e, [
        { label: "Copy Path", action: () => void copyText(change.path) },
        { label: "Open in Editor", action: () => void openEditor({ folder: cwd, file: change.path }) },
        { label: `Show in ${fileManager()}`, action: () => void reveal(`${cwd}/${change.path}`) },
      ])}
    >
      <span className="header-chevron"><Icon icon={collapsed ? ChevronRight : ChevronDown} sf={collapsed ? "chevron.right" : "chevron.down"} size={9} weight="semibold" /></span>
      <span className="header-status" style={{ color: changeTint(change.status) }}><Sym icon={STATUS_ICON[symbol]} sf={symbol} size={11.5} /></span>
      <span className="header-path" title={change.path}><bdi>{change.path}</bdi></span>
      <button className={`header-copy ${copied ? "copied" : ""}`} title="Copy path" onClick={(e) => {
        e.stopPropagation();
        void copyText(change.path);
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      }}>
        <Icon icon={copied ? Check : Copy} sf={copied ? "checkmark" : "doc.on.doc"} size={10.5} />
      </button>
      <span className="list-spacer" />
      <span className="header-counts">
        {(change.removed ?? 0) > 0 && <span className="error-text">−{change.removed}</span>}
        {(change.added ?? 0) > 0 && <span className="ok">+{change.added}</span>}
      </span>
    </div>
  );
}

/** The changed files as a tree beside their diffs; a file picked scrolls its
 *  diff into view. */
function ChangedFilesTree({ changes, picked, hide, pick }: { changes: WorkspaceChange[]; picked: string | null; hide: () => void; pick: (path: string) => void }) {
  const [closed, setClosed] = useState<Set<string>>(new Set());
  const nodes = useMemo(() => {
    const out: { id: string; name: string; depth: number; change?: WorkspaceChange }[] = [];
    const seen = new Set<string>();
    for (const change of changes) {
      const parts = change.path.split("/").filter(Boolean);
      let shut = false;
      for (let depth = 0; depth < Math.max(parts.length - 1, 0); depth++) {
        const folder = parts.slice(0, depth + 1).join("/");
        if (!shut && !seen.has(folder)) {
          seen.add(folder);
          out.push({ id: folder, name: parts[depth], depth });
        }
        if (closed.has(folder)) shut = true;
      }
      if (!shut) out.push({ id: change.path, name: parts[parts.length - 1] ?? change.path, depth: parts.length - 1, change });
    }
    return out;
  }, [changes, closed]);

  return (
    <div className="changed-tree" style={{ width: TREE_WIDTH }}>
      <div className="changed-tree-head">
        <span className="changed-tree-title">Files</span>
        <span className="list-spacer" />
        <span className="faint">{changes.length}</span>
        <button className="changed-tree-hide" title="Hide file tree" onClick={hide}><Icon icon={X} sf="xmark" size={9} weight="semibold" /></button>
      </div>
      <div className="changed-tree-rows">
        {nodes.map((node) => (
          <button
            key={node.id}
            className={`tree-row ${node.change && picked === node.id ? "picked" : ""}`}
            style={{ paddingLeft: node.depth * 12 + 6 }}
            title={node.id}
            onClick={() => {
              if (node.change) pick(node.change.path);
              else setClosed((c) => {
                const next = new Set(c);
                if (next.has(node.id)) next.delete(node.id);
                else next.add(node.id);
                return next;
              });
            }}
          >
            {node.change ? (
              <>
                <FileTypeIcon name={node.name} size={13} />
                <span className="tree-name" style={picked === node.id ? { color: "var(--working)" } : undefined}>{node.name}</span>
                <span className="list-spacer" />
                <span className="tree-letter" style={{ color: changeTint(node.change.status) }}>{changeLetter(node.change.status)}</span>
              </>
            ) : (
              <>
                <span className="tree-chevron"><Icon icon={closed.has(node.id) ? ChevronRight : ChevronDown} sf={closed.has(node.id) ? "chevron.right" : "chevron.down"} size={8} weight="semibold" /></span>
                <span className="tree-folder">{node.name}</span>
                <span className="list-spacer" />
                <span className="tree-dot" />
              </>
            )}
          </button>
        ))}
      </div>
    </div>
  );
}
