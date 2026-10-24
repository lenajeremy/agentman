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
