import AsyncStorage from "@react-native-async-storage/async-storage";
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import {
  agentColor,
  agentColorKey,
  assignHues,
  parseAgentColorBy,
  type AgentColorBy,
} from "./agent-colors";
import { useTheme } from "./appearance";
import { stillHidden } from "./board";
import type { Session } from "./protocol";
import { useStore } from "./store";

const COLOR_BY_KEY = "agentman.board.colorBy.v1";
const HUES_KEY = "agentman.board.hues.v1";
const HIDDEN_KEY = "agentman.board.hidden.v1";
/** Ids kept of agents hidden long ago: enough that nothing the user could
 *  still care about falls off. */
const HIDDEN_KEPT = 200;

/**
 * What the Board remembers on this device: what colours agents, each
 * project's or folder's colour for good, and which agents are hidden. Above
 * every screen, because any list of agents shows the colour, and an agent
 * that comes to need the user must leave the hidden list even while the
 * Board is closed.
 */
interface BoardPrefs {
  colorBy: AgentColorBy;
  setColorBy(next: AgentColorBy): void;
  /** The colour an agent is shown in, or null. */
  colorOf(session: Pick<Session, "state" | "question" | "cwd">): string | null;
  hidden: ReadonlySet<string>;
  hide(sessionId: string): void;
  show(sessionId: string): void;
  showAll(): void;
}

const noPrefs: BoardPrefs = {
  colorBy: "none",
  setColorBy: () => {},
  colorOf: () => null,
  hidden: new Set(),
  hide: () => {},
  show: () => {},
  showAll: () => {},
};

const BoardPrefsContext = createContext<BoardPrefs>(noPrefs);

export const useBoardPrefs = (): BoardPrefs => useContext(BoardPrefsContext);

function readJson<T>(raw: string | null, fallback: T, valid: (value: unknown) => value is T): T {
  if (!raw) return fallback;
  try {
    const value: unknown = JSON.parse(raw);
    return valid(value) ? value : fallback;
  } catch {
    return fallback;
  }
}

const isHueMap = (value: unknown): value is Record<string, number> =>
  typeof value === "object" && value !== null && !Array.isArray(value) &&
  Object.values(value).every((index) => typeof index === "number");

const isIdList = (value: unknown): value is string[] =>
  Array.isArray(value) && value.every((id) => typeof id === "string");

export function BoardPrefsProvider({ children }: { children: ReactNode }) {
  const store = useStore();
  const { color, scheme } = useTheme();
  const [ready, setReady] = useState(false);
  const [colorBy, setColorByState] = useState<AgentColorBy>("none");
  const [hues, setHues] = useState<Record<string, number>>({});
  const [hidden, setHidden] = useState<ReadonlySet<string>>(new Set<string>());

  useEffect(() => {
    let active = true;
    void Promise.all([
      AsyncStorage.getItem(COLOR_BY_KEY),
      AsyncStorage.getItem(HUES_KEY),
      AsyncStorage.getItem(HIDDEN_KEY),
    ])
      .then(([by, storedHues, storedHidden]) => {
        if (!active) return;
        setColorByState(parseAgentColorBy(by));
        setHues(readJson(storedHues, {}, isHueMap));
        setHidden(new Set(readJson(storedHidden, [], isIdList)));
      })
      .catch(() => {})
      .finally(() => {
        if (active) setReady(true);
      });
    return () => {
      active = false;
    };
  }, []);

  // A project or folder gets its colour the first time it shows while
  // agents are coloured that way, and keeps it from then on.
  useEffect(() => {
    if (!ready) return;
    const keys = store.sessions.flatMap((session) => {
      const key = agentColorKey(session.cwd, colorBy);
      return key ? [key] : [];
    });
    setHues((current) => {
      const next = assignHues(current, keys);
      if (next !== current) void AsyncStorage.setItem(HUES_KEY, JSON.stringify(next)).catch(() => {});
      return next;
    });
  }, [ready, store.sessions, colorBy]);

  const saveHidden = useCallback((next: ReadonlySet<string>) => {
    setHidden(next);
    void AsyncStorage.setItem(HIDDEN_KEY, JSON.stringify([...next].slice(-HIDDEN_KEPT))).catch(() => {});
  }, []);

  // A hidden agent that needs the user is back for good.
  useEffect(() => {
    if (!ready || hidden.size === 0) return;
    const next = stillHidden(hidden, store.sessions);
    if (next.size !== hidden.size) saveHidden(next);
  }, [ready, hidden, store.sessions, saveHidden]);

  const setColorBy = useCallback((next: AgentColorBy) => {
    setColorByState(next);
    void AsyncStorage.setItem(COLOR_BY_KEY, next).catch(() => {});
  }, []);

  const value = useMemo<BoardPrefs>(() => ({
    colorBy,
    setColorBy,
    colorOf: (session) => agentColor(session, colorBy, hues, color, scheme),
    hidden,
    hide: (sessionId) => saveHidden(new Set(hidden).add(sessionId)),
    show: (sessionId) => {
      const next = new Set(hidden);
      next.delete(sessionId);
      saveHidden(next);
    },
    showAll: () => saveHidden(new Set()),
  }), [colorBy, setColorBy, hues, color, scheme, hidden, saveHidden]);

  return <BoardPrefsContext.Provider value={value}>{children}</BoardPrefsContext.Provider>;
}
