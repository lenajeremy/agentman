import AsyncStorage from "@react-native-async-storage/async-storage";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import {
  applyDelta,
  openCards as openCardsOf,
  prependOlder,
  unreadCount,
  upsertEntry,
} from "./manager-feed";
import type {
  Brief,
  DaemonEvent,
  Key,
  PermissionCheck,
  ManagerCard,
  ManagerEntry,
  ManagerSchedule,
  ManagerSettings,
  ManagerStatus,
  Note,
  QuestionAnswer,
} from "./protocol";
import { useStore } from "./store";

/**
 * The conversation with Agentman, the manager in the Mac's daemon: the same
 * one the Mac shows, heard over the relay. Replies stream in, cards wait for
 * the user, and what arrived since they last looked counts as unread.
 */
export interface Manager {
  entries: ManagerEntry[];
  hasOlder: boolean;
  status: ManagerStatus;
  briefs: Record<string, Brief>;
  schedules: ManagerSchedule[];
  settings: ManagerSettings | null;
  /** Whether the first page has arrived. */
  loaded: boolean;
  /** False when the Mac runs an agentman too old to have Agentman. */
  supported: boolean;
  /** The last thing that went wrong talking to Agentman. */
  problem: string | null;
  unread: number;
  openCards: ManagerCard[];
  thinking: boolean;
  needsKey: boolean;
  reload(): Promise<void>;
  /** Says something to Agentman, with images already left with the relay. */
  send(text: string, uploadIds?: string[]): Promise<boolean>;
  stop(): void;
  loadOlder(): Promise<void>;
  /** Answers a card. Returns the problem, if any. */
  reply(card: ManagerCard, answer: QuestionAnswer & { text?: string }): Promise<string | null>;
  /** Saves a suggested note, as written or as edited. */
  saveNote(card: ManagerCard, edited?: Note): Promise<string | null>;
  /** Puts a card away unanswered. */
  dismiss(card: ManagerCard): Promise<string | null>;
  /** Switches the model Agentman's conversation runs on. */
  setChatModel(model: string): Promise<string | null>;
  /** Starts a new conversation; the one before is archived on the Mac, and
   *  Agentman keeps what lasts of it in its private notes. */
  reset(): Promise<string | null>;
  /** Saves Agentman's settings whole. Returns the problem, if any. */
  saveSettings(next: ManagerSettings): Promise<string | null>;
  /** What Agentman would do with a request, under the settings as saved. */
  checkPermission(kind: string, target: string, folder: string): Promise<PermissionCheck | string>;
  /** The user's notes, without bodies, newest first. */
  notes: Note[];
  loadNotes(): Promise<void>;
  /** One note whole. */
  readNote(name: string, tier: string): Promise<Note | null>;
  /** Saves a note and returns it as saved, or the problem. */
  writeNote(note: Note): Promise<Note | string>;
  deleteNote(note: Note): Promise<string | null>;
  /** The keys Agentman hands out, names only. */
  keys: Key[];
  loadKeys(): Promise<void>;
  /** Saves a key's name, description and rule; its value is set on the Mac. */
  saveKeyDetails(key: Key): Promise<string | null>;
  deleteKey(name: string): Promise<string | null>;
  cancelSchedule(schedule: ManagerSchedule): Promise<string | null>;
  /** The user has seen everything so far. */
  markRead(): void;
  /** An image the user sent Agentman, as a data URI. */
  keptImage(name: string): Promise<string | null>;
}

const LAST_READ_KEY = "agentman.managerLastRead";
const idle: ManagerStatus = { state: "idle", keySet: true };
const keptImages = new Map<string, string>();

const ManagerContext = createContext<Manager | null>(null);

export function useManager(): Manager {
  const manager = useContext(ManagerContext);
  if (!manager) throw new Error("useManager must be used inside <ManagerProvider>");
  return manager;
}

function message(reason: unknown): string {
  return reason instanceof Error ? reason.message : String(reason);
}

