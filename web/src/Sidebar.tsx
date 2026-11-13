// The sidebar, as Views/Sidebar.swift draws it: the app's name, search and
// the filter menu, the agents in sections (pinned, working, the rest, and
// "Earlier here" in a folder), the archived shelf, Agentman's card and the
// connection at the foot.

import { sidebarHits, useMessageHits } from "./board/search";
import { Snippet } from "./board/parts";
import { openHit } from "./board/search";
import { agentColor, useColorBy } from "./board/colors";
import { confirmEnd, endable } from "./AgentHeader";
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent } from "react";
import { ChevronRight, Folder as FolderIcon, ListFilter, PanelLeft, Search, Settings as Gear, Sparkles, SquarePen, X, ChartSpline } from "lucide-react";
import type { SearchHit, Session } from "@shared/protocol";
import { useManagerSummary } from "./manager/summary";
import { openAgent, openAgentman, openNewAgent, useRoute, openUsage } from "./nav";
import { inShell, openSettings } from "./host";
import { shortcutLabel } from "./palette/PaletteView";
import { isMuted, setMuted } from "./notify/policy";
import { usePref, setPref } from "./prefs";
import { useAgentmanOn } from "./shell/agentman";
import { AgentRow } from "./shell/AgentRow";
import { StickerOnDots } from "./shell/Sticker";
import { useTheme } from "./settings/theme";
import { FilterPanel } from "./shell/FilterPanel";
import { useServerSystem } from "./shell/system";
import { Sym } from "./shell/Sym";
import { archive, filterState, inFolder, isArchived, makeSections, pin, shownInOrder, unarchive, unpin, useChoices, type Choices } from "./shell/sections";
import { ask, loadFolders, setShell, useShell, type FilterState } from "./shell/state";
import { refreshBranches, useStatus, useStore } from "./store";
import { contextMenu, menuUnder, type MenuEntry } from "./thread/menu";
import { AppIcon, ChromeButton, CircleLeftHalfFilled, Icon, Spacer } from "./ui";
import "./styles/sidebar.css";

export const SIDEBAR_DEFAULT = 256;
export const SIDEBAR_MIN = 208;

/** Never so wide that the conversation is squeezed below 640. */
export function clampSidebar(width: number, windowWidth: number): number {
  return Math.min(Math.max(width, SIDEBAR_MIN), Math.max(SIDEBAR_MIN, windowWidth - 640));
}

/** A session as am serve sends it to this app: with the agent's own name for
 *  one the user renamed, which the phone's types leave out. */
type AppSession = Session & { originalName?: string };
const ownName = (s: Session) => (s as AppSession).originalName;

const STATE_NAMES: Record<FilterState, string> = { waiting_input: "Needs you", busy: "Working", idle: "Idle" };

function liveIds(sessions: Session[]) {
  return new Set(sessions.map((s) => s.id));
}

