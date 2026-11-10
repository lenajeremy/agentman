// The floating palettes over the editor (Design/PaletteView.swift): Go to
// File (⌘P, EditorWindow.swift's GoToFile) and Search in Folder (⇧⌘F,
// EditorSearch.swift's GoToText).

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Search } from "lucide-react";
import { Icon } from "../ui";
import { FileTypeIcon } from "./fileIcons";
import { paths, type SearchMatch } from "./fs";
import { fuzzyFilter } from "./fuzzy";
import type { EditorStore } from "./store";
import { useKeyboardBack } from "../palette/PaletteView";

/** Gives the keyboard back to the editor's code, which sits in a frame of
 *  its own, or to whatever else had it. */
export function editorKeyboardBack(store: EditorStore) {
  return (before: Element | null) => {
    if (before instanceof HTMLIFrameElement && before.classList.contains("monaco")) store.monaco?.focus();
    else if (before instanceof HTMLElement && before.isConnected) before.focus();
  };
}

interface Item {
  id: string;
  title: string;
  detail?: string;
  /** A file's own icon. */
  file: string;
  /** A search result's line, with what matched marked. */
  line?: ReactNode;
}

function PaletteView({
  placeholder,
  results,
  choose,
  dismiss,
  typed,
  submit,
  accessory,
  status,
  initial = "",
  giveBack,
}: {
  placeholder: string;
  results: (query: string) => Item[];
  choose: (item: Item) => void;
  dismiss: () => void;
  typed?: (query: string) => void;
  submit?: (query: string) => boolean;
  accessory?: ReactNode;
  status?: string | null;
  initial?: string;
  giveBack?: (before: Element | null) => void;
}) {
  useKeyboardBack(giveBack);
  const [query, setQuery] = useState(initial);
  const [selected, setSelected] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const items = results(query);

  useEffect(() => {
    input.current?.focus();
    typed?.(query);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useLayoutEffect(() => {
    list.current?.querySelector(`[data-index="${selected}"]`)?.scrollIntoView({ block: "nearest" });
  }, [selected]);

  return (
    <div className="palette-layer" onMouseDown={(e) => e.target === e.currentTarget && dismiss()}>
      <div className="palette">
        <div className="palette-field">
          <span className="palette-search">
            <Icon icon={Search} sf="magnifyingglass" size={13} weight="medium" />
          </span>
          <input
            ref={input}
            value={query}
            placeholder={placeholder}
            spellCheck={false}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelected(0);
              typed?.(e.target.value);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                if (submit?.(query)) return;
                const item = items[selected];
                if (item) choose(item);
              } else if (e.key === "ArrowDown") {
                e.preventDefault();
                setSelected(Math.min(selected + 1, Math.max(items.length - 1, 0)));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSelected(Math.max(selected - 1, 0));
              } else if (e.key === "Escape") {
                e.preventDefault();
                dismiss();
              }
              e.stopPropagation();
            }}
          />
          {accessory}
        </div>
        {items.length > 0 ? (
          <>
            <div className="palette-divider" />
            <div ref={list} className="palette-list" style={{ height: Math.min(items.length * 34 + 12, 380) }}>
              {items.map((item, index) => (
                <div
                  key={item.id}
                  data-index={index}
                  className={`palette-row ${index === selected ? "selected" : ""}`}
                  onMouseEnter={() => setSelected(index)}
                  onClick={() => choose(item)}
                >
                  <span className="palette-icon">
                    <FileTypeIcon name={item.file} size={15} />
                  </span>
                  {item.line ? <span className="palette-line">{item.line}</span> : <span className="palette-title">{item.title}</span>}
                  {item.detail && <span className={`palette-detail ${item.line ? "fixed" : ""}`}>{item.detail}</span>}
                  <span className="palette-spacer" />
                </div>
              ))}
            </div>
            {status && (
              <>
                <div className="palette-divider" />
                <div className="palette-status">{status}</div>
              </>
            )}
          </>
        ) : query ? (
          <>
            <div className="palette-divider" />
            <div className="palette-empty">{status ?? "Nothing matches"}</div>
          </>
        ) : null}
      </div>
    </div>
  );
}

