// Knowledge (Views/KnowledgeSettings.swift): the user's notes for their
// agents. Shared notes any agent can read through Agentman's tools; private
// ones only Agentman reads. A list on the left, the note on a card beside it.

import { useEffect, useState } from "react";
import type { Note } from "@shared/protocol";
import { BookOpen, ChevronDown, Lock, Plus, Users } from "lucide-react";
import { menuUnder } from "../thread/menu";
import { Icon } from "../ui";
import { Checkbox, PushButton, TextArea } from "./controls";
import { deleteNote, loadNotes, readNote, settingsState, useSettingsState, writeNote } from "./state";

interface Draft {
  title: string;
  tier: string;
  always: boolean;
  body: string;
  /** The note as saved, when this edits one. */
  original?: Note;
}

const empty = (): Draft => ({ title: "", tier: "shared", always: false, body: "" });
const fromNote = (note: Note): Draft => ({ title: note.title, tier: note.tier, always: note.always ?? false, body: note.body ?? "", original: note });
const noteKey = (note: Note) => `${note.tier}/${note.name}`;

export function KnowledgePage() {
  const { notes } = useSettingsState();
  const [selection, setSelection] = useState<string>();
  const [draft, setDraft] = useState<Draft>(empty);
  const [problem, setProblem] = useState<string>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    void loadNotes().then(() => {
      // For screenshots and tests: a note open at launch, by name.
      const name = new URLSearchParams(location.search).get("note");
      const note = name ? settingsState().notes.find((n) => n.name === name) : undefined;
      if (note) choose(note);
    });
  }, []);

  const choose = (note: Note) => {
    setSelection(noteKey(note));
    void readNote(note.name, note.tier).then((full) => {
      if (full) {
        setDraft(fromNote(full));
        setProblem(undefined);
      }
    });
  };

  const save = async () => {
    if (saving || !draft.title.trim()) return;
    setSaving(true);
    const note: Note = {
      name: draft.original?.name ?? "",
      tier: draft.tier,
      title: draft.title,
      always: draft.tier === "shared" && draft.always,
      body: draft.body,
      updatedAt: 0,
    };
    const saved = await writeNote(note);
    if (saved.note) {
      setDraft(fromNote(saved.note));
      setSelection(noteKey(saved.note));
      setProblem(undefined);
    } else {
      setProblem(saved.problem);
    }
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
    <div className="knowledge">
      <div className="page-head">
        <div className="page-head-text">
          <h1 className="settings-display">Knowledge</h1>
          <div className="page-intro">
            Notes for your agents: how you like things done, facts about your projects. Shared notes any agent can read through Agentman; private ones only Agentman
            reads.
          </div>
        </div>
        <span className="page-head-gap" />
        <PushButton
          icon={Plus}
          sf="plus"
          onClick={() => {
            setSelection(undefined);
            setDraft(empty());
            setProblem(undefined);
          }}
        >
          New note
        </PushButton>
      </div>
      <div className="knowledge-body">
        <div className="note-list card">
          <div className="note-list-scroll">
            {notes.map((note) => (
              <button key={noteKey(note)} className={`note-row ${selection === noteKey(note) ? "selected" : ""}`} onClick={() => choose(note)}>
                <span className="note-title">{note.title}</span>
                <span className="note-meta">
                  <Icon icon={note.tier === "shared" ? Users : Lock} sf={note.tier === "shared" ? "person.2" : "lock"} size={12} />
                  <span>{note.tier === "shared" ? "Shared" : "Private"}</span>
                  {note.always && <span>· In instructions</span>}
                </span>
              </button>
            ))}
          </div>
          {notes.length === 0 && (
            <div className="note-empty">
              <span className="faint">
                <Icon icon={BookOpen} sf="book" size={18} />
              </span>
              <span className="note-empty-title">No notes yet</span>
              <span className="note-empty-text">Notes you write, or save from Agentman's suggestions, show here.</span>
            </div>
          )}
        </div>
        <div className="note-editor card">
          <textarea
            className="note-title-field"
            rows={1}
            value={draft.title}
            placeholder="Title"
            spellCheck={false}
            onChange={(e) => setDraft({ ...draft, title: e.target.value.replace(/\n/g, " ") })}
            onInput={(e) => {
              const el = e.currentTarget;
              el.style.height = "auto";
              el.style.height = `${Math.min(el.scrollHeight, 3 * 22)}px`;
            }}
          />
          <div className="note-who">
            <button
              className="tier-menu"
              title={draft.tier === "shared" ? "Any agent can read it through Agentman" : "Only Agentman reads it; agents get what it tells them"}
              onClick={(e) =>
                menuUnder(e.currentTarget, [
                  { label: "Shared with agents", checked: draft.tier === "shared", action: () => setDraft({ ...draft, tier: "shared" }) },
                  { label: "Private to Agentman", checked: draft.tier === "private", action: () => setDraft({ ...draft, tier: "private" }) },
                ])
              }
            >
              <Icon icon={draft.tier === "shared" ? Users : Lock} sf={draft.tier === "shared" ? "person.2" : "lock"} size={10} weight="medium" />
              <span>{draft.tier === "shared" ? "Shared" : "Private"}</span>
              <span className="faint">
                <Icon icon={ChevronDown} sf="chevron.down" size={7} weight="bold" />
              </span>
            </button>
            {draft.tier === "shared" && (
              <Checkbox
                on={draft.always}
                onChange={(always) => setDraft({ ...draft, always })}
                title="Agentman writes it into CLAUDE.md or AGENTS.md when you ask it to update an agent's instructions."
              >
                In project instructions
              </Checkbox>
            )}
          </div>
          <div className="settings-divider" />
          <TextArea
            className="note-body"
            value={draft.body}
            placeholder="Write the note: how you like things done, or what agents should know."
            onChange={(body) => setDraft({ ...draft, body })}
          />
          <div className="note-actions">
            {problem && <span className="note-problem">{problem}</span>}
            <span className="row-spacer" />
            {draft.original && (
              <PushButton
                destructive
                onClick={() =>
                  void deleteNote(draft.original!).then((p) => {
                    setProblem(p);
                    if (!p) {
                      setSelection(undefined);
                      setDraft(empty());
                    }
                  })
                }
              >
                Delete
              </PushButton>
            )}
            <PushButton prominent disabled={saving || !draft.title.trim()} onClick={() => void save()}>
              {draft.original ? "Save" : "Add note"}
            </PushButton>
          </div>
        </div>
      </div>
    </div>
  );
}
