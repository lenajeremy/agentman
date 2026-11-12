// Agentman, the manager, in the detail area (Views/ManagerChatView.swift):
// the entry page while there is no conversation, then the conversation with
// its cards and schedules over the message box. In an editor's agent panel
// (inPanel) narrower, with no header of its own.

import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent } from "react";
import { ArrowDown, Images, MessageSquarePlus } from "lucide-react";
import { chatItems } from "@shared/manager-feed";
import { useStatus } from "../store";
import { confirmSheet } from "../thread/menu";
import { AppIcon, ChromeButton, Icon } from "../ui";
import { dragHoldsImages, encoded, fromFiles, fromPaths, MAX_COUNT, type Attachment } from "./attach";
import { ChatItemView, ThinkingRow } from "./ChatItems";
import { ManagerBox, ManagerComposer, ComposerFeedback } from "./Composer";
import { SchedulesButton } from "./Schedules";
import { loadOlder, needsKey, reset, send, setProblem, showsEntry, stop, thinking, useManager } from "./store";
import "../styles/manager.css";

const NEW_CHAT_MESSAGE = "This conversation is set aside, and Agentman keeps what matters from it in its private notes. Cards still waiting stay.";

const query = new URLSearchParams(location.search);

const STARTERS = ["What needs me?", "What happened while I was away?", "What is everyone working on?"];

/** How close to the bottom still counts as there; how close to the top
 *  starts loading what came before (Design/FollowsEnd.swift). */
const SLACK = 80;
const TOP_SLACK = 160;

