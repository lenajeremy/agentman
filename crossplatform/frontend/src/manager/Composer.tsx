// Agentman's message box (ManagerChatView.swift's ManagerComposer, KeyBox,
// ManagerBox): a soft pill whose button stops Agentman while it replies,
// larger on the entry page; the API key box before Agentman has a key; and
// the notes under it while am serve isn't there.

import { useEffect, useLayoutEffect, useRef, useState, type ClipboardEvent, type KeyboardEvent, type ReactNode } from "react";
import { ArrowRight, ArrowUp, CircleAlert, Key, Plus, Square, X } from "lucide-react";
import { openURL } from "../host";
import { useStatus } from "../store";
import { Icon } from "../ui";
import { fromFiles, MAX_COUNT, pick, type Attachment } from "./attach";
import { needsKey, provider, setKey, setProblem, useManager } from "./store";
import { VoiceButton } from "./Voice";

/** An error over the box, following its rounded shape, with its own ×
 *  (AgentThreadView.swift's ComposerFeedback). */
export function ComposerFeedback({ message, radius, children }: { message?: string; radius: number; children: ReactNode }) {
  return (
    <div className={`am-feedback ${message ? "shown" : ""}`} style={{ borderRadius: radius }}>
      {message && (
        <div className="am-feedback-line">
          <Icon icon={CircleAlert} sf="exclamationmark.circle" size={12} weight="medium" className="am-feedback-icon" />
          <span className="am-feedback-text" title={message}>{message}</span>
          <button className="am-feedback-close" title="Dismiss error" aria-label="Dismiss error" onClick={() => setProblem(undefined)}>
            <Icon icon={X} sf="xmark" size={10} weight="semibold" />
          </button>
        </div>
      )}
      {children}
    </div>
  );
}

/** Images waiting in the box, each with a way to take it out. */
function AttachmentStrip({ attachments, onRemove }: { attachments: Attachment[]; onRemove: (id: string) => void }) {
  return (
    <div className="am-strip">
      {attachments.map((a) => (
        <div key={a.id} className="am-strip-item">
          <img src={a.src} alt="" />
          <button className="am-strip-remove" title="Remove" onClick={() => onRemove(a.id)}>
            <Icon icon={X} sf="xmark" size={8} weight="bold" />
          </button>
        </div>
      ))}
    </div>
  );
}

/** The words: Return sends, Shift-Return starts a new line, and it grows to
 *  ten lines (Design/MessageField.swift). */
