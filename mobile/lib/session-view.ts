import type { Message } from "./protocol";

/**
 * The session's mode and context use as one short chip beside the model, or
 * null when the agent reported neither. The mode is the agent's own word,
 * kept as it spelled it; context is whole percent.
 *
 * A session whose modes can be switched always gets the chip, since it is
 * the control that switches them; with no mode reported it reads "mode".
 */
export function statusChip(
  mode: string | undefined,
  contextPercent: number | undefined,
  switchable = false,
): { mode?: string; context?: string; label: string } | null {
  const reported = mode?.trim() || undefined;
  const word = reported ?? (switchable ? "mode" : undefined);
  const context =
    contextPercent && contextPercent > 0
      ? `${Math.min(100, Math.round(contextPercent))}%`
      : undefined;
  if (!word && !context) return null;
  const label = [reported ? `${reported} mode` : word ?? "", context ? `context ${context} full` : ""]
    .filter(Boolean)
    .join(", ");
  return { mode: word, context, label };
}

/**
 * Whether a message earns a row in the feed.
 *
 * An agent that streams its reply can withdraw it: the preview row is
 * rewritten with no text once the real one lands under its own id. An empty
 * assistant row is that withdrawal, not something the agent said, and drawn
 * it is a gap in the conversation with nothing in it.
 */
export function showsInFeed(message: Message): boolean {
  if (message.role !== "assistant" || message.tool) return true;
  return (message.text ?? "").trim().length > 0;
}