export function Sidebar({ width, headerLeading }: { width: number; headerLeading: number }) {
  const { sessions, branches, version } = useStore();
  const shell = useShell();
  const look = useTheme();
  const choices = useChoices();
  const route = useRoute();
  const { status } = useStatus();
  const [workingOpen, setWorkingOpen] = usePref("sidebarWorkingOpen", true);
  const [archivedOpen, setArchivedOpen] = usePref("sidebarArchivedOpen", false);
  const [, setHidden] = usePref("sidebarHidden", false);
  const selected = route.kind === "agent" ? route.id : undefined;

  useEffect(() => {
    void loadFolders();
  }, [status]);
  // The branch under each agent's title, kept current as agents switch.
  useEffect(() => {
    if (status !== "connected") return;
    void refreshBranches();
    const timer = window.setInterval(() => void refreshBranches(), 10_000);
    return () => window.clearInterval(timer);
  }, [status, sessions.length]);

  const sections = useMemo(() => {
    const query = shell.search.trim().toLowerCase();
    const shown = sessions.filter((s) =>
      (!query || s.name.toLowerCase().includes(query) || s.cwd.toLowerCase().includes(query) || (branches[s.cwd] ?? "").toLowerCase().includes(query))
      && inFolder(s.cwd, shell.folderFilter)
      && (!shell.stateFilter || filterState(s) === shell.stateFilter));
    // The daemon's order first (state, then most recent), as the Mac's list.
    const rank = (s: Session) => ({ waiting_input: 0, busy: 1, idle: 2, ended: 3 } as Record<string, number>)[s.state] ?? 4;
    shown.sort((a, b) => rank(a) - rank(b) || (b.lastActivityAt || 0) - (a.lastActivityAt || 0));
    return makeSections(shown, choices);
  }, [sessions, branches, shell.search, shell.folderFilter, shell.stateFilter, choices]);

  const earlierHere = useMemo(() => {
    const running = liveIds(sessions);
    return shell.pastSessions.filter((s) => !running.has(s.id));
  }, [sessions, shell.pastSessions]);

  /** Up and down step through the agents, as a List's own selection would;
   *  up from the first opens Agentman. */
  function move(e: KeyboardEvent) {
    if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
    if ((e.target as HTMLElement).tagName === "INPUT") return;
    e.preventDefault();
    const ids = [...shownInOrder(sections, workingOpen, archivedOpen), ...(shell.folderFilter ? earlierHere : [])].map((s) => s.id);
    if (!ids.length) return;
    const at = selected ? ids.indexOf(selected) : -1;
    if (at < 0) {
      if (e.key === "ArrowDown") openAgent(ids[0]);
      return;
    }
    if (e.key === "ArrowDown") {
      if (at + 1 < ids.length) openAgent(ids[at + 1]);
    } else if (at === 0) openAgentman();
    else openAgent(ids[at - 1]);
  }

  // Searching, what was said too: a row shows the line that matched, and
  // the conversations that matched by what they said alone are listed under
  // the rest (SearchStore.sidebarHits).
  const hits = useMessageHits(shell.search);
  const found = useMemo(() => sidebarHits(shell.search, hits, sections, sessions, shell.folderFilter, shell.stateFilter), [shell.search, hits, sections, sessions, shell.folderFilter, shell.stateFilter]);
  const row = (s: Session, inList = true) => (
    <AgentButton key={s.id} session={s} branch={branches[s.cwd]} selected={s.id === selected} inList={inList} choices={choices} live={sessions}
      match={found.bySession[s.id]} query={found.query} />
  );

  const { pinned, needsYou, working, active, archived } = sections;
  const nothingFound = !!found.query && !found.searching && !pinned.length && !needsYou.length && !working.length && !active.length && !archived.length && !found.others.length;
  const listRef = useRef<HTMLDivElement>(null);

  return (
    <nav className="sidebar" style={{ width }}>
      <div className="sidebar-top">
        <div className="sidebar-header drag" style={{ paddingLeft: headerLeading }}>
          <AppIcon size={20} />
          <span className="app-name">Agentman</span>
          <Spacer />
          <span className="no-drag"><ChromeButton icon={PanelLeft} sf="sidebar.left" title="Hide the sidebar (⌃⌘S)" onClick={() => setHidden(true)} /></span>
        </div>
        <div className="search-row">
          {/* In New, a field of its own: edged, a little raised. */}
          <span className="search-field">
          <span className="search-icon"><Icon icon={Search} sf="magnifyingglass" size={12} /></span>
          <input
            id="search"
            aria-label="Search agents and messages"
            placeholder="Search"
            spellCheck={false}
            value={shell.search}
            onChange={(e) => setShell({ search: e.target.value })}
            // Escape clears it, as the Mac's does.
            onKeyDown={(e) => e.key === "Escape" && setShell({ search: "" })}
          />
          {/* A browser has no menu bar to show ⌘K in (Go › Command
              Palette…), so the field shows it, and a click opens it. */}
          {!inShell && !shell.search && (
            <button className="palette-hint" title="Go to an agent, run a command, or ask Agentman" aria-label="Command palette"
              onMouseDown={(e) => e.preventDefault()} onClick={() => setShell({ showPalette: true })}>
              {shortcutLabel("⌘K")}
            </button>
          )}
          </span>
          <FilterMenu sessions={sessions} />
          <button className="new-agent-button" title={withKey("New agent", "⌘N")} aria-label="New agent" onClick={() => openNewAgent()}>
            <Icon icon={SquarePen} sf="square.and.pencil" size={12} />
          </button>
        </div>
      </div>
      <div
        className="agent-list"
        ref={listRef}
        tabIndex={-1}
        onKeyDown={move}
        onMouseDown={(e) => {
          if ((e.target as HTMLElement).closest(".agent-row")) listRef.current?.focus({ preventScroll: true });
        }}
      >
        {(shell.folderFilter || shell.stateFilter) && <ActiveFilters />}
        {pinned.length > 0 && (
          <>
            <SectionHeading title="Pinned" />
            {pinned.map((s) => row(s))}
          </>
        )}
        {needsYou.length > 0 && (
          <>
            <SectionHeading title="Needs you" count={needsYou.length} tint />
            {needsYou.map((s) => row(s))}
          </>
        )}
        {working.length > 0 && (
          <>
            <SectionHeading title="Working" count={working.length} open={workingOpen} onToggle={() => setWorkingOpen(!workingOpen)} />
            {workingOpen && working.map((s) => row(s))}
          </>
        )}
        {active.length > 0 && (pinned.length > 0 || needsYou.length > 0 || working.length > 0) && <div className="section-rule" />}
        {active.map((s) => row(s))}
        {found.others.length > 0 && (
          <>
            <div className="earlier-head">In messages</div>
            {found.others.map((hit) => hit.session && (
              <AgentButton key={`hit-${hit.session.id}`} session={hit.session} branch={branches[hit.session.cwd]} selected={hit.session.id === selected}
                inList choices={choices} live={sessions} match={hit} query={found.query} />
            ))}
          </>
        )}
        {nothingFound && (
          <div className="nothing-found">
            <Icon icon={Search} sf="magnifyingglass" size={22} />
            <div className="nothing-found-title">Nothing found</div>
            <div className="nothing-found-text">
              {shell.folderFilter || shell.stateFilter
                ? `No agent matches \u201C${found.query}\u201D with these filters.`
                : `No agent or message matches \u201C${found.query}\u201D.`}
            </div>
          </div>
        )}
        {/* In a folder, the sessions that ran there and ended: read them, or resume one. */}
        {shell.folderFilter && earlierHere.length > 0 && (
          <>
            <div className="earlier-head">Earlier here</div>
            {earlierHere.map((s) => (
              <AgentButton key={s.id} session={s} branch={branches[s.cwd]} selected={s.id === selected} inList earlier choices={choices} live={sessions} />
            ))}
          </>
        )}
        {sessions.length === 0 && status === "connected" && (
          look === "site" ? (
            // The website's terminal sticker on its dots, a Rubik title, the
            // commands set as code so they never break across lines, and the
            // site's orange button (EmptySidebar).
            <div className="no-agents site">
              <StickerOnDots name="terminal" size={118} />
              <div className="no-agents-title">No agents yet</div>
              <div className="no-agents-text">Start one here, or with <code>am claude</code> or <code>am codex</code> in a terminal.</div>
              <button className="no-agents-button" onClick={() => openNewAgent()}>
                <Icon icon={SquarePen} sf="square.and.pencil" size={12} />
                New Agent
              </button>
            </div>
          ) : (
            <div className="no-agents">
              <Icon icon={Sparkles} sf="sparkles" size={37} weight="regular" />
              <div className="no-agents-title">No agents yet</div>
              <div className="no-agents-text">Start one here, or with am claude or am codex in a terminal.</div>
              <button className="no-agents-button" onClick={() => openNewAgent()}>
                <Icon icon={SquarePen} sf="square.and.pencil" size={10.5} />
                New Agent
              </button>
            </div>
          )
        )}
      </div>
      <div className="sidebar-bottom">
        {archived.length > 0 && (
          <div className="shelf">
            <SectionHeading title="Archived" count={archived.length} open={archivedOpen} onToggle={() => setArchivedOpen(!archivedOpen)} shelf />
            {archivedOpen && (
              <div className="shelf-list" style={{ maxHeight: Math.min(archived.length * 80, 190) }}>
                {archived.map((s) => row(s, false))}
              </div>
            )}
          </div>
        )}
        <AgentmanCard selected={route.kind === "agentman"} sessions={sessions} />
        <ConnectionFooter version={version} />
      </div>
    </nav>
  );
}

