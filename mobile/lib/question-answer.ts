import type { Question, QuestionAnswer, QuestionOption } from "./protocol";

/**
 * Longest note sent with a choice. It is a sentence telling the agent what to
 * do instead, typed into a terminal box on the Mac, not a message.
 */
export const MAX_NOTE_CHARS = 2000;

/**
 * What one tap on an option sends in the one-tap layout, or null when the tap
 * should open the option's note instead. Only an option the agent marked
 * withText opens one; every other option stays a single tap.
 */
export function tapAnswer(option: Pick<QuestionOption, "key" | "withText">): QuestionAnswer | null {
  return option.withText ? null : { optionKey: option.key };
}

/**
 * The answer sent from an option's note: the option, plus the note when there
 * is one. An empty note is the plain choice, so opening the box never forces
 * anyone to write in it.
 *
 * The note goes on one line. Every CLI that takes one types it into a
 * single-line box where a newline submits, so a line break would send the
 * first half and leave the rest to land wherever the agent's focus went next.
 */
export function noteAnswer(key: string, note: string): QuestionAnswer {
  const text = note.replace(/\s+/g, " ").trim().slice(0, MAX_NOTE_CHARS).trim();
  return text ? { optionKey: key, answerText: text } : { optionKey: key };
}

/**
 * What the Submit button sends in the full form — multiple choice, a custom
 * answer, or options with previews — or null when it cannot send yet.
 *
 * One chosen option sends its key, with its note when it takes one. Anything
 * else is the API shape: every chosen key and the custom text.
 */
export function submitAnswer(
  question: Pick<Question, "multiple" | "options">,
  selected: readonly string[],
  custom: string,
  note = "",
): QuestionAnswer | null {
  const customText = custom.trim();
  const count = selected.length + (customText ? 1 : 0);
  if (count === 0 || (!question.multiple && count !== 1)) return null;
  if (!question.multiple && selected.length === 1) {
    const option = question.options.find((candidate) => candidate.key === selected[0]);
    return option?.withText ? noteAnswer(selected[0], note) : { optionKey: selected[0] };
  }
  return {
    optionKeys: selected.length > 0 ? [...selected] : undefined,
    answerText: customText || undefined,
  };
}

/** What a form's review lists: each question answered, and its warning
 *  when some were not ("⚠ You have not answered all questions"). */
export interface ReviewAnswers {
  warning?: string;
  answers: { question: string; answer: string }[];
}

/**
 * Reads Claude's review of a form ("● Which shape?" over "→ Square") so it
 * can be shown as rows rather than as terminal text. Null when the text is
 * not in that shape, and it is shown as it came.
 */
export function reviewAnswers(detail: string): ReviewAnswers | null {
  let warning: string | undefined;
  const answers: { question: string; answer: string }[] = [];
  let asked: string | null = null;
  for (const raw of detail.split("\n")) {
    const line = raw.trim();
    if (line.startsWith("⚠")) {
      warning = line.slice(1).trim();
    } else if (line.startsWith("●")) {
      asked = line.slice(1).trim();
    } else if (line.startsWith("→") && asked !== null) {
      answers.push({ question: asked, answer: line.slice(1).trim() });
      asked = null;
    } else if (line) {
      return null;
    }
  }
  if (!warning && answers.length === 0) return null;
  return { warning, answers };
}
