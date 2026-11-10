// What Agentman's conversation shows (ManagerChatView.swift's ChatItemView):
// the user's words and images, Agentman's replies, its activity folded into
// a line, images it looked at, news from agents, cards, receipts, schedules,
// errors and the compaction divider.

import { useEffect, useState, type ReactNode } from "react";
import {
  BookOpen, Check, ChevronRight, CircleAlert, CircleStop, CircleX, Eye, FileText, Hand, Image, Key, MessageSquareCheck, Send, ShieldCheck, X,
  Bell,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ManagerEntry } from "@shared/protocol";
import type { ChatItem } from "@shared/manager-feed";
import { openLightbox } from "../lightbox";
import { Markdown } from "../Markdown";
import { openAgent } from "../nav";
import { useStore } from "../store";
import { contextMenu } from "../thread/menu";
import { AgentMark, AppIcon, Icon } from "../ui";
import { CompactCard } from "./CompactCard";
import { NoteReceipt, NoteSuggestionCard } from "./NoteSuggestion";
import { closeEntry, dismissCard, keptImage, seenImage, useManager } from "./store";
import { useSwipeToDismiss } from "./swipe";
import { keepLastWords } from "./text";
import { shortTime, useTimeLocale } from "./time";

/** Copies a picture to the clipboard, as "Copy Image" does. */
async function copyImage(src: string) {
  try {
    const blob = await (await fetch(src)).blob();
    const png = blob.type === "image/png" ? blob : await new Promise<Blob>((resolve, reject) => {
      const img = new window.Image();
      img.onload = () => {
        const c = document.createElement("canvas");
        c.width = img.naturalWidth;
        c.height = img.naturalHeight;
        c.getContext("2d")?.drawImage(img, 0, 0);
        c.toBlob((b) => (b ? resolve(b) : reject(new Error("no image"))), "image/png");
      };
      img.onerror = reject;
      img.src = src;
    });
    await navigator.clipboard.write([new ClipboardItem({ "image/png": png })]);
  } catch {
    // The clipboard refused it.
  }
}

function useSession(id?: string) {
  const { sessions } = useStore();
  return id ? sessions.find((s) => s.id === id) : undefined;
}

/** The small × that takes a card out of the conversation. */
export function CloseButton({ help, onClick, className = "" }: { help: string; onClick: () => void; className?: string }) {
  return (
    <button className={`am-close ${className}`} title={help} aria-label="Close" onClick={onClick}>
      <Icon icon={X} sf="xmark" size={10} weight="semibold" />
    </button>
  );
}

function SentImage({ name, group }: { name: string; group: string[] }) {
  const [src, setSrc] = useState<string | null>(useManager().images[`kept\0${name}`] ?? null);
  const [size, setSize] = useState<{ w: number; h: number } | null>(null);
  useEffect(() => {
    let live = true;
    void keptImage(name).then((url) => live && setSrc(url));
    return () => {
      live = false;
    };
  }, [name]);
  if (!src || !size) {
    return (
      <span className="am-sent placeholder">
        {src && <img src={src} alt="" style={{ display: "none" }} onLoad={(e) => setSize({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })} />}
        <span className="am-spinner" />
      </span>
    );
  }
  const width = Math.min(220, Math.max(80, (110 * size.w) / Math.max(size.h, 1)));
  const open = async () => {
    const names = group.includes(name) ? group : [name];
    const srcs = await Promise.all(names.map((n) => keptImage(n)));
    openLightbox(names.map((n, i) => ({ src: srcs[i] ?? "", title: "Sent to Agentman" })), names.indexOf(name));
  };
  return (
    <button className="am-sent" style={{ width }} title="Open full window" onClick={() => void open()} onContextMenu={(e) => contextMenu(e, [{ label: "Copy Image", action: () => void copyImage(src) }])}>
      <img src={src} alt="" />
    </button>
  );
}

function UserEntry({ entry }: { entry: ManagerEntry }) {
  return (
    <div className="am-user">
      {entry.images && entry.images.length > 0 && (
        <div className="am-sent-row">
          {entry.images.map((name) => <SentImage key={name} name={name} group={entry.images!} />)}
        </div>
      )}
      {entry.text && <div className="am-bubble">{keepLastWords(entry.text)}</div>}
    </div>
  );
}

/** Three dots that breathe while Agentman works. */
export function ThinkingDots() {
  return (
    <span className="am-dots" aria-label="Agentman is thinking">
      <span />
      <span />
      <span />
    </span>
  );
}

export function ThinkingRow() {
  return (
    <div className="am-thinking">
      <AppIcon size={22} />
      <ThinkingDots />
    </div>
  );
}

