// The decision an agent is blocked on, answerable in place (Views/
// QuestionCard.swift): a permission prompt, a multiple-choice question, a
// typed answer, or one of Claude Code's own dialogs.
//
// A question the agent asked reads as Claude's own app shows one: the
// question as the heading, the choices under it, a row to write your own,
// and a ✕ to close it unanswered. A form of several questions names each one
// along the top, ticked once answered, with ‹ › to move between them. A
// permission prompt keeps its command first and its question under it.

import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ArrowUp, Check, ChevronLeft, ChevronRight, CornerDownLeft, Pencil, ShieldCheck, Sparkle, TriangleAlert, X } from "lucide-react";
import type { Question, QuestionOption, QuestionStep, Session } from "@shared/protocol";
import { parseMarkdown, type Inline } from "@shared/markdown";
import { reviewAnswers } from "@shared/question-answer";
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

/** Whether it takes a typed answer as well as its options. */
export function takesTypedAnswer(question: Question): boolean {
  return question.custom === true && question.multiple !== true;
}

/** Whether it asks for permission to do something: its options say yes or
 *  allow, rather than offering answers. */
export function isApproval(question: Question): boolean {
  return question.options.some((o) => /^(yes|allow|approve)/.test(o.label.toLowerCase()));
}

/** A form of several questions, with the one on screen known. */
export const isForm = (q: Question) => (q.steps?.length ?? 0) > 1 && (q.step ?? 0) > 0;
/** The form's review of its answers, the step past its last question. */
export const isReview = (q: Question) => isForm(q) && q.reviewStep === true && q.step === (q.steps?.length ?? 0) + 1;
/** The furthest step a form moves to: its review, or its last question. */
const lastStep = (q: Question) => (q.steps?.length ?? 0) + (q.reviewStep === true ? 1 : 0);
/** The form names its questions briefly enough to list them all, as
 *  Claude's headers do; Antigravity names each by the whole question. */
const namesSteps = (q: Question) => q.steps?.every((s) => s.title !== "" && [...s.title].length <= 24) ?? false;

const EDGES = /^[\p{P}\p{Zs}\t`]+|[\p{P}\p{Zs}\t`]+$/gu;

/** Whether the question's sentence only says again what is shown above it
 *  ("Run git push origin main?" under that command): then it is left out,
 *  and the command is said once. */
export function promptRepeatsDetail(question: Question): boolean {
  const detail = question.detail?.trim();
  if (!detail || detail.includes("\n")) return false;
  const prompt = question.prompt.trim();
  if (!prompt.toLowerCase().includes(detail.toLowerCase())) return false;
  // What is left once the command is taken out is only a verb or two.
  const rest = prompt.replace(new RegExp(detail.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "gi"), "").replace(EDGES, "");
  return rest.split(" ").filter(Boolean).length <= 2;
}

/** The agent's own question, not a permission prompt: its words are the
 *  heading. */
function askedByAgent(question: Question): boolean {
  return isReview(question) || (!isApproval(question) && !question.detail && !!question.prompt);
}

