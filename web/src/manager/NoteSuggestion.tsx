// A note Agentman suggests keeping (Views/NoteSuggestion.swift): what it
// would save and why, with Save, Edit and Dismiss; nothing is kept until the
// user saves it. Its receipt once decided, and the popup at the window's top
// right while the conversation isn't on screen.

import { useEffect, useState } from "react";
import { Bookmark, BookmarkCheck, BookmarkX, Lock, Users, X } from "lucide-react";
import type { ManagerCard, Note } from "@shared/protocol";
import { useRoute } from "../nav";
import { Icon } from "../ui";
import { dismissCard, noteSuggestions, saveNote, useManager } from "./store";
import { InlineMarkdown, keepLastWords } from "./text";

const query = new URLSearchParams(location.search);
/** For screenshots: the suggestion opened for editing (AGENTMAN_EDIT_NOTE). */
const editAtStart = query.get("editNote") === "1";

export function NoteSuggestionCard({ card, position }: { card: ManagerCard; position?: string }) {
  const note = card.note;
  const { appActive } = useManager();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState("");
  const [text, setText] = useState("");
  const [tier, setTier] = useState("shared");
  const [always, setAlways] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();

  const beginEditing = () => {
    setTitle(note?.title ?? card.title);
    setText(note?.body ?? card.detail ?? "");
    setTier(note?.tier ?? "shared");
    setAlways(note?.always ?? false);
    setProblem(undefined);
    setEditing(true);
  };
  useEffect(() => {
    if (editAtStart) beginEditing();
    // Once, as the Mac's onAppear.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const act = (work: () => Promise<string | undefined>) => {
    setBusy(true);
    setProblem(undefined);
    void work().then((p) => {
      setProblem(p);
      setBusy(false);
    });
  };
  const save = (edited: boolean) => {
    const changed: Note | undefined = edited
      ? { name: note?.name ?? "", tier, title: title.trim(), always: tier === "private" ? false : always, body: text.trim(), updatedAt: 0 }
      : undefined;
    act(() => saveNote(card, changed));
  };
  const canSaveEdit = !busy && title.trim().length > 0 && text.trim().length > 0;

  return (
    <div
      className={`am-note ${appActive ? "" : "inactive"}`}
      onKeyDown={(e) => {
        // Save note is the default button while editing.
        if (editing && e.key === "Enter" && !e.shiftKey && (e.target as HTMLElement).tagName !== "TEXTAREA" && canSaveEdit) save(true);
      }}
    >
      <div className="am-note-head">
        <span className="am-note-bookmark"><Icon icon={Bookmark} sf="bookmark" size={12} weight="semibold" /></span>
        <span className="am-note-kicker">Agentman suggests a note</span>
        <span className="am-spacer" />
        {position && <span className="am-note-position">{position}</span>}
        <button className="am-note-x" title="Dismiss: don't keep it" disabled={busy} onClick={() => act(() => dismissCard(card))}>
          <Icon icon={X} sf="xmark" size={10} weight="semibold" />
        </button>
      </div>

      {editing ? (
        <div className="am-note-editor">
          <input className="am-rounded-field am-note-title-field" value={title} placeholder="Title" onChange={(e) => setTitle(e.target.value)} />
          <textarea className="am-note-body-field" value={text} onChange={(e) => setText(e.target.value)} />
          <div className="am-segmented" role="radiogroup">
            <button role="radio" aria-checked={tier === "shared"} className={tier === "shared" ? "on" : ""} onClick={() => setTier("shared")}>Shared with your agents</button>
            <button role="radio" aria-checked={tier === "private"} className={tier === "private" ? "on" : ""} onClick={() => setTier("private")}>Only Agentman</button>
          </div>
          <label className={`am-checkbox ${tier === "private" ? "disabled" : ""}`}>
            <input type="checkbox" checked={always} disabled={tier === "private"} onChange={(e) => setAlways(e.target.checked)} />
            <span>Put it in every project's instructions</span>
          </label>
        </div>
      ) : (
        <div className="am-note-reading">
          <div className="am-note-title">{note?.title ?? card.title}</div>
          <div className="am-note-body"><InlineMarkdown text={note?.body ?? card.detail ?? ""} /></div>
          {card.reason && <div className="am-note-why">{keepLastWords(card.reason)}</div>}
          <div className="am-note-who">
            <Icon icon={note?.tier === "private" ? Lock : Users} sf={note?.tier === "private" ? "lock" : "person.2"} size={10} weight="medium" />
            <span>{note?.tier === "private" ? "Only Agentman reads it" : "Shared with your agents"}</span>
            {note?.always && <span>· In every project's instructions</span>}
          </div>
        </div>
      )}

      {problem && <div className="am-problem">{problem}</div>}

      <div className="am-note-buttons">
        {editing ? (
          <>
            <button className="am-push" disabled={busy} onClick={() => setEditing(false)}>Cancel</button>
            <button className="am-push prominent" disabled={!canSaveEdit} onClick={() => save(true)}>Save note</button>
          </>
        ) : (
          <>
            <button className="am-push" disabled={busy} onClick={() => act(() => dismissCard(card))}>Dismiss</button>
            <button className="am-push" disabled={busy} onClick={beginEditing}>Edit</button>
            <button className="am-push prominent" disabled={busy} onClick={() => save(false)}>Save note</button>
          </>
        )}
      </div>
    </div>
  );
}

/** A suggested note once the user decided: kept, or not. */
export function NoteReceipt({ card }: { card: ManagerCard }) {
  const kept = card.state === "answered";
  return (
    <div className="am-receipt">
      <span className={kept ? "am-receipt-check" : "am-faint"}>
        <Icon icon={kept ? BookmarkCheck : BookmarkX} sf={kept ? "bookmark.fill" : "bookmark.slash"} size={10} weight="semibold" />
      </span>
      <span className="am-receipt-text">{`Note “${card.note?.title ?? card.title}” · ${card.outcome ?? (kept ? "Saved" : "Not saved")}`}</span>
    </div>
  );
}

/** The suggestion waiting longest, at the top right of whatever is on
 *  screen, but not over Agentman's conversation, which shows it in place. */
export function NoteSuggestionPopup() {
  const s = useManager();
  const route = useRoute();
  const waiting = noteSuggestions(s);
  const card = waiting[0];
  if (!card || route.kind === "agentman") return null;
  return (
    <div className="am-note-popup">
      <NoteSuggestionCard key={card.id} card={card} position={waiting.length > 1 ? `1 of ${waiting.length}` : undefined} />
    </div>
  );
}