/** An agent in the list, opening its thread when clicked; its right-click
 *  menu renames, pins, archives and mutes. */
function AgentButton({ session, branch, selected, inList, earlier, choices, live, match, query = "" }: {
  session: Session; branch?: string; selected: boolean; inList: boolean; earlier?: boolean; choices: Choices; live: Session[];
  /** What a search found in its conversation, which a click opens at. */
  match?: SearchHit; query?: string;
}) {
  const agentmanOn = useAgentmanOn();
  const [colorBy] = useColorBy();
  const { renaming } = useShell();
  function menu(e: MouseEvent) {
    const ids = liveIds(live);
    const entries: MenuEntry[] = [
      { label: "Rename…", action: () => setShell({ renaming: session.id }) },
      { label: "Show Project", action: () => setShell({ folderFilter: session.cwd }) },
    ];
    if (ownName(session)) {
      entries.push({ label: `Use the agent's own name, “${ownName(session)}”`, action: () => void rename(session.id, "") });
    }
    entries.push({ separator: true });
    entries.push(choices.pinned.includes(session.id)
      ? { label: "Unpin", action: () => unpin(session.id, ids) }
      : { label: "Pin", action: () => pin(session.id, ids) });
    entries.push(isArchived(session, choices)
      ? { label: "Unarchive", action: () => unarchive(session.id, ids) }
      : { label: "Archive", action: () => archive(session.id, ids) });
    if (endable(session)) entries.push({ label: "End Session…", action: () => void confirmEnd(session) });
    entries.push({ separator: true });
    // Agentman speaks for the agents while it is on; this one can speak for itself as well.
    if (agentmanOn) {
      entries.push({
        label: "Notify Me About This Agent",
        checked: session.alerts === true,
        action: () => void ask({ type: "set_agent_alerts", sessionId: session.id, alerts: session.alerts !== true }),
      });
    }
    const muted = isMuted(session.cwd);
    entries.push({
      label: muted ? "Unmute Notifications From This Folder" : "Mute Notifications From This Folder",
      action: () => setMuted(session.cwd, !muted),
    });
    contextMenu(e, entries);
  }
  // Archived, its colour goes grey with the rest of it.
  const color = agentColor(session, colorBy);
  const tint = color && isArchived(session, choices) ? "var(--faint)" : color;
  return (
    <div
      className={`agent-row ${inList ? "in-list" : "on-shelf"} ${earlier ? "earlier" : ""} ${selected ? "selected" : ""}`}
      role="button"
      aria-selected={selected}
      onClick={() => (match ? openHit(match, query) : openAgent(session.id))}
      onContextMenu={earlier ? undefined : menu}
    >
      {tint && <span className="agent-tint" style={{ background: tint }} aria-hidden />}
      <AgentRow session={session} branch={branch} archived={isArchived(session, choices)} title={renaming === session.id ? <RenameField session={session} /> : undefined} />
      {match && <div className="agent-match" title={match.snippet}><Snippet hit={match} query={query} /></div>}
    </div>
  );
}

