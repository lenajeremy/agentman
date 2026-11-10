// An agent waiting on the user, small (Views/CompactCard.swift): who, what
// it asks in a line or two, and the answer a tap away: its options, with
// Agentman's pick marked, or a line to Agentman. The same in a toast and in
// Agentman's chat. Its choices are QuestionCard.swift's, drawn compact.

import { useState } from "react";
import { ArrowUp, ArrowUpRight, CornerDownLeft, Sparkle, X } from "lucide-react";
import type { ManagerCard, Question, QuestionOption } from "@shared/protocol";
import { cardQuestion } from "@shared/manager-feed";
import { openAgent } from "../nav";
import { useStore } from "../store";
import { AgentMark, AppIcon, Icon } from "../ui";
import { replyToCard, type QuestionReply } from "./store";
import { InlineMarkdown, MiddleTruncated } from "./text";

/** Agentman's pick when it made one; otherwise the first affirmative
 *  option, the one most people reach for. */
function primaryKey(question: Question, suggested?: string): string | undefined {
  if (suggested && question.options.some((o) => o.key === suggested)) return suggested;
  const affirmative = question.options.find((o) => /^(yes|allow|approve|continue|proceed)/.test(o.label.toLowerCase()));
  return (affirmative ?? question.options[0])?.key;
}

function OptionRow({ number, option, pick, leading, onChoose }: { number: number; option: QuestionOption; pick: boolean; leading: boolean; onChoose: () => void }) {
  return (
    <button className={`am-option ${leading || pick ? "lead" : ""}`} title={option.withText ? "Choose, and add a note for the agent" : option.label} onClick={onChoose}>
      <span className="am-option-number tabular">{number}</span>
      <span className="am-option-label">{option.label}</span>
      {pick && <span className="am-option-pick" title="Agentman's pick"><Icon icon={Sparkle} sf="sparkle" size={10} weight="semibold" /></span>}
      <span className="am-option-return"><Icon icon={CornerDownLeft} sf="return" size={11} weight="semibold" /></span>
    </button>
  );
}

/** A question's options, small: one tap answers; a box for a typed answer
 *  that, in Agentman's chat, goes to Agentman. */
function CompactChoices({ question, suggested, answer }: { question: Question; suggested?: string; answer: (reply: QuestionReply) => Promise<boolean> }) {
  const [note, setNote] = useState("");
  const [noting, setNoting] = useState<QuestionOption | null>(null);
  const [checked, setChecked] = useState<Set<string>>(() => new Set(question.options.filter((o) => o.checked).map((o) => o.key)));
  const [busy, setBusy] = useState(false);
  const lead = primaryKey(question, suggested);

  const send = (reply: QuestionReply) => {
    const text = reply.text?.trim();
    const r = { ...reply, text: text ? text : undefined };
    if (!r.optionKey && !r.optionKeys?.length && !r.text) return;
    setBusy(true);
    void answer(r).then((ok) => {
      if (ok) {
        setNote("");
        setNoting(null);
      }
      setBusy(false);
    });
  };

  return (
    <fieldset className="am-choices" disabled={busy}>
      {question.multiple ? (
        <div className="am-multi">
          {question.options.map((o) => (
            <label key={o.key} className="am-check">
              <input
                type="checkbox"
                checked={checked.has(o.key)}
                onChange={(e) => {
                  const next = new Set(checked);
                  if (e.target.checked) next.add(o.key);
                  else next.delete(o.key);
                  setChecked(next);
                }}
              />
              <span>{o.label}</span>
            </label>
          ))}
          <button className="am-submit" disabled={checked.size === 0} onClick={() => send({ optionKeys: [...checked] })}>Submit</button>
        </div>
      ) : noting ? (
        <div className="am-noting">
          <div className="am-noting-label">{noting.label}</div>
          <div className="am-typed">
            <input className="am-rounded-field" value={note} placeholder="Tell the agent what to do instead" onChange={(e) => setNote(e.target.value)} onKeyDown={(e) => e.key === "Enter" && send({ optionKey: noting.key, text: note })} autoFocus />
            <button className="am-push" onClick={() => setNoting(null)}>Cancel</button>
            <button className="am-push" disabled={!note.trim()} onClick={() => send({ optionKey: noting.key, text: note })}>Send</button>
          </div>
        </div>
      ) : (
        <div className="am-options">
          {question.options.map((o, i) => (
            <OptionRow
              key={o.key}
              number={i + 1}
              option={o}
              pick={o.key === suggested}
              leading={o.key === lead}
              onChoose={() => (o.withText ? setNoting(o) : send({ optionKey: o.key }))}
            />
          ))}
        </div>
      )}
      {question.custom && !question.multiple && !noting && (
        <div className="am-typed">
          <input className="am-rounded-field small" value={note} placeholder="Or reply to Agentman" onChange={(e) => setNote(e.target.value)} onKeyDown={(e) => e.key === "Enter" && send({ text: note })} />
          <button className="am-push small" disabled={!note.trim() || busy} onClick={() => send({ text: note })}>Send</button>
        </div>
      )}
    </fieldset>
  );
}

