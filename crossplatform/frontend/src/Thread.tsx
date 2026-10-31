// One agent's conversation: its words in markdown, its tool steps as quiet
// one-line rows, the user's messages as bubbles, and a box to write to it.

import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import type { Message, Session } from "@shared/protocol";
import { Markdown } from "./Markdown";
import { follow, loadOlder, sendMessage, useStore } from "./store";

function ToolRow({ message }: { message: Message }) {
  const tool = message.tool!;
  return (
    <div className={`tool ${tool.status ?? ""}`}>
      <span className="tool-name">{tool.title || tool.name}</span>
      {tool.summary && !tool.title && <span className="tool-summary">{tool.summary}</span>}
    </div>
  );
}

function Row({ message }: { message: Message }) {
  if (message.tool) return <ToolRow message={message} />;
  if (!message.text) return null;
  if (message.role === "user") {
    return (
      <div className="user-row">
        <div className="bubble">{message.text}</div>
      </div>
    );
  }
  if (message.role === "system") return <div className="system">{message.text}</div>;
  return <Markdown text={message.text} />;
}

export function Thread({ session }: { session: Session }) {
  const { messages, hasMore } = useStore();
  const list = messages[session.id] ?? [];
  const scroller = useRef<HTMLDivElement>(null);
  const atEnd = useRef(true);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);

  useEffect(() => follow(session.id), [session.id]);
  useEffect(() => {
    atEnd.current = true;
  }, [session.id]);

  // Stay at the end while the conversation grows, unless the user scrolled up.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atEnd.current) el.scrollTop = el.scrollHeight;
  }, [list.length, session.id]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  const send = async () => {
    const text = draft.trim();
    if (!text || sending) return;
    setSending(true);
    try {
      await sendMessage(session.id, text);
      setDraft("");
      atEnd.current = true;
    } finally {
      setSending(false);
    }
  };

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  };

  return (
    <div className="thread">
      <div className="thread-scroll" ref={scroller} onScroll={onScroll}>
        <div className="thread-column">
          {hasMore[session.id] && (
            <button className="older" onClick={() => loadOlder(session.id)}>Show earlier messages</button>
          )}
          {list.length === 0 && <div className="empty">No messages yet</div>}
          {list.map((m) => (
            <Row key={m.id} message={m} />
          ))}
        </div>
      </div>
      <div className="composer">
        <textarea
          id="composer"
          value={draft}
          placeholder={`Message ${session.name || "the agent"}`}
          rows={1}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKey}
        />
        <button className="send" disabled={!draft.trim() || sending} onClick={() => void send()} aria-label="Send">
          ↑
        </button>
      </div>
    </div>
  );
}
