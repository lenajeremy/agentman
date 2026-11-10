// Which notifications the user wants (App/NotificationPolicy.swift): a
// switch per kind, muted folders, quiet hours. Owned by the shell area; the
// Agentman area asks it before posting Agentman's own. A stub until then.

export type NotificationKind = "finished" | "needsYou" | "agentman" | "noteSuggestion";

export function shouldNotify(kind: NotificationKind, folder?: string): boolean {
  void kind;
  void folder;
  return true;
}
