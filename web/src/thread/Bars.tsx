// The bars and notes around a thread (AgentThreadView.swift's ReviewBar,
// ChangedBar, ResumeBar, EmptyThread, ComposerFeedback; FollowsEnd.swift's
// JumpToLatest; ImagePreview.swift's DropTarget).

import type { ReactNode } from "react";
import { ArrowDown, ChevronRight, CircleAlert, ClipboardList, Eye, Folder, Images, Moon, X } from "lucide-react";
import type { Session } from "@shared/protocol";
import { AgentMark } from "../ui";
import { Sym } from "./Sym";
import { Spinner } from "./Loader";

/** A plan waiting for the user's review, over the thread. */
export function ReviewBar({ count, agent, onOpen }: { count: number; agent: string; onOpen: () => void }) {
  return (
    <button className="thread-bar review" onClick={onOpen}>
      <span className="thread-bar-icon review"><Sym icon={ClipboardList} sf="list.bullet.clipboard" size={11} weight="medium" /></span>
      <span className="thread-bar-label">{count === 1 ? `${agent} wants you to review a plan` : `${agent} wants you to review ${count} documents`}</span>
      <span className="thread-bar-action">Review</span>
    </button>
  );
}

/** "4 files changed", over a thread, opening the agent's changes. */
export function ChangedBar({ count, onOpen }: { count: number; onOpen: () => void }) {
  return (
    <button className="thread-bar" onClick={onOpen} title="See what changed">
      <span className="thread-bar-icon changed"><Sym icon={Folder} sf="folder" size={11} weight="medium" /></span>
      <span className="thread-bar-label">{`${count} file${count === 1 ? "" : "s"} changed`}</span>
      <span className="thread-bar-chevron"><Sym icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" /></span>
    </button>
  );
}

/** In place of the message box for a session that has ended. */
export function ResumeBar({ resuming, onResume }: { resuming: boolean; onResume: () => void }) {
  return (
    <div className="resume-bar">
      <span className="resume-icon"><Sym icon={Moon} sf="moon.zzz" size={13} /></span>
      <div className="resume-words">
        <div className="resume-title">This session has ended</div>
        <div className="resume-note">Its conversation is here to read. Resume it to carry on.</div>
      </div>
      <button className="prominent" disabled={resuming} onClick={onResume}>
        {resuming ? <Spinner size={16} light /> : "Resume"}
      </button>
    </div>
  );
}

/** In place of the message box for a session only watched here that
 *  Agentman can take over from its terminal (AgentThreadView's TakeOverBar). */
export function TakeOverBar({ working, onTakeOver }: { working: boolean; onTakeOver: () => void }) {
  return (
    <div className="resume-bar">
      <span className="resume-icon"><Sym icon={Eye} sf="eye" size={13} /></span>
      <div className="resume-words">
        <div className="resume-title">You're watching this session</div>
        <div className="resume-note">
          {working ? "It runs in a terminal. You can take it over when this turn ends." : "It runs in a terminal. Take it over to message it here; its terminal closes."}
        </div>
      </div>
      <button className="prominent" disabled={working} onClick={onTakeOver}>Take Over…</button>
    </div>
  );
}

/** An agent with nothing to show yet. */
export function EmptyThread({ session }: { session: Session }) {
  return (
    <div className="empty-thread">
      <span style={{ opacity: 0.9, display: "flex" }}><AgentMark kind={session.kind} size={28} /></span>
      <div className="empty-thread-title">No messages yet</div>
      <div className="empty-thread-note">
        {session.inject !== "none" ? "What you send appears here, then the agent's replies." : "Its conversation appears here as it works."}
      </div>
    </div>
  );
}

/** Back to the latest message, shown while the user has scrolled up. */
export function JumpToLatest({ shown, onJump }: { shown: boolean; onJump: () => void }) {
  return (
    <button className={`jump-latest ${shown ? "shown" : ""}`} onClick={onJump} title="Jump to the latest" aria-label="Jump to the latest message" tabIndex={shown ? 0 : -1}>
      <Sym icon={ArrowDown} sf="arrow.down" size={13} weight="semibold" />
    </button>
  );
}

/** Where dropped images will land, over the conversation while they are
 *  dragged across it. */
export function DropTarget({ label }: { label: string }) {
  return (
    <div className="drop-target">
      <div className="drop-target-box">
        <Dashed radius={18} width={2} dash="6 5" />
        <span className="drop-target-label">
          <Sym icon={Images} sf="photo.on.rectangle" size={14} weight="medium" />
          {label}
        </span>
      </div>
    </div>
  );
}

/** A dashed rounded edge inside its parent (positioned), as SwiftUI's
 *  strokeBorder with a dash: CSS can't choose the dashes. */
export function Dashed({ radius, width = 1, dash }: { radius: number; width?: number; dash: string }) {
  const inset = width / 2;
  return (
    <svg className="dashed" aria-hidden>
      <rect
        x={inset} y={inset} rx={radius - inset} fill="none" strokeWidth={width} strokeDasharray={dash}
        style={{ width: `calc(100% - ${width}px)`, height: `calc(100% - ${width}px)` }}
      />
    </svg>
  );
}

/** An error that follows the composer's rounded silhouette, with its own
 *  way to dismiss it. */
export function ComposerFeedback({ message, onDismiss, children }: { message?: string; onDismiss: () => void; children: ReactNode }) {
  return (
    <div className={`composer-feedback ${message ? "has-error" : ""}`}>
      {message && (
        <div className="composer-error">
          <span className="composer-error-icon"><Sym icon={CircleAlert} sf="exclamationmark.circle" size={12} weight="medium" /></span>
          <span className="composer-error-text" title={message}>{message}</span>
          <button className="composer-error-close" onClick={onDismiss} title="Dismiss error" aria-label="Dismiss error">
            <Sym icon={X} sf="xmark" size={10} weight="semibold" />
          </button>
        </div>
      )}
      {children}
    </div>
  );
}
