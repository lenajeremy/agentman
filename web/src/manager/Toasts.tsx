// Agentman's news while the user is elsewhere in the app (Views/AgentmanToasts.swift):
// small, in the window's bottom-right corner, newest lowest. News fades on
// its own unless the pointer rests on it; a card stays until it is
// answered, answered anywhere else, or put away.
//
// Mounted once, in the main window: it also tells the store where that
// window is, which makes it the window that tells the user anything.

import { useEffect, useState } from "react";
import { X } from "lucide-react";
import type { ManagerEntry } from "@shared/protocol";
import { cardIsOpen } from "@shared/manager-feed";
import { openAgent, openAgentmanChat, useRoute } from "../nav";
import { useStore } from "../store";
import { AgentMark, AppIcon, Icon } from "../ui";
import { CompactCard } from "./CompactCard";
import { keepLastWords } from "./text";
import { dismissToast, entryById, plain, showToast, syncMainWindow, useManager, type Toast } from "./store";

/** How long news stays without the pointer on it. */
const FADES = 8000;

const query = new URLSearchParams(location.search);
/** For screenshots: entries shown as toasts at launch (toasts=<id>,<id>). */
const seeded = (query.get("toasts") ?? "").split(",").filter(Boolean);

function NewsToast({ entry, close }: { entry: ManagerEntry; close: () => void }) {
  const { sessions } = useStore();
  const session = entry.sessionId ? sessions.find((s) => s.id === entry.sessionId) : undefined;
  const open = () => {
    if (entry.sessionId && session) openAgent(entry.sessionId);
    else openAgentmanChat();
    close();
  };
  return (
    <div className="am-news" role="button" title={entry.sessionId ? `Open ${session?.name ?? "the agent"}` : "Open Agentman"} onClick={open}>
      <div className="am-news-head">
        {session ? (
          <>
            <AgentMark kind={session.kind} size={14} />
            <span className="am-compact-name">{session.name}</span>
          </>
        ) : (
          <>
            <AppIcon size={14} />
            <span className="am-compact-name">Agentman</span>
          </>
        )}
        {entry.role === "agent" && <span className="am-news-says">says</span>}
        <span className="am-spacer" />
        <button
          className="am-icon-button"
          title="Hide"
          onClick={(e) => {
            e.stopPropagation();
            close();
          }}
        >
          <Icon icon={X} sf="xmark" size={9} weight="semibold" />
        </button>
      </div>
      <div className="am-news-text">{keepLastWords(plain(entry.text))}</div>
    </div>
  );
}

function ToastView({ toast }: { toast: Toast }) {
  const s = useManager();
  const entry = entryById(toast.id, s);
  const [hovering, setHovering] = useState(false);
  const open = cardIsOpen(entry?.card);

  // News goes after a while, unless the pointer rests on it.
  useEffect(() => {
    if (toast.card || hovering) return;
    const timer = window.setTimeout(() => dismissToast(toast.id), FADES);
    return () => window.clearTimeout(timer);
  }, [toast.card, toast.id, hovering]);
  // A card answered or put away anywhere takes its toast with it.
  useEffect(() => {
    if (entry?.card && !open) dismissToast(toast.id);
  }, [entry?.card, open, toast.id]);

  let body = null;
  if (entry?.card && open) {
    body = <CompactCard card={entry.card} closeHelp="Hide; it stays in Agentman's chat" close={async () => {
      dismissToast(toast.id);
      return undefined;
    }} />;
  } else if (entry && !entry.card) {
    body = <NewsToast entry={entry} close={() => dismissToast(toast.id)} />;
  }
  if (!body) return null;
  return (
    <div className="am-toast" onMouseEnter={() => setHovering(true)} onMouseLeave={() => setHovering(false)}>
      {body}
    </div>
  );
}

export function AgentmanToasts() {
  const s = useManager();
  const route = useRoute();
  const { sessions } = useStore();
  useEffect(() => syncMainWindow(route, sessions), [route, sessions]);
  useEffect(() => {
    if (!seeded.length || !s.loaded) return;
    for (const id of seeded) {
      const entry = entryById(id);
      if (entry) showToast(entry);
    }
    seeded.length = 0;
  }, [s.loaded]);
  return (
    <div className="am-toasts">
      {s.toasts.map((t) => <ToastView key={t.id} toast={t} />)}
    </div>
  );
}