async function rename(sessionId: string, name: string) {
  try {
    await ask({ type: "rename_session", sessionId, text: name.trim() });
  } catch {
    // The name stays as it was.
  }
}

/** A session's name, edited where it shows: Return keeps it, Escape or
 *  clicking away leaves it as it was. */
function RenameField({ session }: { session: Session }) {
  const [name, setName] = useState(session.name);
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    ref.current?.focus();
    // The caret at the end, as a focused NSTextField puts it.
    const end = ref.current?.value.length ?? 0;
    ref.current?.setSelectionRange(end, end);
  }, []);
  function save() {
    const next = name.trim();
    setShell({ renaming: null });
    if (next !== session.name) void rename(session.id, next);
  }
  return (
    <input
      ref={ref}
      className="rename-field"
      aria-label="Name"
      placeholder="Name"
      value={name}
      spellCheck={false}
      onClick={(e) => e.stopPropagation()}
      onChange={(e) => setName(e.target.value)}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Enter") save();
        if (e.key === "Escape") setShell({ renaming: null });
      }}
      onBlur={() => setShell({ renaming: null })}
    />
  );
}

/** One button for what the list shows: agents in one state, and in one
 *  folder, with how many are in each. */
/** The filter button and its panel (FilterMenu, FilterPanel.swift). */
function FilterMenu({ sessions }: { sessions: Session[] }) {
  const { stateFilter, folderFilter } = useShell();
  const filtering = !!stateFilter || !!folderFilter;
  const button = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);
  return (
    <>
      <button
        ref={button}
        className={`filter-button ${filtering ? "filtering" : ""}`}
        title="Show agents in one state, or in one folder with the sessions that ended there"
        aria-label="Filter"
        aria-expanded={open}
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => setOpen((was) => !was)}
      >
        <Icon icon={ListFilter} sf="line.3.horizontal.decrease" size={12} />
      </button>
      {open && <FilterPanel sessions={sessions} anchor={button.current} close={close} />}
    </>
  );
}

