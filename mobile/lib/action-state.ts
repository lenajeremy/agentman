import type { Message, Question, SendStatus, Session } from "./protocol";

export type AgentActionKind = "answer" | "interrupt";

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
        item.status === "failed" || !item.text) return true;
    const row = userRows.find((message) =>
      message.text === item.text &&
      !item.knownUserMessageIds?.includes(message.id) &&
      !claimed.has(message.id),
    );
    if (!row) return true;
    claimed.add(row.id);
    return item.status !== "delivered";
  });
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