export function QuestionCard({ session, question }: { session: Session; question: Question }) {
  // The question a move was asked for, until the form shows it. A new
  // question is a new card (keyed by its id), with neither.
  const [moving, setMoving] = useState<number | null>(null);
  const [closing, setClosing] = useState(false);
  const asked = askedByAgent(question);
  const form = isForm(question);
  const steps = question.steps ?? [];

  const answer = (reply: QuestionReply) => {
    if (reply.optionKeys?.length) return perform(session.id, { type: "answer_question", questionId: question.id, optionKeys: reply.optionKeys });
    return perform(session.id, {
      type: "answer_question", questionId: question.id, optionKey: reply.optionKey,
      ...(reply.text ? { answerText: reply.text } : {}),
    });
  };

  const move = (step: number) => {
    if (moving !== null || step === question.step) return;
    setMoving(step);
    void perform(session.id, { type: "move_question", questionId: question.id, step }).then((moved) => {
      // The next look at the agent shows where it went; if it never does,
      // the choices come back rather than staying greyed.
      setTimeout(() => setMoving(null), moved ? 4000 : 0);
    });
  };

  const close = () => {
    setClosing(true);
    void perform(session.id, { type: "close_question", questionId: question.id }).then((closed) => {
      setTimeout(() => setClosing(false), closed ? 4000 : 0);
    });
  };

  const current = moving ?? question.step ?? 0;
  const title = (
    <span className={`question-title-text ${asked ? "asked" : ""}`}>{question.title || "Needs your answer"}</span>
  );
  const review = isReview(question) && question.detail ? reviewAnswers(question.detail) : null;

  return (
    <div className={`decision-panel question-card ${asked ? "asked" : ""}`}>
      <div className="question-title">
        <span className="question-dot" />
        {form && namesSteps(question) ? (
          // Every question's name, or, too many for the width, the one on
          // screen.
          <Fits
            wide={<QuestionSteps steps={steps} review={question.reviewStep === true} current={current} move={move} />}
            narrow={<span className="question-title-text asked">{current >= 1 && current <= steps.length ? steps[current - 1].title : "Review"}</span>}
          />
        ) : (
          <span className="question-title-fill">{title}</span>
        )}
        {form && (
          <StepPager current={moving ?? question.step ?? 1} count={steps.length} last={lastStep(question)} move={move} disabled={moving !== null || closing} />
        )}
        {question.closable === true && (
          <HeaderButton icon={X} sf="xmark" label={`Close the question unanswered: ${session.name} waits for your message`} disabled={closing} onClick={close} />
        )}
      </div>
      {asked && <div className="question-heading">{inlineMarkdown(question.prompt)}</div>}
      {question.detail ? (review ? <ReviewList review={review} /> : <div className="code-box">{question.detail}</div>) : null}
      <QuestionNotice question={question} />
      {!asked && question.prompt && !promptRepeatsDetail(question) ? <div className="question-prompt">{inlineMarkdown(question.prompt)}</div> : null}
      {/* The form is moving to another question: these are going. */}
      <div className={`question-choices ${moving !== null || closing ? "stepping" : ""}`} inert={moving !== null || closing ? true : undefined}>
        <QuestionChoices question={question} typedPrompt="Write your own answer" answer={answer} />
      </div>
    </div>
  );
}

/** Its wide content when that fits the width, else its narrow content, as
 *  ViewThatFits picks. The wide one is measured where it can't be seen. */
function Fits({ wide, narrow }: { wide: ReactNode; narrow: ReactNode }) {
  const slot = useRef<HTMLSpanElement>(null);
  const measure = useRef<HTMLSpanElement>(null);
  const [fits, setFits] = useState(true);
  useLayoutEffect(() => {
    const check = () => {
      if (slot.current && measure.current) setFits(measure.current.scrollWidth <= slot.current.clientWidth + 0.5);
    };
    check();
    const observer = new ResizeObserver(check);
    if (slot.current) observer.observe(slot.current);
    if (measure.current) observer.observe(measure.current);
    return () => observer.disconnect();
  });
  return (
    <span className="question-fits" ref={slot}>
      <span className={`question-fits-wide ${fits ? "" : "measuring"}`} ref={measure} aria-hidden={fits ? undefined : true}>{wide}</span>
      {!fits && narrow}
    </span>
  );
}

/** A form's questions by name along the top of its card: the one on screen
 *  in full colour, a tick on each answered, and a click on one goes to it. */
function QuestionSteps({ steps, review, current, move }: { steps: QuestionStep[]; review: boolean; current: number; move: (step: number) => void }) {
  return (
    <span className="question-steps">
      {steps.map((step, i) => (
        <StepTab key={i} title={step.title} answered={step.answered === true} current={current === i + 1} onClick={() => move(i + 1)} />
      ))}
      {review && <StepTab title="Review" answered={false} current={current === steps.length + 1} onClick={() => move(steps.length + 1)} />}
    </span>
  );
}

function StepTab({ title, answered, current, onClick }: { title: string; answered: boolean; current: boolean; onClick: () => void }) {
  return (
    <button className={`step-tab ${current ? "current" : ""}`} onClick={onClick} title={answered ? `${title}: answered` : title} aria-pressed={current}>
      {answered && <span className="step-tab-tick"><Sym icon={Check} sf="checkmark" size={9} weight="bold" /></span>}
      <span className="step-tab-title">{title}</span>
    </button>
  );
}

/** ‹ 2 of 3 ›: back and on through a form, one question at a time. Past the
 *  last question is the review of the answers. */
