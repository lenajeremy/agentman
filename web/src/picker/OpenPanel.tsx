// The open panel, drawn by the page in a browser where there is no
// NSOpenPanel to ask for: macOS's layout (the sidebar's places, back and
// forward, the folder's path menu, a search field, the list by name, date,
// size and kind; New Folder, Cancel and the default button), browsing the
// computer through am serve (internal/apphost's browse.go), so it answers
// with paths there, as the Mac's panel does. Its keys are the Mac's: ↑↓,
// Return, Escape, ⌘↑ and ⌘↓, ⌘[ and ⌘], ⇧⌘. for hidden files, ⇧⌘G (or a
// typed / or ~) to go to a folder, ⇧⌘H home, ⇧⌘D the desktop.

import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createRoot } from "react-dom/client";
import {
  AppWindow, ChevronDown, ChevronLeft, ChevronRight, ChevronUp, CircleArrowDown, File, FileArchive, FileCode, FileImage,
  FileText, Folder, HardDrive, House, LayoutGrid, Monitor, Search, type LucideIcon,
} from "lucide-react";
import { hostCall } from "../host";
import { popupMenu, type MenuEntry } from "../thread/menu";
import { Icon } from "../ui";
import "../styles/picker.css";

export interface OpenOptions {
  /** Folders to choose (canChooseDirectories), or else files. */
  folders?: boolean;
  multiple?: boolean;
  /** Only pictures (allowedContentTypes [.image]). */
  images?: boolean;
  /** The line above the panel's toolbar, as panel.message. */
  message?: string;
  /** The default button's word, as panel.prompt; "Open" by default. */
  button?: string;
  /** The folder it opens in; home when there is none. */
  start?: string;
  /** New Folder, as canCreateDirectories. */
  newFolder?: boolean;
}

interface Entry {
  name: string;
  path: string;
  dir: boolean;
  package?: boolean;
  hidden?: boolean;
  size: number;
  modified: number;
}

interface Listing {
  path: string;
  entries: Entry[];
  truncated?: boolean;
}

interface Place {
  name: string;
  path: string;
  kind: string;
}

interface Places {
  home: string;
  favorites: Place[];
  locations: Place[];
}

type SortKey = "name" | "modified" | "size" | "kind";

const IMAGE = /\.(png|jpe?g|gif|webp|heic|heif|bmp|tiff?|svg)$/i;

let showing = false;

/** Opens the panel; the paths chosen, or none when cancelled. */
export function openPanel(options: OpenOptions): Promise<string[]> {
  if (showing) return Promise.resolve([]);
  showing = true;
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  const before = document.activeElement as HTMLElement | null;
  return new Promise((resolve) => {
    const done = (paths: string[]) => {
      root.unmount();
      host.remove();
      showing = false;
      before?.focus?.();
      resolve(paths);
    };
    root.render(<OpenPanel options={options} onDone={done} />);
  });
}

