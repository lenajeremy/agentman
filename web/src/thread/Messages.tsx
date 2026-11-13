// The words in a thread (AgentThreadView.swift's MessageRow, AgentReply,
// LongText, FoldedText, QueuedBubble; ImagePreview.swift's SentFiles and
// SentFileChips): the user's messages as bubbles with what went with them,
// the agent's replies in markdown with Copy beside them.

import { useEffect, useRef, useState, type MouseEvent } from "react";
import { AlignLeft, Check, ChevronRight, Copy, FileText, Image as ImageIcon } from "lucide-react";
import type { Message } from "@shared/protocol";
import { openLightbox } from "../lightbox";
import { openPath } from "../host";
import { Markdown } from "../Markdown";
import { Sym } from "./Sym";
import { Dashed } from "./Bars";
import { copyImage, copyText } from "./clipboard";
import { FileIcon } from "./FileIcon";
import { baseName, sentMessage } from "./items";
import { isCompactionSummary } from "./compaction";
import { contextMenu } from "./menu";
import { naturalSize } from "./ToolRun";
import { seenImage, useAreFiles } from "./state";
import { useTightWidth } from "./tight";

/** A user's message, clipped to eight lines when it is long, with the way to
 *  read the rest. */
function LongText({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const long = text.length > 700 || (text.match(/\n/g)?.length ?? 0) > 10;
  const body = useRef<HTMLDivElement>(null);
  useTightWidth(body, [text, open]);
  return (
    <div className="long-text">
      <div ref={body} className={`long-text-body ${long && !open ? "clamped" : ""}`}>{text}</div>
      {long && (
        <button className="link-button" onClick={() => setOpen(!open)}>{open ? "Show less" : "Show more"}</button>
      )}
    </div>
  );
}

/** Text that rarely needs reading, folded into one quiet line. */
function FoldedText({ label, text }: { label: string; text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="folded">
      <button className="folded-toggle" onClick={() => setOpen(!open)}>
        <Sym icon={AlignLeft} sf="text.alignleft" size={10} weight="medium" />
        <span>{label}</span>
        <span className={`run-chevron ${open ? "open" : ""}`}><Sym icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" /></span>
      </button>
      {open && <div className="folded-text">{text}</div>}
    </div>
  );
}

/** One of the images sent with a message, read from where the daemon filed
 *  it, as a thumbnail that opens full window. */
function SentFile({ sessionId, path, group }: { sessionId: string; path: string; group: string[] }) {
  const [image, setImage] = useState<{ src: string; width: number; height: number } | null>(null);
  const [missing, setMissing] = useState(false);
  useEffect(() => {
    let cancelled = false;
    void seenImage(sessionId, path).then(async (src) => {
      const size = src ? await naturalSize(src) : null;
      if (cancelled) return;
      if (src && size) setImage({ src, ...size });
      else setMissing(true);
    });
    return () => {
      cancelled = true;
    };
  }, [sessionId, path]);
  if (image) {
    return (
      <button
        className="sent-image"
        title="Open full window"
        onClick={() => openLightbox(group.map((each) => ({ src: "", title: baseName(each), path: each, load: () => seenImage(sessionId, each) })), group.indexOf(path))}
        onContextMenu={(e: MouseEvent) => contextMenu(e, [{ label: "Copy Image", action: () => void copyImage(image.src) }])}
      >
        <img src={image.src} alt="" style={{ width: Math.min(220, Math.max(80, (110 * image.width) / Math.max(image.height, 1))), height: 110 }} />
      </button>
    );
  }
  if (missing) {
    return (
      <span className="image-gone">
        <Sym icon={ImageIcon} sf="photo" size={12} />
        Image no longer kept
      </span>
    );
  }
  return <span className="sent-image-wait" />;
}

/** A file sent with a message, by name; a click opens it as Finder would. */
function SentFileChip({ path }: { path: string }) {
  return (
    <button className="sent-chip" title={path} onClick={() => void openPath(path)}>
      <FileIcon path={path} size={18} fallback={FileText} />
      <span className="sent-chip-name">{baseName(path)}</span>
    </button>
  );
}

/** The files a message names first, as chips, once each is known to be a
 *  file: what isn't stays words. */
function UserMessage({ message }: { message: Message }) {
  const text = message.text ?? "";
  const isFile = useAreFiles(sentMessage(text, message.images ?? []).files);
  const sent = sentMessage(text, message.images ?? [], isFile);
  return (
    <div className="user-message">
      {sent.images.length > 0 && (
        <div className="sent-images">
          {sent.images.map((path) => <SentFile key={path} sessionId={message.sessionId} path={path} group={sent.images} />)}
        </div>
      )}
      {sent.files.length > 0 && (
        <div className="sent-chips">
          {sent.files.map((path) => <SentFileChip key={path} path={path} />)}
        </div>
      )}
      {sent.text && (
        <div className="user-row">
          <div className="bubble" onContextMenu={(e) => contextMenu(e, [{ label: "Copy", action: () => void copyText(sent.text) }])}>
            <LongText text={sent.text} />
          </div>
        </div>
      )}
    </div>
  );
}

/** An agent's reply, with Copy beside it while the pointer is over it, and
 *  in its context menu. */
function AgentReply({ message }: { message: Message }) {
  const text = message.text ?? "";
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void copyText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div
      className={`reply ${message.isSidechain ? "sidechain" : ""}`}
      onContextMenu={(e) => contextMenu(e, [{ label: "Copy", action: () => void copyText(text) }])}
    >
      <div className="reply-body"><Markdown text={text} /></div>
      <button className={`copy ${copied ? "copied" : ""}`} onClick={copy} title={copied ? "Copied" : "Copy the reply"}>
        <Sym icon={copied ? Check : Copy} sf={copied ? "checkmark" : "doc.on.doc"} size={11} />
      </button>
    </div>
  );
}

/** One entry in a conversation that isn't a tool call. */
export function MessageRow({ message }: { message: Message }) {
  switch (message.role) {
    case "user":
      if (isCompactionSummary(message.text ?? "")) return <FoldedText label="Summary of the earlier conversation" text={message.text ?? ""} />;
      return <UserMessage message={message} />;
    case "assistant":
      return <AgentReply message={message} />;
    case "system":
      return <div className="system">{message.text ?? ""}</div>;
    default:
      return null;
  }
}

/** A message the agent has not read yet: held until its current step ends,
 *  or queued until its turn does. */
export function QueuedBubble({ text, note, onSendNow }: { text: string; note: string; onSendNow?: () => Promise<boolean> }) {
  const [sending, setSending] = useState(false);
  const words = useRef<HTMLDivElement>(null);
  useTightWidth(words, [text]);
  return (
    <div className="user-row">
      <div className="queued">
        <div className="queued-bubble"><Dashed radius={18} dash="4 3" /><div ref={words}>{text}</div></div>
        <div className="queued-note">
          <span>{note}</span>
          {onSendNow && (
            <button
              className="link-button"
              disabled={sending}
              title="Sends every waiting message now instead of when this step ends"
              onClick={() => {
                setSending(true);
                void onSendNow().finally(() => setSending(false));
              }}
            >
              {sending ? "Sending…" : "Send now"}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