/** What the list is narrowed to, above it, each undone with its ×. */
function ActiveFilters() {
  const { folderFilter, stateFilter } = useShell();
  const token = (title: string, folder: boolean, clear: () => void) => (
    <button className="filter-token" title="Show every agent again" onClick={clear}>
      {folder && <Sym icon={FolderIcon} sf="folder" size={10} weight="medium" />}
      <span className="filter-token-title">{title}</span>
      <Sym className="filter-token-x" icon={X} sf="xmark" size={8} weight="bold" />
    </button>
  );
  return (
    <div className="active-filters">
      {folderFilter && token(folderFilter.replace(/\/+$/, "").split("/").pop() || folderFilter, true, () => setShell({ folderFilter: null }))}
      {stateFilter && token(STATE_NAMES[stateFilter], false, () => setShell({ stateFilter: null }))}
    </div>
  );
}

/** A heading in the list: a section's name, and for one that folds, how
 *  many are in it and a chevron that opens and closes it. */
function SectionHeading({ title, count, open, onToggle, shelf, tint }: { title: string; count?: number; open?: boolean; onToggle?: () => void; shelf?: boolean; tint?: boolean }) {
  return (
    <div
      className={`section-head ${onToggle ? "folds" : ""} ${shelf ? "shelf-head" : ""} ${tint ? "needs-you" : ""}`}
      role={onToggle ? "button" : "heading"}
      aria-expanded={onToggle ? open : undefined}
      onClick={onToggle}
    >
      <span className="section-title">{title}</span>
      <Spacer min={4} />
      {count !== undefined && <span className="count">{count}</span>}
      {onToggle && (
        <span className={`chevron ${open ? "open" : ""}`}><Icon icon={ChevronRight} sf="chevron.right" size={9} weight="semibold" /></span>
      )}
    </div>
  );
}

/** Agentman, at the foot: its mark, what it has to say, and anything new
 *  since the user last looked. */
