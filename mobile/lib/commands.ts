/**
 * Slash commands in an agent's message box: the "/" menu, as the agent's own
 * terminal offers it. The Mac lists what the agent would offer (its own
 * commands and the user's skills), a command is sent like any message, and a
 * panel command sent bare (/usage, /status) is read off the terminal instead,
 * because it never reaches the transcript.
 */

import type { CommandPanel, DaemonEvent, Request, Session, SlashCommand } from "./protocol";

type Ask = (request: Request, timeoutMs?: number) => Promise<DaemonEvent>;

/** Commands are typed into the agent's prompt, so only a Claude or Codex
 *  the Mac started in a terminal it can type into takes them. */
export function takesCommands(session: Pick<Session, "kind" | "inject"> | undefined): boolean {
  return (session?.kind === "claude" || session?.kind === "codex") && session.inject === "tmux";
}

/** A name being typed after "/", or after "$" for an agent whose skills take
 *  it (Codex). */
export interface CommandQuery {
  prefix: "/" | "$";
  query: string;
}

/**
 * What is being typed after the "/" (or "$", when `skills`), while the name
 * is still being typed: null once there is a space after it, or when the box
 * holds anything else.
 */
export function slashQuery(draft: string, skills = false): CommandQuery | null {
  const match = (skills ? /^([/$])(\S*)$/ : /^(\/)(\S*)$/).exec(draft);
  return match ? { prefix: match[1] as "/" | "$", query: match[2] } : null;
}

/** A command as the menu and the box write it: "/compact", "$imagegen". */
export function commandLabel(command: SlashCommand): string {
  return `${command.prefix ?? "/"}${command.name}`;
}

/** A command as sent: its name and what follows it. */
export interface CommandCall {
  name: string;
  args: string;
}

/** The command a message runs, if it is one: "/" and a name that is all of
 *  the first word. */
export function commandCall(text: string): CommandCall | null {
  const match = /^\/([^\s/][^\s]*)(?:\s+([\s\S]*))?$/.exec(text.trim());
  return match ? { name: match[1], args: (match[2] ?? "").trim() } : null;
}

/** Letters only, as Claude's menu compares them: "adddir" finds add-dir. */
function squash(text: string): string {
  return text.toLowerCase().replace(/[:_\-\s]/g, "");
}

/**
 * The commands matching what has been typed, best first: the name itself,
 * then names that start with it, then a word within a name that does
 * ("deploy" finds vercel:deploy), then any that contain it. Built-in
 * commands lead within each, as in the agent's own menu.
 */
export function matchCommands(
  commands: readonly SlashCommand[],
  query: string,
  limit = 60,
  /** "$" lists only what takes "$": Codex's skills. */
  prefix: "/" | "$" = "/",
): SlashCommand[] {
  const typed = query.toLowerCase();
  const squashed = squash(query);
  const rank = (command: SlashCommand): number => {
    const name = command.name.toLowerCase();
    if (typed === "") return 3;
    if (name === typed) return 0;
    if (name.startsWith(typed) || squash(name).startsWith(squashed)) return 1;
    if (name.split(/[:_\-]/).some((word) => word.startsWith(typed))) return 2;
    if (squashed && squash(name).includes(squashed)) return 3;
    return -1;
  };
  const builtinFirst = (command: SlashCommand) => (command.source === "builtin" ? 0 : 1);
  return commands
    .filter((command) => prefix === "/" || command.prefix === prefix)
    .map((command) => ({ command, rank: rank(command) }))
    .filter((entry) => entry.rank >= 0)
    .sort(
      (a, b) =>
        a.rank - b.rank ||
        builtinFirst(a.command) - builtinFirst(b.command) ||
        a.command.name.localeCompare(b.command.name),
    )
    .slice(0, limit)
    .map((entry) => entry.command);
}

/** What the box holds once a command is picked: a space after it when it
 *  takes something, so the next word is its argument. A skill always does:
 *  what it is for comes next. */
export function pickedCommand(command: SlashCommand): string {
  return command.argumentHint || command.prefix === "$" ? `${commandLabel(command)} ` : commandLabel(command);
}

/** What sending a message does, when it is a command. */
export type CommandSend =
  | { kind: "panel"; name: string }
  | { kind: "model" }
  | { kind: "message" };

export function commandSend(text: string, commands: readonly SlashCommand[]): CommandSend {
  const call = commandCall(text);
  if (!call || call.args) return { kind: "message" };
  const command = commands.find((candidate) => candidate.name === call.name);
  if (command?.panel) return { kind: "panel", name: call.name };
  // Bare, /model opens a picker in the terminal; the model button is that
  // picker here.
  if (call.name === "model") return { kind: "model" };
  return { kind: "message" };
}

/** A session's "/" menu. */
export async function listCommands(ask: Ask, sessionId: string): Promise<SlashCommand[]> {
  const reply = await ask({ type: "list_commands", sessionId });
  return reply.commands ?? [];
}

/** Runs a panel command on the Mac and resolves with what it showed. A
 *  panel can take a few seconds to settle (/usage scans first). */
export async function runPanel(ask: Ask, sessionId: string, name: string): Promise<CommandPanel> {
  const reply = await ask({ type: "command_panel", sessionId, text: `/${name}` }, 30_000);
  if (!reply.panel) throw new Error("The Mac did not say what the panel showed.");
  return reply.panel;
}
