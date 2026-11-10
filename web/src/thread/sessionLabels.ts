// Modes, models and agents as a person would say them (Design/SessionLabels.swift):
// "Accept edits" for accept-edits, "Opus 5.5" for claude-opus-5-5.

export function modeLabel(mode: string): string {
  const raw = mode.trim();
  const known: Record<string, string> = {
    default: "Default", kiro_default: "Default", kiro_planner: "Planner", kiro_guide: "Guide",
    "accept-edits": "Accept edits", "accept edits": "Accept edits", acceptedits: "Accept edits",
    bypasspermissions: "Bypass permissions",
  };
  const found = known[raw.toLowerCase()];
  if (found) return found;
  let words = raw;
  if (words.toLowerCase().startsWith("kiro_")) words = words.slice(5);
  words = words.replace(/[_-]+/g, " ").trim();
  if (!words) return raw;
  return words.charAt(0).toUpperCase() + words.slice(1);
}

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1).toLowerCase();

export function modelLabel(model: string): string {
  const raw = model.trim();
  let m = /^claude-(opus|sonnet|haiku)-(\d+)(?:[-.](\d+))?(?:-\d{8})?$/i.exec(raw);
  if (m) return m[3] ? `${cap(m[1])} ${m[2]}.${m[3]}` : `${cap(m[1])} ${m[2]}`;
  m = /^claude-(\d+(?:\.\d+)?)-(opus|sonnet|haiku)(?:-\d{8})?$/i.exec(raw);
  if (m) return `${cap(m[2])} ${m[1]}`;
  m = /^gpt-([\w.]+?)(?:-(.+))?$/i.exec(raw);
  if (m) {
    const rest = m[2] ? " " + m[2].split("-").map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(" ") : "";
    return `GPT-${m[1]}${rest}`;
  }
  if (raw === "auto") return "Auto";
  return raw;
}

/** An agent's product name, by its kind: "Claude Code" for claude. */
export function agentProduct(kind: string): string {
  switch (kind) {
    case "claude": return "Claude Code";
    case "codex": return "Codex";
    case "opencode": return "OpenCode";
    case "cursor": return "Cursor";
    case "cursor-cli": return "Cursor CLI";
    case "kiro": return "Kiro";
    case "antigravity": return "Antigravity";
    default: return kind.split(/\b/).map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join("");
  }
}