function AgentmanCard({ selected, sessions }: { selected: boolean; sessions: Session[] }) {
  const summary = useManagerSummary();
  const unread = selected ? 0 : summary.unread;
  const { status } = useStatus();
  const waiting = sessions.filter((s) => filterState(s) === "waiting_input").length;
  const working = sessions.filter((s) => s.state === "busy").length;
  const attention = summary.cardsWaiting > 0 || waiting > 0;
  // AgentmanCard.summary: what it has for you, what needs you, what's new,
  // what's working; quiet is only known while connected.
  const text = summary.cardsWaiting > 0
    ? summary.cardsWaiting === 1 ? "1 thing needs you" : `${summary.cardsWaiting} things need you`
    : waiting > 0 ? waiting === 1 ? "1 agent needs you" : `${waiting} agents need you`
    : unread > 0 ? unread === 1 ? "1 new message" : `${unread} new messages`
    : working > 0 ? working === 1 ? "1 agent working" : `${working} agents working`
    : status === "connected" ? "All quiet" : "Not connected";
  return (
    <div className="agentman-card-wrap">
      <button className={`agentman-card ${selected ? "selected" : ""}`} title="Agentman (⌥⌘0)" aria-selected={selected} onClick={openAgentman}>
        <AppIcon size={19} />
        <span className="agentman-text">
          <span className="agentman-title">Agentman</span>
          <span className={`agentman-summary ${attention ? "needs" : ""}`}>{text}</span>
        </span>
        <Spacer min={4} />
        {unread > 0 && <span className="unread-badge" aria-label={`${unread} new`}>{unread > 99 ? "99+" : unread}</span>}
      </button>
    </div>
  );
}

/** Whether the app can reach am serve, and which version it is. */
function ConnectionFooter({ version }: { version?: string }) {
  const { status } = useStatus();
  // The computer am serve runs on, which this page is on: a Mac says so.
  const mac = useServerSystem() === "darwin";
  const connected = mac ? "Mac connected" : "Connected";
  const text = status === "connected" ? (version ? `${connected} · am ${version}` : connected)
    // The whole story, and the way back, is in the banner over the page.
    : status === "connecting" ? "Connecting…" : "Not connected";
  const [socket, setSocket] = useState("");
  useEffect(() => {
    void import("./host").then(({ endpoint }) => endpoint()).then((e) => setSocket(e.url)).catch(() => {});
  }, [status]);
  return (
    <footer className="sidebar-footer" title={socket}>
      <span className={`conn ${status}`} />
      <span className="conn-text">{text}</span>
      {/* SwiftUI's Spacer keeps 8 at least. */}
      <Spacer min={8} />
      <UsageButton />
      <AppearanceMenu />
      {!inShell && <SettingsButton />}
    </footer>
  );
}

/** "New agent (⌘N)", or just "New agent" where the key isn't the page's
 *  (a browser keeps ⌘N for a window of its own). */
function withKey(words: string, key: string) {
  const label = shortcutLabel(key);
  return label ? `${words} (${label})` : words;
}

/** Settings, in a browser, which has no menu bar to hold it (the Mac's
 *  Agentman › Settings…, ⌘,); the desktop shell keeps it in its menu. */
function SettingsButton() {
  return (
    <button className="settings-button" title={withKey("Settings", "⌘,")} aria-label="Settings" onMouseDown={(e) => e.preventDefault()} onClick={() => void openSettings()}>
      <Icon icon={Gear} sf="gearshape" size={13} />
    </button>
  );
}

/** Opens the usage page, beside the appearance switch (Sidebar.swift's
 *  UsageButton). */
function UsageButton() {
  const route = useRoute();
  return (
    <button className={`usage-button${route.kind === "usage" ? " open" : ""}`} title="Usage: what the agents spent" aria-label="Usage" onClick={openUsage}>
      <Icon icon={ChartSpline} sf="chart.xyaxis.line" size={13} />
    </button>
  );
}

/** Light, Dark, or Match the Mac (SettingsView.swift's AppearanceMenu). */
function AppearanceMenu() {
  const [appearance] = usePref("appearance", "system");
  return (
    <button
      className="appearance"
      title="Appearance: Light, Dark, or Match the Mac"
      aria-label="Appearance"
      onMouseDown={(e) => e.preventDefault()}
      onClick={(e) => menuUnder(e.currentTarget, [
        { label: "Light", checked: appearance === "light", action: () => setPref("appearance", "light") },
        { label: "Dark", checked: appearance === "dark", action: () => setPref("appearance", "dark") },
        { label: "Match the Mac", checked: appearance === "system", action: () => setPref("appearance", "system") },
      ])}
    >
      <Icon icon={CircleLeftHalfFilled} sf="circle.lefthalf.filled" size={13} />
    </button>
  );
}
