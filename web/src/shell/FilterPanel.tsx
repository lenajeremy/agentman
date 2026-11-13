// What the sidebar's filter button opens (Views/FilterPanel.swift): the
// agents to show, by state and by folder (any folder at all), and what
// colours them. A popover under the button: a click elsewhere or Escape
// closes it.

import { useEffect, useRef, useState } from "react";
import { Check, FolderPlus } from "lucide-react";
import type { Session } from "@shared/protocol";
import { COLOR_BY, useColorBy } from "../board/colors";
import { hostCall, pickFolder } from "../host";
import { folderLabel } from "../labels";
import { usePref } from "../prefs";
import { Segments } from "../ui";
import { candidates, CHOSEN_KEY, place, remember, shown, type Chosen } from "./folderChoices";
import { cachedHome } from "./native";
import { filterState, inFolder } from "./sections";
import { loadFolders, setShell, useShell, type FilterState } from "./state";
import { Sym } from "./Sym";

const STATES: (FilterState | null)[] = [null, "waiting_input", "busy", "idle"];

const counted = (title: string, count: number) => (count > 0 ? `${title} ${count}` : title);

export function FilterPanel({ sessions, anchor, close }: { sessions: Session[]; anchor: HTMLElement | null; close: () => void }) {
  const { stateFilter, folderFilter, folders } = useShell();
  const [colorBy, setColorBy] = useColorBy();
  const [chosen, setChosen] = usePref<Chosen>(CHOSEN_KEY, {});
  const panel = useRef<HTMLDivElement>(null);
  const home = cachedHome();
  // Whether each folder offered is still there, as the Mac asks its disk.
  const [there, setThere] = useState<Record<string, boolean>>({});
  const paths = candidates(folders, chosen, home);
  const choices = shown(paths, (path) => there[path] === true, folderFilter);
  const checking = paths.slice(0, 24).filter((path) => there[path] === undefined).join("\n");

  useEffect(() => void loadFolders(), []);
  useEffect(() => {
    if (!checking) return;
    let stale = false;
    const ask = checking.split("\n");
    void Promise.all(ask.map((path) => hostCall<{ Exists?: boolean }>("Files.Folder", path).then((f) => f?.Exists === true, () => false)))
      .then((found) => !stale && setThere((known) => ({ ...known, ...Object.fromEntries(ask.map((path, i) => [path, found[i]])) })));
    return () => { stale = true; };
  }, [checking]);

  // A popover: a click elsewhere or Escape closes it.
  useEffect(() => {
    const down = (e: MouseEvent) => {
      if (!panel.current?.contains(e.target as Node) && !anchor?.contains(e.target as Node)) close();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        close();
      }
    };
    document.addEventListener("mousedown", down);
    window.addEventListener("keydown", key, true);
    return () => {
      document.removeEventListener("mousedown", down);
      window.removeEventListener("keydown", key, true);
    };
  }, [anchor, close]);

  const counts = { waiting_input: 0, busy: 0, idle: 0 };
  for (const s of sessions) if (inFolder(s.cwd, folderFilter)) counts[filterState(s)] += 1;
  const running = (path: string) => sessions.filter((s) => s.state !== "ended" && inFolder(s.cwd, path)).length;
  const pick = (folder: string | null) => {
    setShell({ folderFilter: folder });
    close();
  };
  const choose = async () => {
    close();
    const path = await pickFolder("Choose a folder to show the agents that work in it", folderFilter ?? home, { button: "Show", newFolder: true });
    if (!path) return;
    setChosen(remember(chosen, path));
    setShell({ folderFilter: path });
  };

  const box = anchor?.getBoundingClientRect();
  // Under the button, its middle on the button's, kept on the page.
  const left = box ? Math.max(8, Math.min(window.innerWidth - 308, box.left + box.width / 2 - 150)) : 8;
  const top = box ? box.bottom + 8 : 40;

  return (
    <div className="filter-panel" ref={panel} style={{ left, top }} role="dialog" aria-label="Filter">
      <div className="filter-heading">Show</div>
      <Segments
        options={STATES}
        value={stateFilter}
        title={(state) => state === null ? "All" : state === "waiting_input" ? counted("Needs you", counts.waiting_input) : state === "busy" ? counted("Working", counts.busy) : counted("Idle", counts.idle)}
        onChange={(state) => setShell({ stateFilter: state })}
      />
      <div className="filter-heading spaced">Folder</div>
      <div className="filter-rows">
        <FilterRow name="All folders" chosen={!folderFilter} onClick={() => pick(null)} />
        {choices.map((path) => (
          <FilterRow key={path} name={folderLabel(path)} place={place(path, home)} count={running(path)} chosen={folderFilter === path} onClick={() => pick(path)} />
        ))}
        <FilterRow name="Choose a folder…" chooser chosen={false} onClick={() => void choose()} />
      </div>
      <div className="filter-heading spaced">Color agents by</div>
      <Segments options={COLOR_BY.map((c) => c.id)} value={colorBy} title={(id) => (id === "none" ? "None" : COLOR_BY.find((c) => c.id === id)!.title)} onChange={setColorBy} />
    </div>
  );
}

/** One choice in the panel's folder list: its name, where it is, and how
 *  many agents work there now. */
function FilterRow({ name, place, count = 0, chooser = false, chosen, onClick }: {
  name: string; place?: string; count?: number; chooser?: boolean; chosen: boolean; onClick: () => void;
}) {
  return (
    <button className={`filter-row ${chosen ? "chosen" : ""} ${chooser ? "chooser" : ""}`} onClick={onClick} title={place ? `${place}/${name}` : undefined} aria-pressed={chooser ? undefined : chosen}>
      <span className="filter-row-mark">
        {chooser ? <Sym icon={FolderPlus} sf="folder.badge.plus" size={11.5} /> : <Sym icon={Check} sf="checkmark" size={10} weight="semibold" />}
      </span>
      <span className="filter-row-name">{name}</span>
      {/* Whole or not at all: a cut place says nothing. Where it doesn't
          fit it wraps, out of sight, behind an empty first line. */}
      <span className="filter-row-place-slot"><span />{place && <span className="filter-row-place">{place}</span>}</span>
      {count > 0 && <span className="filter-row-count">{count}</span>}
    </button>
  );
}
