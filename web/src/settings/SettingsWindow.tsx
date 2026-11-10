// The Settings window (Views/SettingsView.swift): its pages in a sidebar,
// General, Notifications, Devices, Permissions, Personality, Knowledge, Keys
// and Languages, the last one shown kept; the page; and, on the pages that
// edit Agentman's settings, a bar to save or revert changes. Opened by
// windows.go with ?view=settings&page=<page>; the page "pair" is Devices,
// pairing a new one.

import { useEffect, useState } from "react";
import type { ManagerSettings } from "@shared/protocol";
import { Events } from "@wailsio/runtime";
import { BellDot, BookOpen, Braces, KeyRound, MessageSquareText, Settings as Gear, ShieldCheck, Smartphone, type LucideIcon } from "lucide-react";
import { platform } from "../host";
import { usePref } from "../prefs";
import { Icon } from "../ui";
import { PushButton } from "./controls";
import { DevicesPage } from "./DevicesPage";
import { GeneralPage } from "./GeneralPage";
import { KeysPage } from "./KeysPage";
import { KnowledgePage } from "./KnowledgePage";
import { LanguagesPage } from "./LanguagesPage";
import { NotificationsPage } from "./NotificationsPage";
import { PermissionsPage } from "./PermissionsPage";
import { PersonalityPage } from "./PersonalityPage";
import { editable, saveSettings, useSettingsState } from "./state";
import "../styles/settings.css";

export type Page = "general" | "notifications" | "devices" | "permissions" | "personality" | "knowledge" | "keys" | "languages";

const PAGES: { id: Page; title: string; sf: string; icon: LucideIcon }[] = [
  { id: "general", title: "General", sf: "gearshape", icon: Gear },
  { id: "notifications", title: "Notifications", sf: "bell.badge", icon: BellDot },
  { id: "devices", title: "Devices", sf: "iphone", icon: Smartphone },
  { id: "permissions", title: "Permissions", sf: "checkmark.shield", icon: ShieldCheck },
  { id: "personality", title: "Personality", sf: "text.bubble", icon: MessageSquareText },
  { id: "knowledge", title: "Knowledge", sf: "book", icon: BookOpen },
  { id: "keys", title: "Keys", sf: "key", icon: KeyRound },
  { id: "languages", title: "Languages", sf: "curlybraces", icon: Braces },
];

const isPage = (p: string | null): p is Page => PAGES.some((x) => x.id === p);
const query = new URLSearchParams(location.search);
const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

export function SettingsWindow() {
  const [stored, setPage] = usePref<string>("settingsTab", "general");
  const page: Page = isPage(stored) ? stored : "general";
  const { settings } = useSettingsState();
  const [draft, setDraft] = useState<ManagerSettings | undefined>(settings);
  const active = useWindowActive();
  const [, setOs] = useState("");
  // Bumped by each ask to pair a device, which opens Devices pairing.
  const [pairRequest, setPairRequest] = useState(0);

  useEffect(() => {
    void platform().then(setOs);
    // A link to a page (agentman://settings/<page>) shows it, as the Mac's.
    const show = (asked: string | null) => {
      if (asked === "pair") {
        setPage("devices");
        setPairRequest((n) => n + 1);
      } else if (isPage(asked)) setPage(asked);
    };
    show(query.get("page"));
    if (!inShell) return;
    return Events.On("agentman:settings-page", (event) => show(String(event.data)));
  }, [setPage]);

  // The draft starts as the settings once they come, and stays the user's
  // until saved or reverted.
  useEffect(() => {
    if (!draft && settings) {
      // For comparisons: unsaved=1 makes a change, so the save bar shows.
      setDraft(query.get("unsaved") === "1" ? { ...settings, briefs: !settings.briefs } : settings);
    }
  }, [settings, draft]);

  const editsSettings = page === "general" || page === "permissions" || page === "personality";
  const unsaved = !!draft && !!settings && editable(draft) !== editable(settings);

  return (
    <div className={`settings ${active ? "active" : "inactive"}`}>
      <nav className="settings-sidebar">
        {PAGES.map((p) => (
          <button key={p.id} className={`settings-tab ${p.id === page ? "selected" : ""}`} aria-selected={p.id === page} onClick={() => setPage(p.id)}>
            <span className="tab-icon">
              <Icon icon={p.icon} sf={p.sf} size={13} weight="medium" />
            </span>
            <span className="tab-title">{p.title}</span>
          </button>
        ))}
      </nav>
      <div className="settings-divider-v" />
      <main className="settings-content">
        {page === "general" && <GeneralPage draft={draft} setDraft={setDraft} />}
        {page === "notifications" && <NotificationsPage />}
        {page === "devices" && <DevicesPage pairRequest={pairRequest} />}
        {page === "permissions" && <PermissionsPage draft={draft} setDraft={setDraft} />}
        {page === "personality" && <PersonalityPage draft={draft} setDraft={setDraft} />}
        {page === "knowledge" && <KnowledgePage />}
        {page === "keys" && <KeysPage />}
        {page === "languages" && <LanguagesPage />}
        {draft && unsaved && editsSettings && <SaveBar draft={draft} revert={() => setDraft(settings)} />}      </main>
    </div>
  );
}

/** Appears when a page has changes, to save or undo them. */
function SaveBar({ draft, revert }: { draft: ManagerSettings; revert: () => void }) {
  const [problem, setProblem] = useState<string>();
  const [saving, setSaving] = useState(false);
  const save = async () => {
    if (saving) return;
    setSaving(true);
    setProblem(await saveSettings(draft));
    setSaving(false);
  };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        void save();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  return (
    <div className="save-bar">
      <span className={`save-note ${problem ? "problem" : ""}`}>{problem ?? "Unsaved changes"}</span>
      <span className="row-spacer" />
      <PushButton onClick={revert}>Revert</PushButton>
      <PushButton prominent disabled={saving} onClick={() => void save()}>
        Save
      </PushButton>
    </div>
  );
}

/** Whether this window is the active one, as AppKit's controls follow. */
export function useWindowActive(): boolean {
  const [active, setActive] = useState(() => document.hasFocus());
  useEffect(() => {
    const on = () => setActive(true);
    const off = () => setActive(false);
    window.addEventListener("focus", on);
    window.addEventListener("blur", off);
    return () => {
      window.removeEventListener("focus", on);
      window.removeEventListener("blur", off);
    };
  }, []);
  return active;
}
