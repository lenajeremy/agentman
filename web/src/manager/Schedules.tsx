// The clock in Agentman's title bar while it has something scheduled, and
// the popover listing its reminders and watches, each with a way to cancel
// it (ManagerChatView.swift's SchedulesButton, SchedulesList).

import { useEffect, useRef, useState } from "react";
import { Bell, CircleX, Clock, Eye, Repeat } from "lucide-react";
import type { ManagerSchedule } from "@shared/protocol";
import { Icon } from "../ui";
import { cancelSchedule, useManager } from "./store";
import { keepLastWords } from "./text";
import { shortTime, useTimeLocale } from "./time";

type Schedule = ManagerSchedule & { times?: number; fired?: number; until?: number };

const time = shortTime;

function title(item: Schedule): string {
  if (item.kind !== "watch") return item.text;
  const name = item.agentName ?? "An agent";
  const until = item.condition === "needs_user" ? "needs you" : item.condition === "idle" ? "is idle" : item.condition === "ended" ? "ends" : "finishes its turn";
  return `When ${name} ${until}` + (item.text ? `: ${item.text}` : "");
}

function when(item: Schedule): string {
  if (item.kind === "watch") return `Gives up at ${time(item.due)}`;
  let text = `Next at ${time(item.due)}`;
  if (item.every && item.every > 0) {
    const minutes = Math.floor(item.every / 60);
    text += minutes % 60 === 0 && minutes >= 60 ? ` · every ${minutes / 60} h` : ` · every ${minutes} min`;
    if (item.times && item.times > 0) text += ` · ${item.times - (item.fired ?? 0)} more`;
    else if (item.until && item.until > 0) text += ` · until ${time(item.until)}`;
  }
  return text;
}

function SchedulesList() {
  useTimeLocale();
  const s = useManager();
  const [problem, setProblem] = useState<string>();
  return (
    <div className="am-schedules">
      <div className="am-schedules-title">Scheduled</div>
      {s.schedules.length === 0 && <div className="am-schedules-empty">Nothing scheduled. Ask Agentman to remind itself, or to watch an agent until it's done.</div>}
      {(s.schedules as Schedule[]).map((item) => (
        <div key={item.id} className="am-schedule">
          <span className="am-schedule-icon">
            {item.kind === "watch" ? <Icon icon={Eye} sf="eye" size={12} weight="medium" /> : (item.every ?? 0) > 0 ? <Icon icon={Repeat} sf="repeat" size={12} weight="medium" /> : <Icon icon={Bell} sf="bell" size={12} weight="medium" />}
          </span>
          <div className="am-schedule-text">
            <div className="am-schedule-what">{keepLastWords(title(item))}</div>
            <div className="am-schedule-when">{when(item)}</div>
          </div>
          <button className="am-schedule-cancel" title="Cancel" onClick={() => void cancelSchedule(item).then(setProblem)}>
            <Icon icon={CircleX} sf="xmark.circle.fill" size={14} />
          </button>
        </div>
      ))}
      {problem && <div className="am-problem">{problem}</div>}
    </div>
  );
}

const openAtStart = new URLSearchParams(location.search).get("schedules") === "1";

export function SchedulesButton() {
  const s = useManager();
  const [open, setOpen] = useState(openAtStart);
  const button = useRef<HTMLButtonElement>(null);
  const popover = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);

  useEffect(() => {
    if (!open) return;
    const place = () => {
      const box = button.current?.getBoundingClientRect();
      if (box) setAt({ x: box.left + box.width / 2, y: box.bottom });
    };
    place();
    const close = (e: MouseEvent) => {
      if (!popover.current?.contains(e.target as Node) && !button.current?.contains(e.target as Node)) setOpen(false);
    };
    const escape = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", escape);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", escape);
      window.removeEventListener("resize", place);
    };
  }, [open]);

  return (
    <>
      <button ref={button} className="am-scheduled-button no-drag" title="What Agentman has scheduled for itself" onClick={() => setOpen(!open)}>
        <Icon icon={Clock} sf="clock" size={12} />
        <span>{s.schedules.length} scheduled</span>
      </button>
      {open && at && (
        <div ref={popover} className="am-popover" style={{ left: Math.min(at.x - 180, window.innerWidth - 368), top: at.y + 4 }}>
          <span className="am-popover-arrow" style={{ left: at.x - Math.min(at.x - 180, window.innerWidth - 368) }} />
          <SchedulesList />
        </div>
      )}
    </>
  );
}
