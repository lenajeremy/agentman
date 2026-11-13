// The summary Claude Code writes when it compacts a conversation, which
// arrives as a message from the user (AgentThreadView.swift's
// MessageRow.isCompactionSummary), folded in the thread.

const opening = "This session is being continued from a previous conversation";

/** Newer Claude Code puts a note or two before it, opening with a tag such
 *  as <artifact-content-authored-by-others/>. */
export function isCompactionSummary(text: string): boolean {
  if (text.startsWith(opening)) return true;
  return text.startsWith("<") && text.slice(0, 2000).includes("\n" + opening);
}
