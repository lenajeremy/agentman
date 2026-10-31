// Code to read (Design/CodeTextView.swift, read-only): line numbers down the
// side, syntax colours, ⌘F to find, and long lines that scroll rather than
// wrap. Google Sans Code 12; numbers at 10.5, faint, 8 from the ruler's edge.

import { useEffect, useMemo, useRef, useState } from "react";
import { Search, X } from "lucide-react";
import { Icon } from "../ui";
import { languageFor, runs } from "./highlight";

/** Past this much text, colouring would cost more than it gives. */
const COLOUR_LIMIT = 400_000;

export function CodeView({ text, path }: { text: string; path: string }) {
  const lines = useMemo(() => text.split("\n"), [text]);
  const language = useMemo(() => (text.length <= COLOUR_LIMIT ? languageFor(path) : null), [text, path]);
  // The ruler: 8 per digit (at least three) and 18 more.
  const digits = Math.max(3, String(lines.length).length);
  const ruler = digits * 8 + 18;
  const scroller = useRef<HTMLDivElement>(null);
  const [finding, setFinding] = useState(false);
  const [query, setQuery] = useState("");
  const [current, setCurrent] = useState(0);

  const matches = useMemo(() => {
    if (!finding || !query) return [] as { line: number; start: number }[];
    const found: { line: number; start: number }[] = [];
    const needle = query.toLowerCase();
    lines.forEach((line, i) => {
      const hay = line.toLowerCase();
      for (let at = hay.indexOf(needle); at >= 0; at = hay.indexOf(needle, at + needle.length)) found.push({ line: i, start: at });
    });
    return found;
  }, [finding, query, lines]);

  useEffect(() => {
    if (!matches.length) return;
    const row = scroller.current?.querySelector<HTMLElement>(`[data-line="${matches[current % matches.length].line}"]`);
    row?.scrollIntoView({ block: "center" });
  }, [current, matches]);

  return (
    <div
      className="code-view"
      tabIndex={0}
      onKeyDown={(e) => {
        if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "f") {
          e.preventDefault();
          setFinding(true);
        } else if (e.key === "Escape" && finding) {
          setFinding(false);
        }
      }}
    >
      {finding && (
        <div className="find-bar">
          <span className="find-field">
            <Icon icon={Search} sf="magnifyingglass" size={11} />
            <input
              autoFocus
              value={query}
              placeholder="Find"
              onChange={(e) => {
                setQuery(e.target.value);
                setCurrent(0);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") setCurrent((n) => (matches.length ? (n + (e.shiftKey ? matches.length - 1 : 1)) % matches.length : 0));
                if (e.key === "Escape") setFinding(false);
              }}
            />
          </span>
          <span className="find-count">{query ? `${matches.length ? (current % matches.length) + 1 : 0} of ${matches.length}` : ""}</span>
          <button className="find-done" onClick={() => setFinding(false)} title="Done"><Icon icon={X} sf="xmark" size={9} weight="semibold" /></button>
        </div>
      )}
      <div className="code-scroll" ref={scroller}>
        <div className="code-grid" style={{ gridTemplateColumns: `${ruler}px max-content` }}>
          <div className="code-ruler" aria-hidden>
            {lines.map((_, i) => <div key={i} className="code-number">{i + 1}</div>)}
          </div>
          <pre className="code-text">
            {lines.map((line, i) => (
              <div key={i} data-line={i} className="code-line">
                {highlightFound(line, i, language, finding ? query : "", matches, current)}
              </div>
            ))}
          </pre>
        </div>
      </div>
    </div>
  );
}

function highlightFound(line: string, index: number, language: ReturnType<typeof languageFor>, query: string,
  matches: { line: number; start: number }[], current: number) {
  const parts = runs(line || " ", language && line.length <= 1500 ? language : null).map((r, k) =>
    r.token ? <span key={k} className={`tok-${r.token}`}>{r.text}</span> : r.text,
  );
  if (!query || !matches.some((m) => m.line === index)) return parts;
  // A found line is drawn plain with its matches marked: simpler than
  // cutting coloured runs, and the mark is what's being looked for.
  const out: React.ReactNode[] = [];
  let at = 0;
  const lower = line.toLowerCase();
  const needle = query.toLowerCase();
  const active = matches.length ? matches[current % matches.length] : undefined;
  for (let i = lower.indexOf(needle); i >= 0; i = lower.indexOf(needle, i + needle.length)) {
    out.push(line.slice(at, i));
    const isActive = active?.line === index && active.start === i;
    out.push(<mark key={i} className={isActive ? "found current" : "found"}>{line.slice(i, i + needle.length)}</mark>);
    at = i + needle.length;
  }
  out.push(line.slice(at));
  return out;
}