export function AgentmanView({ inPanel = false }: { inPanel?: boolean }) {
  const s = useManager();
  const { status } = useStatus();
  const [draft, setDraft] = useState(() => (query.get("draft") ?? "").replace(/\\n/g, "\n"));
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [sending, setSending] = useState(false);
  const [dropping, setDropping] = useState(false);
  const [atEnd, setAtEnd] = useState(true);
  const scroller = useRef<HTMLDivElement>(null);
  const column = useRef<HTMLDivElement>(null);
  const endMark = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const prepending = useRef<{ height: number; top: number } | null>(null);
  const entry = showsEntry(s);
  const items = useMemo(() => chatItems(s.entries), [s.entries]);
  const busy = thinking(s);
  const gutter = inPanel ? 14 : 36;

  // For screenshots and tests: images and words waiting in the box
  // (AGENTMAN_ATTACH, AGENTMAN_DRAFT).
  useEffect(() => {
    const paths = (query.get("attach") ?? "").split(":").filter(Boolean);
    if (paths.length) void fromPaths(paths).then((a) => a.length && setAttachments(a.slice(0, MAX_COUNT)));
  }, []);

  // The end of the conversation, below the last message: following it
  // brings the end's foot to the view's, as scrollTo("end", anchor:
  // .bottom) does, leaving the column's 6 below out of sight.
  const toEnd = (smooth = false) => {
    const el = scroller.current;
    const end = endMark.current;
    if (!el) return;
    // The entry page stays at its top, as the Mac's does.
    if (!end) {
      el.scrollTop = 0;
      return;
    }
    const top = end.offsetTop + end.offsetHeight - el.clientHeight;
    if (smooth) el.scrollTo({ top, behavior: "smooth" });
    else el.scrollTop = top;
  };

  // Earlier messages arrive above: the first one stays where the reader
  // had it. New ones, while the reader is at the end, keep it there.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (prepending.current) {
      el.scrollTop = prepending.current.top + (el.scrollHeight - prepending.current.height);
      prepending.current = null;
    } else if (following.current) {
      toEnd();
    }
  }, [s.entries, busy, entry]);

  // The conversation grows as images and replies are laid out.
  useEffect(() => {
    const el = scroller.current;
    const inner = column.current;
    if (!el || !inner) return;
    const observer = new ResizeObserver(() => {
      if (following.current && !prepending.current) toEnd();
    });
    observer.observe(inner);
    observer.observe(el);
    return () => observer.disconnect();
  }, [entry]);

  const earlier = async () => {
    const el = scroller.current;
    if (!el || !s.hasOlder || s.loadingOlder) return;
    prepending.current = { height: el.scrollHeight, top: el.scrollTop };
    await loadOlder();
    // Nothing arrived: nothing to keep in place.
    if (prepending.current && el.scrollHeight === prepending.current.height) prepending.current = null;
  };

  // Messages too few to scroll have no top to arrive at.
  useEffect(() => {
    const el = scroller.current;
    if (!el || entry || !s.hasOlder || s.loadingOlder) return;
    const timer = window.setTimeout(() => {
      if (el.scrollHeight <= el.clientHeight) void earlier();
    }, 500);
    return () => window.clearTimeout(timer);
  });

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    const end = el.scrollTop + el.clientHeight >= el.scrollHeight - SLACK;
    following.current = end;
    if (end !== atEnd) setAtEnd(end);
    if (!end && el.scrollTop <= TOP_SLACK) void earlier();
  };

  const submit = (asked?: string) => {
    const message = (asked ?? draft).trim();
    if ((!message && attachments.length === 0) || sending) return;
    setSending(true);
    following.current = true;
    setAtEnd(true);
    toEnd(true);
    const images = attachments;
    void (async () => {
      if (await send(message, await encoded(images))) {
        setDraft("");
        setAttachments([]);
      }
      setSending(false);
    })();
  };

  const newChat = async () => {
    const choice = await confirmSheet("Start a new chat?", NEW_CHAT_MESSAGE, ["New Chat", "Cancel"]);
    if (choice === "New Chat") setProblem(await reset());
  };

  // Images dropped anywhere on the conversation wait in the box.
  const dropProps = {
    onDragOver: (e: DragEvent) => {
      if (!dragHoldsImages(e)) return;
      e.preventDefault();
      if (!dropping) setDropping(true);
    },
    onDragLeave: (e: DragEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node)) setDropping(false);
    },
    onDrop: (e: DragEvent) => {
      if (!dragHoldsImages(e)) return;
      e.preventDefault();
      setDropping(false);
      const files = e.dataTransfer.files;
      void fromFiles(files).then((found) => {
        setAttachments((now) => [...now, ...found.slice(0, Math.max(0, MAX_COUNT - now.length))]);
      });
    },
  };

  const boxProps = { text: draft, setText: setDraft, attachments, setAttachments, sending, onSend: submit };

  return (
    <div className={`am-page ${inPanel ? "in-panel" : "with-header"}`}>
      {!inPanel && (
        <header className="am-header drag">
          <span className="am-title">Agentman</span>
          <span className="am-spacer" />
          {s.schedules.length > 0 && <SchedulesButton />}
          {s.entries.length > 0 && (
            <span className="no-drag">
              <ChromeButton icon={MessageSquarePlus} sf="plus.bubble" title="Start a new conversation with Agentman" onClick={() => void newChat()} />
            </span>
          )}
        </header>
      )}
      <div className="am-conversation" {...dropProps}>
        <div className="am-scroll-wrap">
          <div className="am-scroll" ref={scroller} onScroll={onScroll}>
            <div ref={column} className={`am-column ${entry ? "entry" : ""}`} style={{ paddingLeft: gutter, paddingRight: gutter }}>
              {s.hasOlder && s.entries.length > 0 && <div className="am-loading-earlier"><span className="am-spinner" /></div>}
              {items.map((item) => <ChatItemView key={item.key} item={item} gallery={[]} />)}
              {busy && s.entries[s.entries.length - 1]?.streaming !== true && <ThinkingRow />}
              {entry ? (
                <div className="am-hero">
                  <span className="am-hero-space top" />
                  <div className="am-hero-name">
                    <AppIcon size={46} />
                    <span className="am-wordmark">Agentman</span>
                  </div>
                  <div className="am-hero-box">
                    <ManagerBox {...boxProps} large />
                  </div>
                  {/* A few questions to start with, once there is someone to
                      ask (EntryHero.starters). */}
                  {status === "connected" && !needsKey(s) && (
                    <div className="am-starters">
                      {STARTERS.map((question) => (
                        <button key={question} className="am-starter" onClick={() => { setDraft(question); submit(question); }}>{question}</button>
                      ))}
                    </div>
                  )}
                  <span className="am-hero-space" />
                  <span className="am-hero-space small" />
                </div>
              ) : (
                <div className="am-end" ref={endMark} />
              )}
            </div>
          </div>
          <div className="am-top-fade" />
          {!atEnd && !entry && (
            <button className="am-jump" title="Jump to the latest" aria-label="Jump to the latest message" onClick={() => {
              following.current = true;
              setAtEnd(true);
              toEnd(true);
            }}>
              <Icon icon={ArrowDown} sf="arrow.down" size={13} weight="semibold" />
            </button>
          )}
        </div>
        {!entry && (
          <div className="am-foot" style={{ paddingLeft: gutter, paddingRight: gutter }}>
            {status !== "connected" || needsKey(s) ? (
              <ManagerBox {...boxProps} />
            ) : (
              <ComposerFeedback message={s.problem} radius={22}>
                <ManagerComposer {...boxProps} thinking={busy} onStop={stop} />
              </ComposerFeedback>
            )}
          </div>
        )}
        {dropping && (
          <div className="am-drop">
            <span className="am-drop-label"><Icon icon={Images} sf="photo.on.rectangle" size={14} weight="medium" />Drop images for Agentman</span>
          </div>
        )}
      </div>
    </div>
  );
}
