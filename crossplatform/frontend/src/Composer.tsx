// The message box (AgentThreadView.swift's composer, Design/MessageField.swift).

import { useState, type KeyboardEvent } from "react";
import { ArrowUp, Mic, Paperclip } from "lucide-react";
import type { Session } from "@shared/protocol";
import { modelLabel } from "@shared/session-view";
import { agentName, needsYou } from "./labels";
import { sendMessage } from "./store";
import { AgentMark, Icon, Spacer } from "./ui";

function placeholder(s: Session): string {
  const name = s.name || agentName(s.kind);
  if (needsYou(s) && s.question) return "Answer the question above first";
  if (s.inject === "none") return `Start this agent with am ${s.kind} to message it from here`;
  if (s.inject === "hook") return `Message ${name}: it reads it when its turn ends (start it with am ${s.kind} to type in directly)`;
  return `Message ${name}`;
}

export function Composer({ session, onSent }: { session: Session; onSent: () => void }) {
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const canSend = draft.trim().length > 0 && !sending;

  const send = async () => {
    const text = draft.trim();
    if (!text || sending) return;
    setSending(true);
    try {
      await sendMessage(session.id, text);
      setDraft("");
      onSent();
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
    <div className="composer-wrap">
      <div className="composer-box">
        <textarea id="composer" className="composer-field" value={draft} rows={1} placeholder={placeholder(session)} onChange={(e) => setDraft(e.target.value)} onKeyDown={onKey} />
        <div className="composer-controls">
          <button className="clip" aria-label="Attach"><Icon icon={Paperclip} sf="paperclip" size={14} /></button>
          <Spacer min={8} />
          <button className="mic" aria-label="Record"><Icon icon={Mic} sf="mic" size={13} weight="semibold" /></button>
          <span className="model-chip">
            <AgentMark kind={session.kind} size={14} />
            {session.model ? modelLabel(session.model) : agentName(session.kind)}
          </span>
          <button className={`send ${canSend ? "can" : ""}`} disabled={!canSend} onClick={() => void send()} aria-label="Send">
            <Icon icon={ArrowUp} sf="arrow.up" size={13} weight="bold" />
          </button>
        </div>
      </div>
    </div>
  );
}
