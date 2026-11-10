// The pieces of the message box (FileMentions.swift's MentionList,
// SlashCommands.swift's SlashList and CommandPanelSheet, ImagePreview.swift's
// AttachmentStrip, VoiceRecorder.swift's VoiceButton).

import { useEffect, useRef, useState } from "react";
import { CornerDownLeft, FileText, Mic, X } from "lucide-react";
import type { CommandPanel } from "@shared/protocol";
import type { Attachment } from "./attachments";
import { commandLabel, type Mentions, type Slash } from "./completions";
import { FileIcon } from "./FileIcon";
import { fileTypeIcon } from "./fileTypeIcon";
import { Spinner } from "./Loader";
import { Sym } from "./Sym";
import { elapsed, Recording, transcribe } from "./voice";

/** The files that match what follows @, over the box. */
export function MentionList({ mentions, onPick }: { mentions: Mentions; onPick: (index: number) => void }) {
  return (
    <div className="completion-list" aria-label="Files to mention">
      <div className="completion-heading">{mentions.query ? "Files" : "Suggested"}</div>
      {mentions.results.map((path, i) => {
        const slash = path.lastIndexOf("/");
        const name = path.slice(slash + 1);
        const folder = slash > 0 ? path.slice(0, slash) : "";
        const selected = i === mentions.selected;
        return (
          <div
            key={path}
            className={`mention-row ${selected ? "selected" : ""}`}
            onMouseDown={(e) => {
              e.preventDefault();
              onPick(i);
            }}
            onMouseEnter={() => mentions.setSelected(i)}
          >
            <span className="mention-icon"><img src={`/assets/files/${fileTypeIcon(name)}.svg`} width={14} height={14} alt="" /></span>
            <span className="mention-name">{name}</span>
            {folder && <span className="mention-folder">{folder}</span>}
            <span className="completion-spacer" />
            {selected && <span className="completion-return"><Sym sf="return" size={9.5} weight="semibold" icon={CornerDownLeft} /></span>}
          </div>
        );
      })}
    </div>
  );
}

/** The commands that match what follows /, over the box. */
export function SlashList({ slash, onPick }: { slash: Slash; onPick: (index: number) => void }) {
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    list.current?.querySelector(".slash-row.selected")?.scrollIntoView({ block: "nearest" });
  }, [slash.selected]);
  return (
    <div className="completion-list" aria-label="Commands">
      {slash.problem && slash.results.length === 0 ? (
        <div className="slash-problem">{slash.problem}</div>
      ) : (
        <div className={`slash-scroll ${slash.results.length > 9 ? "scrolls" : ""}`} ref={list}>
          {slash.results.map((command, i) => {
            const selected = i === slash.selected;
            return (
              <div
                key={commandLabel(command)}
                className={`slash-row ${selected ? "selected" : ""}`}
                onMouseDown={(e) => {
                  e.preventDefault();
                  onPick(i);
                }}
                onMouseEnter={() => slash.setSelected(i)}
              >
                <div className="slash-line">
                  <span className="slash-name">{commandLabel(command)}</span>
                  {command.argumentHint ? <span className="slash-hint">{command.argumentHint}</span> : null}
                  <span className="completion-spacer" />
                  {selected && <span className="completion-return"><Sym sf="return" size={9.5} weight="semibold" icon={CornerDownLeft} /></span>}
                </div>
                {command.description ? <div className="slash-description">{command.description}</div> : null}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Pictures and files waiting in the box, each with a way to take it out. */
export function AttachmentStrip({ attachments, onRemove }: { attachments: Attachment[]; onRemove: (id: string) => void }) {
  return (
    <div className="attachment-strip">
      {attachments.map((a) => (
        <div key={a.id} className="attachment">
          {a.file ? (
            <div className="attachment-file" title={a.file}>
              <FileIcon path={a.file} size={28} fallback={FileText} />
              <span className="attachment-name">{a.file.split("/").pop()}</span>
            </div>
          ) : (
            <img className="attachment-image" src={a.preview} alt="" />
          )}
          <button className="attachment-remove" title="Remove" onClick={() => onRemove(a.id)}>
            <Sym sf="xmark" size={8} weight="bold" icon={X} />
          </button>
        </div>
      ))}
    </div>
  );
}

type VoiceState = { kind: "idle" } | { kind: "recording"; since: number } | { kind: "writing" };

/** The mic beside a message box: tap to speak, tap again to have it written
 *  out, Escape to throw it away. */
export function VoiceButton({ enabled, onText, onProblem }: { enabled: boolean; onText: (text: string) => void; onProblem: (problem: string) => void }) {
  const [state, setState] = useState<VoiceState>({ kind: "idle" });
  const [level, setLevel] = useState(0);
  const [, tick] = useState(0);
  const recording = useRef<Recording | null>(null);

  useEffect(() => () => recording.current?.cancel(), []);
  useEffect(() => {
    if (state.kind !== "recording") return;
    const timer = setInterval(() => tick((n) => n + 1), 1000);
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      recording.current?.cancel();
      recording.current = null;
      setState({ kind: "idle" });
    };
    window.addEventListener("keydown", onKey, true);
    return () => {
      clearInterval(timer);
      window.removeEventListener("keydown", onKey, true);
    };
  }, [state.kind]);

  const tap = async () => {
    if (state.kind === "idle") {
      const rec = new Recording();
      rec.onLevel = setLevel;
      const problem = await rec.start();
      if (problem) return onProblem(problem);
      recording.current = rec;
      setState({ kind: "recording", since: Date.now() });
    } else if (state.kind === "recording") {
      const wav = recording.current?.finish() ?? null;
      recording.current = null;
      setLevel(0);
      if (!wav) {
        setState({ kind: "idle" });
        return onProblem("Nothing was recorded.");
      }
      setState({ kind: "writing" });
      try {
        onText(await transcribe(wav));
      } catch (err) {
        onProblem(err instanceof Error ? err.message : String(err));
      }
      setState({ kind: "idle" });
    }
  };

  const help = state.kind === "idle" ? "Speak your message (Celery writes it out)" : state.kind === "recording" ? "Stop and write it out (Escape throws it away)" : "Writing it out…";
  return (
    <button className={`voice ${state.kind}`} disabled={!enabled || state.kind === "writing"} onClick={() => void tap()} title={help} aria-label={help}>
      {state.kind === "idle" && <Sym sf="mic" size={13} weight="semibold" icon={Mic} />}
      {state.kind === "recording" && (
        <>
          <span className="voice-halo" style={{ transform: `scale(${1 + level * 0.25})` }} />
          <span className="voice-stop" />
          <span className="voice-time">{elapsed((Date.now() - state.since) / 1000)}</span>
        </>
      )}
      {state.kind === "writing" && <Spinner size={16} />}
    </button>
  );
}

/** What a panel command showed, in a sheet: the agent's terminal panel as
 *  text, with its own lines kept. */
export function PanelSheet({ panel, onDone }: { panel: CommandPanel; onDone: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Enter" || e.key === "Escape") {
        e.preventDefault();
        onDone();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onDone]);
  return (
    <div className="sheet-scrim">
      <div className="sheet panel-sheet">
        <div className="panel-sheet-bar">
          <span className="panel-sheet-command">{panel.command}</span>
          <span className="completion-spacer" />
          <button className="mac-button default" onClick={onDone}>Done</button>
        </div>
        <div className="panel-sheet-rule" />
        <div className="panel-sheet-scroll">
          <pre className="panel-sheet-text">{panel.text}</pre>
        </div>
      </div>
    </div>
  );
}