function OpenPanel({ options, onDone }: { options: OpenOptions; onDone: (paths: string[]) => void }) {
  const [places, setPlaces] = useState<Places | null>(null);
  const [path, setPath] = useState("");
  const [listing, setListing] = useState<Listing | null>(null);
  const [problem, setProblem] = useState("");
  const [back, setBack] = useState<string[]>([]);
  const [forward, setForward] = useState<string[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [anchor, setAnchor] = useState(-1);
  const [filter, setFilter] = useState("");
  const [hidden, setHidden] = useState(false);
  const [sort, setSort] = useState<{ key: SortKey; up: boolean }>({ key: "name", up: true });
  const [asking, setAsking] = useState<null | "goto" | "folder">(null);
  const [typed, setTyped] = useState("");
  const [askProblem, setAskProblem] = useState("");
  const list = useRef<HTMLDivElement>(null);
  const typeahead = useRef({ text: "", at: 0 });

  // Where it opens: the folder asked for if it is there, else home.
  useEffect(() => {
    void (async () => {
      const found = (await hostCall<Places>("Files.Places").catch(() => null)) ?? { home: "/", favorites: [], locations: [] };
      setPlaces(found);
      const start = options.start || found.home || "/";
      const info = await hostCall<{ exists: boolean }>("Files.Folder", start).catch(() => ({ exists: false }));
      setPath(info.exists ? start : found.home || "/");
    })();
  }, []);

  useEffect(() => {
    if (!path) return;
    let current = true;
    setProblem("");
    hostCall<Listing>("Files.List", path, hidden)
      .then((l) => current && setListing(l))
      .catch((err) => {
        if (!current) return;
        setListing({ path, entries: [] });
        const name = path === "/" ? path : path.split("/").pop();
        const why = String(err instanceof Error ? err.message : err);
        setProblem(/permission|not permitted/i.test(why)
          ? `The folder “${name}” can’t be opened because you don’t have permission to see its contents.`
          : `The folder “${name}” can’t be opened.`);
      });
    return () => {
      current = false;
    };
  }, [path, hidden]);

  useEffect(() => list.current?.focus(), [listing]);

  const choosable = (e: Entry) =>
    options.folders ? e.dir && !e.package : (!e.dir || !!e.package) && (!options.images || IMAGE.test(e.name));
  const enterable = (e: Entry) => e.dir && !e.package;
  const usable = (e: Entry) => choosable(e) || enterable(e);

  const rows = useMemo(() => {
    const words = filter.trim().toLowerCase();
    const shown = (listing?.entries ?? []).filter((e) => !words || e.name.toLowerCase().includes(words));
    const by = (e: Entry): string | number =>
      sort.key === "modified" ? e.modified : sort.key === "size" ? (e.dir ? -1 : e.size) : sort.key === "kind" ? kindOf(e) : e.name.toLowerCase();
    return [...shown].sort((a, b) => {
      const x = by(a);
      const y = by(b);
      const order = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y), undefined, { numeric: true });
      return (sort.up ? order : -order) || a.name.localeCompare(b.name, undefined, { numeric: true });
    });
  }, [listing, filter, sort]);

  const go = (to: string, remember = true) => {
    if (!to || to === path) return;
    if (remember && path) {
      setBack((b) => [...b, path]);
      setForward([]);
    }
    setPath(to);
    setSelected([]);
    setAnchor(-1);
    setFilter("");
  };
  const goBack = () => {
    const to = back[back.length - 1];
    if (!to) return;
    setBack((b) => b.slice(0, -1));
    setForward((f) => [path, ...f]);
    go(to, false);
  };
  const goForward = () => {
    const to = forward[0];
    if (!to) return;
    setForward((f) => f.slice(1));
    setBack((b) => [...b, path]);
    go(to, false);
  };
  const parent = (p: string) => (p === "/" ? "" : p.replace(/\/[^/]+\/?$/, "") || "/");

  const chosen = rows.filter((e) => selected.includes(e.path));
  const chosenUsable = chosen.filter(choosable);
  // A folder selected while files are wanted: the button opens it.
  const opensFolder = !options.folders && chosen.length === 1 && enterable(chosen[0]);
  const canAnswer = opensFolder || chosenUsable.length > 0 || (!!options.folders && chosen.length === 0 && !!listing && !problem);

  const answer = () => {
    if (opensFolder) return go(chosen[0].path);
    if (chosenUsable.length) return onDone(chosenUsable.map((e) => e.path));
    if (options.folders && chosen.length === 0 && listing) onDone([listing.path]);
  };

  const select = (i: number, extend: boolean, toggle: boolean) => {
    const e = rows[i];
    if (!e || !usable(e)) return;
    if (options.multiple && toggle) {
      setSelected((s) => (s.includes(e.path) ? s.filter((p) => p !== e.path) : [...s, e.path]));
      setAnchor(i);
    } else if (options.multiple && extend && anchor >= 0) {
      const [from, to] = anchor < i ? [anchor, i] : [i, anchor];
      setSelected(rows.slice(from, to + 1).filter(usable).map((r) => r.path));
    } else {
      setSelected([e.path]);
      setAnchor(i);
    }
    list.current?.querySelector(`[data-row="${i}"]`)?.scrollIntoView({ block: "nearest" });
  };

  const step = (by: number, extend: boolean) => {
    const at = rows.findIndex((r) => r.path === selected[selected.length - 1]);
    let i = at < 0 ? (by > 0 ? -1 : rows.length) : at;
    do i += by;
    while (i >= 0 && i < rows.length && !usable(rows[i]));
    if (i >= 0 && i < rows.length) select(i, extend, false);
  };

  const ask = (what: "goto" | "folder") => {
    setAsking(what);
    setTyped(what === "folder" ? "untitled folder" : "");
    setAskProblem("");
  };

  const onKey = (e: ReactKeyboardEvent) => {
    if (asking) return;
    const typing = (e.target as HTMLElement).tagName === "INPUT";
    const cmd = e.metaKey || e.ctrlKey;
    const key = e.key;
    let handled = true;
    if (key === "Escape") onDone([]);
    else if (key === "Enter") canAnswer && answer();
    else if (typing) handled = false;
    else if (key === "ArrowDown" && cmd) chosen.length === 1 && enterable(chosen[0]) ? go(chosen[0].path) : undefined;
    else if (key === "ArrowUp" && cmd) go(parent(path));
    else if (key === "ArrowDown") step(1, e.shiftKey);
    else if (key === "ArrowUp") step(-1, e.shiftKey);
    else if (cmd && key === "[") goBack();
    else if (cmd && key === "]") goForward();
    else if (cmd && e.shiftKey && (key === "." || key === ">")) setHidden((h) => !h);
    else if (cmd && e.shiftKey && key.toLowerCase() === "g") ask("goto");
    else if (cmd && e.shiftKey && key.toLowerCase() === "h" && places) go(places.home);
    else if (cmd && e.shiftKey && key.toLowerCase() === "d" && places) go(`${places.home}/Desktop`);
    else if (cmd && key.toLowerCase() === "a" && options.multiple) setSelected(rows.filter(usable).map((r) => r.path));
    else if (!cmd && (key === "/" || key === "~")) {
      ask("goto");
      setTyped(key);
    } else if (!cmd && key.length === 1) {
      // Typing a name selects the first that starts with it.
      const now = Date.now();
      const t = typeahead.current;
      t.text = now - t.at > 900 ? key.toLowerCase() : t.text + key.toLowerCase();
      t.at = now;
      const i = rows.findIndex((r) => usable(r) && r.name.toLowerCase().startsWith(t.text));
      if (i >= 0) select(i, false, false);
    } else handled = false;
    if (handled) {
      e.preventDefault();
      e.stopPropagation();
    }
  };

  const finishAsk = async () => {
    if (asking === "goto") {
      const to = typed.trim();
      const info = await hostCall<{ exists: boolean }>("Files.Folder", expand(to, places?.home ?? "")).catch(() => ({ exists: false }));
      if (!info.exists) return setAskProblem("The folder can’t be found.");
      setAsking(null);
      go(expand(to, places?.home ?? "").replace(/(.)\/+$/, "$1"));
    } else if (asking === "folder") {
      try {
        const made = await hostCall<string>("Files.MakeFolder", path, typed);
        setAsking(null);
        go(made);
      } catch (err) {
        setAskProblem(capitalize(String(err instanceof Error ? err.message : err)) + ".");
      }
    }
  };

  const pathMenu = (button: HTMLElement) => {
    const entries: MenuEntry[] = [];
    for (let p = path; p; p = parent(p)) {
      const at = p;
      entries.push({ label: at === "/" ? computerName(places) : at.split("/").pop(), action: () => go(at) });
    }
    const box = button.getBoundingClientRect();
    popupMenu(entries, box.left, box.bottom + 4);
  };

  const here = path === "/" ? computerName(places) : path.split("/").pop() || path;
  const columns = (key: SortKey, label: string) => (
    <button className={`op-col op-col-${key}${sort.key === key ? " sorted" : ""}`} onClick={() => setSort((s) => ({ key, up: s.key === key ? !s.up : true }))}>
      <span>{label}</span>
      {sort.key === key && (sort.up ? <ChevronUp size={11} /> : <ChevronDown size={11} />)}
    </button>
  );

  return (
    <div className="op-shade" onKeyDown={onKey} onMouseDown={(e) => e.target === e.currentTarget && e.preventDefault()}>
      <div className="op-panel" role="dialog" aria-label={options.message || "Open"}>
        <aside className="op-sidebar">
          {places && (
            <>
              <div className="op-side-title">Favorites</div>
              {places.favorites.map((p) => <SideRow key={p.path} place={p} current={path} onGo={go} />)}
              <div className="op-side-title">Locations</div>
              {places.locations.map((p) => <SideRow key={p.path} place={p} current={path} onGo={go} />)}
            </>
          )}
        </aside>
        <section className="op-main">
          {options.message && <div className="op-message">{options.message}</div>}
          <div className="op-toolbar">
            <div className="op-nav">
              <button aria-label="Back" disabled={!back.length} onClick={goBack}><ChevronLeft size={17} /></button>
              <button aria-label="Forward" disabled={!forward.length} onClick={goForward}><ChevronRight size={17} /></button>
            </div>
            <button className="op-where" onClick={(e) => pathMenu(e.currentTarget)}>
              <Folder size={14} className="op-folder-glyph" />
              <span>{here}</span>
              <ChevronDown size={12} />
            </button>
            <label className="op-search">
              <Search size={13} />
              <input value={filter} placeholder="Search" onChange={(e) => setFilter(e.target.value)} spellCheck={false} />
            </label>
          </div>
          <div className="op-head">
            {columns("name", "Name")}
            {columns("modified", "Date Modified")}
            {columns("size", "Size")}
            {columns("kind", "Kind")}
          </div>
          <div className="op-list" ref={list} tabIndex={0} role="listbox" aria-multiselectable={!!options.multiple}>
            {problem ? (
              <div className="op-problem">{problem}</div>
            ) : (
              rows.map((e, i) => (
                <div
                  key={e.path}
                  data-row={i}
                  role="option"
                  aria-selected={selected.includes(e.path)}
                  className={`op-row${i % 2 ? " odd" : ""}${selected.includes(e.path) ? " selected" : ""}${usable(e) ? "" : " dim"}`}
                  onMouseDown={(m) => {
                    m.preventDefault();
                    list.current?.focus();
                    select(i, m.shiftKey, m.metaKey || m.ctrlKey);
                  }}
                  onDoubleClick={() => (enterable(e) ? go(e.path) : choosable(e) && onDone([e.path]))}
                >
                  <span className="op-cell op-name">
                    <EntryIcon entry={e} />
                    <span className="op-label">{e.name}</span>
                  </span>
                  <span className="op-cell op-modified">{when(e.modified)}</span>
                  <span className="op-cell op-size">{e.dir ? "--" : size(e.size)}</span>
                  <span className="op-cell op-kind">{kindOf(e)}</span>
                </div>
              ))
            )}
          </div>
        </section>
        <footer className="op-footer">
          {options.newFolder && <button className="op-button" onClick={() => ask("folder")}>New Folder</button>}
          <span className="op-spacer" />
          <button className="op-button" onClick={() => onDone([])}>Cancel</button>
          <button className="op-button default" disabled={!canAnswer} onClick={answer}>{opensFolder ? "Open" : options.button || "Open"}</button>
        </footer>
        {asking && (
          <div className="op-ask-shade">
            <form
              className="op-ask"
              onSubmit={(e) => {
                e.preventDefault();
                void finishAsk();
              }}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  e.preventDefault();
                  e.stopPropagation();
                  setAsking(null);
                  list.current?.focus();
                }
              }}
            >
              <div className="op-ask-title">{asking === "goto" ? "Go to the folder:" : "New Folder"}</div>
              {asking === "folder" && <div className="op-ask-label">Name of new folder inside “{here}”:</div>}
              <input
                autoFocus
                value={typed}
                spellCheck={false}
                onFocus={(e) => asking === "folder" && e.currentTarget.select()}
                onChange={(e) => {
                  setTyped(e.target.value);
                  setAskProblem("");
                }}
              />
              {askProblem && <div className="op-ask-problem">{askProblem}</div>}
              <div className="op-ask-buttons">
                <button type="button" className="op-button" onClick={() => setAsking(null)}>Cancel</button>
                <button type="submit" className="op-button default" disabled={!typed.trim()}>{asking === "goto" ? "Go" : "Create"}</button>
              </div>
            </form>
          </div>
        )}
      </div>
    </div>
  );
}

