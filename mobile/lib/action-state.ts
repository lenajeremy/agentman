import type { Message, Question, SendStatus, Session } from "./protocol";

export type AgentActionKind = "answer" | "interrupt" | "send_queued";

/** A non-message command whose result needs to stay visible in the UI. */
export interface PendingAgentAction {
  clientId: string;
  sessionId: string;
  kind: AgentActionKind;
  status: "sending" | SendStatus;
  error?: string;
  /** Answers belong to one exact question. A later question must start clean. */
  questionIdentity?: string;
}

interface PendingSendLike {
  clientId: string;
  sessionId: string;
  status: "sending" | SendStatus;
  error?: string;
  text?: string;
  retainUntilTranscript?: boolean;
  knownUserMessageIds?: string[];
}

interface SendResult {
  clientId?: string;
  status?: SendStatus;
  error?: string;
}

/**
 * Stable identity for deciding whether an answer result still belongs on screen.
 * Terminal focus and checkbox marks are deliberately excluded: those can change
 * while one multi-select answer is being driven through the CLI.
 */
export function questionIdentity(question: Question): string {
  return JSON.stringify([
    question.id ?? null,
    question.title ?? null,
    question.prompt,
    question.detail ?? null,
    question.multiple ?? false,
    question.custom ?? false,
    question.options.map((option) => [
      option.key,
      option.label,
      option.description ?? null,
    ]),
  ]);
}

/** Identity of the complete terminal snapshot, including focus and checkmarks. */
export function questionSnapshotIdentity(question: Question): string {
  return JSON.stringify([
    questionIdentity(question),
    question.options.map((option) => [
      option.key,
      option.preview ?? null,
      option.selected ?? false,
      option.checked ?? false,
    ]),
  ]);
}

/** Cursor's transcript can lag behind the terminal acknowledgement. */
export function applyPendingSendResult<T extends PendingSendLike>(
  pending: readonly T[],
  result: SendResult,
): T[] {
  if (!result.clientId) return pending as T[];
  if (result.status === "delivered") {
    return pending.flatMap((item) =>
      item.clientId !== result.clientId
        ? [item]
        : item.retainUntilTranscript
          ? [{ ...item, status: "delivered" as const }]
          : [],
    );
  }
  return pending.map((item) =>
    item.clientId === result.clientId
      ? { ...item, status: result.status ?? "failed", error: result.error }
      : item,
  );
}

/** Replace a Cursor send echo only when its actual user row has arrived. */
export function reconcileCursorSendEchoes<T extends PendingSendLike>(
  pending: readonly T[],
  sessionId: string,
  messages: readonly Message[],
): T[] {
  const userRows = messages.filter(
    (message) => message.sessionId === sessionId && message.role === "user",
  );
  const claimed = new Set<string>();
  return pending.filter((item) => {
    if (item.sessionId !== sessionId || !item.retainUntilTranscript ||
        item.status === "failed") return true;
    // An image-only send has no text to match by: Cursor records its row with
    // the image paths taken out. Once delivered, the next new row from the
    // user is that message. Before then a new row could be something typed at
    // the terminal, so nothing is claimed.
    if (!item.text && item.status !== "delivered") return true;
    const row = userRows.find((message) =>
      (!item.text || message.text === item.text) &&
      !item.knownUserMessageIds?.includes(message.id) &&
      !claimed.has(message.id),
    );
    if (!row) return true;
    claimed.add(row.id);
    return item.status !== "delivered";
  });
}

/**
 * Hand a message sent while Claude was working over to Claude's own queue.
 *
 * Claude Code holds such a message until its current step ends, so the
 * transcript has nothing to show for it yet, and the phone used to drop the
 * bubble the moment the keys were typed. A message sent while it works is
 * kept instead (see `retainUntilTranscript`), until one of these:
 *
 * - The Mac lists it in `session.queued`. The queued row shows it from then
 *   on, with the same label, so the bubble never leaves the screen.
 * - The session stops working: the queue has been read, and the message is
 *   in the transcript or about to be.
 *
 * The transcript arriving first is handled by reconcileCursorSendEchoes.
 */