function AssistantReply({ entry }: { entry: ManagerEntry }) {
  return (
    <div className="am-reply">
      <span className="am-reply-badge"><AppIcon size={22} /></span>
      <div className="am-reply-body">{entry.text ? <Markdown text={entry.text} /> : <ThinkingDots />}</div>
    </div>
  );
}

/** An icon for what Agentman did. */
function activityIcon(tool?: string): [LucideIcon, string] {
  switch (tool) {
    case "send_to_agent": return [Send, "paperplane"];
    case "answer_question": return [MessageSquareCheck, "checkmark.bubble"];
    case "approve": return [ShieldCheck, "checkmark.shield"];
    case "interrupt": return [CircleStop, "stop.circle"];
    case "dismiss_card": return [CircleX, "xmark.circle"];
    case "give_key": return [Key, "key"];
    case "search_knowledge":
    case "read_knowledge":
    case "save_knowledge": return [BookOpen, "book"];
    case "update_instructions": return [FileText, "doc.text"];
    case "view_image": return [Image, "photo"];
    default: return [Eye, "eye"];
  }
}

function ActivityRow({ entry }: { entry: ManagerEntry }) {
  const [icon, sf] = activityIcon(entry.tool);
  return (
    <div className="am-act-row">
      <span className="am-act-icon"><Icon icon={icon} sf={sf} size={9.5} weight="semibold" /></span>
      <span className="am-act-text">{entry.text}</span>
    </div>
  );
}

/** Agentman's tool calls, folded into one quiet line until opened. */
function ActivityRun({ entries }: { entries: ManagerEntry[] }) {
  const [open, setOpen] = useState(false);
  if (entries.length === 1) {
    return <div className="am-acts"><ActivityRow entry={entries[0]} /></div>;
  }
  const last = entries[entries.length - 1];
  const [icon, sf] = activityIcon(last.tool);
  return (
    <div className="am-acts">
      <button className="am-act-toggle" onClick={() => setOpen(!open)}>
        <span className="am-act-toggle-icon"><Icon icon={icon} sf={sf} size={10} weight="semibold" /></span>
        <span className="am-act-last">{last.text}</span>
        <span className="am-act-more">and {entries.length - 1} more</span>
        <Icon icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" className={`am-chevron ${open ? "open" : ""}`} />
      </button>
      {open && (
        <>
          <div className="am-act-rule" />
          <div className="am-act-list">
            {entries.map((e) => <ActivityRow key={e.id} entry={e} />)}
          </div>
        </>
      )}
    </div>
  );
}

/** An image Agentman looked at: a short label and a thumbnail that opens
 *  full window (Design/ImagePreview.swift's ImageThumbnail, carded). */
function ImageActivity({ entry, gallery }: { entry: ManagerEntry; gallery: string[] }) {
  const sessionId = entry.sessionId!;
  const path = entry.path!;
  const [src, setSrc] = useState<string | null>(useManager().images[`${sessionId}\0${path}`] ?? null);
  const [failed, setFailed] = useState(false);
  const [size, setSize] = useState<{ w: number; h: number } | null>(null);
  useEffect(() => {
    if (src) return;
    let live = true;
    void (async () => {
      // A daemon that has just started may not know the agent's files yet.
      for (let attempt = 0; attempt < 4 && live; attempt++) {
        if (attempt > 0) await new Promise((r) => setTimeout(r, 1500));
        const url = await seenImage(sessionId, path);
        if (url) {
          if (live) setSrc(url);
          return;
        }
      }
      if (live) setFailed(true);
    })();
    return () => {
      live = false;
    };
  }, [sessionId, path, src]);
  const width = size ? Math.min(280, Math.max(90, (120 * size.w) / Math.max(size.h, 1))) : 160;
  const open = async () => {
    const paths = gallery.includes(path) ? gallery : [path];
    const srcs = await Promise.all(paths.map((p) => seenImage(sessionId, p)));
    openLightbox(paths.map((p, i) => ({ src: srcs[i] ?? "", title: p.split("/").pop() ?? p })), paths.indexOf(path));
  };
  return (
    <div className="am-image-act">
      <div className="am-image-head" title={path}>
        <span className="am-act-icon"><Icon icon={Image} sf="photo" size={10} weight="medium" /></span>
        <span className="am-image-label">{entry.text}</span>
      </div>
      {src && size ? (
        <button className="am-thumb" style={{ width }} title="Open full window" onClick={() => void open()} onContextMenu={(e) => contextMenu(e, [{ label: "Copy Image", action: () => void copyImage(src) }])}>
          <img src={src} alt="" />
        </button>
      ) : !failed ? (
        <span className="am-thumb placeholder">
          {src && <img src={src} alt="" style={{ display: "none" }} onLoad={(e) => setSize({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })} />}
          <span className="am-spinner" />
        </span>
      ) : null}
    </div>
  );
}

