// What the workspace card shows when opened up (WorkspacePanel.swift): the
// changes, the folder's files, the plans the agent wrote, and over them the
// diff, the file or the plan picked.

import { useEffect, useState } from "react";
import { Check, ChevronRight, Copy, FileText, Folder, Pencil, ClipboardList, ListChecks, Footprints, FileType, Image, Film, File, CheckCircle2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { Artifact, Session, WorkspaceChange } from "@shared/protocol";
import { openEditor, reveal } from "../host";
import { agentName, since } from "../labels";
import { openLightbox } from "../lightbox";
import { Markdown } from "../Markdown";
import { contextMenu } from "../thread/menu";
import { Icon } from "../ui";
import { CodeView } from "../panel/CodeView";
import { Sym } from "../panel/Sym";
import { byteCount, changeLetter, changeTint, compareNames, FileTypeIcon, folderOf, nameOf } from "../panel/files";
import { parseRows, type Row } from "../panel/diffLayout";
import { copyText, copyImage } from "../panel/clipboard";
import { artifacts, review, workspace } from "./api";
import { EmptyNote, Loaded } from "./Loaded";
import { fileManager } from "./place";

export type CardRoute =
  | { kind: "diff"; path: string }
  | { kind: "file"; path: string }
  | { kind: "folder"; path: string }
  | { kind: "artifact"; artifact: Artifact };

export function routeTitle(route: CardRoute): string {
  return route.kind === "artifact" ? route.artifact.title ?? route.artifact.name : route.path;
}

// MARK: Changes

export function ChangesList({ session, open }: { session: Session; open: (route: CardRoute) => void }) {
  // Read again when a turn ends.
  const [reload, setReload] = useState(0);
  const [seen, setSeen] = useState(session.state);
  useEffect(() => {
    if (session.state !== seen) {
      setSeen(session.state);
      if (session.state !== "busy") setReload((n) => n + 1);
    }
  }, [session.state, seen]);
  return (
    <Loaded load={() => workspace("list_changes", session.id)} deps={[session.id, reload]}>
      {(result) => {
        const changes = result.changes ?? [];
        if (!changes.length) {
          return <EmptyNote text={(result.hidden ?? 0) > 0 ? "Only files holding secrets have changed; they aren't shown." : "Nothing has changed since the last commit."} />;
        }
        const added = changes.reduce((n, c) => n + (c.added ?? 0), 0);
        const removed = changes.reduce((n, c) => n + (c.removed ?? 0), 0);
        return (
          <div className="card-list">
            <div className="list-row changes-summary no-separator">
              <span>{changes.length} file{changes.length === 1 ? "" : "s"} changed</span>
              {added > 0 && <span className="ok">+{added.toLocaleString("en-US")}</span>}
              {removed > 0 && <span className="error-text">−{removed.toLocaleString("en-US")}</span>}
            </div>
            {changes.map((change) => <ChangeRow key={change.path} change={change} onClick={() => open({ kind: "diff", path: change.path })} />)}
            {(result.hidden ?? 0) > 0 && (
              <div className="list-row faint caption">{result.hidden} file{result.hidden === 1 ? "" : "s"} holding secrets not shown</div>
            )}
          </div>
        );
      }}
    </Loaded>
  );
}

function ChangeRow({ change, onClick }: { change: WorkspaceChange; onClick: () => void }) {
  return (
    <div className="list-row change-row" onClick={onClick} role="button">
      <span className="change-letter" style={{ color: changeTint(change.status) }}>{changeLetter(change.status)}</span>
      <PathLabel path={change.path} />
      <span className="list-spacer" />
      {(change.added ?? 0) > 0 && <span className="ok">+{change.added}</span>}
      {(change.removed ?? 0) > 0 && <span className="error-text">−{change.removed}</span>}
    </div>
  );
}

/** A path as its file's name, with the folder it is in dimmed before it. */
function PathLabel({ path }: { path: string }) {
  const folder = folderOf(path);
  return (
    <span className="path-label" title={path}>
      {folder && <span className="path-folder"><bdi>{folder}/</bdi></span>}
      <span className="path-name">{nameOf(path)}</span>
    </span>
  );
}

// MARK: Diffs

export function DiffLoader({ session, path }: { session: Session; path: string }) {
  return (
    <Loaded load={() => workspace("file_diff", session.id, { path })} deps={[session.id, path]}>
      {(result) => {
        const rows = parseRows(result.diff ?? "");
        return (
          <div className="card-pane">
            <FileBar session={session} path={path} added={rows.filter((r) => r.kind === "add").length}
              removed={rows.filter((r) => r.kind === "remove").length} copy={result.diff} local />
            <div className="card-divider" />
            {rows.length ? (
              <DiffView rows={rows} truncated={!!result.truncated} />
            ) : (
              <EmptyNote text={result.truncated ? "This diff is too large to show." : "No line changes: a binary file, or only its mode changed."} />
            )}
          </div>
        );
      }}
    </Loaded>
  );
}

/** A diff as lines numbered by what they were and became, added lines on
 *  green and removed on red. */
export function DiffView({ rows, truncated }: { rows: Row[]; truncated: boolean }) {
  return (
    <div className="card-diff">
      <div className="card-diff-lines">
        {rows.map((row) => (
          <div key={row.id} className={`card-diff-row ${row.kind}`}>
            <span className="card-diff-number">{row.oldNumber ?? ""}</span>
            <span className="card-diff-number">{row.newNumber ?? ""}</span>
            <span className="card-diff-marker">{row.kind === "add" ? "+" : row.kind === "remove" ? "−" : ""}</span>
            <span className="card-diff-text">{row.text || " "}</span>
          </div>
        ))}
        {truncated && <div className="card-diff-more">The rest of this diff is too large to show.</div>}
      </div>
    </div>
  );
}

// MARK: Files

export function FileList({ session, path, open }: { session: Session; path: string; open: (route: CardRoute) => void }) {
  return (
    <Loaded load={() => workspace("list_files", session.id, { path })} deps={[session.id, path]}>
      {(result) => {
        const entries = [...(result.entries ?? [])].sort((a, b) =>
          a.directory !== b.directory ? (a.directory ? -1 : 1) : compareNames(a.name, b.name));
        if (!entries.length) return <EmptyNote text="This folder is empty." />;
        return (
          <div className="card-list">
            {entries.map((entry) => {
              const child = path ? `${path}/${entry.name}` : entry.name;
              return (
                <div key={entry.name} className="list-row file-row" role="button"
                  onClick={() => open(entry.directory ? { kind: "folder", path: child } : { kind: "file", path: child })}>
                  <span className="file-row-icon"><FileTypeIcon name={entry.name} directory={entry.directory} size={15} /></span>
                  <span className="file-row-name">{entry.name}</span>
                  <span className="list-spacer" />
                  {entry.size !== undefined && !entry.directory && <span className="faint">{byteCount(entry.size)}</span>}
                  {entry.directory && <span className="faint chevron"><Sym icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" /></span>}
                </div>
              );
            })}
          </div>
        );
      }}
    </Loaded>
  );
}

export function FileLoader({ session, path }: { session: Session; path: string }) {
  return (
    <Loaded load={() => workspace("read_file", session.id, { path })} deps={[session.id, path]}>
      {(result) => {
        if (result.image) {
          const src = `data:${result.mime ?? "image/png"};base64,${result.image}`;
          return (
            <div className="card-image" title="Open full window">
              <img
                src={src}
                alt=""
                onClick={() => openLightbox([{ src, title: nameOf(path), path: `${session.cwd}/${path}` }])}
                onContextMenu={(e) => contextMenu(e, [{ label: "Copy Image", action: () => void copyImage(src) }])}
              />
            </div>
          );
        }
        if (result.text !== undefined) {
          const lines = result.text.split("\n").length;
          return (
            <div className="card-pane">
              <FileBar session={session} path={path} lines={lines} copy={result.text} local />
              <div className="card-divider" />
              <CodeView text={result.text} path={path} />
              {result.truncated && <div className="long-file">Showing the start of a long file.</div>}
            </div>
          );
        }
        return <EmptyNote text="This file can't be shown here." />;
      }}
    </Loaded>
  );
}

/** The bar over a file or a diff: where it is, how big or how much changed,
 *  and what can be done with it. */
function FileBar({ session, path, added, removed, lines, copy, local }: {
  session: Session; path: string; added?: number; removed?: number; lines?: number; copy?: string; local: boolean;
}) {
  const [copied, setCopied] = useState(false);
  const folder = folderOf(path);
  return (
    <div className="file-bar">
      <FileTypeIcon name={path} size={14} />
      <span className="file-bar-folder" title={path}><bdi>{folder || nameOf(session.cwd)}</bdi></span>
      <span className="list-spacer" />
      {lines !== undefined && <span className="faint">{lines.toLocaleString("en-US")} line{lines === 1 ? "" : "s"}</span>}
      {!!added && <span className="mono-11 ok">+{added}</span>}
      {!!removed && <span className="mono-11 error-text">−{removed}</span>}
      {copy !== undefined && (
        <button className={`bar-icon ${copied ? "copied" : ""}`} title={copied ? "Copied" : "Copy"}
          onClick={() => {
            void copyText(copy);
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1500);
          }}>
          <Icon icon={copied ? Check : Copy} sf={copied ? "checkmark" : "doc.on.doc"} size={11} />
        </button>
      )}
      {local && (
        <>
          <button className="bar-icon" title="Edit in the editor" onClick={() => void openEditor({ folder: session.cwd, file: path })}>
            <Icon icon={Pencil} sf="pencil" size={11} />
          </button>
          <button className="bar-icon" title={`Show in ${fileManager()}`} onClick={() => void reveal(`${session.cwd}/${path}`)}>
            <Icon icon={Folder} sf="folder" size={11} />
          </button>
        </>
      )}
    </div>
  );
}

// MARK: Plans

const KIND: Record<string, [string, LucideIcon]> = {
  plan: ["list.bullet.clipboard", ClipboardList],
  task: ["checklist", ListChecks],
  walkthrough: ["figure.walk", Footprints],
  spec: ["doc.richtext", FileType],
  image: ["photo", Image],
  video: ["film", Film],
};

/** "2 minutes ago", as SwiftUI's relative date style says it. */
function relative(ms: number): string {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  const unit = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;
  if (s < 60) return unit(s, "second");
  if (s < 3600) return unit(Math.floor(s / 60), "minute");
  if (s < 86400) {
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
    return m ? `${unit(h, "hour")}, ${unit(m, "minute")}` : unit(h, "hour");
  }
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600);
  if (d < 7) return h ? `${unit(d, "day")}, ${unit(h, "hour")}` : unit(d, "day");
  const w = Math.floor(d / 7), rd = d % 7;
  return rd ? `${unit(w, "week")}, ${unit(rd, "day")}` : unit(w, "week");
}
void since;

