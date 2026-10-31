// One agent's conversation, as AgentThreadView.swift draws it: replies in
// markdown with a copy button, runs of tool calls folded into one line, and
// the user's messages as bubbles.

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronRight, Copy, FileText, Globe, ListChecks, Pencil, Search, SquareTerminal, Users, Wrench, CircleX } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { Message, Session } from "@shared/protocol";
import { groupTools, runFinished } from "@shared/toolgroups";
import { Markdown } from "./Markdown";
import { runSummary, toolKind, type ToolKind } from "./labels";
import { follow, loadOlder, useStore } from "./store";
import { Icon } from "./ui";
import "./styles/thread.css";

const KIND_ICON: Record<ToolKind, [LucideIcon, string]> = {
  terminal: [SquareTerminal, "terminal"], read: [FileText, "doc.text"], edit: [Pencil, "pencil"], search: [Search, "magnifyingglass"],
  web: [Globe, "globe"], task: [Users, "person.2"], plan: [ListChecks, "checklist"], other: [Wrench, "wrench"],
};

function CallRow({ message }: { message: Message }) {
  const tool = message.tool!;
  const [open, setOpen] = useState(false);
  const failed = tool.status === "error";
  const title = tool.title || [tool.name, tool.summary].filter(Boolean).join(" ");
  return (
    <div className="call">
      <div className="call-row" onClick={() => setOpen(!open)}>
        <span className={`call-icon ${failed ? "failed" : ""}`}>
          {tool.status === "running" ? <span className="loader small" /> : failed ? <Icon icon={CircleX} sf="xmark.circle.fill" size={10} weight="medium" /> : <Icon icon={KIND_ICON[toolKind(tool.name)][0]} sf={KIND_ICON[toolKind(tool.name)][1]} size={10} weight="medium" />}
        </span>
        <span className="call-title">{title}</span>
      </div>
      {open && tool.summary && <div className="call-detail mono">{tool.summary}</div>}
    </div>
  );
}

function ToolRun({ calls }: { calls: Message[] }) {
  const [open, setOpen] = useState(false);
  const failed = calls.filter((c) => c.tool?.status === "error").length;
  const running = !runFinished(calls);
  return (
    <div className="tool-run">
      <button className="run-toggle" onClick={() => setOpen(!open)}>
        {running ? <span className="loader small" /> : <Icon icon={Wrench} sf="wrench.and.screwdriver" size={10} weight="medium" />}
        <span className="run-summary">{runSummary(calls)}</span>
        {failed > 0 && <span className="run-failed">· {failed} failed</span>}
        <Icon icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" className={`run-chevron ${open ? "open" : ""}`} />
      </button>
      {open && (
        <div className="run-calls">
          {calls.map((c) => <CallRow key={c.id} message={c} />)}
        </div>
      )}
    </div>
  );
}

function Reply({ message }: { message: Message }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(message.text ?? "");
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className={`reply ${message.isSidechain ? "sidechain" : ""}`}>
      <div className="reply-body"><Markdown text={message.text ?? ""} /></div>
      <button className={`copy ${copied ? "copied" : ""}`} onClick={() => void copy()} aria-label="Copy">
        <Icon icon={copied ? Check : Copy} sf={copied ? "checkmark" : "doc.on.doc"} size={11} />
      </button>
    </div>
  );
}

function Bubble({ text }: { text: string }) {
  const long = text.length > 700 || text.split("\n").length > 10;
  const [more, setMore] = useState(false);
  return (
    <div className="user-row">
      <div className="bubble-col">
        <div className={`bubble ${long && !more ? "clamped" : ""}`}>{text}</div>
        {long && <button className="show-more" onClick={() => setMore(!more)}>{more ? "Show less" : "Show more"}</button>}
      </div>
    </div>
  );
}

export function Thread({ session, stickToEnd }: { session: Session; stickToEnd: number }) {
  const { messages, hasMore } = useStore();
  const list = messages[session.id] ?? [];
  const items = useMemo(() => groupTools(list), [list]);
  const scroller = useRef<HTMLDivElement>(null);
  const atEnd = useRef(true);

  useEffect(() => follow(session.id), [session.id]);
  useEffect(() => {
    atEnd.current = true;
  }, [session.id, stickToEnd]);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atEnd.current) el.scrollTop = el.scrollHeight;
  }, [items.length, session.id, stickToEnd]);

  const onScroll = () => {
    const el = scroller.current;
    if (el) atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  return (
    <div className="thread-scroll" ref={scroller} onScroll={onScroll}>
      <div className="thread-column">
        {hasMore[session.id] && <button className="older" onClick={() => loadOlder(session.id)}>Show earlier messages</button>}
        {items.map((item) => {
          if (item.kind === "tools") return <ToolRun key={item.key} calls={item.calls} />;
          const m = item.message;
          if (m.role === "tool" && m.tool) return <div key={m.id} className="tool-run single"><CallRow message={m} /></div>;
          if (!m.text) return null;
          if (m.role === "user") return <Bubble key={m.id} text={m.text} />;
          if (m.role === "system") return <div key={m.id} className="system">{m.text}</div>;
          return <Reply key={m.id} message={m} />;
        })}
      </div>
    </div>
  );
}