/** The command or file in a line, unless the title already says it. */
function shownDetail(card: ManagerCard): string | undefined {
  const detail = card.detail?.trim();
  if (!detail) return undefined;
  const line = detail.split("\n").filter(Boolean).pop()?.trim() ?? detail;
  return !line || card.title.includes(line) ? undefined : line;
}

export function CompactCard({ card, closeHelp = "Put away", close }: { card: ManagerCard; closeHelp?: string; close: () => Promise<string | undefined> }) {
  const { sessions } = useStore();
  const session = sessions.find((s) => s.id === card.sessionId);
  const name = session?.name || card.agentName;
  const [reply, setReply] = useState("");
  const [problem, setProblem] = useState<string>();
  const [busy, setBusy] = useState(false);
  const question = cardQuestion(card);
  const offer = card.kind === "offer";
  const detail = shownDetail(card);
  const typed = reply.trim().length > 0;

  const act = (work: () => Promise<string | undefined>) => {
    setBusy(true);
    void work().then((p) => {
      setProblem(p);
      setBusy(false);
    });
  };
  const replyInWords = () => {
    const text = reply.trim();
    if (!text) return;
    act(async () => {
      const p = await replyToCard(card, { text });
      if (!p) setReply("");
      return p;
    });
  };

  return (
    <fieldset className="am-compact" disabled={busy}>
      <div className="am-compact-head">
        {/* An offer is Agentman's, not the agent asking (CompactCard.swift). */}
        {offer ? (
          <>
            <AppIcon size={14} />
            <span className="am-compact-name">Agentman offers</span>
          </>
        ) : (
          <>
            <AgentMark kind={session?.kind ?? ""} size={14} />
            <span className="am-compact-name">{name}</span>
            <span className="am-needs-dot" />
            <span className="am-needs-text">needs you</span>
          </>
        )}
        <span className="am-spacer" />
        {session && !offer && (
          <button className="am-icon-button" title={`Open ${name}`} onClick={() => openAgent(card.sessionId)}>
            <Icon icon={ArrowUpRight} sf="arrow.up.right" size={9} weight="semibold" />
          </button>
        )}
        <button className="am-icon-button" title={closeHelp} onClick={() => act(close)}>
          <Icon icon={X} sf="xmark" size={9} weight="semibold" />
        </button>
      </div>
      <div className="am-compact-title" title={card.title}><InlineMarkdown text={card.title} /></div>
      {offer && card.detail?.trim() ? (
        <div className="am-offer-detail">{card.detail.trim()}</div>
      ) : (
        detail && <MiddleTruncated className="am-compact-detail" text={detail} title={card.detail ?? detail} />
      )}
      {question ? (
        <CompactChoices
          question={question}
          suggested={card.suggested}
          answer={async (answer) => {
            const p = await replyToCard(card, answer);
            setProblem(p);
            return !p;
          }}
        />
      ) : (
        <div className="am-reply-field">
          <input value={reply} placeholder={`Reply to Agentman about ${name}`} onChange={(e) => setReply(e.target.value)} onKeyDown={(e) => e.key === "Enter" && replyInWords()} />
          <button className={`am-reply-send ${typed ? "can" : ""}`} disabled={!typed || busy} title={`Agentman acts on it and passes on what ${name} needs`} onClick={replyInWords}>
            <Icon icon={ArrowUp} sf="arrow.up" size={10} weight="bold" />
          </button>
        </div>
      )}
      {problem && <div className="am-problem">{problem}</div>}
    </fieldset>
  );
}