function MessageField({ value, onChange, placeholder, large, disabled, autoFocus, onSubmit, onImages, fieldRef }: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  large: boolean;
  disabled?: boolean;
  autoFocus?: boolean;
  onSubmit: () => void;
  onImages: (found: Attachment[]) => void;
  fieldRef: React.RefObject<HTMLTextAreaElement | null>;
}) {
  // The line: the font's own height and 3 between lines, as the Mac's.
  const line = large ? 21 : 20;
  useLayoutEffect(() => {
    const el = fieldRef.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(Math.max(el.scrollHeight, line), line * 10)}px`;
  }, [value, line, fieldRef]);
  useEffect(() => {
    if (autoFocus) fieldRef.current?.focus();
  }, [autoFocus, fieldRef]);

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      onSubmit();
    }
  };
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData.files).filter((f) => f.type.startsWith("image/"));
    if (!files.length) return;
    e.preventDefault();
    void fromFiles(files).then(onImages);
  };
  return (
    <textarea
      ref={fieldRef}
      className={`am-field ${large ? "large" : ""}`}
      value={value}
      rows={1}
      placeholder={placeholder}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={onKey}
      onPaste={onPaste}
      spellCheck
    />
  );
}

export function ManagerComposer({ text, setText, attachments, setAttachments, thinking, sending, large = false, placeholder = "Ask Agentman about your agents", disabled, onSend, onStop }: {
  text: string;
  setText: (v: string) => void;
  attachments: Attachment[];
  setAttachments: (a: Attachment[]) => void;
  thinking: boolean;
  sending: boolean;
  large?: boolean;
  placeholder?: string;
  disabled?: boolean;
  onSend: () => void;
  onStop: () => void;
}) {
  const field = useRef<HTMLTextAreaElement>(null);
  const [focused, setFocused] = useState(false);
  const canSend = !sending && (text.trim().length > 0 || attachments.length > 0);
  const add = (found: Attachment[]) => {
    const room = MAX_COUNT - attachments.length;
    if (room > 0 && found.length) setAttachments([...attachments, ...found.slice(0, room)]);
  };
  const size = large ? 32 : 28;
  return (
    <div
      className={`am-composer ${large ? "large" : ""} ${focused ? "focused" : ""}`}
      onMouseDown={(e) => {
        // A click on the card around the text, not on a control, goes to the text.
        if (e.target === e.currentTarget) {
          e.preventDefault();
          field.current?.focus();
        }
      }}
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
    >
      {attachments.length > 0 && <AttachmentStrip attachments={attachments} onRemove={(id) => setAttachments(attachments.filter((a) => a.id !== id))} />}
      <div className="am-composer-row">
        <button
          className="am-round am-plus"
          style={{ width: size, height: size }}
          disabled={disabled || attachments.length >= MAX_COUNT}
          title="Attach images (or paste or drop them)"
          onClick={() => void pick().then(add)}
        >
          <Icon icon={Plus} sf="plus" size={13} weight="semibold" />
        </button>
        <MessageField
          fieldRef={field}
          value={text}
          onChange={setText}
          placeholder={placeholder}
          large={large}
          disabled={disabled}
          autoFocus={large && !disabled}
          onSubmit={() => {
            if (!thinking && canSend) onSend();
          }}
          onImages={add}
        />
        <VoiceButton
          size={size}
          enabled={!disabled}
          onText={(spoken) => {
            const trimmed = text.trim();
            setText(trimmed ? `${trimmed} ${spoken}` : spoken);
            field.current?.focus();
          }}
          onProblem={(p) => setProblem(p)}
        />
        <button
          className={`am-round am-send ${canSend || thinking ? "can" : ""}`}
          style={{ width: size, height: size }}
          disabled={disabled || (!thinking && !canSend)}
          title={thinking ? "Stop" : "Send"}
          aria-label={thinking ? "Stop" : "Send"}
          onClick={() => (thinking ? onStop() : onSend())}
        >
          {thinking ? <Icon icon={Square} sf="stop.fill" size={11} weight="bold" /> : <Icon icon={ArrowUp} sf="arrow.up" size={13} weight="bold" />}
        </button>
      </div>
    </div>
  );
}

/** Where the API key goes, in place of the message box until there is one. */
function KeyBox() {
  const s = useManager();
  const [key, setKeyText] = useState("");
  const [saving, setSaving] = useState(false);
  const [problem, setLocalProblem] = useState<string>();
  const gemini = provider(s) === "gemini";
  const save = async () => {
    const value = key.trim();
    if (!value || saving) return;
    setSaving(true);
    const p = await setKey(value, provider(s));
    setLocalProblem(p);
    if (!p) setKeyText("");
    setSaving(false);
  };
  return (
    <div className="am-keybox-wrap">
      <div className="am-keybox">
        <Icon icon={Key} sf="key" size={13} className="am-key-icon" />
        <input
          className="am-key-field"
          type="password"
          value={key}
          placeholder={gemini ? "Paste your Gemini API key to start" : "Paste your Anthropic API key to start"}
          onChange={(e) => setKeyText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void save();
          }}
          autoFocus
        />
        <button className={`am-round am-send ${key ? "can" : ""}`} style={{ width: 32, height: 32 }} disabled={!key.trim() || saving} onClick={() => void save()} aria-label="Save key">
          <Icon icon={ArrowRight} sf="arrow.right" size={13} weight="bold" />
        </button>
      </div>
      {problem && <div className="am-key-problem">{problem}</div>}
    </div>
  );
}

/** The box to talk to Agentman: a message, its API key before it has one,
 *  or a note that am serve isn't running. */
export function ManagerBox({ text, setText, attachments, setAttachments, sending, large = false, onSend }: {
  text: string;
  setText: (v: string) => void;
  attachments: Attachment[];
  setAttachments: (a: Attachment[]) => void;
  sending: boolean;
  large?: boolean;
  onSend: () => void;
}) {
  const s = useManager();
  const { status } = useStatus();
  const connected = status === "connected";
  const keyNeeded = needsKey(s);
  const gemini = provider(s) === "gemini";
  return (
    <div className="am-box">
      <ComposerFeedback message={s.problem} radius={large ? 26 : 22}>
        {!connected ? (
          <ManagerComposer text="" setText={() => {}} attachments={[]} setAttachments={() => {}} thinking={false} sending large={large} placeholder="Waiting for am serve…" disabled onSend={() => {}} onStop={() => {}} />
        ) : keyNeeded ? (
          <KeyBox />
        ) : (
          <ManagerComposer text={text} setText={setText} attachments={attachments} setAttachments={setAttachments} thinking={false} sending={sending} large={large} onSend={onSend} onStop={() => {}} />
        )}
      </ComposerFeedback>
      <div className="am-box-note">
        {status === "offline" ? (
          <span>am serve isn't running. Start it in a terminal with <b>am serve</b>.</span>
        ) : status === "connecting" ? (
          <span>Connecting…</span>
        ) : keyNeeded ? (
          <span className="am-key-note">
            {gemini ? "Agentman runs on your Gemini API key, kept in your Keychain." : "Agentman runs on your Anthropic API key, kept in your Keychain."}
            <a
              href={gemini ? "https://aistudio.google.com/apikey" : "https://console.anthropic.com/settings/keys"}
              onClick={(e) => {
                e.preventDefault();
                void openURL(e.currentTarget.href);
              }}
            >
              Get a key
            </a>
          </span>
        ) : null}
      </div>
    </div>
  );
}
