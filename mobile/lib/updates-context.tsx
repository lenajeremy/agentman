import AsyncStorage from "@react-native-async-storage/async-storage";
import Constants from "expo-constants";
import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { AppState, Platform } from "react-native";

import type { DaemonInfo } from "./protocol";
import { useStore } from "./store";
import {
  APP_VERSION_URL,
  AppRelease,
  AppUpdate,
  appUpdate,
  MacUpdate,
  macUpdate,
  nextPrompt,
  parseAppRelease,
  Prompted,
  PromptKind,
  RECHECK_MS,
} from "./updates";

const RELEASE_KEY = "agentman.appRelease.v1";
const PROMPTED_KEY = "agentman.updatePrompted.v1";

interface Updates {
  /** This app's version and build, as built. */
  appVersion: string;
  appBuild: number;
  app: AppUpdate | null;
  mac: MacUpdate | null;
  daemonInfo: DaemonInfo | null;
  /** The prompt the board should show next, if any. */
  pending: PromptKind | null;
  /** Record that a prompt was shown, so that version is not offered again. */
  markPrompted(kind: PromptKind): void;
}

const UpdatesContext = createContext<Updates | null>(null);

/** The build this binary was made as: on iOS its own CFBundleVersion, which
 *  the release script set from app.json. Zero where there is none, as on
 *  the web, which TestFlight does not serve and so is never prompted. */
function builtAs(): { version: string; build: number } {
  const config = Constants.expoConfig;
  const raw = Platform.OS === "android"
    ? Constants.platform?.android?.versionCode ?? config?.android?.versionCode
    : Platform.OS === "ios"
      ? Constants.platform?.ios?.buildNumber ?? config?.ios?.buildNumber
      : null;
  const build = typeof raw === "number" ? raw : parseInt(raw ?? "", 10);
  return { version: config?.version ?? "", build: Number.isInteger(build) ? build : 0 };
}

export function UpdatesProvider({ children }: { children: React.ReactNode }) {
  const { daemonInfo } = useStore();
  const [{ version: appVersion, build: appBuild }] = useState(builtAs);
  const [release, setRelease] = useState<AppRelease | null>(null);
  const [prompted, setPrompted] = useState<Prompted | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const fetchedAt = useRef(0);

  // What was known last time, so Settings can say something before the
  // first fetch, or without a connection at all.
  useEffect(() => {
    void AsyncStorage.multiGet([RELEASE_KEY, PROMPTED_KEY])
      .then(([[, cached], [, shown]]) => {
        if (cached) setRelease((current) => current ?? parseAppRelease(JSON.parse(cached)));
        setPrompted(shown ? (JSON.parse(shown) as Prompted) : {});
      })
      .catch(() => setPrompted({}));
  }, []);

  const refresh = useCallback(() => {
    setNow(Date.now());
    if (Date.now() - fetchedAt.current < RECHECK_MS) return;
    fetchedAt.current = Date.now();
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 10_000);
    void fetch(APP_VERSION_URL, { cache: "no-store", signal: controller.signal })
      .then((response) => (response.ok ? response.json() : null))
      .then((body) => {
        const parsed = parseAppRelease(body);
        if (!parsed) return;
        setRelease(parsed);
        void AsyncStorage.setItem(RELEASE_KEY, JSON.stringify(body)).catch(() => {});
      })
      .catch(() => {
        // Offline, or the site is down. The next foreground tries again.
        fetchedAt.current = 0;
      })
      .finally(() => clearTimeout(timer));
  }, []);

  useEffect(() => {
    refresh();
    const subscription = AppState.addEventListener("change", (state) => {
      if (state === "active") refresh();
    });
    return () => subscription.remove();
  }, [refresh]);

  const markPrompted = useCallback((kind: PromptKind) => {
    setPrompted((current) => {
      const next: Prompted = { ...current };
      if (kind === "app") {
        const build = appUpdate(appBuild, release, Date.now())?.latest.build;
        if (build) next.app = build;
      } else {
        const latest = macUpdate(daemonInfo)?.latest;
        if (latest) next.mac = latest;
      }
      void AsyncStorage.setItem(PROMPTED_KEY, JSON.stringify(next)).catch(() => {});
      return next;
    });
  }, [appBuild, release, daemonInfo]);

  const value = useMemo<Updates>(() => {
    const app = appUpdate(appBuild, release, now);
    const mac = macUpdate(daemonInfo);
    return {
      appVersion,
      appBuild,
      app,
      mac,
      daemonInfo,
      // Until the record of past prompts has loaded, nothing is pending:
      // guessing would offer a version twice.
      pending: prompted ? nextPrompt(app, mac, prompted) : null,
      markPrompted,
    };
  }, [appVersion, appBuild, release, now, daemonInfo, prompted, markPrompted]);

  return <UpdatesContext.Provider value={value}>{children}</UpdatesContext.Provider>;
}

export function useUpdates(): Updates {
  const updates = useContext(UpdatesContext);
  if (!updates) throw new Error("useUpdates must be used inside UpdatesProvider");
  return updates;
}
