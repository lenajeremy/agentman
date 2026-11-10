// What the message box offers as it is typed in (Design/FileMentions.swift,
// SlashCommands.swift): the folder's files after @, and the agent's
// commands after / at the start.

import { useCallback, useMemo, useRef, useState } from "react";
import type { Session, SlashCommand } from "@shared/protocol";
import { daemon } from "../daemon";
import { readLocalFile } from "./state";

const SPACE = /\s/;

export interface Mentions {
  query: string | null;
  results: string[];
  selected: number;
  isOpen: boolean;
  setSelected: (index: number) => void;
  update: (display: string, caret: number | null) => void;
  move: (step: number) => void;
  /** The pick, and where it goes in the display text. */
  pick: (index?: number) => { path: string; start: number; end: number } | null;
  dismiss: () => void;
  isFile: (path: string) => boolean;
}

/** `@` and a few letters of a file's name: the folder's files that match,
 *  eight at most; before anything is typed, the files changed. */
export function useMentions(session: Session): Mentions {
  const [query, setQuery] = useState<string | null>(null);
  const [results, setResults] = useState<string[]>([]);
  const [selected, setSelected] = useState(0);
  const [known, setKnown] = useState<Map<string, boolean>>(() => new Map());
  const range = useRef({ start: -1, end: -1 });
  const asked = useRef(0);
  const checking = useRef(new Set<string>());

  const remember = useCallback((paths: string[], is: boolean) => {
    setKnown((now) => {
      if (paths.every((p) => now.get(p) === is)) return now;
      const next = new Map(now);
      for (const p of paths) next.set(p, is);
      return next;
    });
  }, []);

  const isFile = useCallback(
    (path: string) => {
      const found = known.get(path);
      if (found !== undefined) return found;
      if (!checking.current.has(path) && !path.includes("..")) {
        checking.current.add(path);
        void readLocalFile(`${session.cwd.replace(/\/+$/, "")}/${path}`, 0).then((file) => remember([path], !!file));
      }
      return false;
    },
    [known, remember, session.cwd],
  );

  const dismiss = useCallback(() => {
    setQuery(null);
    setResults([]);
    setSelected(0);
    range.current = { start: -1, end: -1 };
  }, []);

  const update = useCallback(
    (text: string, caret: number | null) => {
      if (caret === null || caret > text.length || (caret < text.length && !SPACE.test(text[caret]))) return dismiss();
      let start = caret;
      while (start > 0 && !SPACE.test(text[start - 1])) start -= 1;
      const word = text.slice(start, caret);
      if (!word.startsWith("@") || word.slice(1).includes("@") || word.includes("￼")) return dismiss();
      const typed = word.slice(1);
      // A chip already made isn't asked about again.
      if (typed && known.get(typed)) return dismiss();
      range.current = { start, end: caret };
      setQuery((was) => {
        if (was !== typed) setSelected(0);
        return typed;
      });
      const ask = ++asked.current;
      daemon
        .request({ type: "find_files", sessionId: session.id, text: typed })
        .then((reply) => {
          if (ask !== asked.current) return;
          const files = (reply.files ?? []).slice(0, 8);
          remember(files, true);
          setResults(files);
          setSelected((s) => Math.min(s, Math.max(files.length - 1, 0)));
        })
        .catch(() => {});
    },
    [dismiss, known, remember, session.id],
  );

  return useMemo(() => ({
    query, results, selected, isOpen: query !== null && results.length > 0,
    setSelected, update, dismiss, isFile,
    move: (step: number) => {
      if (results.length) setSelected((s) => (s + step + results.length) % results.length);
    },
    pick: (index?: number) => {
      const i = index ?? selected;
      const path = results[i];
      const { start, end } = range.current;
      dismiss();
      return path !== undefined && start >= 0 ? { path, start, end } : null;
    },
  }), [query, results, selected, update, dismiss, isFile]);
}

/** Commands are typed into the agent's prompt, so only a Claude, Codex or
 *  Antigravity the Mac started in a terminal it can type into takes them. */
export function takesCommands(session: Session): boolean {
  return ["claude", "codex", "antigravity"].includes(session.kind) && session.inject === "tmux";
}

export const commandLabel = (c: SlashCommand) => (c.prefix ?? "/") + c.name;

/** Best first: the name itself, then names that start with what is typed,
 *  then a word within a name that does, then any that contain it. Built-in
 *  commands lead within each. "$" lists only what takes it. */
