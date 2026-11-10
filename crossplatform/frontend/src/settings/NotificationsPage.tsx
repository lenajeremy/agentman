// Notifications (Views/SettingsView.swift NotificationsPage): when this
// computer speaks up: which kinds, folders kept quiet, quiet hours, the
// summary on coming back, and sound. Kept in this app's settings under the
// Mac's own keys (App/NotificationPolicy.swift), which notify/policy.ts reads.

import { useEffect } from "react";
import { usePref } from "../prefs";
import { MenuButton, PopUp, TimeField, Toggle, tilde } from "./controls";
import { Divider, Row, Section, SettingsPage } from "./layout";
import { Icon } from "../ui";
import { BellOff } from "lucide-react";
import { LinkButton } from "./controls";
import { pick, useHome } from "./native";
import { loadFolders, useSettingsState } from "./state";

/** The Mac's NotificationPolicy.Key names. */
export const NotifyKey = {
  finished: "notifyFinished",
  needsYou: "notifyNeedsYou",
  agentman: "notifyAgentman",
  notes: "notifyNotes",
  sound: "notifySound",
  mutedFolders: "mutedFolders",
  quiet: "quietHours",
  quietStart: "quietStart",
  quietEnd: "quietEnd",
  quietLetsNeedsYou: "quietLetsNeedsYou",
  everyAgent: "notifyEveryAgent",
} as const;

/** Minutes after midnight quiet hours start and end, by default. */
export const DEFAULT_QUIET_START = 22 * 60;
export const DEFAULT_QUIET_END = 8 * 60;

export function NotificationsPage() {
  const [finished, setFinished] = usePref(NotifyKey.finished, true);
  const [needsYou, setNeedsYou] = usePref(NotifyKey.needsYou, true);
  const [agentman, setAgentman] = usePref(NotifyKey.agentman, true);
  const [notes, setNotes] = usePref(NotifyKey.notes, true);
  const [sound, setSound] = usePref(NotifyKey.sound, true);
  const [quiet, setQuiet] = usePref(NotifyKey.quiet, false);
  const [quietStart, setQuietStart] = usePref(NotifyKey.quietStart, DEFAULT_QUIET_START);
  const [quietEnd, setQuietEnd] = usePref(NotifyKey.quietEnd, DEFAULT_QUIET_END);
  const [quietLetsNeedsYou, setQuietLetsNeedsYou] = usePref(NotifyKey.quietLetsNeedsYou, true);
  const [everyAgent, setEveryAgent] = usePref(NotifyKey.everyAgent, false);
  const [muted, setMutedList] = usePref<string[]>(NotifyKey.mutedFolders, []);
  const [awaySummary, setAwaySummary] = usePref("awaySummary", true);
  const [awayMinutes, setAwayMinutes] = usePref("awayMinutes", 15);
  const { folders } = useSettingsState();
  const home = useHome();

  useEffect(() => {
    void loadFolders();
  }, []);

  const setMuted = (folder: string, on: boolean) => {
    const next = muted.filter((f) => f !== folder);
    if (on) next.push(folder);
    setMutedList(next.sort());
  };

  const toggle = (title: string, detail: string | undefined, value: boolean, set: (v: boolean) => void, divider = true, disabled = false) => (
    <Row title={title} detail={detail} divider={divider} className={disabled ? "dimmed" : ""}>
      <Toggle on={value} onChange={set} disabled={disabled} />
    </Row>
  );

  return (
    <SettingsPage title="Notifications">
      <Section title="Agentman">
        {toggle("Agentman's messages", "Its replies and briefs while you're elsewhere.", agentman, setAgentman)}
        {toggle("Note suggestions", "Quietly, when it suggests something worth remembering.", notes, setNotes, false)}
      </Section>
      <Section
        title="Agents"
        footer="Never for the thread you're looking at. While Agentman is on, it tells you what matters from your agents, so they only notify you themselves when you ask: here for all of them, or one at a time from an agent's menu in the sidebar."
      >
        {toggle("An agent finishes", "When it ends a turn, with what it said last.", finished, setFinished)}
        {toggle("An agent needs you", "When it waits on an approval or a question.", needsYou, setNeedsYou)}
        {toggle("Every agent, while Agentman is on", "Hear from each agent itself too, not only through Agentman.", everyAgent, setEveryAgent, false)}
      </Section>
      <Section title="Quiet hours" footer="Nothing else comes through in quiet hours; it's all in Agentman when you look.">
        {toggle("Use quiet hours", undefined, quiet, setQuiet)}
        <Row title="From" className={quiet ? "" : "dimmed"}>
          <div className="hstack" style={{ gap: 8 }}>
            <TimeField minutes={quietStart} onChange={setQuietStart} disabled={!quiet} />
            <span className="row-value">to</span>
            <TimeField minutes={quietEnd} onChange={setQuietEnd} disabled={!quiet} />
          </div>
        </Row>
        {toggle("Still tell me when an agent needs me", undefined, quietLetsNeedsYou, setQuietLetsNeedsYou, false, !quiet)}
      </Section>
      <Section
        title="Muted folders"
        footer="Agents working in these folders, or in folders inside them, don't notify. You can also mute a folder from its name in the sidebar."
      >
        {muted.length === 0 && (
          <>
            <div className="card-line muted-none">None</div>
            <Divider />
          </>
        )}
        {muted.map((folder) => (
          <div key={folder}>
            <div className="card-line muted-folder">
              <Icon icon={BellOff} sf="bell.slash" size={11} />
              <span className="folder-path">{tilde(folder, home)}</span>
              <span className="row-spacer" />
              <LinkButton className="caption" onClick={() => setMuted(folder, false)}>
                Unmute
              </LinkButton>
            </div>
            <Divider />
          </div>
        ))}
        <div className="card-line add-line">
          <MenuButton
            entries={() => [
              ...folders
                .slice(0, 15)
                .filter((f) => !muted.includes(f.path))
                .map((f) => ({ label: tilde(f.path, home), action: () => setMuted(f.path, true) })),
              { separator: true },
              {
                label: "Choose…",
                action: () =>
                  void pick({ folders: true, button: "Mute", start: home }).then((paths) => {
                    if (paths[0]) setMuted(paths[0], true);
                  }),
              },
            ]}
          >
            Mute a Folder…
          </MenuButton>
        </div>
      </Section>
      <Section
        title="While you were away"
        footer="Back at the Mac after a while (the app in the background, the screen locked or the Mac asleep), a summary of what each agent did and what needs you."
      >
        {toggle("Show a summary when I'm back", undefined, awaySummary, setAwaySummary)}
        <Row title="After being away for" divider={false} className={awaySummary ? "" : "dimmed"}>
          <PopUp
            value={awayMinutes}
            disabled={!awaySummary}
            options={[
              { value: 10, label: "10 minutes" },
              { value: 15, label: "15 minutes" },
              { value: 30, label: "30 minutes" },
              { value: 60, label: "1 hour" },
              { value: 120, label: "2 hours" },
            ]}
            onChange={setAwayMinutes}
          />
        </Row>
      </Section>
      <Section title="Sound">{toggle("Play a sound", "With each notification but note suggestions.", sound, setSound, false)}</Section>
    </SettingsPage>
  );
}