export function ArtifactList({ session, open }: { session: Session; open: (route: CardRoute) => void }) {
  return (
    <Loaded load={() => artifacts(session.id)} deps={[session.id]}>
      {(list) => {
        if (!list.length) return <EmptyNote text="This agent hasn't written any plans or documents." />;
        return (
          <div className="card-list">
            {list.map((artifact) => {
              const [sf, icon] = KIND[artifact.kind] ?? ["doc", File];
              return (
                <div key={artifact.name} className="list-row artifact-row" role="button" onClick={() => open({ kind: "artifact", artifact })}>
                  <span className="artifact-icon"><Icon icon={icon} sf={sf} size={13} /></span>
                  <div className="artifact-text">
                    <div className="artifact-title-line">
                      <span className="artifact-title">{artifact.title ?? artifact.name}</span>
                      {artifact.review && <span className="review-pill">Needs your review</span>}
                    </div>
                    {artifact.summary && <div className="artifact-summary">{artifact.summary}</div>}
                    <div className="faint">{relative(artifact.updatedAt)}</div>
                  </div>
                </div>
              );
            })}
          </div>
        );
      }}
    </Loaded>
  );
}

/** A plan or document as it reads, with Approve and Ask for changes when the
 *  agent asked for the user's review. */
export function ArtifactView({ session, artifact }: { session: Session; artifact: Artifact }) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const send = async (approve: boolean) => {
    setBusy(true);
    setProblem(null);
    const failed = await review(session.id, artifact.name, approve, comment);
    setBusy(false);
    if (failed) setProblem(failed);
    else setOutcome(approve ? "Approved" : "Sent your changes");
  };
  return (
    <div className="card-pane">
      <Loaded load={() => workspace("read_artifact", session.id, { path: artifact.name })} deps={[session.id, artifact.name]}>
        {(result) => result.image ? (
          <div className="artifact-scroll"><img className="artifact-image" src={`data:${result.mime ?? "image/png"};base64,${result.image}`} alt="" /></div>
        ) : (
          <div className="artifact-scroll selectable"><div className="artifact-doc"><Markdown text={result.text ?? ""} /></div></div>
        )}
      </Loaded>
      {artifact.review && (
        <>
          <div className="card-divider" />
          <div className="review-box">
            {outcome ? (
              <div className="review-outcome"><Icon icon={CheckCircle2} sf="checkmark.circle" size={12} /> {outcome}</div>
            ) : (
              <>
                <div className="review-ask">{agentName(session.kind)} asked for your review</div>
                <textarea className="review-field" rows={1} value={comment} placeholder="What should change? (for Ask for changes)"
                  onChange={(e) => setComment(e.target.value)} />
                <div className="review-buttons">
                  {problem && <span className="error-text">{problem}</span>}
                  <span className="list-spacer" />
                  <button className="push-button" disabled={busy || !comment.trim()} onClick={() => void send(false)}>Ask for changes</button>
                  <button className="push-button default" disabled={busy} onClick={() => void send(true)}>Approve</button>
                </div>
              </>
            )}
          </div>
        </>
      )}
    </div>
  );
}

export { FileText };
