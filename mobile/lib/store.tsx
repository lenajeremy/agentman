import * as Haptics from "expo-haptics";
import * as Notifications from "expo-notifications";
import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { AppState, Platform } from "react-native";

import {
  Client,
  ConnectionState,
  Credentials,
  clearCredentials,
  loadCredentials,
} from "./client";
import {
  applyAgentActionResult,
  applyPendingSendResult,
  clearQueuedForSessionTransitions,
  clearQueuedForTurn,
  questionIdentity,
  reconcileAgentActions,
  upsertAgentAction,
  type PendingAgentAction,
} from "./action-state";
import {
  CATCH_UP_PAGE,
  needsAnotherPage,
  newestTimestamp,
  type CatchUpState,
} from "./catchup";
import {
  canDismiss,
  dismiss as recordDismissal,
  isHidden,
  loadDismissals,
  prune,
  saveDismissals,
  type Dismissals,
} from "./dismissed";
import { draftNamespace } from "./draft-policy";
import { isPushActive, obtainPushToken, setPushActive } from "./push";
import { clearDraft } from "./drafts";
import { newFrameId } from "./id";
import {
  DaemonEvent,
  Message,
  Page,
  QuestionAnswer,
  SendStatus,
  Session,
} from "./protocol";
import {
  reconcileQuestionAlerts,
  sessionNeedsAnswer,
  updateQuestionAlerts,
} from "./question-alerts";
import {
  MAX_RETAINED_MESSAGES,
  mergeRetainedMessages,
} from "./retention";

/** A message the user sent that has not been confirmed yet. */
export interface PendingSend {
  clientId: string;
  sessionId: string;
  text: string;
  status: "sending" | SendStatus;
  error?: string;
  /** Account scope whose persisted composer this send will settle. */
  draftScope?: string;
}

interface PageState {
  cursor?: string;
  hasMore: boolean;
  loading: boolean;
  /** The phone stopped at its bounded cache window; older rows remain on Mac. */
  retentionLimited?: boolean;
}

interface Store {
  ready: boolean;
  credentials: Credentials | null;
  connection: ConnectionState;
  /** Whether the Mac itself is reachable, which is distinct from the relay. */
  daemonOnline: boolean;
  lastSeenAt: number | null;
  sessions: Session[];
  /** What the status board shows: sessions minus the ones swiped away, which
   *  stay hidden until they are triggered again. Deliberately separate from
   *  `sessions` — a hidden session must still be openable by id, or a
   *  notification for one would lead to a screen that cannot load. */
  visibleSessions: Session[];
  messages: Record<string, Message[]>;
  pageState: Record<string, PageState>;
  pending: PendingSend[];
  actions: PendingAgentAction[];

  signIn(creds: Credentials): void;
  signOut(): Promise<void>;
  refresh(): void;
  openSession(sessionId: string): void;
  closeSession(sessionId: string): void;
  loadOlder(sessionId: string): void;
  sendMessage(sessionId: string, text: string): string;
  interruptSession(sessionId: string): void;
  answerQuestion(sessionId: string, answer: QuestionAnswer): void;
  dismissPending(clientId: string): void;
  /** Hide an idle session until it does something again. */
  dismissSession(sessionId: string): void;
  /** Undo that. */
  restoreSession(sessionId: string): void;
  /** Bring back everything hidden. Without a way out, hiding a row would be a
   *  one-way door: a session stays invisible until the agent happens to act,
   *  which for a finished session may be never. */
  restoreAllSessions(): void;
}

const StoreContext = createContext<Store | null>(null);

export function useStore(): Store {
  const store = useContext(StoreContext);
  if (!store) throw new Error("useStore must be used inside <StoreProvider>");
  return store;
}

/** Clear drafts only for sends proven to have left pending state successfully. */
function clearSettledDrafts(before: PendingSend[], after: PendingSend[]): void {
  const remaining = new Set(after.map((item) => item.clientId));
  for (const item of before) {
    if (item.status !== "queued" || remaining.has(item.clientId) || !item.draftScope) {
      continue;
    }
    void clearDraft(item.draftScope, item.sessionId);
  }
}

