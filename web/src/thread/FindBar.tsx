// Find in one conversation, at the top right of its thread as T3 Code has
// it (Views/FindBar.swift): the words, which match is shown of how many,
// and the way to the one before and the next.

import { useEffect, useRef } from "react";
import { ChevronDown, ChevronUp, Search, X } from "lucide-react";
import { findCount, findMissing, type Finding } from "./find";

export function FindBar({ finding, setQuery, previous, next, close }: {
  finding: Finding;
  setQuery: (q: string) => void;
  previous: () => void;
  next: () => void;
  close: () => void;
}) {
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => field.current?.focus(), []);
  return (
    <div className="find-bar" role="search">
      <Search size={11} strokeWidth={2.4} />
      <input
        ref={field}
        value={finding.query}
        placeholder="Find in conversation"
        aria-label="Find in conversation"
        spellCheck={false}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            if (e.shiftKey) previous();
            else next();
          } else if (e.key === "Escape") {
            e.preventDefault();
            close();
          }
        }}
      />
      <span className={`find-count${findMissing(finding) ? " missing" : ""}`}>{findCount(finding)}</span>
      <button title="Previous match (⇧↩)" aria-label="Previous match" disabled={!finding.matches.length} onClick={previous}><ChevronUp size={12} strokeWidth={2.6} /></button>
      <button title="Next match (↩)" aria-label="Next match" disabled={!finding.matches.length} onClick={next}><ChevronDown size={12} strokeWidth={2.6} /></button>
      <button title="Close (esc)" aria-label="Close" onClick={close}><X size={12} strokeWidth={2.6} /></button>
    </div>
  );
}
