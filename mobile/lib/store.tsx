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
  canDismiss,
  dismiss as recordDismissal,
  isHidden,
  loadDismissals,
  prune,
  saveDismissals,
  type Dismissals,
} from "./dismissed";
import { DaemonEvent, Message, Page, SendStatus, Session } from "./protocol";

/** A message the user sent that has not been confirmed yet. */
export interface PendingSend {
  clientId: string;
  sessionId: string;
  text: string;
  status: "sending" | SendStatus;
  error?: string;
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
  pageState: Record<string, { cursor?: string; hasMore: boolean; loading: boolean }>;
  pending: PendingSend[];

  signIn(creds: Credentials): void;
  signOut(): Promise<void>;
  refresh(): void;
  openSession(sessionId: string): void;
  closeSession(sessionId: string): void;
  loadOlder(sessionId: string): void;
  sendMessage(sessionId: string, text: string): void;
  answerQuestion(sessionId: string, optionKey: string): void;
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

/** Merge by id, newest wins — a live-tailed message can also arrive in a page. */
function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  if (incoming.length === 0) return existing;
  const byId = new Map<string, Message>();
  for (const message of existing) byId.set(message.id, message);
  for (const message of incoming) byId.set(message.id, message);
  return Array.from(byId.values()).sort((a, b) => {
    if (a.ts !== b.ts) return a.ts - b.ts;
    return a.id < b.id ? -1 : 1;
  });
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
  const [dismissals, setDismissals] = useState<Dismissals>({});

  const clientRef = useRef<Client | null>(null);
  /** Maps a fetch_messages frame id to the session that asked, so a page can be
   *  filed correctly when several sessions are paging at once. */
  const pageRequests = useRef(new Map<string, string>());

  const handleEvent = useCallback((event: DaemonEvent, replyTo?: string) => {
    switch (event.type) {
      case "sessions": {
        const list = event.sessions ?? [];
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
        setSessions((current) => {
          const index = current.findIndex((s) => s.id === updated.id);
          if (index === -1) return [...current, updated];
          const next = [...current];
          next[index] = updated;
          return next;
        });
        break;
      }

      case "session_gone":
        setSessions((current) => current.filter((s) => s.id !== event.sessionId));
        break;

      case "messages": {
        const sessionId = event.sessionId;
        if (!sessionId || !event.messages) break;
        setMessages((current) => ({
          ...current,
          [sessionId]: mergeMessages(current[sessionId] ?? [], event.messages!),
        }));
        break;
      }

      case "page": {
        const page = event.page as Page | undefined;
        if (!page) break;
        const sessionId = page.sessionId || (replyTo ? pageRequests.current.get(replyTo) : undefined);
        if (!sessionId) break;
        if (replyTo) pageRequests.current.delete(replyTo);

        setMessages((current) => ({
          ...current,
          [sessionId]: mergeMessages(current[sessionId] ?? [], page.messages),
        }));
        setPageState((current) => ({
          ...current,
          [sessionId]: { cursor: page.nextCursor, hasMore: page.hasMore, loading: false },
        }));
        break;
      }

      case "turn_complete": {
        // The whole point of the product: tell the user their agent finished.
        // Guarded because neither module exists on web, where an unhandled
        // rejection here takes the whole screen down.
        if (Platform.OS !== "web") {
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
        setPending((current) =>
          current.map((item) =>
            item.clientId === event.clientId
              ? { ...item, status: event.status ?? "failed", error: event.error }
              : item,
          ),
        );
        break;
      }
    }
  }, []);

  const attach = useCallback(
    (creds: Credentials) => {
      clientRef.current?.close();
      const client = new Client(creds, {
        onEvent: handleEvent,
        onControl: (control) => {
          if (control.type === "daemon_offline" && control.lastSeenAt) {
            setLastSeenAt(control.lastSeenAt);
          }
        },
        onConnectionChange: (state, online) => {
          setConnection(state);
          setDaemonOnline(online);
        },
      });
      clientRef.current = client;
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

      signIn(creds) {
        setCredentials(creds);
        attach(creds);
      },

      async signOut() {
        clientRef.current?.close();
        clientRef.current = null;
        await clearCredentials();
        setCredentials(null);
        setSessions([]);
        setMessages({});
        setConnection("unpaired");
        setDismissals({});
        void saveDismissals({});
      },

      refresh() {
        clientRef.current?.poke();
      },

      openSession(sessionId) {
        clientRef.current?.subscribe(sessionId);
        // Only fetch history the first time; re-entering a session should not
        // refetch what is already on screen.
        if (!pageState[sessionId]) {
          setPageState((current) => ({
            ...current,
            [sessionId]: { hasMore: true, loading: true },
          }));
          const id = clientRef.current?.send({
            type: "fetch_messages",
            sessionId,
            limit: 40,
          });
          if (id) pageRequests.current.set(id, sessionId);
        }
      },

      closeSession(sessionId) {
        // Unsubscribing on blur is what keeps idle sessions free: the daemon
        // stops tailing a transcript nobody is looking at.
        clientRef.current?.unsubscribe(sessionId);
      },

      loadOlder(sessionId) {
        const state = pageState[sessionId];
        if (!state?.hasMore || state.loading) return;
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
        if (id) pageRequests.current.set(id, sessionId);
      },

      sendMessage(sessionId, text) {
        const clientId = `send-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
        setPending((current) => [
          ...current,
          { clientId, sessionId, text, status: "sending" },
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
      },

      answerQuestion(sessionId, optionKey) {
        clientRef.current?.send({
          type: "answer_question",
          sessionId,
          optionKey,
          clientId: `answer-${Date.now()}`,
        });
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
    [ready, credentials, connection, daemonOnline, lastSeenAt, sessions, visibleSessions, messages, pageState, pending, dismissals, attach],
  );

  return <StoreContext.Provider value={store}>{children}</StoreContext.Provider>;
}
