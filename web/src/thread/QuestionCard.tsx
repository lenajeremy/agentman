// The decision an agent is blocked on, answerable in place (Views/
// QuestionCard.swift): a permission prompt, a multiple-choice question, a
// typed answer, or one of Claude Code's own dialogs.

import { useState, type ReactNode } from "react";
import { CornerDownLeft, Sparkle } from "lucide-react";
import type { Question, QuestionOption, Session } from "@shared/protocol";
import { parseMarkdown, type Inline } from "@shared/markdown";
import { Sym } from "./Sym";
import { perform } from "./state";

export interface QuestionReply {
  optionKey?: string;
  optionKeys?: string[];
  text?: string;
}

/** Inline markdown only, for a question's prompt: bold, italic, code, links. */
function inlineMarkdown(text: string): ReactNode {
  const first = parseMarkdown(text)[0];
  const content: Inline[] = first && (first.kind === "paragraph" || first.kind === "heading") ? first.content : [{ kind: "text", text }];
  const walk = (nodes: Inline[]): ReactNode[] =>
    nodes.map((node, i) => {
      switch (node.kind) {
        case "text": return node.text;
        case "code": case "math": return <code key={i}>{node.text}</code>;
        case "bold": return <strong key={i}>{walk(node.children)}</strong>;
        case "italic": return <em key={i}>{walk(node.children)}</em>;
        case "strike": return <s key={i}>{walk(node.children)}</s>;
        case "link": case "file": return <span key={i} className="question-link">{walk(node.children)}</span>;
        case "break": return <br key={i} />;
      }
    });
  return walk(content);
}

export function QuestionCard({ session, question }: { session: Session; question: Question }) {
  const answer = (reply: QuestionReply) => {
    if (reply.optionKeys?.length) return perform(session.id, { type: "answer_question", questionId: question.id, optionKeys: reply.optionKeys });
    return perform(session.id, {
      type: "answer_question", questionId: question.id, optionKey: reply.optionKey,
      ...(reply.text ? { answerText: reply.text } : {}),
    });
  };
  return (
    <div className="decision-panel question-card">
      <div className="question-title">
        <span className="question-dot" />
        <span>{question.title || "Needs your answer"}</span>
      </div>
      {question.detail ? <div className="code-box">{question.detail}</div> : null}
      {question.prompt ? <div className="question-prompt">{inlineMarkdown(question.prompt)}</div> : null}
      <QuestionChoices question={question} answer={answer} />
    </div>
  );
}

/** A question's options as buttons, with a note for an option that takes
 *  one, ticks for several, and a box for a typed answer. Compact for a card
 *  in a toast or in Agentman's chat. */
