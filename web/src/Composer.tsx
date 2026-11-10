// The box at the foot of an agent's thread (AgentThreadView.swift's
// Composer, Design/MessageField.swift): words, with files named by @ and the
// agent's commands by /; pictures and files from the paperclip, a paste or
// a drop; the agent's mode and model; a voice button; Send, or Stop while
// the agent works. The thread's error shows on it, and an ended session
// offers Resume in its place.

import { useStatus } from "./store";
import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from "react";
import { ArrowUp, ChevronDown, Paperclip } from "lucide-react";
import type { CommandPanel, Session } from "@shared/protocol";
import { daemon, type AppRequest } from "./daemon";
import { pickFiles } from "./host";
import { resumable } from "./AgentHeader";
import { openAgent } from "./nav";
import { AgentMark } from "./ui";
import { addAttachments, attachmentsOf, encoded, fromFiles, fromPath, setAttachments, useAttachments, MAX_ATTACHMENTS, type Attachment } from "./thread/attachments";
import { ComposerFeedback, ResumeBar } from "./thread/Bars";
import { commandSend, takesCommands, useMentions, useSlash } from "./thread/completions";
import { AttachmentStrip, MentionList, PanelSheet, SlashList, VoiceButton } from "./thread/ComposerParts";
import { confirmSheet, menuUnder } from "./thread/menu";
import { MessageField, type FieldApi } from "./thread/MessageField";
import { agentProduct, modeLabel, modelLabel } from "./thread/sessionLabels";
import { draftFor, perform, readLocalFile, saveDraft, send, setActionError, useActionError } from "./thread/state";
import { isApproval, takesTypedAnswer } from "./thread/QuestionCard";
import { Sym } from "./thread/Sym";
import { addSpoken } from "./thread/voice";
import "./styles/composer.css";

// As the Mac's (AgentThreadView.placeholder): what the box says about why
// it's off, or what typing in it does.
function placeholder(s: Session, connected: boolean): string {
  if (!connected) return "Can't send while Agentman isn't connected";
  if (s.inject === "none") {
    if (s.state === "ended") return "This session has ended";
    // Muse lets one client drive a session: a terminal has this one.
    if (s.kind === "muse") return "Open in a terminal: close it there to message it from here";
    return `Started outside Agentman: start it with am ${s.kind} to message it here`;
  }
  if (s.question) {
    if (takesTypedAnswer(s.question)) return "Type an answer, or pick one above";
    return isApproval(s.question) ? "Approve or deny above to continue" : "Pick an answer above to continue";
  }
  if (s.inject === "hook") return `Message ${s.name}: it reads it when its turn ends (start it with am ${s.kind} to type in directly)`;
  return `Message ${s.name}`;
}

/** How the agent is given a file: by its own path, which it reads where it
 *  is; or, when that path can't follow an "@" as one word, by the path of a
 *  copy the daemon keeps (attach_file). */
async function fileReference(sessionId: string, path: string): Promise<string> {
  if (!/\s/.test(path)) return path;
  const name = path.split("/").pop() ?? path;
  const file = await readLocalFile(path, 10 << 20);
  if (!file?.data) throw new Error(`${name} couldn't be read.`);
  const size = 96_000;
  const parts = Math.max(1, Math.ceil(file.data.length / size));
  const id = `app-${crypto.randomUUID().slice(0, 8)}`;
  for (let part = 0; part < parts; part += 1) {
    const reply = await daemon.request(
      { type: "attach_file", sessionId, file: { id, part, parts, name, data: file.data.slice(part * size, (part + 1) * size) } } as AppRequest,
      60_000,
    );
    if (!reply.attached) throw new Error((reply.error ?? `${name} couldn't be sent.`).replace(/^(daemon|source|tmux): /, ""));
    if (reply.attached.done && reply.attached.path) return reply.attached.path;
  }
  throw new Error(`${name} didn't reach the daemon.`);
}

