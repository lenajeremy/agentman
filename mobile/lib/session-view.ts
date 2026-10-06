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

/**
 * A mode as a person would say it: "Planner" for kiro_planner, "Accept
 * edits" for accept-edits. Agents report their own identifiers, which are
 * right to send back but read like code in a header. The identifier is still
 * what a switch sends; this is only how it is shown.
 */
export function modeLabel(mode: string): string {
  const raw = mode.trim();
  const known: Record<string, string> = {
    default: "Default",
    kiro_default: "Default",
    kiro_planner: "Planner",
    kiro_guide: "Guide",
    "accept-edits": "Accept edits",
    "accept edits": "Accept edits",
    acceptedits: "Accept edits",
    bypasspermissions: "Bypass permissions",
  };
  const found = known[raw.toLowerCase()];
  if (found) return found;
  const words = raw.replace(/^kiro_/i, "").replace(/[_-]+/g, " ").trim();
  if (!words) return raw;
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * A model as a person would say it: "Opus 5.5" for claude-opus-5-5,
 * "GPT-6.1 Sol" for gpt-6.1-sol. Names an agent already prints for people,
 * such as "Gemini 3.8 Pro (High)", are left exactly as they are.
 */
export function modelLabel(model: string): string {
  const raw = model.trim();
  const claude = raw.match(/^claude-(opus|sonnet|haiku)-(\d+)(?:[-.](\d+))?(?:-\d{8})?$/i);
  if (claude) {
    const family = claude[1].charAt(0).toUpperCase() + claude[1].slice(1).toLowerCase();
    return claude[3] ? `${family} ${claude[2]}.${claude[3]}` : `${family} ${claude[2]}`;
  }
  const older = raw.match(/^claude-(\d+(?:\.\d+)?)-(opus|sonnet|haiku)(?:-\d{8})?$/i);
  if (older) {
    return `${older[2].charAt(0).toUpperCase()}${older[2].slice(1).toLowerCase()} ${older[1]}`;
  }
  const gpt = raw.match(/^gpt-([\w.]+?)(?:-(.+))?$/i);
  if (gpt) {
    const rest = gpt[2] ? " " + gpt[2].split("-").map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(" ") : "";
    return `GPT-${gpt[1]}${rest}`;
  }
  if (raw === "auto") return "Auto";
  return raw;
}
