// One agent's conversation, as AgentThreadView.swift draws it: replies in
// markdown with a copy button, runs of tool calls folded into one line,
// pictures the agent read, the user's messages as bubbles, what is queued
// for the agent, and its question to answer. It follows the end while the
// reader is there (FollowsEnd.swift) and loads what came before at the top.

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Session } from "@shared/protocol";
import { agentName } from "./labels";
import { follow, loadOlder, loadThrough, useStore } from "./store";
import { FindBar } from "./thread/FindBar";
import { markFound, searchThread, takeJump, useJump, type Finding } from "./thread/find";
import { ChangedBar, DropTarget, EmptyThread, JumpToLatest, ReviewBar } from "./thread/Bars";
import { addDropped, dropAccepts } from "./thread/attachments";
import { threadItems, threadPictures } from "./thread/items";
import { LatestTurnLine, WorkedLine } from "./thread/Worked";
import { giveKeyboardBack, keyboardNow, type KeyboardPlace } from "./palette/PaletteView";
import { workDuration } from "@shared/toolgroups";
import { Spinner } from "./thread/Loader";
import { MessageRow, QueuedBubble } from "./thread/Messages";
import { QuestionCard } from "./thread/QuestionCard";
import { ImageThumbnail, PicturesRun, ToolRun } from "./thread/ToolRun";
import { dropDelivered, perform, useQueued } from "./thread/state";
import { useChangedCount } from "./thread/changes";
import "./styles/thread.css";

/** How close to the bottom still counts as there, and how close to the top
 *  starts loading what came before (FollowsEnd.swift). */
const SLACK = 80;
const TOP_SLACK = 160;

