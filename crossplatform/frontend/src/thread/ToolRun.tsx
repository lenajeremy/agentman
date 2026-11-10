// Tool calls in a thread (AgentThreadView.swift's ToolRun, ToolCallRow and
// StepDetail; ImagePreview.swift's ImageThumbnail): a run as one quiet line,
// opened one row a call, each call's command and output a click away, and
// the pictures the agent read under their line.

import { useEffect, useMemo, useState, type MouseEvent } from "react";
import {
  ChevronRight, CircleX, FileText, Globe, Image as ImageIcon, ListChecks, Pencil, Search, SquareTerminal, Users, Wrench,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { Message } from "@shared/protocol";
import { parseDiff, type DiffRow } from "@shared/diff";
import { openLightbox } from "../lightbox";
import { Sym } from "./Sym";
import { baseName, runCounts, tidy, toolKind, toolSymbol, toolVerb } from "./items";
import { Loader, Spinner } from "./Loader";
import { contextMenu } from "./menu";
import { copyImage } from "./clipboard";
import { seenImage, titleTools, useToolTitle } from "./state";

const LUCIDE: Record<string, LucideIcon> = {
  terminal: SquareTerminal, "doc.text": FileText, pencil: Pencil, magnifyingglass: Search, globe: Globe,
  "person.2": Users, checklist: ListChecks, wrench: Wrench,
};

function StatusIcon({ call }: { call: Message }) {
  const kind = toolKind(call.tool?.name);
  const symbol = toolSymbol(kind);
  return (
    <span className="call-icon">
      {call.tool?.status === "running" ? (
        // A view without words sits on the baseline by its bottom.
        <span style={{ display: "flex", marginTop: 12 - 11 }}><Loader /></span>
      ) : call.tool?.status === "error" ? (
        <span className="call-failed"><Sym icon={CircleX} sf="xmark.circle.fill" size={10} weight="medium" text={12} /></span>
      ) : (
        <Sym icon={LUCIDE[symbol]} sf={symbol} size={10} weight="medium" text={12} />
      )}
    </span>
  );
}

/** A diff as lines numbered by what they were and became (WorkspacePanel's DiffView). */
function DiffLines({ rows }: { rows: DiffRow[] }) {
  return (
    <div className="step-diff" style={{ height: Math.min(220, rows.length * 17 + 16) }}>
      <div className="step-diff-rows">
        {rows.map((row, i) => (
          <div key={i} className={`diff-line ${row.kind}`}>
            <span className="diff-num">{row.oldNumber ?? ""}</span>
            <span className="diff-num">{row.newNumber ?? ""}</span>
            <span className="diff-mark">{row.kind === "add" ? "+" : row.kind === "remove" ? "−" : ""}</span>
            <span className="diff-text">{row.text === "" ? " " : row.text}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

/** What a step ran and what came of it: its command, then its output, in a
 *  box that scrolls when long. */
function StepDetail({ command, output, failed }: { command?: string; output?: string; failed: boolean }) {
  const text = output?.trim() ?? "";
  const diff = useMemo(() => (text.includes("@@ -") ? parseDiff(text) : []), [text]);
  return (
    <div className="step-detail">
      {command ? <div className="step-command">{command}</div> : null}
      {text && text !== "[image]" && diff.length > 0 ? <DiffLines rows={diff} /> : null}
      {text && text !== "[image]" && diff.length === 0 ? (
        <div className="step-output">
          <div className={`step-output-text ${failed ? "failed" : ""}`}>{text}</div>
        </div>
      ) : null}
    </div>
  );
}

/** One tool call in a line: what it did in plain words, with the raw command
 *  or path a click away; a call from a daemon too old to describe it shows
 *  its verb and target. */
export function ToolCallRow({ call }: { call: Message }) {
  const [open, setOpen] = useState(false);
  const title = call.tool?.title;
  const side = call.isSidechain ? " sidechain" : "";
  if (title) {
    return (
      <div className={`call${side}`}>
        <button className="call-row" onClick={() => setOpen(!open)} title={open ? "Hide the command" : "Show the command"}>
          <StatusIcon call={call} />
          <span className="call-title">{title}</span>
        </button>
        {open && <StepDetail command={call.tool?.summary} output={call.text} failed={call.tool?.status === "error"} />}
      </div>
    );
  }
  const kind = toolKind(call.tool?.name);
  const target = tidy(call.tool?.summary ?? call.text ?? "");
  return (
    <div className={`call-raw${side}`}>
      <StatusIcon call={call} />
      <span className="call-verb">{toolVerb(kind)}</span>
      {target ? <span className="call-target" title={call.tool?.summary ?? ""}>{middle(target)}</span> : null}
    </div>
  );
}

/** Truncated in the middle, as the Mac's lineLimit(1) .truncationMode(.middle):
 *  CSS can only cut the end, so the start and end are two halves. */
function middle(text: string) {
  const cut = Math.ceil(text.length / 2);
  return (
    <span className="middle">
      <span className="middle-start">{text.slice(0, cut)}</span>
      <span className="middle-end">{text.slice(cut)}</span>
    </span>
  );
}

/** A run of tool calls as one quiet line: what was done, in counts, or what
 *  is running now. Opened, one tidy row a call. */
export function ToolRun({ calls, said = "" }: { calls: Message[]; said?: string }) {
  const [open, setOpen] = useState(false);
  const title = useToolTitle(calls);
  const finished = !calls.some((c) => c.tool?.status === "running");
  const failures = calls.filter((c) => c.tool?.status === "error").length;
  const sidechain = calls.every((c) => c.isSidechain);
  useEffect(() => {
    if (finished && calls.length > 1) void titleTools(calls, said);
  }, [finished, calls, said]);

  if (calls.length === 1) {
    return (
      <div className={`tool-run${sidechain ? " sidechain" : ""}`}>
        <ToolCallRow call={calls[0]} />
      </div>
    );
  }
  const last = calls.at(-1)!;
  let summary: string;
  if (last.tool?.status === "running") {
    summary = last.tool.title ? `${last.tool.title}…` : `${toolVerb(toolKind(last.tool.name))} ${tidy(last.tool.summary ?? last.text ?? "")}…`;
  } else {
    summary = title || runCounts(calls);
  }
  return (
    <div className={`tool-run${sidechain ? " sidechain" : ""}`}>
      <button className="run-toggle" onClick={() => setOpen(!open)}>
        {last.tool?.status === "running" ? <Loader /> : <Sym icon={Wrench} sf="wrench.and.screwdriver" size={10} weight="medium" />}
        <span className="run-summary">{summary}</span>
        {failures > 0 && <span className="run-failed">· {failures} failed</span>}
        <span className={`run-chevron ${open ? "open" : ""}`}><Sym icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" /></span>
      </button>
      {open && (
        <>
          <div className="run-rule" />
          <div className="run-calls">
            {calls.map((c) => <ToolCallRow key={c.id} call={c} />)}
          </div>
        </>
      )}
    </div>
  );
}

/** Opens the lightbox on one of the thread's pictures; the arrows go through
 *  the rest. */
function openPicture(sessionId: string, path: string, gallery: readonly string[]) {
  const paths = gallery.includes(path) ? [...gallery] : [path];
  openLightbox(
    paths.map((each) => ({ src: "", title: baseName(each), path: each, load: () => seenImage(sessionId, each) })),
    paths.indexOf(path),
  );
}

/** An image the agent read, as a short label and a thumbnail that opens it
 *  full window. In a thread: its line in a tool card and the picture under
 *  it; alone, for a row of them under one run. */
export function ImageThumbnail({ sessionId, path, gallery, pictureOnly = false }: {
  sessionId: string;
  path: string;
  gallery: readonly string[];
  pictureOnly?: boolean;
}) {
  const [image, setImage] = useState<{ src: string; width: number; height: number } | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let cancelled = false;
    setImage(null);
    setFailed(false);
    void (async () => {
      // A daemon that has just started may not know the agent's files yet;
      // it is asked a few times before the thumbnail gives up.
      for (let attempt = 0; attempt < 4 && !cancelled; attempt += 1) {
        if (attempt > 0) await new Promise((r) => setTimeout(r, 1500));
        const src = await seenImage(sessionId, path);
        if (src) {
          const size = await naturalSize(src);
          if (!cancelled && size) setImage({ src, ...size });
          return;
        }
      }
      if (!cancelled) setFailed(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId, path]);

  const picture = image ? (
    <button
      className="thumb"
      title="Open full window"
      onClick={() => openPicture(sessionId, path, gallery)}
      onContextMenu={(e: MouseEvent) => contextMenu(e, [{ label: "Copy Image", action: () => void copyImage(image.src) }])}
    >
      <img src={image.src} alt="" style={{ width: Math.min(280, Math.max(90, (120 * image.width) / Math.max(image.height, 1))), height: 120 }} />
    </button>
  ) : !failed ? (
    <div className="thumb-wait"><Spinner /></div>
  ) : null;

  if (pictureOnly) return picture;
  return (
    <div className="image-read">
      <div className="image-read-label" title={path}>
        <span className="call-icon"><Sym icon={ImageIcon} sf="photo" size={10} weight="medium" /></span>
        <span className="image-read-name">{`Read ${baseName(path)}`}</span>
      </div>
      {picture && <div className="image-read-picture">{picture}</div>}
    </div>
  );
}

/** Images read one after another: the run's line, and the pictures side by
 *  side under it, as Claude's own app shows them. */
export function PicturesRun({ sessionId, calls, paths, said, gallery }: {
  sessionId: string;
  calls: Message[];
  paths: string[];
  said: string;
  gallery: readonly string[];
}) {
  return (
    <div className="pictures-run">
      <ToolRun calls={calls} said={said} />
      <div className="pictures-row">
        {paths.map((path, i) => <ImageThumbnail key={`${i}-${path}`} sessionId={sessionId} path={path} gallery={gallery} pictureOnly />)}
      </div>
    </div>
  );
}

/** A picture's size in pixels, once it has loaded. */
export function naturalSize(src: string): Promise<{ width: number; height: number } | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve({ width: img.naturalWidth, height: img.naturalHeight });
    img.onerror = () => resolve(null);
    img.src = src;
  });
}