function notifyQuestion(session: Session) {
  if (Platform.OS === "web") return;
  // Once the Mac has a push token it sends these itself, and it can reach a
  // suspended app that this cannot. Scheduling here too would double every
  // alert.
  if (isPushActive()) return;
  void Notifications.scheduleNotificationAsync({
    content: {
      title: `${session.name || session.kind} needs your answer`,
      body: session.question?.prompt || "The agent is waiting for your input.",
      sound: true,
      data: { sessionId: session.id, kind: "question" },
    },
    trigger: null,
  }).catch(() => {});
  void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Warning).catch(() => {});
}

export function StoreProvider({ children }: { children: React.ReactNode }) {
  const [ready, setReady] = useState(false);
  const [credentials, setCredentials] = useState<Credentials | null>(null);
  const [connection, setConnection] = useState<ConnectionState>("connecting");
  const [daemonOnline, setDaemonOnline] = useState(false);
  const [lastSeenAt, setLastSeenAt] = useState<number | null>(null);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [messages, setMessages] = useState<Record<string, Message[]>>({});
  const [pageState, setPageState] = useState<Store["pageState"]>({});
  const [pending, setPending] = useState<PendingSend[]>([]);
  const [actions, setActions] = useState<PendingAgentAction[]>([]);
  const [dismissals, setDismissals] = useState<Dismissals>({});
  const sessionsRef = useRef<Session[]>([]);
  const messagesRef = useRef<Record<string, Message[]>>({});

  const clientRef = useRef<Client | null>(null);
  /** Maps a fetch_messages frame id to the session that asked, so a page can be
   *  filed correctly when several sessions are paging at once. */
  const pageRequests = useRef(new Map<string, string>());
  /** Fetches that are filling the gap left by navigating away, kept apart from
   *  scroll-back requests. Both ask for a page, but a catch-up page comes from
   *  the newest end of the feed, so letting it set the scroll cursor would make
   *  "load older" jump over everything in between. */
  const catchUpRequests = useRef(new Map<string, CatchUpState>());
  /** Navigation can beat secure credential loading on a notification cold start.
   *  Keep intent outside the socket so attach() can fulfill it later. */
  const desiredSubscriptions = useRef(new Set<string>());
  const pendingInitialLoads = useRef(new Set<string>());
  const pendingCatchUps = useRef(new Map<string, CatchUpState>());
  /** Sessions whose current blocked period has already produced an alert. */
  const announcedQuestions = useRef(new Set<string>());

  const mergeSessionMessages = useCallback((sessionId: string, incoming: Message[]) => {
    const retained = mergeRetainedMessages(
      messagesRef.current[sessionId] ?? [],
      incoming,
    );
    messagesRef.current = {
      ...messagesRef.current,
      [sessionId]: retained.messages,
    };
    setMessages(messagesRef.current);
    return retained;
  }, []);

  const handleEvent = useCallback((event: DaemonEvent, replyTo?: string) => {
    switch (event.type) {
      case "sessions": {
        const list = event.sessions ?? [];
        const previous = sessionsRef.current;
        const liveIds = new Set(list.map((session) => session.id));
        const removedIds = Object.keys(messagesRef.current).filter((id) => !liveIds.has(id));
        if (removedIds.length > 0) {
          const nextMessages = { ...messagesRef.current };
          for (const id of removedIds) {
            delete nextMessages[id];
            desiredSubscriptions.current.delete(id);
            pendingInitialLoads.current.delete(id);
            pendingCatchUps.current.delete(id);
            clientRef.current?.forgetSession(id);
          }
          for (const [requestId, sessionId] of pageRequests.current) {
            if (!liveIds.has(sessionId)) pageRequests.current.delete(requestId);
          }
          for (const [requestId, state] of catchUpRequests.current) {
            if (!liveIds.has(state.sessionId)) catchUpRequests.current.delete(requestId);
          }
          messagesRef.current = nextMessages;
          setMessages(nextMessages);
          setPageState((current) => {
            const nextPages = { ...current };
            for (const id of removedIds) delete nextPages[id];
            return nextPages;
          });
        }
        const alerts = reconcileQuestionAlerts(announcedQuestions.current, list);
        announcedQuestions.current = alerts.announced;
        for (const session of alerts.newlyPending) notifyQuestion(session);
        setPending((current) => {
          const next = clearQueuedForSessionTransitions(current, previous, list);
          clearSettledDrafts(current, next);
          return next;
        });
        setActions((current) => reconcileAgentActions(current, list));
        sessionsRef.current = list;
        setSessions(list);
        // A full list from a connected daemon is the only safe moment to prune:
        // doing it from a session_update would judge every other dismissal
        // against a list of one, and drop them all.
        setDismissals((current) => {
          const next = prune(list, current);
          if (Object.keys(next).length !== Object.keys(current).length) {
            void saveDismissals(next);
          }
          return next;
        });
        break;
      }

      case "session_update": {
        const updated = event.session;
        if (!updated) break;
        const alert = updateQuestionAlerts(announcedQuestions.current, updated);
        announcedQuestions.current = alert.announced;
        if (alert.newlyPending) notifyQuestion(alert.newlyPending);
        const previous = sessionsRef.current;
        const currentIndex = previous.findIndex((session) => session.id === updated.id);
        const next = currentIndex < 0
          ? [...previous, updated]
          : previous.map((session, index) =>
              index === currentIndex ? updated : session,
            );
        setPending((current) => {
          const nextPending = clearQueuedForSessionTransitions(current, previous, next);
          clearSettledDrafts(current, nextPending);
          return nextPending;
        });
        setActions((current) => reconcileAgentActions(current, next));
        sessionsRef.current = next;
        setSessions((current) => {
          const index = current.findIndex((s) => s.id === updated.id);
          if (index === -1) return [...current, updated];
          const next = [...current];
          next[index] = updated;
          return next;
        });
        break;
      }

      case "session_gone": {
        if (event.sessionId) announcedQuestions.current.delete(event.sessionId);
        const previous = sessionsRef.current;
        const next = previous.filter((session) => session.id !== event.sessionId);
        setPending((current) =>
          current.filter((item) => item.sessionId !== event.sessionId),
        );
        setActions((current) => reconcileAgentActions(current, next));
        if (event.sessionId) {
          const nextMessages = { ...messagesRef.current };
          delete nextMessages[event.sessionId];
          messagesRef.current = nextMessages;
          setMessages(nextMessages);
          setPageState((current) => {
            const nextPages = { ...current };
            delete nextPages[event.sessionId!];
            return nextPages;
          });
          for (const [requestId, sessionId] of pageRequests.current) {
            if (sessionId === event.sessionId) pageRequests.current.delete(requestId);
          }
          for (const [requestId, state] of catchUpRequests.current) {
            if (state.sessionId === event.sessionId) catchUpRequests.current.delete(requestId);
          }
          pendingInitialLoads.current.delete(event.sessionId);
          pendingCatchUps.current.delete(event.sessionId);
          desiredSubscriptions.current.delete(event.sessionId);
          clientRef.current?.forgetSession(event.sessionId);
        }
        sessionsRef.current = next;
        setSessions((current) => current.filter((s) => s.id !== event.sessionId));
        break;
      }

      case "messages": {
        const sessionId = event.sessionId;
        if (!sessionId || !event.messages) break;
        if (
          !desiredSubscriptions.current.has(sessionId) &&
          !sessionsRef.current.some((session) => session.id === sessionId)
        ) break;
        const retained = mergeSessionMessages(sessionId, event.messages);
        if (retained.limited || retained.messages.length >= MAX_RETAINED_MESSAGES) {
          setPageState((current) => {
            const state = current[sessionId];
            if (!retained.limited && !state?.hasMore) return current;
            return {
              ...current,
              [sessionId]: {
                ...state,
                hasMore: false,
                loading: false,
                retentionLimited: true,
              },
            };
          });
        }
        break;
      }

      case "page": {
        const page = event.page as Page | undefined;
        if (!page) break;

        // A catch-up page: merge it, and keep walking back until it overlaps
        // what we already had. Anything else would leave a silent hole.
        const catching = replyTo ? catchUpRequests.current.get(replyTo) : undefined;
        const requestedSession = replyTo
          ? pageRequests.current.get(replyTo)
          : undefined;
        // A removed session can still have a page goroutine completing on the
        // daemon. Its correlation was deleted during cleanup; do not recreate
        // the transcript cache from that late response.
        if (replyTo && !catching && !requestedSession) break;
        if (catching) {
          catchUpRequests.current.delete(replyTo!);
          const sessionId = page.sessionId || catching.sessionId;
          const retained = mergeSessionMessages(sessionId, page.messages);
          if (
            retained.limited ||
            (retained.messages.length >= MAX_RETAINED_MESSAGES && page.hasMore)
          ) {
            setPageState((current) => ({
              ...current,
              [sessionId]: {
                ...current[sessionId],
                hasMore: false,
                loading: false,
                retentionLimited: true,
              },
            }));
          }

          if (needsAnotherPage(page.messages, page.hasMore, catching)) {
            const id = clientRef.current?.send({
              type: "fetch_messages",
              sessionId,
              before: page.nextCursor,
              limit: CATCH_UP_PAGE,
            });
            if (id) {
              catchUpRequests.current.set(id, { ...catching, depth: catching.depth + 1 });
            }
          }
          break;
        }

        const sessionId = page.sessionId || requestedSession;
        if (!sessionId) break;
        if (replyTo) pageRequests.current.delete(replyTo);

        const retained = mergeSessionMessages(sessionId, page.messages);
        const retentionLimited = retained.limited ||
          (retained.messages.length >= MAX_RETAINED_MESSAGES && page.hasMore);
        setPageState((current) => ({
          ...current,
          [sessionId]: {
            cursor: page.nextCursor,
            hasMore: retentionLimited ? false : page.hasMore,
            loading: false,
            retentionLimited: current[sessionId]?.retentionLimited || retentionLimited,
          },
        }));
        break;
      }

      case "turn_complete": {
        setPending((pendingSends) => {
          const next = clearQueuedForTurn(pendingSends, event.sessionId);
          clearSettledDrafts(pendingSends, next);
          return next;
        });
        const current = sessionsRef.current.find((session) => session.id === event.sessionId);
        if (current && sessionNeedsAnswer(current)) break;
        // The whole point of the product: tell the user their agent finished.
        // Guarded because neither module exists on web, where an unhandled
        // rejection here takes the whole screen down.
        if (Platform.OS !== "web" && !isPushActive()) {
          void Notifications.scheduleNotificationAsync({
            content: {
              title: `${event.sessionName ?? "Agent"} finished`,
              body: event.preview || "Tap to see what it did.",
              sound: true,
              data: { sessionId: event.sessionId },
            },
            trigger: null,
          }).catch(() => {});
          void Haptics.notificationAsync(
            Haptics.NotificationFeedbackType.Success,
          ).catch(() => {});
        }
        break;
      }

      case "send_result": {
        if (!event.clientId) break;
        setPending((current) => {
          if (event.status === "delivered") {
            const sent = current.find((item) => item.clientId === event.clientId);
            if (sent?.draftScope) void clearDraft(sent.draftScope, sent.sessionId);
          }
          return applyPendingSendResult(current, event);
        });
        setActions((current) => applyAgentActionResult(current, event));
        break;
      }

	  case "error": {
		// A failed history request must release its loading state. Otherwise a
		// transient adapter error leaves the spinner and pagination disabled for
		// the rest of the app process.
		const sessionId = replyTo ? pageRequests.current.get(replyTo) : undefined;
		if (replyTo) {
		  pageRequests.current.delete(replyTo);
		  catchUpRequests.current.delete(replyTo);
		}
		if (sessionId) {
		  setPageState((current) => ({
			...current,
			[sessionId]: { ...current[sessionId], hasMore: false, loading: false },
		  }));
		}
		break;
	  }
    }
  }, [mergeSessionMessages]);

  const attach = useCallback(
    (creds: Credentials) => {
      clientRef.current?.close();
      const client = new Client(creds, {
        onEvent: handleEvent,
        onControl: (control, replyTo) => {
          if (control.type === "daemon_offline" && control.lastSeenAt) {
            setLastSeenAt(control.lastSeenAt);
          }
          // An offline Mac is transient, so the Client retains idempotent reads
          // for replay. A relay error is permanent for that exact request and
          // must retire both its replay entry and UI correlation.
          if (replyTo && (control.type === "daemon_offline" || control.type === "error")) {
            const sessionId = pageRequests.current.get(replyTo) ??
              catchUpRequests.current.get(replyTo)?.sessionId;
            if (control.type === "error") {
              pageRequests.current.delete(replyTo);
              catchUpRequests.current.delete(replyTo);
            }
            if (sessionId) {
              setPageState((current) => ({
                ...current,
                [sessionId]: {
                  ...current[sessionId],
                  hasMore: control.type === "error"
                    ? false
                    : (current[sessionId]?.hasMore ?? true),
                  loading: false,
                },
              }));
            }
          }
          if (
            control.type === "daemon_online" ||
            (control.type === "hello" && control.daemonOnline === true)
          ) {
            const waiting = new Set(pageRequests.current.values());
            if (waiting.size > 0) {
              setPageState((current) => {
                const next = { ...current };
                for (const sessionId of waiting) {
                  next[sessionId] = {
                    ...next[sessionId],
                    hasMore: next[sessionId]?.hasMore ?? true,
                    loading: true,
                  };
                }
                return next;
              });
            }
          }
        },
        onConnectionChange: (state, online) => {
          setConnection(state);
          setDaemonOnline(online);
        },
      });
      clientRef.current = client;

      // Hand the Mac a push token so it can reach this phone once iOS suspends
      // the app. Best-effort: on a simulator, without permission, or in Expo Go
      // there is no token, and the app falls back to local notifications.
      void obtainPushToken()
        .then((token) => {
          if (!token || clientRef.current !== client) return;
          client.registerPush(token);
          setPushActive(true);
        })
        .catch(() => {});

      for (const sessionId of desiredSubscriptions.current) client.subscribe(sessionId);
      for (const sessionId of pendingInitialLoads.current) {
        const id = client.send({ type: "fetch_messages", sessionId, limit: 40 });
        if (id) pageRequests.current.set(id, sessionId);
        else {
          setPageState((current) => ({
            ...current,
            [sessionId]: { hasMore: true, loading: false },
          }));
        }
      }
      pendingInitialLoads.current.clear();
      for (const [sessionId, state] of pendingCatchUps.current) {
        const id = client.send({
          type: "fetch_messages",
          sessionId,
          limit: CATCH_UP_PAGE,
        });
        if (id) catchUpRequests.current.set(id, state);
      }
      pendingCatchUps.current.clear();
      client.connect();
    },
    [handleEvent],
  );

  useEffect(() => {
    void (async () => {
      const creds = await loadCredentials();
      setCredentials(creds);
      if (creds) attach(creds);
      else setConnection("unpaired");
      setReady(true);
    })();
    return () => clientRef.current?.close();
  }, [attach]);

  // Restore what the user hid before the app was last closed.
  useEffect(() => {
    void loadDismissals().then(setDismissals);
  }, []);

  // A phone spends most of its life asleep. Reconnect the moment it wakes,
  // rather than waiting out a backoff the user is watching.
  useEffect(() => {
    const subscription = AppState.addEventListener("change", (next) => {
      if (next === "active") clientRef.current?.poke();
    });
    return () => subscription.remove();
  }, []);

  const visibleSessions = useMemo(
    () => sessions.filter((session) => !isHidden(session, dismissals)),
    [sessions, dismissals],
  );

  const store: Store = useMemo(
    () => ({
      ready,
      credentials,
      connection,
      daemonOnline,
      lastSeenAt,
      sessions,
      visibleSessions,
      messages,
      pageState,
      pending,
      actions,

      signIn(creds) {
		// Pairing to another daemon is an account boundary. Never retain the
		// previous machine's transcripts or pending messages on the new board.
		pageRequests.current.clear();
		catchUpRequests.current.clear();
		desiredSubscriptions.current.clear();
		pendingInitialLoads.current.clear();
		pendingCatchUps.current.clear();
		setSessions([]);
		messagesRef.current = {};
		setMessages({});
		setPageState({});
		setPending([]);
		setActions([]);
		setDismissals({});
		announcedQuestions.current.clear();
		sessionsRef.current = [];
		void saveDismissals({});
        setCredentials(creds);
        attach(creds);
      },

      async signOut() {
        clientRef.current?.close();
        clientRef.current = null;
        await clearCredentials();
        setCredentials(null);
        setSessions([]);
        messagesRef.current = {};
        setMessages({});
		setPageState({});
		setPending([]);
		setActions([]);
		pageRequests.current.clear();
		catchUpRequests.current.clear();
		desiredSubscriptions.current.clear();
		pendingInitialLoads.current.clear();
		pendingCatchUps.current.clear();
		announcedQuestions.current.clear();
		sessionsRef.current = [];
		setDaemonOnline(false);
		setLastSeenAt(null);
        setConnection("unpaired");
        setDismissals({});
        void saveDismissals({});
      },

      refresh() {
        clientRef.current?.poke();
      },

      openSession(sessionId) {
        desiredSubscriptions.current.add(sessionId);
        clientRef.current?.subscribe(sessionId);

        const known = messages[sessionId] ?? [];
        if (known.length === 0) {
          // First visit: load the tail of the conversation.
          setPageState((current) => ({
            ...current,
            [sessionId]: { hasMore: true, loading: true },
          }));
          if (!clientRef.current) {
            pendingInitialLoads.current.add(sessionId);
            return;
          }
          const id = clientRef.current?.send({
            type: "fetch_messages",
            sessionId,
            limit: 40,
          });
		  if (id) {
			pageRequests.current.set(id, sessionId);
		  } else {
			setPageState((current) => ({
			  ...current,
			  [sessionId]: { hasMore: true, loading: false },
			}));
		  }
          return;
        }

        // Coming back. Subscribing only starts the live tail from now, so
        // whatever the agent did while this screen was closed would be missing
        // — and because the screen already has messages, nothing else would
        // ever fetch it. Ask for the newest page and walk back until it
        // overlaps what we have.
        //
        // Bounded on purpose: a long absence stops after a few pages rather
        // than pulling a whole transcript down a phone connection. The rest is
        // still reachable by scrolling up.
        const newest = newestTimestamp(known);
        if (!clientRef.current) {
          pendingCatchUps.current.set(sessionId, {
            sessionId,
            sinceTs: newest,
            depth: 0,
          });
          return;
        }
        const id = clientRef.current?.send({
          type: "fetch_messages",
          sessionId,
          limit: CATCH_UP_PAGE,
        });
        if (id) catchUpRequests.current.set(id, { sessionId, sinceTs: newest, depth: 0 });
      },

      closeSession(sessionId) {
        // Unsubscribing on blur is what keeps idle sessions free: the daemon
        // stops tailing a transcript nobody is looking at.
        desiredSubscriptions.current.delete(sessionId);
        pendingInitialLoads.current.delete(sessionId);
        pendingCatchUps.current.delete(sessionId);
        clientRef.current?.unsubscribe(sessionId);
      },

      loadOlder(sessionId) {
        const state = pageState[sessionId];
        if (!state?.hasMore || state.loading) return;
        // A relay-deferred request retains its correlation for replay. Do not
        // pile duplicate reads onto it while its spinner is intentionally down.
        if ([...pageRequests.current.values()].includes(sessionId)) return;
        setPageState((current) => ({
          ...current,
          [sessionId]: { ...state, loading: true },
        }));
        const id = clientRef.current?.send({
          type: "fetch_messages",
          sessionId,
          before: state.cursor,
          limit: 40,
        });
		if (id) {
		  pageRequests.current.set(id, sessionId);
		} else {
		  setPageState((current) => ({
			...current,
			[sessionId]: { ...state, loading: false },
		  }));
		}
      },

      sendMessage(sessionId, text) {
        const clientId = `send-${newFrameId()}`;
        const draftScope = credentials ? draftNamespace(credentials) : undefined;
        setPending((current) => [
          ...current,
          { clientId, sessionId, text, status: "sending", draftScope },
        ]);
        const sent = clientRef.current?.send({
          type: "send_message",
          sessionId,
          text,
          clientId,
        });
        if (!sent) {
          setPending((current) =>
            current.map((item) =>
              item.clientId === clientId
                ? { ...item, status: "failed", error: "Not connected" }
                : item,
            ),
          );
        }
        return clientId;
      },

      interruptSession(sessionId) {
        const clientId = `interrupt-${newFrameId()}`;
        const action: PendingAgentAction = {
          clientId,
          sessionId,
          kind: "interrupt",
          status: "sending",
        };
        setActions((current) => upsertAgentAction(current, action));
        const sent = clientRef.current?.send({
          type: "interrupt",
          sessionId,
          clientId,
        });
        if (!sent) {
          setActions((current) =>
            applyAgentActionResult(current, {
              clientId,
              status: "failed",
              error: "Your Mac is not connected.",
            }),
          );
        }
      },

      answerQuestion(sessionId, answer) {
        const clientId = `answer-${newFrameId()}`;
        const question = sessions.find((session) => session.id === sessionId)?.question;
        const action: PendingAgentAction = {
          clientId,
          sessionId,
          kind: "answer",
          status: "sending",
          questionIdentity: question ? questionIdentity(question) : undefined,
        };
        setActions((current) => upsertAgentAction(current, action));
        const sent = clientRef.current?.send({
          type: "answer_question",
          sessionId,
          ...answer,
          questionId: question?.id,
          clientId,
        });
        if (!sent) {
          setActions((current) =>
            applyAgentActionResult(current, {
              clientId,
              status: "failed",
              error: "Your Mac is not connected.",
            }),
          );
        }
        // The question clears itself on the next discovery sweep, so the row
        // is left alone rather than optimistically hidden — if the keystroke
        // did not land, the user needs to still see the choice.
      },

      dismissPending(clientId) {
        setPending((current) => current.filter((item) => item.clientId !== clientId));
      },

      dismissSession(sessionId) {
        const session = sessions.find((item) => item.id === sessionId);
        // Re-checked here rather than trusted from the UI: between the swipe
        // starting and the finger lifting, the agent may have started working.
        if (!session || !canDismiss(session)) return;
        setDismissals((current) => {
          const next = recordDismissal(session, current);
          void saveDismissals(next);
          return next;
        });
      },

      restoreSession(sessionId) {
        setDismissals((current) => {
          if (!(sessionId in current)) return current;
          const next = { ...current };
          delete next[sessionId];
          void saveDismissals(next);
          return next;
        });
      },

      restoreAllSessions() {
        setDismissals(() => {
          void saveDismissals({});
          return {};
        });
      },
    }),
    [ready, credentials, connection, daemonOnline, lastSeenAt, sessions, visibleSessions, messages, pageState, pending, actions, dismissals, attach],
  );

  return <StoreContext.Provider value={store}>{children}</StoreContext.Provider>;
}
