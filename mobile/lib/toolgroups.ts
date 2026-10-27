import type { Message } from "./protocol";

/**
 * Runs of tool calls, gathered so the conversation reads as what was done
 * rather than a log of every command. Two calls or more in a row make a run;
 * a single call stays a row of its own. The Mac groups them the same way.
 *
 * Calls that read a picture run apart from the rest, so the pictures can be
 * shown: one is a row with its picture under it, and several in a row are
 * one run with theirs side by side under it (`pictures`), as Claude's own
 * app shows them.
 */
export type FeedItem =
  | { kind: "message"; message: Message }
  | { kind: "tools"; key: string; calls: Message[]; said: string; pictures?: boolean };

export function groupTools(
  messages: readonly Message[],
  /** A call that read a picture, which is shown under it. */
  readsPicture: (message: Message) => boolean = () => false,
): FeedItem[] {
  const items: FeedItem[] = [];
  let run: Message[] = [];
  let pictures: Message[] = [];
  let said = "";
  const flush = () => {
    if (run.length === 1) items.push({ kind: "message", message: run[0] });
    else if (run.length > 1) items.push({ kind: "tools", key: toolRunKey(run), calls: run, said });
    run = [];
  };
  const flushPictures = () => {
    if (pictures.length === 1) items.push({ kind: "message", message: pictures[0] });
    else if (pictures.length > 1) {
      items.push({ kind: "tools", key: toolRunKey(pictures), calls: pictures, said, pictures: true });
    }
    pictures = [];
  };
  for (const message of messages) {
    if (message.role === "tool" && message.tool && readsPicture(message)) {
      flush();
      pictures.push(message);
      continue;
    }
    if (message.role === "tool" && message.tool) {
      flushPictures();
      run.push(message);
      continue;
    }
    flush();
    flushPictures();
    if (message.role === "assistant" && message.text) said = message.text;
    items.push({ kind: "message", message });
  }
  flush();
  flushPictures();
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

const IMAGE = /\.(png|jpe?g|gif|webp|heic)$/i;

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
    const image = kind?.[2] === "file" && kind[1] === "Read" && IMAGE.test(call.tool?.summary ?? "");
    const verb = image ? "Looked at" : (kind?.[1] ?? "Used");
    const noun = image ? "image" : (kind?.[2] ?? "tool");
    const id = `${verb} ${noun}`;
    if (!counts.has(id)) order.push(id);
    const entry = counts.get(id) ?? { verb, noun, n: 0 };
    entry.n += 1;
    counts.set(id, entry);
  }
  const phrases = order.map((id, i) => {
    const { verb, noun, n } = counts.get(id)!;
    const one = /^[aeiou]/.test(noun) ? `an ${noun}` : `a ${noun}`;
    const words = `${i === 0 ? verb : verb.toLowerCase()} ${n === 1 ? one : `${n} ${noun}s`}`;
    return verb === "Searched" ? `${i === 0 ? "Searched" : "searched"} ${n === 1 ? "once" : `${n} times`}` : words;
  });
  if (phrases.length <= 1) return phrases[0] ?? "Used tools";
  return `${phrases.slice(0, -1).join(", ")} and ${phrases.at(-1)}`;
}