export function QuestionChoices({ question, suggested, typedPrompt = "Or type an answer", compact = false, answer }: {
  question: Question;
  suggested?: string;
  typedPrompt?: string;
  compact?: boolean;
  answer: (reply: QuestionReply) => Promise<boolean>;
}) {
  const [note, setNote] = useState("");
  const [noting, setNoting] = useState<QuestionOption | null>(null);
  const [checked, setChecked] = useState<Set<string>>(() => new Set(question.options.filter((o) => o.checked).map((o) => o.key)));
  const [busy, setBusy] = useState(false);

  const send = (reply: QuestionReply) => {
    const text = reply.text?.trim();
    const made = { ...reply, text: text ? text : undefined };
    if (!made.optionKey && !made.optionKeys?.length && !made.text) return;
    setBusy(true);
    void answer(made).then((ok) => {
      if (ok) {
        setNote("");
        setNoting(null);
      }
      setBusy(false);
    });
  };

  // Agentman's pick when it made one; otherwise the first affirmative
  // option, the one most people reach for.
  const primary = (() => {
    if (suggested && question.options.some((o) => o.key === suggested)) return suggested;
    const yes = question.options.find((o) => /^(yes|allow|approve|continue|proceed)/.test(o.label.toLowerCase()));
    return (yes ?? question.options[0])?.key;
  })();

  let choices: ReactNode;
  if (question.multiple) {
    choices = (
      <div className="choices-multiple">
        {question.options.map((option) => (
          <label key={option.key} className="mac-checkbox-row">
            <input
              type="checkbox"
              className="mac-checkbox"
              checked={checked.has(option.key)}
              disabled={busy}
              onChange={(e) => {
                const next = new Set(checked);
                if (e.target.checked) next.add(option.key);
                else next.delete(option.key);
                setChecked(next);
              }}
            />
            <span>{option.label}</span>
          </label>
        ))}
        <button className="choice-button primary" disabled={checked.size === 0 || busy} onClick={() => send({ optionKeys: [...checked] })}>Submit</button>
      </div>
    );
  } else if (noting) {
    choices = (
      <div className="choices-note">
        <div className="choices-note-label">{noting.label}</div>
        <div className="mac-field-row">
          <input
            className="mac-field"
            autoFocus
            placeholder="Tell the agent what to do instead"
            value={note}
            disabled={busy}
            onChange={(e) => setNote(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing) send({ optionKey: noting.key, text: note });
            }}
          />
          <button className="mac-button" disabled={busy} onClick={() => setNoting(null)}>Cancel</button>
          <button className="mac-button" disabled={busy || !note.trim()} onClick={() => send({ optionKey: noting.key, text: note })}>Send</button>
        </div>
      </div>
    );
  } else {
    choices = (
      <div className={`choices-single ${compact ? "compact" : ""}`}>
        {question.options.map((option, i) => (
          <OptionRow
            key={option.key}
            number={i + 1}
            option={option}
            pick={option.key === suggested}
            leading={option.key === primary}
            compact={compact}
            disabled={busy}
            onChoose={() => (option.withText ? setNoting(option) : send({ optionKey: option.key }))}
          />
        ))}
      </div>
    );
  }

  return (
    <div className={`choices ${compact ? "compact" : ""}`}>
      {choices}
      {question.custom && !question.multiple && !noting && (
        <div className="mac-field-row">
          <input
            className={`mac-field ${compact ? "small" : ""}`}
            placeholder={typedPrompt}
            value={note}
            disabled={busy}
            onChange={(e) => setNote(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing) send({ text: note });
            }}
          />
          <button className={`mac-button ${compact ? "small" : ""}`} disabled={!note.trim() || busy} onClick={() => send({ text: note })}>Send</button>
        </div>
      )}
    </div>
  );
}

/** One choice: its number, its words in full, and Agentman's pick marked.
 *  The likeliest answer is lightly filled; the rest fill as the pointer
 *  passes, with a ⏎ at the end. */
function OptionRow({ number, option, pick, leading, compact, disabled, onChoose }: {
  number: number;
  option: QuestionOption;
  pick: boolean;
  leading: boolean;
  compact: boolean;
  disabled: boolean;
  onChoose: () => void;
}) {
  return (
    <button
      className={`option-row ${leading || pick ? "leading" : ""} ${compact ? "compact" : ""}`}
      disabled={disabled}
      onClick={onChoose}
      title={option.withText ? "Choose, and add a note for the agent" : option.label}
    >
      <span className="option-number">{number}</span>
      <span className="option-words">
        <span className="option-label">{option.label}</span>
        {!compact && option.description ? <span className="option-description">{option.description}</span> : null}
      </span>
      {pick && compact && <span className="option-pick-mark" title="Agentman's pick"><Sym icon={Sparkle} sf="sparkle" size={10} weight="semibold" /></span>}
      {pick && !compact && (
        <span className="option-pick"><Sym icon={Sparkle} sf="sparkle" size={11} weight="medium" />Agentman's pick</span>
      )}
      <span className="option-return"><Sym icon={CornerDownLeft} sf="return" size={11} weight="semibold" /></span>
    </button>
  );
}
