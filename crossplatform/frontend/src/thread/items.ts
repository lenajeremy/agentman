// What a thread shows, as AgentThreadView.swift makes it from the messages:
// runs of tool calls folded into one line each, pictures the agent read
// shown as pictures, and the user's messages split into what went with them.

import type { Message } from "@shared/protocol";

export type ThreadItem =
  | { kind: "message"; id: string; message: Message }
  /** A run of tool calls, with what the agent said just before it. */
  | { kind: "tools"; id: string; calls: Message[]; said: string }
  /** An image the agent read, shown as a thumbnail rather than its path. */
  | { kind: "image"; id: string; message: Message; path: string }
  /** Images read one after another: one run, the pictures side by side. */
  | { kind: "images"; id: string; calls: Message[]; paths: string[]; said: string };

/** The image file a path names, if it is one (ImageThumbnail.imagePath). */
export function imagePath(text: string): string | null {
  return /\/[^\s"'`]+?\.(?:png|jpe?g|gif|webp|heic|bmp|tiff?)/i.exec(text)?.[0] ?? null;
}

export function threadItems(messages: readonly Message[]): ThreadItem[] {
  const items: ThreadItem[] = [];
  let run: Message[] = [];
  let pictures: { message: Message; path: string }[] = [];
  let said = "";
  const flush = () => {
    if (run.length) items.push({ kind: "tools", id: run.at(-1)!.id, calls: run, said });
    run = [];
  };
  const flushPictures = () => {
    if (pictures.length === 1) {
      const only = pictures[0];
      items.push({ kind: "image", id: only.message.id, message: only.message, path: only.path });
    } else if (pictures.length > 1) {
      items.push({
        kind: "images", id: pictures.at(-1)!.message.id, calls: pictures.map((p) => p.message),
        paths: pictures.map((p) => p.path), said,
      });
    }
    pictures = [];
  };
  for (const message of messages) {
    const path = message.role === "tool" && toolKind(message.tool?.name).kind === "read"
      ? imagePath(message.tool?.summary ?? message.text ?? "") : null;
    if (path) {
      flush();
      pictures.push({ message, path });
    } else if (message.role === "tool") {
      flushPictures();
      run.push(message);
    } else {
      flush();
      flushPictures();
      if (message.role === "assistant") said = message.text ?? "";
      items.push({ kind: "message", id: message.id, message });
    }
  }
  flush();
  flushPictures();
  return items;
}

/** The pictures the agent looked at, in order, each once: what the image
 *  viewer's arrows go through. */
export function threadPictures(items: readonly ThreadItem[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of items) {
    const paths = item.kind === "image" ? [item.path] : item.kind === "images" ? item.paths : [];
    for (const path of paths) {
      if (!seen.has(path)) {
        seen.add(path);
        out.push(path);
      }
    }
  }
  return out;
}

export interface ToolKind {
  kind: "command" | "read" | "edit" | "write" | "search" | "web" | "task" | "plan" | "other";
  /** For another tool: its name, or the last word of an MCP tool's. */
  name: string;
}

/** What kind of thing a tool call does, by the agent's name for the tool. */
export function toolKind(raw: string | undefined): ToolKind {
  const name = (raw ?? "").toLowerCase();
  const of = (kind: ToolKind["kind"]): ToolKind => ({ kind, name });
  switch (name) {
    case "bash": case "shell": case "exec_command": case "run_shell_command": case "local_shell": case "terminal": case "command":
      return of("command");
    case "read": case "read_file": case "view": case "cat": case "notebookread":
      return of("read");
    case "edit": case "multiedit": case "apply_patch": case "str_replace": case "str_replace_editor": case "notebookedit": case "replace":
      return of("edit");
    case "write": case "create_file": case "write_file":
      return of("write");
    case "grep": case "glob": case "search": case "find": case "list": case "ls": case "codebase_search": case "file_search":
      return of("search");
    case "webfetch": case "fetch": case "websearch": case "web_search": case "web_fetch":
      return of("web");
    case "task": case "agent": case "subagent":
      return of("task");
    case "todowrite": case "update_plan": case "todo_write":
      return of("plan");
    default:
      if (name.startsWith("mcp__")) return { kind: "other", name: name.split("_").at(-1) || "tool" };
      return { kind: "other", name: name || "tool" };
  }
}

export function toolVerb(kind: ToolKind): string {
  switch (kind.kind) {
    case "command": return "Ran";
    case "read": return "Read";
    case "edit": return "Edited";
    case "write": return "Wrote";
    case "search": return "Searched";
    case "web": return "Fetched";
    case "task": return "Delegated";
    case "plan": return "Planned";
    case "other": return `Used ${kind.name}`;
  }
}

/** The SF Symbol for a kind, and the Lucide icon standing in for it. */
export function toolSymbol(kind: ToolKind): string {
  switch (kind.kind) {
    case "command": return "terminal";
    case "read": return "doc.text";
    case "edit": case "write": return "pencil";
    case "search": return "magnifyingglass";
    case "web": return "globe";
    case "task": return "person.2";
    case "plan": return "checklist";
    case "other": return "wrench";
  }
}

function phrase(kind: ToolKind["kind"], count: number): string {
  const one = count === 1;
  switch (kind) {
    case "command": return one ? "Ran a command" : `Ran ${count} commands`;
    case "read": return one ? "Read a file" : `Read ${count} files`;
    case "edit": return one ? "Edited a file" : `Edited ${count} files`;
    case "write": return one ? "Wrote a file" : `Wrote ${count} files`;
    case "search": return one ? "Searched once" : `Searched ${count} times`;
    case "web": return one ? "Fetched a page" : `Fetched ${count} pages`;
    case "task": return one ? "Delegated a task" : `Delegated ${count} tasks`;
    case "plan": return "Updated the plan";
    case "other": return one ? "Used a tool" : `Used ${count} tools`;
  }
}

/** "Ran 3 commands, read 2 files and searched once": a run in counts, by
 *  kind (other tools of any name count together, as the Mac's do only when
 *  their names match). */
export function runCounts(calls: readonly Message[]): string {
  const order: string[] = [];
  const counts = new Map<string, { kind: ToolKind["kind"]; n: number }>();
  for (const call of calls) {
    const kind = toolKind(call.tool?.name);
    const id = kind.kind === "other" ? `other:${kind.name}` : kind.kind;
    if (!counts.has(id)) order.push(id);
    const entry = counts.get(id) ?? { kind: kind.kind, n: 0 };
    entry.n += 1;
    counts.set(id, entry);
  }
  const phrases = order.map((id) => phrase(counts.get(id)!.kind, counts.get(id)!.n));
  if (!phrases.length) return "Used tools";
  const rest = phrases.slice(1).map((p) => p.charAt(0).toLowerCase() + p.slice(1));
  if (rest.length === 1) return `${phrases[0]} and ${rest[0]}`;
  if (rest.length > 1) return `${phrases[0]}, ${rest.slice(0, -1).join(", ")} and ${rest.at(-1)}`;
  return phrases[0];
}

/** A target made readable: home as ~, an agent's own temporary folder left
 *  out (scratchpad/…, tasks/…), and whitespace on one line. A page doesn't
 *  know the user's home: a user's folder under /Users or /home stands for
 *  it. */
export function tidy(text: string): string {
  let out = text.replace(/\/(?:Users|home)\/[^/\s"'`]+/g, "~");
  out = out.replace(/\/(?:private\/)?tmp\/claude-\d+\/[^/\s]+\/[0-9a-f-]{36}\//g, "");
  return out.split(/[\n\r]+/).join(" ").trim();
}

/** The summary Claude Code writes when it compacts a conversation, which
 *  arrives as a message from the user. */
export function isCompactionSummary(text: string): boolean {
  return text.startsWith("This session is being continued from a previous conversation");
}

/** An image the daemon filed for a message: under its own images folder. */
function isKeptImage(token: string): boolean {
  return token.startsWith("/") && token.includes("/.agentman/images/") && !token.includes("..") &&
    ["png", "jpg", "jpeg", "gif", "webp"].includes(token.split(".").at(-1)?.toLowerCase() ?? "");
}

export interface SentMessage {
  images: string[];
  /** Files sent with it, named ahead of the words as @path: candidates the
   *  view still checks are files. */
  files: string[];
  text: string;
}

/**
 * A message the user sent, split into the images that went with it, the
 * files named ahead of its words, and its words (ImagePreview.swift's
 * SentMessage). `isFile` says whether a path is a file of the user's.
 */
export function sentMessage(message: string, attached: readonly string[] = [], isFile: (path: string) => boolean = () => true): SentMessage {
  const images = [...attached];
  const files: string[] = [];
  let rest = message;
  for (;;) {
    const token = /^\S+/.exec(rest)?.[0];
    if (!token) break;
    if (isKeptImage(token)) {
      if (!attached.includes(token)) images.push(token);
    } else if (token.startsWith("@/") && !token.includes("..") && isFile(token.slice(1))) {
      files.push(token.slice(1));
    } else {
      break;
    }
    rest = rest.slice(token.length).replace(/^\s+/, "");
  }
  return { images, files, text: rest };
}

/** The base name of a path. */
export function baseName(path: string): string {
  return path.replace(/\/+$/, "").split("/").pop() || path;
}