function StepPager({ current, count, last, move, disabled }: { current: number; count: number; last: number; move: (step: number) => void; disabled: boolean }) {
  return (
    <span className="step-pager">
      <HeaderButton icon={ChevronLeft} sf="chevron.left" label="Previous question" disabled={disabled || current <= 1} onClick={() => move(current - 1)} />
      <span className="step-pager-count">{current > count ? "Review" : `${current} of ${count}`}</span>
      <HeaderButton icon={ChevronRight} sf="chevron.right" label={current === count ? "Review your answers" : "Next question"} disabled={disabled || current >= last} onClick={() => move(current + 1)} />
    </span>
  );
}

/** A small icon button at the top of the card. */
function HeaderButton({ icon, sf, label, disabled, onClick }: { icon: typeof X; sf: string; label: string; disabled: boolean; onClick: () => void }) {
  return (
    <button className="question-header-button" disabled={disabled} onClick={onClick} title={label} aria-label={label}>
      <Sym icon={icon} sf={sf} size={11} weight="semibold" />
    </button>
  );
}

/** The answers a form's review lists ("● Which shape?" over "→ Square"), as
 *  rows to read rather than terminal text, with its warning when some are
 *  unanswered. */
function ReviewList({ review }: { review: NonNullable<ReturnType<typeof reviewAnswers>> }) {
  return (
    <div className="review-answers">
      {review.answers.map((row, i) => (
        <div key={i} className="review-answer">
          <span className="review-answer-question">{inlineMarkdown(row.question)}</span>
          <span className="review-answer-answer">{row.answer}</span>
        </div>
      ))}
      {review.warning && (
        <div className="review-warning"><Sym icon={TriangleAlert} sf="exclamationmark.triangle" size={12} />{review.warning}</div>
      )}
    </div>
  );
}

/** That agreeing brings up a permission dialog from macOS itself. */
export function QuestionNotice({ question }: { question: Question }) {
  if (!question.notice) return null;
  return <div className="question-notice"><Sym icon={ShieldCheck} sf="lock.shield" size={12} />{question.notice}</div>;
}

/** A question's options as buttons, with a note for an option that takes
 *  one, ticks for several, and a box for a typed answer. Compact for a card
 *  in a toast or in Agentman's chat; roomy, compact with room to read them,
 *  on a card of the Board. */
