// Small pieces the Board's cards share.

import type { LucideIcon } from "lucide-react";
import type { SearchHit } from "@shared/protocol";
import { markedParts } from "./search";
import { Icon } from "../ui";

/** Something to do with an agent, a click away, as a small chip; warns,
 *  before something that can't be taken back (BoardView.swift's BoardLink). */
export function BoardLink({ title, icon, sf, onClick, warns = false, help }: {
  title: string;
  icon: LucideIcon;
  sf: string;
  onClick: () => void;
  warns?: boolean;
  help?: string;
}) {
  return (
    <button className={`board-link${warns ? " warns" : ""}`} title={help} onClick={onClick}>
      <Icon icon={icon} sf={sf} size={10} />
      <span>{title}</span>
    </button>
  );
}

/** A hit's snippet, who said it first, the words marked (SearchWords.snippet). */
export function Snippet({ hit, query }: { hit: SearchHit; query: string }) {
  return (
    <>
      <span className={hit.role === "user" ? "snippet-you" : "snippet-agent"}>{hit.role === "user" ? "You: " : "Agent: "}</span>
      {markedParts(hit.snippet, query).map((p, i) => (p.marked ? <b key={i}>{p.text}</b> : <span key={i}>{p.text}</span>))}
    </>
  );
}
