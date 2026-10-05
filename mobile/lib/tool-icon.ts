import type Feather from "@expo/vector-icons/Feather";
import type { ComponentProps } from "react";

export type FeatherName = ComponentProps<typeof Feather>["name"];

/**
 * A glyph for the kind of work a tool does. Names come straight from each
 * CLI (Claude's "Read", Codex's "shell", OpenCode's "webfetch"), so this
 * matches on meaning rather than exact spelling, and anything unknown gets a
 * neutral mark instead of a wrong one.
 *
 * Order matters, because names combine words. "TodoWrite" is a list, not an
 * edit, so todo and plan are tried before write. Asking is matched as a word
 * of its own, because "ask" also sits inside "task", and a subagent is not a
 * question.
 */
export function toolIcon(name: string): FeatherName {
  const n = name.toLowerCase();
  if (/(bash|shell|exec|command|terminal)/.test(n)) return "terminal";
  if (/(question|(^|[\s_.-])ask($|[\s_.-]|user))/.test(n)) return "help-circle";
  if (/artifact/.test(n)) return "file-text";
  if (/image/.test(n)) return "image";
  if (/(todo|plan)/.test(n)) return "check-square";
  if (/(delete|remove|trash)/.test(n)) return "trash-2";
  if (/(edit|write|patch|notebook)/.test(n)) return "edit-3";
  if (/(read|view|cat|open)/.test(n)) return "file-text";
  if (/(grep|glob|search|find|list|ls)/.test(n)) return "search";
  if (/(web|fetch|http|url|browse)/.test(n)) return "globe";
  if (/(task|agent)/.test(n)) return "git-branch";
  return "tool";
}
