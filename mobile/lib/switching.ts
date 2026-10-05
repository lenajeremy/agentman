import type { Session } from "./protocol";

export type SwitchKind = "mode" | "model";

/**
 * What the phone may switch a session to, and what it is on now, or null
 * when the agent offers nothing. A model is offered only with a scope: the
 * agent has to say whether a switch touches this session alone or its
 * default too, or the phone could not tell anyone what they are agreeing to.
 */
export function switchChoices(
  session: Pick<Session, "mode" | "modes" | "model" | "models" | "modelScope">,
  kind: SwitchKind,
): { values: string[]; current?: string } | null {
  if (kind === "mode") {
    return session.modes && session.modes.length > 0
      ? { values: session.modes, current: session.mode }
      : null;
  }
  return session.models && session.models.length > 0 && session.modelScope
    ? { values: session.models, current: session.model }
    : null;
}

/**
 * The sentence a model switch that also changes the Mac's default is
 * confirmed with, naming the agent, or null when the switch touches this
 * session alone and needs no confirmation.
 */
export function defaultScopeWarning(
  session: Pick<Session, "modelScope">,
  kind: SwitchKind,
  agent: string,
  value: string,
): string | null {
  if (kind !== "model" || session.modelScope !== "default") return null;
  return `${value} also becomes the default model for new ${agent} sessions on your Mac.`;
}

/** A failed switch, in words about the switch rather than the transport. */
export function switchFailure(kind: SwitchKind, value: string, error: string): string {
  const reason = error.replace(/^(daemon|source): /, "").trim();
  return `Couldn’t switch the ${kind} to ${value}${reason ? `: ${reason}` : "."}`;
}