export function Composer({ session, onSent }: { session: Session; onSent: () => void }) {
  const [draft, setDraft] = useState(() => draftFor(session.id));
  const [sending, setSending] = useState(false);
  const [readingPanel, setReadingPanel] = useState(false);
  const [panel, setPanel] = useState<CommandPanel | null>(null);
  const [focused, setFocused] = useState(false);
  const [resuming, setResuming] = useState(false);
  const attachments = useAttachments(session.id);
  const error = useActionError(session.id);
  const field = useRef<FieldApi>(null);
  const mentions = useMentions(session);
  const slash = useSlash(session);
  const canReceive = session.inject !== "none";
  const { status } = useStatus();
  const connected = status === "connected";
  // While a question takes a typed answer, the box takes it.
  const typedAnswer = session.question && takesTypedAnswer(session.question) ? session.question : null;
  const enabled = canReceive && !sending && !readingPanel && connected && (!session.question || !!typedAnswer);
  const canSend = enabled && (draft.trim() !== "" || attachments.length > 0);
  const modes = session.modes ?? [];
  const models = session.modelScope ? session.models ?? [] : [];

  // An unsent draft waits for the user's return, as on the phone.
  useEffect(() => setDraft(draftFor(session.id)), [session.id]);

  // For tests and pictures (the AGENTMAN_OPEN query): draft=<text> sets the
  // box's words; type=<text> types them, as the Mac's AGENTMAN_TEST_TYPE.
  useEffect(() => {
    const query = new URLSearchParams(location.search);
    if (query.get("agent") !== session.id) return;
    const draft = query.get("draft");
    if (draft !== null) setDraft(draft);
    const typed = query.get("type");
    if (!typed) return;
    const timer = setTimeout(() => {
      field.current?.focus();
      document.execCommand("insertText", false, typed);
    }, 3000);
    return () => clearTimeout(timer);
  }, [session.id]);
  const change = useCallback(
    (text: string) => {
      setDraft(text);
      saveDraft(session.id, text);
      slash.update(text);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [session.id, slash.update],
  );

  const sendNow = async () => {
    let text = draft.trim();
    if (!canSend) return;
    // While a question takes a typed answer, what is typed is the answer.
    if (typedAnswer) {
      if (!text) return;
      setSending(true);
      try {
        if (await perform(session.id, { type: "answer_question", questionId: typedAnswer.id, answerText: text })) setDraft("");
      } finally {
        setSending(false);
      }
      return;
    }
    // A panel command sent bare is read off the agent's terminal, since it
    // never reaches the transcript; a bare /model would leave a picker open.
    if (takesCommands(session) && attachments.length === 0) {
      const what = commandSend(text, slash.commands);
      if (what.kind === "panel") {
        setReadingPanel(true);
        try {
          const reply = await daemon.request({ type: "command_panel", sessionId: session.id, text: `/${what.name}` }, 40_000);
          if (reply.panel) {
            setDraft("");
            saveDraft(session.id, "");
            setPanel(reply.panel);
          } else {
            setActionError(session.id, (reply.error ?? "The panel couldn't be read.").replace(/^(daemon|source|tmux): /, ""));
          }
        } catch (err) {
          setActionError(session.id, err instanceof Error ? err.message : String(err));
        }
        setReadingPanel(false);
        return;
      }
      if (what.kind === "model") {
        setActionError(session.id, "Choose a model with the model button, or send /model and its name.");
        return;
      }
    }
    setSending(true);
    onSent();
    const list = attachmentsOf(session.id);
    try {
      const images = await encoded(list);
      // Files are named ahead of the words, as the phone sends them.
      const references: string[] = [];
      for (const a of list) {
        if (a.file) references.push(`@${await fileReference(session.id, a.file)}`);
      }
      if (references.length) text = references.join(" ") + (text ? `\n\n${text}` : "");
      if (await send(session.id, text, images)) {
        setDraft("");
        saveDraft(session.id, "");
        setAttachments(session.id, []);
      }
    } catch (err) {
      setActionError(session.id, err instanceof Error ? err.message : String(err));
    }
    setSending(false);
  };

  const pickMention = (index?: number) => {
    const picked = mentions.pick(index);
    if (picked) field.current?.replace(picked.start, picked.end, `@${picked.path} `);
  };
  const pickCommand = (index?: number) => {
    const picked = slash.pick(index);
    if (picked !== null) field.current?.replaceAll(picked);
  };

  const onKey = (e: KeyboardEvent<HTMLDivElement>): boolean => {
    if (slash.isOpen && slash.results.length) {
      switch (e.key) {
        case "ArrowDown": slash.move(1); return true;
        case "ArrowUp": slash.move(-1); return true;
        case "Escape": slash.dismiss(); return true;
        case "Enter":
        case "Tab": {
          // Return on a name already typed in full sends it; Tab, or Return
          // on part of one, completes it.
          const first = slash.results[0];
          if (e.key === "Enter" && first && slash.query === first.name && !first.argumentHint) {
            slash.dismiss();
            return false;
          }
          pickCommand();
          return true;
        }
      }
    }
    if (mentions.isOpen) {
      switch (e.key) {
        case "ArrowDown": mentions.move(1); return true;
        case "ArrowUp": mentions.move(-1); return true;
        case "Escape": mentions.dismiss(); return true;
        case "Enter":
        case "Tab": pickMention(); return true;
      }
    }
    return false;
  };

  const attach = async () => {
    const paths = await pickFiles("Choose images or files to send");
    const found: Attachment[] = [];
    for (const path of paths) {
      const made = await fromPath(path);
      if (made) found.push(made);
    }
    addAttachments(session.id, found);
  };

  const switchTo = async (model: boolean, value: string) => {
    const kind = model ? "model" : "mode";
    const label = model ? modelLabel(value) : modeLabel(value);
    try {
      const reply = await daemon.request({ type: model ? "set_model" : "set_mode", sessionId: session.id, text: value, clientId: `app-switch-${crypto.randomUUID()}` });
      if (reply.type === "error" || reply.status === "failed") {
        const reason = (reply.error ?? "").replace(/^(daemon|source): /, "");
        setActionError(session.id, `Couldn't switch the ${kind} to ${label}${reason ? `: ${reason}` : "."}`);
      } else {
        setActionError(session.id, null);
      }
    } catch (err) {
      setActionError(session.id, `Couldn't switch the ${kind} to ${label}: ${err instanceof Error ? err.message : String(err)}`);
    }
  };

  const switchMenu = (button: HTMLElement, values: string[], current: string | undefined, label: (v: string) => string, choose: (v: string) => void) => {
    menuUnder(button, values.map((value) => ({
      label: label(value),
      checked: value === current,
      action: () => {
        if (value !== current) choose(value);
      },
    })));
  };

  const chooseModel = async (value: string) => {
    if (session.modelScope === "default") {
      const name = modelLabel(value);
      const chosen = await confirmSheet(`Switch to ${name}?`, `${name} also becomes the default model for new ${agentProduct(session.kind)} sessions on your Mac.`, ["Switch", "Cancel"]);
      if (chosen !== "Switch") return;
    }
    void switchTo(true, value);
  };

  // A click on the card around the words, not on a control, goes to them.
  const cardClick = (e: MouseEvent<HTMLDivElement>) => {
    if (!enabled) return;
    const target = e.target as HTMLElement;
    if (target.closest("button, .message-field, .completion-list, .attachment")) return;
    e.preventDefault();
    field.current?.focus();
  };

  const stop = session.state === "busy" && canReceive && draft.trim() === "" && attachments.length === 0;

  return (
    <div className="composer-wrap">
      <ComposerFeedback message={error} onDismiss={() => setActionError(session.id, null)}>
        {resumable(session) ? (
          <ResumeBar
            resuming={resuming}
            onResume={async () => {
              setResuming(true);
              try {
                const reply = await daemon.request({ type: "resume_session", sessionId: session.id, clientId: `app-resume-${crypto.randomUUID()}` });
                if (reply.type === "error" || !reply.sessionId) {
                  setActionError(session.id, (reply.error ?? "It couldn't be resumed.").replace(/^daemon: /, ""));
                } else {
                  openAgent(reply.sessionId);
                }
              } catch (err) {
                setActionError(session.id, err instanceof Error ? err.message : String(err));
              }
              setResuming(false);
            }}
          />
        ) : (
          <div className={`composer-box ${focused ? "focused" : ""}`} onMouseDown={cardClick}>
            {slash.isOpen ? (
              <div className="completion-anchor"><SlashList slash={slash} onPick={pickCommand} /></div>
            ) : mentions.isOpen ? (
              <div className="completion-anchor"><MentionList mentions={mentions} onPick={pickMention} /></div>
            ) : null}
            {canReceive && attachments.length > 0 && (
              <AttachmentStrip attachments={attachments} onRemove={(id) => setAttachments(session.id, attachments.filter((a) => a.id !== id))} />
            )}
            <div className={`composer-field ${enabled ? "" : "disabled"}`}>
              <MessageField
                ref={field}
                value={draft}
                placeholder={placeholder(session, connected)}
                enabled={enabled}
                onChange={change}
                onSubmit={() => void sendNow()}
                onKey={onKey}
                onCaret={mentions.update}
                onFocusChange={(now) => {
                  setFocused(now);
                  if (!now) {
                    mentions.dismiss();
                    slash.dismiss();
                  }
                }}
                onPasteImages={(files) => {
                  if (!canReceive) return false;
                  void fromFiles(files).then((found) => addAttachments(session.id, found));
                  return true;
                }}
                isFile={mentions.isFile}
              />
            </div>
            <div className="composer-controls">
              {canReceive && (
                <button
                  className="clip"
                  disabled={!enabled || attachments.length >= MAX_ATTACHMENTS}
                  title="Attach images (or paste or drop them)"
                  onClick={() => void attach()}
                >
                  <Sym sf="paperclip" size={14} icon={Paperclip} />
                </button>
              )}
              {modes.length > 0 && (
                <button
                  className="switch-menu"
                  disabled={!enabled}
                  title="Switch the agent's mode"
                  onClick={(e) => switchMenu(e.currentTarget, modes, session.mode, modeLabel, (v) => void switchTo(false, v))}
                >
                  {session.mode ? modeLabel(session.mode) : "Choose"}
                  <Sym sf="chevron.down" size={8} weight="bold" icon={ChevronDown} />
                </button>
              )}
              <span className="composer-spacer" />
              {models.length > 0 && (
                <button
                  className="switch-menu"
                  disabled={!enabled}
                  title="Switch the agent's model"
                  onClick={(e) => switchMenu(e.currentTarget, models, session.model, modelLabel, (v) => void chooseModel(v))}
                >
                  {session.model ? modelLabel(session.model) : "Choose"}
                  <Sym sf="chevron.down" size={8} weight="bold" icon={ChevronDown} />
                </button>
              )}
              {canReceive && (
                <VoiceButton
                  enabled={enabled}
                  onText={(spoken) => {
                    const next = addSpoken(draft, spoken);
                    setDraft(next);
                    saveDraft(session.id, next);
                    setTimeout(() => field.current?.focus(), 0);
                  }}
                  onProblem={(problem) => setActionError(session.id, problem)}
                />
              )}
              {models.length === 0 && (
                <span className="model-chip">
                  <AgentMark kind={session.kind} size={14} />
                  <span className="model-chip-name">{session.model ? modelLabel(session.model) : agentProduct(session.kind)}</span>
                </span>
              )}
              {stop ? (
                <button
                  className="stop"
                  title="Stop the agent's turn"
                  aria-label="Stop the agent's turn"
                  onClick={() => daemon.send({ type: "interrupt", sessionId: session.id })}
                >
                  <span className="stop-square" />
                </button>
              ) : (
                <button className={`send ${canSend ? "can" : ""} ${enabled ? "" : "off"}`} disabled={!canSend} title="Send" aria-label="Send" onClick={() => void sendNow()}>
                  <Sym sf="arrow.up" size={13} weight="bold" icon={ArrowUp} />
                </button>
              )}
            </div>
          </div>
        )}
      </ComposerFeedback>
      {panel && <PanelSheet panel={panel} onDone={() => setPanel(null)} />}
    </div>
  );
}