export function QuestionChoices({ question, suggested, typedPrompt = "Or type an answer", compact: small = false, roomy = false, typed = true, answer }: {
  question: Question;
  suggested?: string;
  typedPrompt?: string;
  compact?: boolean;
  roomy?: boolean;
  /** Whether to show the typed answer's box: not where the message box
   *  below takes it. */
  typed?: boolean;
  answer: (reply: QuestionReply) => Promise<boolean>;
}) {
  // Roomy is compact with room to read them: compact first.
  const compact = small || roomy;
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

  // The answer worth the orange on the Board: Agentman's pick, or a yes.
  const strong = (() => {
    if (suggested && question.options.some((o) => o.key === suggested)) return suggested;
    return question.options.find((o) => /^(yes|allow|approve|continue|proceed)/.test(o.label.toLowerCase()))?.key;
  })();
  // The answer already given, back on a question of a form; Agentman's pick
  // when it made one; otherwise the first affirmative option, the one most
  // people reach for.
  const primary = question.options.find((o) => o.checked)?.key ?? strong ?? question.options[0]?.key;
  // Words being written are the answer: the choices step back.
  const writing = typed && !compact && question.custom === true && note.trim() !== "";

  let choices: ReactNode;
  if (question.multiple) {
    choices = (
      <div className={`choices-multiple ${compact ? "compact" : ""}`}>
        {question.options.map((option) => (
          <CheckRow
            key={option.key}
            option={option}
            checked={checked.has(option.key)}
            compact={compact}
            disabled={busy}
            onToggle={() => {
              const next = new Set(checked);
              if (next.has(option.key)) next.delete(option.key);
              else next.add(option.key);
              setChecked(next);
            }}
          />
        ))}
        <div className="choices-submit">
          {/* In the order they are listed, not the order ticked. */}
          <button className="choice-button primary" disabled={checked.size === 0 || busy} onClick={() => send({ optionKeys: question.options.map((o) => o.key).filter((k) => checked.has(k)) })}>Submit</button>
          <span className="choices-count">{checked.size === 0 ? "Tick one or more" : checked.size === 1 ? "1 chosen" : `${checked.size} chosen`}</span>
        </div>
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
      <div className={`choices-single ${compact ? "compact" : ""} ${roomy ? "roomy" : ""} ${writing ? "stepping" : ""}`}>
        {question.options.map((option, i) => (
          <OptionRow
            key={option.key}
            number={i + 1}
            option={option}
            pick={option.key === suggested}
            leading={option.key === primary}
            chosen={option.checked === true}
            compact={compact}
            roomy={roomy}
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
      {typed && question.custom && !question.multiple && !noting && (
        !compact ? (
          <WriteYourOwn prompt={typedPrompt} text={note} setText={setNote} busy={busy} send={() => send({ text: note })} />
        ) : (
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
        )
      )}
    </div>
  );
}

/** One choice: its number, its words in full, and Agentman's pick marked.
 *  The likeliest answer is lightly filled; the rest fill as the pointer
 *  passes, with a ⏎ at the end; the answer given before, back on a question
 *  of a form, is ticked. */
function OptionRow({ number, option, pick, leading, chosen, compact, roomy, disabled, onChoose }: {
  number: number;
  option: QuestionOption;
  pick: boolean;
  leading: boolean;
  chosen: boolean;
  compact: boolean;
  roomy: boolean;
  disabled: boolean;
  onChoose: () => void;
}) {
  return (
    <button
      className={`option-row ${leading || pick ? "leading" : ""} ${compact ? "compact" : ""} ${roomy ? "roomy" : ""}`}
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
      {chosen ? (
        <span className="option-chosen" title="Your answer"><Sym icon={Check} sf="checkmark" size={11} weight="bold" /></span>
      ) : (
        <span className="option-return"><Sym icon={CornerDownLeft} sf="return" size={11} weight="semibold" /></span>
      )}
    </button>
  );
}

/** One of several choices that may be ticked: a box where a single choice
 *  has its number, its words in full, and a fill while ticked. */
function CheckRow({ option, checked, compact, disabled, onToggle }: {
  option: QuestionOption;
  checked: boolean;
  compact: boolean;
  disabled: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      className={`check-row ${checked ? "checked" : ""} ${compact ? "compact" : ""}`}
      disabled={disabled}
      onClick={onToggle}
      role="checkbox"
      aria-checked={checked}
      title={checked ? `Untick ${option.label}` : `Tick ${option.label}`}
    >
      <span className="check-box-slot">
        <span className="check-box">{checked && <Sym icon={Check} sf="checkmark" size={compact ? 9 : 10} weight="bold" />}</span>
      </span>
      <span className="option-words">
        <span className="option-label">{option.label}</span>
        {!compact && option.description ? <span className="option-description">{option.description}</span> : null}
      </span>
    </button>
  );
}

/** The row under a question's choices for an answer of your own: a pencil
 *  where a choice has its number, the words, and a send arrow once there
 *  are some. Return sends; Shift-Return starts a new line. */
function WriteYourOwn({ prompt, text, setText, busy, send }: { prompt: string; text: string; setText: (text: string) => void; busy: boolean; send: () => void }) {
  const field = useRef<HTMLTextAreaElement>(null);
  const empty = text.trim() === "";
  // As tall as its words, up to six lines.
  useLayoutEffect(() => {
    const el = field.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, 6 * 17)}px`;
  }, [text]);
  return (
    <div className="write-own" onClick={() => field.current?.focus()}>
      <span className="write-own-icon"><Sym icon={Pencil} sf="pencil" size={11} weight="semibold" /></span>
      <textarea
        ref={field}
        className="write-own-field"
        rows={1}
        placeholder={prompt}
        value={text}
        disabled={busy}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !e.altKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            send();
          }
        }}
      />
      <button className={`write-own-send ${empty ? "hidden" : ""}`} disabled={empty || busy} onClick={send} title="Send this answer" aria-label="Send this answer" tabIndex={empty ? -1 : 0}>
        <Sym icon={ArrowUp} sf="arrow.up" size={11} weight="bold" />
      </button>
    </div>
  );
}