const PLACE_ICONS: Record<string, { sf: string; icon: LucideIcon }> = {
  applications: { sf: "app.dashed", icon: LayoutGrid },
  desktop: { sf: "menubar.dock.rectangle", icon: Monitor },
  documents: { sf: "doc", icon: FileText },
  downloads: { sf: "arrow.down.circle", icon: CircleArrowDown },
  home: { sf: "house", icon: House },
  computer: { sf: "internaldrive", icon: HardDrive },
  drive: { sf: "externaldrive", icon: HardDrive },
};

function SideRow({ place, current, onGo }: { place: Place; current: string; onGo: (path: string) => void }) {
  const icon = PLACE_ICONS[place.kind] ?? PLACE_ICONS.home;
  return (
    <button className={`op-side-row${current === place.path ? " current" : ""}`} onClick={() => onGo(place.path)}>
      <Icon icon={icon.icon} sf={icon.sf} size={15} className="op-side-icon" />
      <span>{place.name}</span>
    </button>
  );
}

function EntryIcon({ entry }: { entry: Entry }) {
  const ext = entry.name.split(".").pop()?.toLowerCase() ?? "";
  if (entry.package && ext === "app") return <AppWindow size={15} className="op-icon app" />;
  if (entry.dir) return <Folder size={15} className="op-icon folder" />;
  if (IMAGE.test(entry.name)) return <FileImage size={15} className="op-icon" />;
  if (["zip", "gz", "tgz", "tar", "dmg", "xz", "7z"].includes(ext)) return <FileArchive size={15} className="op-icon" />;
  if (KINDS[ext]?.endsWith("source") || ["js", "ts", "tsx", "jsx", "json", "sh", "py", "rb", "rs", "c", "h"].includes(ext)) {
    return <FileCode size={15} className="op-icon" />;
  }
  if (["md", "txt", "pdf", "rtf", "doc", "docx"].includes(ext)) return <FileText size={15} className="op-icon" />;
  return <File size={15} className="op-icon" />;
}

