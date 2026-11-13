// Shipping finished agents together and the work lined up for tonight
// (Views/BoardActions.swift's ShipSheet and TonightPanel).

import { useEffect, useRef, useState } from "react";
import { useKeyboardBack } from "../palette/PaletteView";
import { CircleArrowUp, CircleCheck, CircleMinus, CircleX, Clock, Moon, TriangleAlert, X } from "lucide-react";
import type { Session, ShipItem } from "@shared/protocol";
import { openURL } from "../host";
import { addToQueue, removeFromQueue, ship, useManager } from "../manager/store";
import { saveSettings } from "../settings/state";
import { Loader } from "../shell/Loader";
import { loadFolders, useShell } from "../shell/state";
import { git, type GitState } from "../workspace/api";
import { shipReason, shipSummary } from "./git";

/** Choosing finished agents to ship together, and watching it happen. */
export function ShipSheet({ sessions, close }: { sessions: Session[]; close: () => void }) {
  useKeyboardBack();
  const manager = useManager();
  const [states, setStates] = useState<Record<string, GitState>>({});
  const [loaded, setLoaded] = useState(false);
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [runId, setRunId] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const run = runId && manager.shipRun?.id === runId ? manager.shipRun : null;

  useEffect(() => {
    void (async () => {
      const found: Record<string, GitState> = {};
      for (const s of sessions) {
        try {
          found[s.id] = await git("git_status", s.id);
        } catch {
          // Not in a repository: its row says so.
        }
      }
      setStates(found);
      // Work Agentman had doubts about stays unticked until the user says.
      setChosen(new Set(sessions.filter((s) => !shipReason(s, found[s.id], manager.verifications[s.id]) && manager.verifications[s.id]?.state !== "concerns").map((s) => s.id)));
      setLoaded(true);
    })();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [close]);

  const go = async () => {
    const ids = sessions.map((s) => s.id).filter((id) => chosen.has(id));
    const answer = await ship(ids);
    if (answer.id) setRunId(answer.id);
    else setProblem(answer.problem ?? "That didn't work.");
  };

  return (
    <div className="sheet-backdrop">
      <div className="sheet board-ship" role="dialog" aria-label="Ship finished work">
        <div className="board-ship-head">
          <div className="board-ship-title">Ship finished work</div>
          <div className="board-ship-subtitle">Each one is committed, pushed and opened as a pull request. Default branches are left to you.</div>
        </div>
        <div className="board-ship-list">
          {run ? run.items.map((item) => <ShipProgressRow key={item.sessionId} item={item} />)
            : !loaded ? <div className="board-ship-reading"><Loader size={11} /><span>Reading where each one stands</span></div>
            : sessions.map((s) => {
              const state = states[s.id];
              const check = manager.verifications[s.id];
              const reason = shipReason(s, state, check);
              return (
                <label key={s.id} className={`board-ship-row${reason ? " off" : ""}`}>
                  <input type="checkbox" className="checkbox" disabled={!!reason} checked={chosen.has(s.id)} onChange={(e) => {
                    const next = new Set(chosen);
                    if (e.target.checked) next.add(s.id);
                    else next.delete(s.id);
                    setChosen(next);
                  }} />
                  <span className="board-ship-words">
                    <span className="board-ship-name">{s.name}</span>
                    <span className="board-ship-why">{reason ?? (state ? shipSummary(state) : "")}</span>
                  </span>
                  {check?.state === "concerns" && <span className="board-ship-doubt" title={check.summary ?? "Agentman's check found something"}><TriangleAlert size={10} /></span>}
                </label>
              );
            })}
        </div>
        {problem && <div className="commit-problem error-text">{problem}</div>}
        <div className="board-ship-buttons">
          <span className="card-spacer" />
          {run?.state === "done" ? (
            <button className="push-button default" onClick={close}>Done</button>
          ) : !run ? (
            <>
              <button className="push-button" onClick={close}>Cancel</button>
              <button className="push-button default" disabled={chosen.size === 0} onClick={() => void go()}>
                <CircleArrowUp size={12} />{chosen.size ? `Ship ${chosen.size}` : "Ship"}
              </button>
            </>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function ShipProgressRow({ item }: { item: ShipItem }) {
  const icon = item.state === "working" ? <Loader size={10} />
    : item.state === "shipped" ? <CircleCheck size={11} className="ok" />
    : item.state === "failed" ? <CircleX size={11} className="error" />
    : item.state === "skipped" ? <CircleMinus size={11} className="faint" />
    : <Clock size={10} className="faint" />;
  return (
    <div className="board-ship-row">
      <span className="board-ship-icon">{icon}</span>
      <span className="board-ship-words">
        <span className="board-ship-name">{item.name}</span>
        {item.note && <span className={`board-ship-why${item.state === "failed" ? " error-text" : ""}`}>{item.note}</span>}
      </span>
      {item.url && <button className="board-ship-link" onClick={() => void openURL(item.url!)}>View PR</button>}
    </div>
  );
}

const TONIGHT_AGENTS = ["claude", "codex", "cursor", "opencode", "kiro", "antigravity"];

/** The moon in the Board's top bar: tonight's work, and how many wait. */
export function TonightButton() {
  const { queue } = useManager();
  const [open, setOpen] = useState(false);
  const waiting = queue.filter((q) => q.state === "waiting" || q.state === "running").length;
  const button = useRef<HTMLButtonElement>(null);
  return (
    <span className="board-tonight">
      <button ref={button} className={`board-top-button${waiting ? " counted" : ""}`} title="Work lined up for tonight" aria-label="Tonight's queue" onClick={() => setOpen(!open)}>
        <Moon size={11} />
        {waiting > 0 && <span className="board-top-count">{waiting}</span>}
      </button>
      {open && <TonightPanel anchor={button.current} close={() => setOpen(false)} />}
    </span>
  );
}

function TonightPanel({ anchor, close }: { anchor: HTMLElement | null; close: () => void }) {
  // Closed, the keyboard goes back to the button that opened it.
  useKeyboardBack();
  const { queue, settings } = useManager();
  const { folders } = useShell();
  const [task, setTask] = useState("");
  const [folder, setFolder] = useState("");
  const [agent, setAgent] = useState("claude");
  const [problem, setProblem] = useState<string | undefined>();
  const panel = useRef<HTMLDivElement>(null);
  const autopilot = settings?.autopilot;
  const canAdd = task.trim() !== "" && folder !== "";
  useEffect(() => void loadFolders(), []);

  // A popover: a click elsewhere or Escape closes it.
  useEffect(() => {
    const down = (e: MouseEvent) => {
      if (!panel.current?.contains(e.target as Node) && !anchor?.contains(e.target as Node)) close();
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && close();
    document.addEventListener("mousedown", down);
    window.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      window.removeEventListener("keydown", key);
    };
  }, [anchor, close]);

  const box = anchor?.getBoundingClientRect();
  const left = box ? Math.max(8, Math.min(window.innerWidth - 388, box.left + box.width / 2 - 190)) : 8;
  const top = box ? box.bottom + 8 : 40;

  const add = async () => {
    const said = await addToQueue(agent, folder, task.trim());
    setProblem(said);
    if (!said) setTask("");
  };
  const turnOn = async () => {
    if (!settings) return setProblem("Agentman isn't connected.");
    const base = { verify: false, sendBack: false, learnRules: false, unstick: false, overlap: false, learnNotes: false, overnight: false };
    setProblem(await saveSettings({ ...settings, autopilot: { ...base, ...settings.autopilot, overnight: true } }));
  };

  return (
    <div className="board-popover" ref={panel} style={{ left, top }} role="dialog" aria-label="Tonight">
      <div className="board-popover-head">
        <div className="board-popover-title">Tonight</div>
        <div className="board-popover-subtitle">Runs {autopilot?.nightStart ?? "23:00"} to {autopilot?.nightEnd ?? "07:00"}, with a report in the morning.</div>
      </div>
      {queue.map((item) => (
        <div key={item.id} className="board-queue-item">
          <span className="board-queue-words">
            <span className="board-queue-task">{item.task}</span>
            <span className="board-queue-meta">{item.folder.replace(/\/+$/, "").split("/").pop()} · {item.agent} · {item.state}</span>
          </span>
          {item.state === "waiting" && (
            <button className="board-queue-remove" aria-label="Remove from tonight" onClick={() => void removeFromQueue(item.id).then(setProblem)}><X size={9} strokeWidth={3} /></button>
          )}
        </div>
      ))}
      {queue.length > 0 && <div className="board-rule" />}
      <textarea className="board-tonight-task" rows={2} placeholder="What should happen tonight?" value={task} onChange={(e) => setTask(e.target.value)} />
      <div className="board-tonight-row">
        <select value={folder} onChange={(e) => setFolder(e.target.value)}>
          <option value="">Choose a folder</option>
          {folders.map((f) => <option key={f.path} value={f.path}>{f.path.replace(/\/+$/, "").split("/").pop()}</option>)}
        </select>
        <select value={agent} onChange={(e) => setAgent(e.target.value)}>
          {TONIGHT_AGENTS.map((k) => <option key={k} value={k}>{k.charAt(0).toUpperCase() + k.slice(1)}</option>)}
        </select>
        <button className="push-button default small" disabled={!canAdd} onClick={() => void add()}>Add</button>
      </div>
      {problem && <div className="commit-problem error-text">{problem}</div>}
      {autopilot?.overnight !== true && (
        <>
          <div className="board-rule" />
          <div className="board-tonight-off">
            <span>The overnight queue is off</span>
            <span className="card-spacer" />
            <button className="push-button small" onClick={() => void turnOn()}>Turn On</button>
          </div>
        </>
      )}
    </div>
  );
}