export function settleQueuedEchoes<T extends PendingSendLike>(
  pending: readonly T[],
  sessions: readonly Session[],
): T[] {
  const byId = new Map(sessions.map((session) => [session.id, session]));
  return pending.filter((item) => {
    if (!item.retainUntilTranscript || item.status !== "delivered") return true;
    const session = byId.get(item.sessionId);
    if (!session || session.kind !== "claude") return true;
    if (isQueued(session, item.text)) return false;
    return session.state === "busy" || session.state === "waiting_input";
  });
}

/** Whether the Mac lists this text among the session's waiting messages. The
 *  agent may store it with its images' references added, so containing the
 *  sent text is enough. */
export function isQueued(session: Pick<Session, "queued">, text: string | undefined): boolean {
  const sent = text?.trim();
  if (!sent) return false;
  return (session.queued ?? []).some((queued) => queued.trim().includes(sent));
}

/** Apply the daemon's acknowledgement to an answer or interrupt command. */
export function applyAgentActionResult(
  actions: readonly PendingAgentAction[],
  result: SendResult,
): PendingAgentAction[] {
  if (!result.clientId) return actions as PendingAgentAction[];
  return actions.map((action) =>
    action.clientId === result.clientId
      ? { ...action, status: result.status ?? "failed", error: result.error }
      : action,
  );
}

/** Replace an earlier attempt of the same kind for the same session. */
export function upsertAgentAction(
  actions: readonly PendingAgentAction[],
  next: PendingAgentAction,
): PendingAgentAction[] {
  return [
    ...actions.filter(
      (action) => action.sessionId !== next.sessionId || action.kind !== next.kind,
    ),
    next,
  ];
}

/**
 * Drop operations that no longer match live agent state.
 *
 * A delivered answer stays visible until discovery proves the question moved
 * on. A failed answer stays retryable only while that same question remains.
 * Interrupt feedback stays while the interrupted turn is still busy.
 */
export function reconcileAgentActions(
  actions: readonly PendingAgentAction[],
  sessions: readonly Session[],
): PendingAgentAction[] {
  const live = new Map(sessions.map((session) => [session.id, session]));
  return actions.filter((action) => {
    const session = live.get(action.sessionId);
    if (!session) return false;
    if (action.kind === "interrupt") return session.state === "busy";
    // Done once the agent has nothing left waiting: it read them. A failure
    // stays until the next attempt replaces it, so it can be said.
    if (action.kind === "send_queued") {
      return action.status === "failed" || Boolean(session.queued?.length);
    }
    return Boolean(
      session.question &&
        action.questionIdentity === questionIdentity(session.question),
    );
  });
}

/** A queued hook message has been handed off when that session's turn ends. */
export function clearQueuedForTurn<T extends PendingSendLike>(
  pending: readonly T[],
  sessionId?: string,
): T[] {
  if (!sessionId) return pending as T[];
  return pending.filter(
    (item) => item.sessionId !== sessionId || item.status !== "queued",
  );
}

/**
 * Covers the polling completion path and reconnection after a missed event.
 * A busy-to-idle/ended transition is the same boundary as turn_complete.
 */
export function clearQueuedForSessionTransitions<T extends PendingSendLike>(
  pending: readonly T[],
  previous: readonly Session[],
  next: readonly Session[],
): T[] {
  const before = new Map(previous.map((session) => [session.id, session.state]));
  const completed = new Set(
    next
      .filter(
        (session) =>
          before.get(session.id) === "busy" &&
          (session.state === "idle" || session.state === "ended"),
      )
      .map((session) => session.id),
  );
  return pending.filter(
    (item) =>
      !(item.status === "queued" && completed.has(item.sessionId)),
  );
}