/** ⌘P: any file in the folder by a few of its letters. */
export function GoToFile({ store }: { store: EditorStore }) {
  useEffect(() => {
    void store.loadAllFiles();
  }, [store]);
  const results = (query: string): Item[] => {
    let list: string[];
    if (!query.trim()) {
      // Open files first, then what changed.
      const open = store.documents.map((d) => d.path);
      list = [...open, ...Object.keys(store.marks).sort().filter((p) => !open.includes(p))];
    } else {
      list = fuzzyFilter(query, store.allFiles, 60);
    }
    return list.slice(0, 60).map((path) => {
      const folder = paths.dir(path);
      return { id: path, title: paths.base(path), detail: folder || undefined, file: path };
    });
  };
  const close = () => {
    store.quickOpen = false;
    store.changed();
  };
  return (
    <PaletteView
      placeholder="Go to file"
      giveBack={editorKeyboardBack(store)}
      results={results}
      choose={(item) => {
        close();
        store.open(item.id);
      }}
      dismiss={close}
    />
  );
}

/** ⇧⌘F: the words in the folder's files. */
export function GoToText({ store }: { store: EditorStore }) {
  const typing = useRef(0);
  useEffect(() => () => window.clearTimeout(typing.current), []);
  const results = store.searchResults;
  const status = (() => {
    if (results?.problem) return results.problem;
    if (store.searchRunning) return "Searching…";
    if (!results || !results.query) return null;
    if (results.total === 0) return "No results";
    const count = results.files.length;
    return `${results.total} result${results.total === 1 ? "" : "s"} in ${count} file${count === 1 ? "" : "s"}` +
      (results.truncated ? ", the first 2000" : "");
  })();
  const matches = new Map<string, SearchMatch>();
  const items = (query: string): Item[] => {
    if (!results || !results.query || !query) return [];
    return results.files.flatMap((file) =>
      file.matches.map((match) => {
        const id = `${match.path}:${match.line}:${match.column}`;
        matches.set(id, match);
        return { id, title: match.preview, detail: `${place(file.path)}:${match.line}`, file: file.path, line: highlighted(match) };
      }),
    );
  };
  const toggle = (label: string, key: keyof typeof store.searchOptions, help: string, underline = false) => {
    const on = store.searchOptions[key];
    return (
      <button
        className={`search-toggle ${on ? "on" : ""}`}
        title={help}
        tabIndex={-1}
        style={{ textDecoration: underline ? "underline" : undefined }}
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => store.setSearchOptions({ ...store.searchOptions, [key]: !on })}
      >
        {label}
      </button>
    );
  };
  return (
    <PaletteView
      placeholder={`Search in ${store.name}`}
      giveBack={editorKeyboardBack(store)}
      initial={store.searchQuery}
      results={items}
      status={status}
      choose={(item) => {
        const match = matches.get(item.id);
        if (match) store.openMatch(match);
      }}
      dismiss={() => {
        store.searchOpen = false;
        store.changed();
      }}
      typed={(query) => {
        // A second after the typing stops.
        window.clearTimeout(typing.current);
        typing.current = window.setTimeout(() => store.runSearch(query), 1000);
      }}
      submit={(query) => {
        // Return searches at once when what shows is for other words.
        const shown = store.searchResults;
        if (shown?.query === query && JSON.stringify(shown.options) === JSON.stringify(store.searchOptions)) return false;
        window.clearTimeout(typing.current);
        store.runSearch(query);
        return true;
      }}
      accessory={
        <span className="search-toggles">
          {toggle("Aa", "caseSensitive", "Match Case")}
          {toggle("ab", "wholeWord", "Match Whole Word", true)}
          {toggle(".*", "regex", "Use Regular Expression")}
        </span>
      }
    />
  );
}

/** A file named with its folder: two files of one name are told apart. */
function place(path: string) {
  return path.split("/").slice(-2).join("/");
}

/** A match's line with the matches marked; columns are UTF-16 units, as
 *  JavaScript counts them. */
function highlighted(match: SearchMatch): ReactNode {
  const out: ReactNode[] = [];
  let at = 0;
  const ranges = [...match.highlights].sort((a, b) => a.location - b.location);
  for (const [i, range] of ranges.entries()) {
    if (range.location < at) continue;
    if (range.location > at) out.push(match.preview.slice(at, range.location));
    out.push(<mark key={i}>{match.preview.slice(range.location, range.location + range.length)}</mark>);
    at = range.location + range.length;
  }
  if (at < match.preview.length) out.push(match.preview.slice(at));
  return out;
}
