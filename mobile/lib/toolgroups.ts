import type { Message } from "./protocol";

/**
 * Runs of tool calls, gathered so the conversation reads as what was done
 * rather than a log of every command. Two calls or more in a row make a run;
 * a single call stays a row of its own. The Mac groups them the same way.
 */
export type FeedItem =
  | { kind: "message"; message: Message }
  | { kind: "tools"; key: string; calls: Message[]; said: string };

export function groupTools(messages: readonly Message[]): FeedItem[] {
  const items: FeedItem[] = [];
  let run: Message[] = [];
  let said = "";
  const flush = () => {
    if (run.length === 1) items.push({ kind: "message", message: run[0] });
    else if (run.length > 1) items.push({ kind: "tools", key: toolRunKey(run), calls: run, said });
    run = [];
  };
  for (const message of messages) {
    if (message.role === "tool" && message.tool) {
      run.push(message);
      continue;
    }
    flush();
    if (message.role === "assistant" && message.text) said = message.text;
    items.push({ kind: "message", message });
  }
  flush();
  return items;
}

/** A run's identity: its first and last call and how many there are. */
export function toolRunKey(calls: readonly Message[]): string {
  return `${calls[0]?.id ?? ""}|${calls.at(-1)?.id ?? ""}|${calls.length}`;
}

/** Whether every call in a run has finished. */
export function runFinished(calls: readonly Message[]): boolean {
  return !calls.some((call) => call.tool?.status === "running");
}

/**
 * A run told by its steps' own words: "Rebuilt the daemon, then paired the
 * phone", or the first two and how many more. Undefined when the steps have
 * no words of their own, as from an older Mac.
 */
export function runFromSteps(calls: readonly Message[]): string | undefined {
  const titles = calls.map((call) => call.tool?.title ?? "").filter(Boolean);
  if (titles.length !== calls.length || titles.length === 0) return undefined;
  const lower = (s: string) => s.charAt(0).toLowerCase() + s.slice(1);
  if (titles.length === 1) return titles[0];
  if (titles.length === 2) return `${titles[0]}, then ${lower(titles[1])}`;
  const more = titles.length - 2;
  return `${titles[0]}, ${lower(titles[1])} and ${more} more step${more === 1 ? "" : "s"}`;
}

const KINDS: [RegExp, string, string][] = [
  [/^(bash|shell|exec_command|run_shell_command|local_shell|terminal|bashoutput)$/i, "Ran", "command"],
  [/^(read|read_file|view|cat|notebookread)$/i, "Read", "file"],
  [/^(edit|multiedit|apply_patch|str_replace|str_replace_editor|notebookedit|replace)$/i, "Edited", "file"],
  [/^(write|create_file|write_file)$/i, "Wrote", "file"],
  [/^(grep|glob|search|find|list|ls|codebase_search|file_search)$/i, "Searched", "time"],
  [/^(webfetch|fetch|websearch|web_search|web_fetch)$/i, "Looked up", "page"],
];

/**
 * A run told by what kinds of step it took: "Ran 3 commands and read 2
 * files". For a Mac too old to describe the steps themselves.
 */
export function runCounts(calls: readonly Message[]): string {
  const order: string[] = [];
  const counts = new Map<string, { verb: string; noun: string; n: number }>();
  for (const call of calls) {
    const name = call.tool?.name ?? "tool";
    const kind = KINDS.find(([pattern]) => pattern.test(name));
    const verb = kind?.[1] ?? "Used";
    const noun = kind?.[2] ?? "tool";
    const id = `${verb} ${noun}`;
    if (!counts.has(id)) order.push(id);
    const entry = counts.get(id) ?? { verb, noun, n: 0 };
    entry.n += 1;
    counts.set(id, entry);
  }
  const phrases = order.map((id, i) => {
    const { verb, noun, n } = counts.get(id)!;
    const words = `${i === 0 ? verb : verb.toLowerCase()} ${n === 1 ? `a ${noun}` : `${n} ${noun}s`}`;
    return verb === "Searched" ? `${i === 0 ? "Searched" : "searched"} ${n === 1 ? "once" : `${n} times`}` : words;
  });
  if (phrases.length <= 1) return phrases[0] ?? "Used tools";
  return `${phrases.slice(0, -1).join(", ")} and ${phrases.at(-1)}`;
}