/** News from one agent, as Agentman put it, with the way to that agent. */
function BriefCard({ entry }: { entry: ManagerEntry }) {
  const session = useSession(entry.sessionId);
  const brief = useManager().briefs[entry.sessionId ?? ""];
  const swipe = useSwipeToDismiss(async () => (await closeEntry(entry)) === undefined);
  return (
    <div className="am-brief" {...swipe}>
      <span className="am-brief-mark"><AgentMark kind={session?.kind ?? ""} size={20} /></span>
      <div className="am-brief-body">
        <div className="am-brief-name">{session?.name || brief?.name || "An agent"}</div>
        <div className="am-brief-text">{keepLastWords(entry.text)}</div>
        {brief?.needsYou && (
          <div className="am-label am-needs">
            <Icon icon={Hand} sf="hand.raised" size={12} />
            <span>{brief.needsYou}</span>
          </div>
        )}
      </div>
      {session && <button className="am-open-agent" onClick={() => openAgent(session.id)}>Open agent</button>}
      <CloseButton help="Close (or swipe it aside)" onClick={() => void closeEntry(entry)} />
    </div>
  );
}

/** A one-line receipt that offers its × only under the pointer. */
function ClosesOnHover({ entry, children }: { entry: ManagerEntry; children: ReactNode }) {
  return (
    <div className="am-hover-close">
      {children}
      <CloseButton help="Close" className="hover-only" onClick={() => void closeEntry(entry)} />
    </div>
  );
}

/** A card the user answered here, folded to what they chose. */
function CardReceipt({ entry }: { entry: ManagerEntry }) {
  const card = entry.card!;
  const session = useSession(card.sessionId);
  return (
    <div className="am-receipt">
      <span className="am-receipt-check"><Icon icon={Check} sf="checkmark" size={10} weight="bold" /></span>
      <span className="am-receipt-text">{`${session?.name || card.agentName} · ${card.outcome ?? "Answered"}`}</span>
      {session && <button className="am-link" onClick={() => openAgent(card.sessionId)}>Open</button>}
    </div>
  );
}

function OpenCard({ entry }: { entry: ManagerEntry }) {
  const card = entry.card!;
  const swipe = useSwipeToDismiss(async () => (await dismissCard(card)) === undefined);
  return (
    <div className="am-card-box" {...swipe}>
      <CompactCard card={card} close={() => dismissCard(card)} />
    </div>
  );
}

/** One of Agentman's own reminders or watches waking it. */
function ScheduledLine({ entry }: { entry: ManagerEntry }) {
  useTimeLocale();
  const time = shortTime(entry.ts);
  return (
    <div className="am-scheduled">
      <span className="am-act-icon">{entry.tool === "watch" ? <Icon icon={Eye} sf="eye" size={10} weight="medium" /> : <Icon icon={Bell} sf="bell" size={10} weight="medium" />}</span>
      <span className="am-scheduled-text">{entry.text}</span>
      <span className="am-faint">{time}</span>
    </div>
  );
}

/** Where the conversation was compacted. */
function CompactedDivider({ text }: { text: string }) {
  return (
    <div className="am-compacted" title="Agentman summarised the conversation before this point, to keep it quick and inexpensive.">
      <span className="am-rule" />
      <span>{text}</span>
      <span className="am-rule" />
    </div>
  );
}

export function ChatItemView({ item, gallery }: { item: ChatItem; gallery: string[] }) {
  if (item.kind === "activities") return <ActivityRun entries={item.entries} />;
  if (item.kind === "image") return <ImageActivity entry={item.entry} gallery={gallery} />;
  const entry = item.entry;
  switch (entry.role) {
    case "user":
      return <UserEntry entry={entry} />;
    case "assistant":
      return <AssistantReply entry={entry} />;
    case "brief":
    case "agent":
      return <BriefCard entry={entry} />;
    case "scheduled":
      return <ScheduledLine entry={entry} />;
    case "card": {
      const card = entry.card;
      if (!card) return null;
      if (card.kind === "note") {
        return card.state === "open" ? <NoteSuggestionCard card={card} /> : <ClosesOnHover entry={entry}><NoteReceipt card={card} /></ClosesOnHover>;
      }
      return card.state === "open" ? <OpenCard entry={entry} /> : <ClosesOnHover entry={entry}><CardReceipt entry={entry} /></ClosesOnHover>;
    }
    case "error":
      return (
        <div className="am-label am-error">
          <Icon icon={CircleAlert} sf="exclamationmark.circle" size={12} />
          <span>{entry.text}</span>
        </div>
      );
    case "compacted":
      return <CompactedDivider text={entry.text} />;
    default:
      return <div className="am-faint-line">{entry.text}</div>;
  }
}