// What Finder calls a file, by its extension.
const KINDS: Record<string, string> = {
  png: "PNG image", jpg: "JPEG image", jpeg: "JPEG image", gif: "GIF image", webp: "WebP image", heic: "HEIC image",
  svg: "SVG image", tif: "TIFF image", tiff: "TIFF image", bmp: "Windows BMP image", pdf: "PDF document",
  txt: "Plain Text", md: "Markdown", json: "JSON", yml: "YAML", yaml: "YAML", toml: "TOML", csv: "CSV",
  html: "HTML text", css: "CSS", js: "JavaScript script", mjs: "JavaScript script", ts: "TypeScript", tsx: "TypeScript JSX",
  jsx: "JavaScript JSX", go: "Go source", swift: "Swift source", py: "Python script", rb: "Ruby script",
  rs: "Rust source", c: "C source", h: "C header", cpp: "C++ source", java: "Java source", kt: "Kotlin source",
  sh: "Shell script", zsh: "Shell script", zip: "ZIP archive", gz: "gzip compressed archive", tar: "tar archive",
  dmg: "Disk Image", app: "Application", mov: "QuickTime movie", mp4: "MPEG-4 movie", mp3: "MP3 audio", wav: "Waveform audio",
  log: "Log File", lock: "Document", mod: "Document", sum: "Document",
};

