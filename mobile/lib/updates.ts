// Telling someone their app or their Mac is behind, once per new version.
//
// Two sources, because the two halves ship separately. The app's latest
// build is in a small file the iOS release publishes to the website, since
// TestFlight has no API an app can ask. The Mac's is worked out by the Mac
// itself, which asks GitHub and passes the answer along with its session
// list. Neither request carries anything about the person asking.
//
// Plain functions only, so node can test them without React Native.

import type { DaemonInfo, ReleaseNote } from "./protocol";

/** Written by the iOS release script, served by the website. */
export const APP_VERSION_URL = "https://agentman-nu.vercel.app/app-version.json";

/** Upgrading the Mac when it did not say how it was installed. */
export const DEFAULT_UPGRADE = "curl -fsSL https://agentman-nu.vercel.app/install | sh";

/** Every release's notes, when the Mac did not send its own link. */
export const DEFAULT_CHANGELOG = "https://github.com/lenajeremy/agentman/releases";

/**
 * How long after a build ships before the app offers it. Apple processes an
 * upload for a while before TestFlight will install it, and an Update button
 * that leads to nothing new is worse than an offer that comes a little late.
 */
export const PROCESSING_GRACE_MS = 60 * 60 * 1000;

/** How often the app asks the website again while it stays open. */
export const RECHECK_MS = 6 * 60 * 60 * 1000;

export interface AppBuildNote {
  build: number;
  version: string;
  /** Milliseconds. When the build was sent to Apple. */
  date: number;
  changes: string[];
}

export interface AppRelease {
  /** Where to get it: the TestFlight invite. */
  url: string;
  /** Newest first. */
  builds: AppBuildNote[];
}

/** Reads the website's file, or null for anything that is not one. */
export function parseAppRelease(value: unknown): AppRelease | null {
  if (!isRecord(value) || typeof value.url !== "string" || !/^https:\/\//.test(value.url) ||
      !Array.isArray(value.builds)) {
    return null;
  }
  const builds: AppBuildNote[] = [];
  for (const entry of value.builds.slice(0, 50)) {
    if (!isRecord(entry) || !Number.isInteger(entry.build) || typeof entry.version !== "string") continue;
    const date = typeof entry.date === "string" ? Date.parse(entry.date) : NaN;
    if (!Number.isFinite(date)) continue;
    const changes = Array.isArray(entry.changes)
      ? entry.changes.filter((change): change is string => typeof change === "string").slice(0, 30)
      : [];
    builds.push({ build: entry.build as number, version: entry.version, date, changes });
  }
  builds.sort((a, b) => b.build - a.build);
  return builds.length > 0 ? { url: value.url, builds } : null;
}

export interface AppUpdate {
  current: number;
  latest: AppBuildNote;
  /** Every build newer than this one, newest first. */
  missing: AppBuildNote[];
  url: string;
}

/** The builds this app is missing that TestFlight can install by now. */
export function appUpdate(currentBuild: number, release: AppRelease | null, now: number): AppUpdate | null {
  if (!release || !Number.isInteger(currentBuild) || currentBuild <= 0) return null;
  const missing = release.builds.filter(
    (build) => build.build > currentBuild && now - build.date >= PROCESSING_GRACE_MS,
  );
  if (missing.length === 0) return null;
  return { current: currentBuild, latest: missing[0], missing, url: release.url };
}

export interface MacUpdate {
  current: string;
  latest: string;
  behind: number;
  releases: ReleaseNote[];
  upgrade: string;
  changelog: string;
}

/** What the Mac said it is missing, or null when it is current or unsure. */
export function macUpdate(info: DaemonInfo | null | undefined): MacUpdate | null {
  if (!info?.latest || !info.behind || info.behind <= 0 || info.latest === info.version) return null;
  return {
    current: info.version,
    latest: info.latest,
    behind: info.behind,
    releases: info.releases ?? [],
    upgrade: info.upgrade || DEFAULT_UPGRADE,
    changelog: info.changelog || DEFAULT_CHANGELOG,
  };
}

/** The version each prompt was last shown for. */
export interface Prompted {
  app?: number;
  mac?: string;
}

export type PromptKind = "app" | "mac";

/**
 * Which prompt to show, if any. Each new version is offered once, whatever
 * the answer: someone who said "Not now" has been told, and Settings keeps
 * the offer for when they want it. The app goes first, as it is one tap.
 */
export function nextPrompt(app: AppUpdate | null, mac: MacUpdate | null, prompted: Prompted): PromptKind | null {
  if (app && prompted.app !== app.latest.build) return "app";
  if (mac && prompted.mac !== mac.latest) return "mac";
  return null;
}

/** The app's line in Settings. */
export function appSummary(version: string, build: number, update: AppUpdate | null): string {
  const current = build > 0 ? `${version} (${build})` : version;
  return update ? `${current} · build ${update.latest.build} is out` : `${current} · up to date`;
}

/** The Mac's line in Settings. */
export function macSummary(info: DaemonInfo | null | undefined): string {
  if (!info) return "Unknown";
  const update = macUpdate(info);
  if (update) return `${info.version} · ${behindText(update.behind)}`;
  return info.latest ? `${info.version} · up to date` : info.version;
}

export function behindText(behind: number): string {
  return behind === 1 ? "1 version behind" : `${behind} versions behind`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