export function matchCommands(commands: SlashCommand[], query: string, prefix = "/"): SlashCommand[] {
  const squash = (t: string) => t.toLowerCase().replace(/[:_\- ]/g, "");
  const typed = query.toLowerCase();
  const squashed = squash(query);
  const rank = (c: SlashCommand): number | null => {
    const name = c.name.toLowerCase();
    if (!typed) return 3;
    if (name === typed) return 0;
    if (name.startsWith(typed) || squash(name).startsWith(squashed)) return 1;
    if (name.split(/[:_-]/).some((w) => w.startsWith(typed))) return 2;
    if (squashed && squash(name).includes(squashed)) return 3;
    return null;
  };
  return commands
    .filter((c) => prefix === "/" || c.prefix === prefix)
    .map((c) => [c, rank(c)] as const)
    .filter((p): p is readonly [SlashCommand, number] => p[1] !== null)
    .sort((a, b) => {
      if (a[1] !== b[1]) return a[1] - b[1];
      const ba = a[0].source === "builtin", bb = b[0].source === "builtin";
      if (ba !== bb) return ba ? -1 : 1;
      return a[0].name < b[0].name ? -1 : a[0].name > b[0].name ? 1 : 0;
    })
    .map((p) => p[0]);
}

export type CommandSend = { kind: "panel"; name: string } | { kind: "model" } | { kind: "message" };

/** What sending does when the message is a bare command. */
export function commandSend(text: string, commands: SlashCommand[]): CommandSend {
  const trimmed = text.trim();
  if (!trimmed.startsWith("/") || trimmed.length < 2 || /\s/.test(trimmed)) return { kind: "message" };
  const name = trimmed.slice(1);
  if (commands.find((c) => c.name === name)?.panel) return { kind: "panel", name };
  if (name === "model") return { kind: "model" };
  return { kind: "message" };
}

export interface Slash {
  commands: SlashCommand[];
  query: string | null;
  results: SlashCommand[];
  selected: number;
  problem: string | null;
  isOpen: boolean;
  setSelected: (i: number) => void;
  update: (text: string) => void;
  move: (step: number) => void;
  pick: (index?: number) => string | null;
  dismiss: () => void;
}

/** `/` and a few letters at the start of the box: the agent's commands. */
export function useSlash(session: Session): Slash {
  const [commands, setCommands] = useState<SlashCommand[]>([]);
  const [query, setQuery] = useState<string | null>(null);
  const [prefix, setPrefix] = useState("/");
  const [selected, setSelected] = useState(0);
  const [problem, setProblem] = useState<string | null>(null);
  const askedAt = useRef<number | null>(null);
  const triggers = session.kind === "codex" ? ["/", "$"] : ["/"];
  const takes = takesCommands(session);

  const results = useMemo(() => (query === null ? [] : matchCommands(commands, query, prefix).slice(0, 60)), [commands, query, prefix]);

  const dismiss = useCallback(() => {
    setQuery(null);
    setSelected(0);
  }, []);

  const update = useCallback(
    (text: string) => {
      const first = text.charAt(0);
      if (!takes || !triggers.includes(first) || /\s/.test(text)) return dismiss();
      setPrefix(first);
      setQuery(text.slice(1));
      // Asked for when first needed, and again after a minute, as skills
      // come and go.
      if (askedAt.current === null || Date.now() - askedAt.current > 60_000) {
        askedAt.current = Date.now();
        daemon
          .request({ type: "list_commands", sessionId: session.id })
          .then((reply) => {
            if (reply.commands) {
              setCommands(reply.commands);
              setProblem(null);
            } else {
              askedAt.current = null;
              const message = (reply.error ?? "The commands couldn't be listed.").replace(/^(daemon|source|tmux): /, "");
              setProblem(message.includes("unsupported request") ? "Update agentman on your Mac to use commands here." : message);
            }
          })
          .catch((err: Error) => {
            askedAt.current = null;
            setProblem(err.message);
          });
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [takes, session.id, session.kind, dismiss],
  );

  return {
    commands, query, results, selected, problem,
    isOpen: query !== null && (results.length > 0 || problem !== null),
    setSelected, update, dismiss,
    move: (step) => {
      if (results.length) setSelected((s) => (s + step + results.length) % results.length);
    },
    pick: (index) => {
      const command = results[index ?? selected];
      dismiss();
      if (!command) return null;
      // A skill is followed by what it is for.
      return commandLabel(command) + (command.argumentHint || command.prefix === "$" ? " " : "");
    },
  };
}