export function Thread({ session, stickToEnd, card = true, panel = false, findable = true, onShowChanges, onReview }: {
  session: Session;
  stickToEnd: number;
  /** ⌘F finds in the conversation; not in an editor's panel, where the
   *  editor's own find has it. */
  findable?: boolean;
  /** Whether the workspace card and the right panel show, for "N files changed". */
  card?: boolean;
  panel?: boolean;
  onShowChanges?: () => void;
  onReview?: () => void;
}) {
  const { messages, hasMore } = useStore();
  const list = useMemo(() => messages[session.id] ?? [], [messages, session.id]);
  const items = useMemo(() => threadItems(list), [list]);
  const gallery = useMemo(() => threadPictures(items), [items]);
  const localQueued = useQueued(session.id);
  const changed = useChangedCount(session);
  const scroller = useRef<HTMLDivElement>(null);
  const column = useRef<HTMLDivElement>(null);
  const atEnd = useRef(true);
  const [following, setFollowing] = useState(true);
  const [dropping, setDropping] = useState(false);
  const loading = useRef(false);
  /** The first item and where it was, kept while older messages arrive. */
  const anchor = useRef<{ id: string; top: number } | null>(null);
  const more = !!hasMore[session.id];
  const canReceive = session.inject !== "none";
  const [finding, setFinding] = useState<Finding | null>(null);
  const itemsRef = useRef(items);
  itemsRef.current = items;
  const jump = useJump();

  /** The row a message is in: its own, or the run of tool calls it is part of. */
  const itemOf = useCallback((messageId: string) => itemsRef.current.find((i) =>
    i.id === messageId || ("calls" in i && i.calls.some((c) => c.id === messageId)) || ("message" in i && i.message.id === messageId))?.id, []);

  /** Reads back to a message if it is not loaded yet, then scrolls to it and
   *  stops following the end, so news does not pull the thread away. */
  const reveal = useCallback(async (messageId: string) => {
    atEnd.current = false;
    setFollowing(false);
    if (!(await loadThrough(session.id, messageId))) return;
    // A frame for the rows read back to be laid out before scrolling.
    await new Promise((r) => setTimeout(r, 60));
    const item = itemOf(messageId);
    const el = item ? scroller.current?.querySelector(`[data-item="${CSS.escape(item)}"]`) : null;
    el?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [session.id, itemOf]);

  const step = (delta: number) => {
    if (!finding || !finding.matches.length) return;
    const count = finding.matches.length;
    const at = finding.index !== undefined ? (finding.index + delta + count) % count : delta > 0 ? 0 : count - 1;
    setFinding({ ...finding, index: at });
    void reveal(finding.matches[at].messageId);
  };

  // Typed into the find bar: the whole conversation searched a moment after
  // typing stops, the newest match shown.
  useEffect(() => {
    if (!finding || finding.query === finding.searched) return;
    const query = finding.query;
    let current = true;
    const timer = window.setTimeout(async () => {
      const matches = await searchThread(session.id, query);
      if (!current) return;
      setFinding((f) => (f && f.query === query ? { ...f, searched: query, matches, index: matches.length ? matches.length - 1 : undefined } : f));
      if (matches.length) void reveal(matches[matches.length - 1].messageId);
    }, 200);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [finding?.query, session.id]);

  // Opened from a search: at the message, its words found.
  useEffect(() => {
    const taken = takeJump(session.id);
    if (!taken) return;
    setFinding({ query: taken.query, searched: taken.query, matches: [] });
    void (async () => {
      const matches = await searchThread(session.id, taken.query);
      const index = matches.findIndex((m) => m.messageId === taken.messageId);
      setFinding({ query: taken.query, searched: taken.query, matches, index: index >= 0 ? index : undefined });
      await reveal(taken.messageId);
    })();
  }, [jump?.serial, session.id]);

  // The words marked wherever the rows are drawn.
  useEffect(() => markFound(column.current, finding, itemOf), [finding, items, itemOf]);
  useEffect(() => () => markFound(null, null, itemOf), [itemOf]);

  // ⌘F finds in the conversation. Closed, the find bar gives the keyboard
  // back to where it was when ⌘F was pressed: the message box, usually.
  const [findFocus, setFindFocus] = useState(0);
  const findFrom = useRef<KeyboardPlace | null>(null);
  const closeFind = () => {
    setFinding(null);
    const from = findFrom.current;
    findFrom.current = null;
    // At once, while the find bar still holds it: a frame later, keys typed
    // meanwhile were lost.
    if (from) giveKeyboardBack(from);
  };
  useEffect(() => {
    if (!findable) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "f") {
        e.preventDefault();
        if (!(document.activeElement as HTMLElement | null)?.closest?.(".find-bar")) findFrom.current = keyboardNow();
        setFinding((f) => f ?? { query: "", matches: [] });
        setFindFocus((n) => n + 1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [findable]);

  useEffect(() => follow(session.id), [session.id]);
  useEffect(() => dropDelivered(session.id, list), [session.id, list]);

  const toEnd = useCallback((smooth: boolean) => {
    const el = scroller.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? "smooth" : "auto" });
  }, []);

  const setAtEnd = (value: boolean) => {
    atEnd.current = value;
    setFollowing(value);
  };

  // A new agent, or the user sent: back to the end.
  useLayoutEffect(() => {
    setAtEnd(true);
    toEnd(false);
  }, [session.id, toEnd]);
  useEffect(() => {
    if (stickToEnd === 0) return;
    setAtEnd(true);
    toEnd(true);
  }, [stickToEnd, toEnd]);

  // Older messages arrived above: the first one shown goes back to where it
  // was, so the reader's place doesn't move.
  useLayoutEffect(() => {
    const el = scroller.current;
    const held = anchor.current;
    if (!el || !held) return;
    const row = el.querySelector<HTMLElement>(`[data-item="${CSS.escape(held.id)}"]`);
    if (row) el.scrollTop += row.getBoundingClientRect().top - held.top;
    anchor.current = null;
  }, [items]);

  // Growth while at the end keeps the end in view: new messages, streamed
  // words, pictures loading.
  useEffect(() => {
    const el = scroller.current;
    const inner = column.current;
    if (!el || !inner) return;
    const observer = new ResizeObserver(() => {
      if (atEnd.current) el.scrollTop = el.scrollHeight;
    });
    observer.observe(inner);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const lastId = list.at(-1)?.id;
  const questionId = session.question?.id;
  useEffect(() => {
    if (atEnd.current) toEnd(true);
  }, [lastId, questionId, toEnd]);

  const earlier = useCallback(async () => {
    const el = scroller.current;
    if (loading.current || !more || !el) return;
    loading.current = true;
    const first = el.querySelector<HTMLElement>("[data-item]");
    if (first) anchor.current = { id: first.dataset.item!, top: first.getBoundingClientRect().top };
    await loadOlder(session.id);
    // A beat for the page to be laid out before the top counts again.
    setTimeout(() => {
      loading.current = false;
      const now = scroller.current;
      if (now && now.scrollHeight <= now.clientHeight) void earlier();
    }, 300);
  }, [more, session.id]);

  // Messages too few to scroll have no top to arrive at.
  useEffect(() => {
    const el = scroller.current;
    if (!el || !more) return;
    const timer = setTimeout(() => {
      if (el.scrollHeight <= el.clientHeight) void earlier();
    }, 500);
    return () => clearTimeout(timer);
  }, [items.length, more, earlier]);

  const lastTop = useRef(0);
  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    const end = el.scrollTop + el.clientHeight >= el.scrollHeight - SLACK;
    // Only the reader leaves the end: growth that lands before the view
    // catches up mustn't stop it following.
    if (end !== atEnd.current && (end || el.scrollTop < lastTop.current)) setAtEnd(end);
    if (el.scrollTop <= TOP_SLACK && lastTop.current > TOP_SLACK && !atEnd.current) void earlier();
    lastTop.current = el.scrollTop;
  };

  const queued = session.queued ?? [];
  const canSendNow = session.kind === "claude" && session.inject === "tmux";
  const empty = list.length === 0 && queued.length === 0 && localQueued.length === 0 && !session.question;

  return (
    <div
      className="thread"
      onDragEnter={(e) => {
        if (canReceive && dropAccepts(e.dataTransfer)) setDropping(true);
      }}
      onDragOver={(e) => {
        if (!canReceive || !dropAccepts(e.dataTransfer)) return;
        e.preventDefault();
        setDropping(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropping(false);
      }}
      onDrop={(e) => {
        setDropping(false);
        if (!canReceive) return;
        e.preventDefault();
        void addDropped(session.id, e.dataTransfer);
      }}
    >
      {session.artifactsToReview ? (
        <div className="thread-bar-wrap">
          <ReviewBar count={session.artifactsToReview} agent={agentName(session.kind)} onOpen={() => onReview?.()} />
        </div>
      ) : null}
      {changed > 0 && !card && !panel ? (
        <div className="thread-bar-wrap">
          <ChangedBar count={changed} onOpen={() => onShowChanges?.()} />
        </div>
      ) : null}
      <div className="thread-body">
        <div className="thread-scroll" ref={scroller} onScroll={onScroll}>
          <div className="thread-column" ref={column}>
            {more && <div className="loading-earlier" aria-label="Loading earlier messages"><Spinner /></div>}
            {items.map((item) => (
              <div key={item.id} data-item={item.id} className="thread-item">
                {item.kind === "message" ? (
                  item.message.role === "tool" ? <ToolRun calls={[item.message]} /> : <MessageRow message={item.message} />
                ) : item.kind === "tools" ? (
                  <ToolRun calls={item.calls} said={item.said} />
                ) : item.kind === "image" ? (
                  <ImageThumbnail sessionId={session.id} path={item.path} gallery={gallery} />
                ) : item.kind === "worked" ? (
                  <WorkedLine text={"Worked for " + workDuration(item.seconds)} />
                ) : (
                  <PicturesRun sessionId={session.id} calls={item.calls} paths={item.paths} said={item.said} gallery={gallery} />
                )}
              </div>
            ))}
            <LatestTurnLine session={session} messages={list} />
            {queued.map((text, i) => (
              <div key={`q${i}`} className="thread-item">
                <QueuedBubble
                  text={text}
                  note="Waiting for the agent to read it"
                  onSendNow={canSendNow ? () => perform(session.id, { type: "send_queued" }) : undefined}
                />
              </div>
            ))}
            {localQueued.map((text, i) => (
              <div key={`l${i}`} className="thread-item">
                <QueuedBubble text={text} note="Queued: it reaches the agent when its turn ends" />
              </div>
            ))}
            {session.question && (
              <div className="thread-item" data-question>
                <QuestionCard key={session.question.id} session={session} question={session.question} typedInComposer />
              </div>
            )}
            <div className="thread-end" />
          </div>
        </div>
        <div className="top-fade" />
        {finding && (
          <FindBar
            key={findFocus}
            finding={finding}
            setQuery={(query) => setFinding({ ...finding, query })}
            previous={() => step(-1)}
            next={() => step(1)}
            close={closeFind}
          />
        )}
        {empty && <EmptyThread session={session} />}
        <JumpToLatest
          shown={!following && !empty}
          onJump={() => {
            setAtEnd(true);
            toEnd(true);
          }}
        />
        {dropping && <DropTarget label={`Drop images for ${session.name}`} />}
      </div>
    </div>
  );
}