export function ManagerProvider({ children }: { children: ReactNode }) {
  const store = useStore();
  const [entries, setEntries] = useState<ManagerEntry[]>([]);
  const [hasOlder, setHasOlder] = useState(false);
  const [status, setStatus] = useState<ManagerStatus>(idle);
  const [briefs, setBriefs] = useState<Record<string, Brief>>({});
  const [schedules, setSchedules] = useState<ManagerSchedule[]>([]);
  const [settings, setSettings] = useState<ManagerSettings | null>(null);
  const [notes, setNotes] = useState<Note[]>([]);
  const [keys, setKeys] = useState<Key[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [supported, setSupported] = useState(true);
  const [problem, setProblem] = useState<string | null>(null);
  const [lastRead, setLastRead] = useState(Number.MAX_SAFE_INTEGER);
  const entriesRef = useRef(entries);
  entriesRef.current = entries;
  // A new conversation, started here or on the Mac, is read afresh.
  const reloadRef = useRef<() => Promise<void>>(async () => {});

  // The first launch starts with nothing unread, not the whole history.
  useEffect(() => {
    void AsyncStorage.getItem(LAST_READ_KEY)
      .then((stored) => {
        const value = stored ? Number(stored) : Date.now();
        if (!stored) void AsyncStorage.setItem(LAST_READ_KEY, String(value)).catch(() => {});
        setLastRead(Number.isFinite(value) ? value : Date.now());
      })
      .catch(() => setLastRead(Date.now()));
  }, []);

  const apply = useCallback((event: DaemonEvent) => {
    switch (event.type) {
      case "manager_entry":
        if (event.entry) {
          const entry = event.entry;
          setEntries((current) => upsertEntry(current, entry));
        }
        break;
      case "manager_delta":
        if (event.entryId && event.delta) {
          const { entryId, delta } = event;
          setEntries((current) => applyDelta(current, entryId, delta));
        }
        break;
      case "manager_status":
        if (event.managerStatus) setStatus(event.managerStatus);
        break;
      case "manager_schedules":
        setSchedules(event.schedules ?? []);
        break;
      case "manager_settings":
        if (event.managerSettings) setSettings(event.managerSettings);
        if (event.managerStatus) setStatus(event.managerStatus);
        break;
      case "knowledge":
        if (event.notes) setNotes(event.notes);
        break;
      case "keys":
        if (event.keys) setKeys(event.keys);
        break;
      case "manager_reset":
        void reloadRef.current();
        break;
      case "manager_brief": {
        const updates = [...(event.briefs ?? []), ...(event.brief ? [event.brief] : [])];
        if (updates.length > 0) {
          setBriefs((current) => {
            const next = { ...current };
            for (const brief of updates) next[brief.sessionId] = brief;
            return next;
          });
        }
        break;
      }
    }
  }, []);

  useEffect(() => store.listen(apply), [store, apply]);

  const reload = useCallback(async () => {
    try {
      const history = await store.ask({ type: "manager_history", limit: 80 });
      setEntries(history.entries ?? []);
      setHasOlder(history.more === true);
      setSupported(true);
      setLoaded(true);
    } catch (reason) {
      const text = message(reason);
      // An agentman from before Agentman does not know the request at all.
      if (/unsupported request|unknown request/i.test(text)) setSupported(false);
      return;
    }
    const [settingsReply, briefReply, scheduleReply] = await Promise.allSettled([
      store.ask({ type: "manager_settings" }),
      store.ask({ type: "manager_briefs" }),
      store.ask({ type: "manager_schedules" }),
    ]);
    if (settingsReply.status === "fulfilled") apply(settingsReply.value);
    if (briefReply.status === "fulfilled") apply(briefReply.value);
    if (scheduleReply.status === "fulfilled") setSchedules(scheduleReply.value.schedules ?? []);
  }, [store, apply]);

  reloadRef.current = reload;

  // Everything afresh whenever the Mac comes within reach.
  const reachable = store.connection === "online" && store.daemonOnline;
  useEffect(() => {
    if (reachable) void reload();
  }, [reachable, reload]);

  const perform = useCallback(async (request: Parameters<typeof store.ask>[0]): Promise<string | null> => {
    try {
      apply(await store.ask(request));
      return null;
    } catch (reason) {
      return message(reason);
    }
  }, [store, apply]);

  const markRead = useCallback(() => {
    const latest = entriesRef.current.at(-1)?.ts;
    if (!latest) return;
    setLastRead((current) => {
      if (latest <= current) return current;
      void AsyncStorage.setItem(LAST_READ_KEY, String(latest)).catch(() => {});
      return latest;
    });
  }, []);

  const value = useMemo<Manager>(() => ({
    entries,
    hasOlder,
    status,
    briefs,
    schedules,
    settings,
    loaded,
    supported,
    problem,
    unread: unreadCount(entries, lastRead),
    openCards: openCardsOf(entries),
    thinking: status.state === "thinking",
    needsKey: settings ? !settings.keySet : status.state === "needs_key",
    reload,
    async send(text, uploadIds = []) {
      setProblem(null);
      const result = await perform({
        type: "manager_send",
        text,
        ...(uploadIds.length > 0 ? { uploadIds } : {}),
      });
      if (result) setProblem(result);
      return result === null;
    },
    stop() {
      void store.ask({ type: "manager_stop" }).catch(() => {});
    },
    async loadOlder() {
      const first = entriesRef.current[0];
      if (!hasOlder || !first) return;
      try {
        const page = await store.ask({ type: "manager_history", before: first.id, limit: 80 });
        setEntries((current) => prependOlder(current, page.entries ?? []));
        setHasOlder(page.more === true);
      } catch {
        // Scrolling up again tries again.
      }
    },
    reply(card, answer) {
      const question = card.kind === "question" || card.kind === "key";
      return perform({
        type: "manager_card_reply",
        cardId: card.id,
        ...(question
          ? {
              ...(answer.optionKey ? { optionKey: answer.optionKey } : {}),
              ...(answer.optionKeys ? { optionKeys: answer.optionKeys } : {}),
              ...(answer.answerText ? { answerText: answer.answerText } : {}),
            }
          : { text: answer.text ?? answer.answerText ?? "" }),
      });
    },
    saveNote(card, edited) {
      return perform({ type: "manager_card_reply", cardId: card.id, ...(edited ? { note: edited } : {}) });
    },
    dismiss(card) {
      return perform({ type: "manager_card_dismiss", cardId: card.id });
    },
    async reset() {
      try {
        await store.ask({ type: "manager_reset" });
        return null;
      } catch (reason) {
        return message(reason);
      }
    },
    setChatModel(model) {
      if (!settings) return Promise.resolve("Agentman's settings haven't arrived yet.");
      return perform({ type: "manager_set_settings", manager: { ...settings, chatModel: model } });
    },
    saveSettings(next) {
      return perform({ type: "manager_set_settings", manager: next });
    },
    async checkPermission(kind, target, folder) {
      try {
        const reply = await store.ask({ type: "manager_check_permission", kind, text: target, path: folder });
        return reply.permission ?? "No answer from Agentman.";
      } catch (reason) {
        return message(reason);
      }
    },
    notes,
    async loadNotes() {
      try {
        apply(await store.ask({ type: "knowledge_list" }));
      } catch {
        // The list keeps what it had.
      }
    },
    async readNote(name, tier) {
      try {
        return (await store.ask({ type: "knowledge_read", path: name, kind: tier })).note ?? null;
      } catch {
        return null;
      }
    },
    async writeNote(note) {
      try {
        const reply = await store.ask({ type: "knowledge_write", note });
        if (!reply.note) return "Couldn't save the note.";
        void store.ask({ type: "knowledge_list" }).then(apply).catch(() => {});
        return reply.note;
      } catch (reason) {
        return message(reason);
      }
    },
    deleteNote(note) {
      return perform({ type: "knowledge_delete", path: note.name, kind: note.tier });
    },
    keys,
    async loadKeys() {
      try {
        apply(await store.ask({ type: "keys_list" }));
      } catch {
        // The list keeps what it had.
      }
    },
    saveKeyDetails(key) {
      return perform({ type: "key_set", key });
    },
    deleteKey(name) {
      return perform({ type: "key_delete", path: name });
    },
    async cancelSchedule(schedule) {
      try {
        const reply = await store.ask({ type: "manager_schedule_cancel", scheduleId: schedule.id });
        setSchedules(reply.schedules ?? []);
        return null;
      } catch (reason) {
        return message(reason);
      }
    },
    markRead,
    async keptImage(name) {
      const cached = keptImages.get(name);
      if (cached) return cached;
      try {
        const reply = await store.ask({ type: "manager_image", path: name });
        const image = reply.workspace;
        if (!image?.image || !image.mime) return null;
        const uri = `data:${image.mime};base64,${image.image}`;
        keptImages.set(name, uri);
        return uri;
      } catch {
        return null;
      }
    },
  }), [entries, hasOlder, status, briefs, schedules, settings, notes, keys, loaded, supported, problem, lastRead, reload, perform, markRead, store]);

  return <ManagerContext.Provider value={value}>{children}</ManagerContext.Provider>;
}