function kindOf(e: Entry): string {
  const dot = e.name.lastIndexOf(".");
  const ext = dot > 0 ? e.name.slice(dot + 1).toLowerCase() : "";
  if (e.package) return KINDS[ext] ?? "Package";
  if (e.dir) return "Folder";
  return KINDS[ext] ?? (ext ? `${ext.toUpperCase()} File` : "Document");
}

// As Finder writes them: 1,000 bytes to the KB.
function size(bytes: number): string {
  if (bytes < 1000) return bytes === 1 ? "1 byte" : `${bytes} bytes`;
  const units = ["KB", "MB", "GB", "TB"];
  let n = bytes / 1000;
  let u = 0;
  while (n >= 1000 && u < units.length - 1) {
    n /= 1000;
    u++;
  }
  return `${n < 10 && u > 0 ? n.toFixed(1) : Math.round(n)} ${units[u]}`;
}

const time = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
const date = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "short", year: "numeric" });

// "Today at 9:41", "Yesterday at 18:02", "3 Nov 2026 at 10:15".
function when(ms: number): string {
  if (!ms) return "--";
  const d = new Date(ms);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const day = new Date(d);
  day.setHours(0, 0, 0, 0);
  const days = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  const prefix = days === 0 ? "Today" : days === 1 ? "Yesterday" : date.format(d);
  return `${prefix} at ${time.format(d)}`;
}

function computerName(places: Places | null): string {
  return places?.locations.find((p) => p.path === "/")?.name ?? "Computer";
}

function expand(path: string, home: string): string {
  if (path === "~" || path.startsWith("~/")) return home + path.slice(1);
  return path;
}

function capitalize(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}
