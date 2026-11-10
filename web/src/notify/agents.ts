// The agents' own notifications (AgentsStore.swift's apply, Notifier.agent):
// "<name> finished" with its last words, "<name> needs you" with the
// question, never for the thread the user is looking at, and as the policy
// allows. And what is on screen, told to the daemon (manager_viewing) when
// it changes and again every minute, so Agentman sends no card or push
// about an agent the user is looking at.

import type { Session } from "@shared/protocol";
import { Call } from "@wailsio/runtime";
import { daemon } from "../daemon";
import { notify } from "../host";
import { getRoute, onRouteChange } from "../nav";
import { agentmanOn } from "../shell/agentman";
import { appActive, onActive } from "../shell/native";
import { getStore } from "../store";
import { agentSpeaks, shouldNotify, sound } from "./policy";
import { panelsShowing, onPanelsChange } from "./onScreen";

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

/** Markdown flattened to a line of plain text for a notification. */
export function plainText(markdown: string): string {
  let text = markdown;
  for (const mark of ["**", "__", "`", "#"]) text = text.split(mark).join("");
  return text
    .split("\n")
    .map((line) => {
      let l = line.trim();
      for (const bullet of ["- ", "* ", "• "]) if (l.startsWith(bullet)) l = l.slice(bullet.length);
      return l;
    })
    .filter(Boolean)
    .join(" ");
}

/** The agents whose threads are in front of the user: the one open in the
 *  main window and those in editors' panels, while the app is in front. */
export function onScreen(): string[] {
  if (!appActive()) return [];
  const shown = new Set(panelsShowing().filter((id) => id !== "agentman"));
  const route = getRoute();
  if (route.kind === "agent") shown.add(route.id);
  return [...shown].sort();
}

function looking(sessionId: string): boolean {
  return onScreen().includes(sessionId);
}

function session(id: string): Session | undefined {
  return getStore().sessions.find((s) => s.id === id);
}

function speaks(id: string, s = session(id)): boolean {
  return agentSpeaks(agentmanOn(), s?.alerts === true);
}

function post(id: string, title: string, body: string, sessionId: string) {
  const link = `agentman://agent/${encodeURIComponent(sessionId)}`;
  const text = [...plainText(body)].slice(0, 400).join("");
  if (inShell) {
    void Call.ByName("main.Host.NotifyWith", { id, title, subtitle: "", body: text, link, silent: !sound() }).catch(() => {});
  } else {
    void notify(id, title, text, link);
  }
}

const questions = new Map<string, string | undefined>();
daemon.on((event) => {
  if (event.type === "sessions") {
    for (const s of event.sessions ?? []) questions.set(s.id, s.question?.id);
  } else if (event.type === "turn_complete" && event.sessionId) {
    const id = event.sessionId;
    if (looking(id) || !speaks(id) || !shouldNotify("finished", session(id)?.cwd)) return;
    const name = session(id)?.name ?? event.sessionName ?? "An agent";
    post(`finished-${id}-${Date.now() / 1000}`, `${name} finished`, event.preview ?? "Its turn is done.", id);
  } else if (event.type === "session_update" && event.session) {
    const s = event.session;
    const known = questions.has(s.id);
    const before = questions.get(s.id);
    questions.set(s.id, s.question?.id);
    // Waiting on the user now, and not before: say so, unless Agentman
    // speaks for it with a card.
    if (!known || !s.question || before === s.question.id) return;
    if (!speaks(s.id, s) || looking(s.id) || !shouldNotify("needsYou", s.cwd)) return;
    post(`needs-${s.id}-${s.question.id}`, `${s.name} needs you`, s.question.title || "It's waiting on you.", s.id);
  }
});

// What is on screen, to the daemon. Agentman forgets after two minutes
// unless told again, so it is said again every minute while anything shows.
const viewerId = `app-${crypto.randomUUID()}`;
let told: string[] | null = null;
let renewal: number | undefined;

export function tellOnScreen(again = false) {
  const shown = onScreen();
  if (!again && told && shown.join("\n") === told.join("\n")) return;
  told = shown;
  daemon.send({ type: "manager_viewing", clientId: viewerId, viewing: shown } as never);
  window.clearInterval(renewal);
  renewal = undefined;
  if (shown.length) renewal = window.setInterval(() => tellOnScreen(true), 60_000);
}

onRouteChange(() => tellOnScreen());
onActive(() => tellOnScreen());
onPanelsChange(() => tellOnScreen());
daemon.onStatus((status) => {
  if (status === "connected") tellOnScreen(true);
});
