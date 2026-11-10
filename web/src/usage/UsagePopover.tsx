// Usage from the editor's status bar, without leaving the editor
// (Editor/UsagePopover.swift): the last 30 days' spend, what is left of each
// plan, and the way to the full page. Plan limits are read when this opens,
// and only then: reading Claude's starts Claude Code, which nobody should
// pay for while coding.

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ArrowUpRight } from "lucide-react";
import type { Limits, Usage } from "@shared/protocol";
import { money } from "@shared/usage";
import { openInMain } from "../host";
import { Loader } from "../shell/Loader";
import { Icon } from "../ui";
import { LIMITS_FOOTNOTE, LimitsPage, readLimits, readUsage } from "./UsageView";

const WIDTH = 560;

export function UsagePopover({ close }: { close: () => void }) {
  const [usage, setUsage] = useState<Usage>();
  const [limits, setLimits] = useState<Limits>();
  const [problem, setProblem] = useState<string>();
  const [reading, setReading] = useState(true);
  const [place, setPlace] = useState<{ left: number; bottom: number; tallest: number }>();
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    void readUsage(30).then((u) => live && setUsage(u)).catch(() => {});
    readLimits(false)
      .then((l) => live && setLimits(l))
      .catch((e: unknown) => live && setProblem(e instanceof Error ? e.message : String(e)))
      .finally(() => live && setReading(false));
    return () => {
      live = false;
    };
  }, []);

  // Above the status bar's Usage, as a popover hangs from it; only as tall
  // as the window has room for, the limits scrolling past that.
  useLayoutEffect(() => {
    const place = () => {
      const anchor = document.querySelector(".status-usage")?.getBoundingClientRect();
      const top = anchor?.top ?? window.innerHeight - 24;
      const middle = anchor ? anchor.left + anchor.width / 2 : window.innerWidth - WIDTH / 2;
      const left = Math.min(Math.max(middle - WIDTH / 2, 8), Math.max(window.innerWidth - WIDTH - 8, 8));
      setPlace({ left, bottom: window.innerHeight - top + 6, tallest: Math.max(top - 160, 260) });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, []);

  // A click elsewhere or Escape closes it; the Usage button toggles it itself.
  useEffect(() => {
    const away = (e: PointerEvent) => {
      const target = e.target as HTMLElement;
      if (!box.current?.contains(target) && !target.closest(".status-usage")) close();
    };
    const escape = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    window.addEventListener("pointerdown", away, true);
    window.addEventListener("keydown", escape);
    return () => {
      window.removeEventListener("pointerdown", away, true);
      window.removeEventListener("keydown", escape);
    };
  }, [close]);

  return (
    <div ref={box} className="usage-popover" role="dialog" aria-label="Usage" style={place ? { left: place.left, bottom: place.bottom, width: WIDTH } : { visibility: "hidden" }}>
      <div className="usage-popover-head">
        <span className="usage-popover-title">Usage</span>
        <span className="usage-spacer" />
        {usage && <span className="usage-caption tabular">{money(usage.cost.input + usage.cost.cacheRead + usage.cost.cacheWrite + usage.cost.output)} in 30 days</span>}
      </div>
      {limits && limits.agents.length > 0 ? (
        <div className="usage-popover-limits" style={{ maxHeight: place?.tallest }}>
          <LimitsPage limits={limits} />
        </div>
      ) : reading ? (
        <div className="usage-popover-reading">
          <Loader size={12} />
          <span>Reading what is left of each plan…</span>
        </div>
      ) : (
        <div className="usage-caption">{problem ?? "No plan limits to show. " + LIMITS_FOOTNOTE}</div>
      )}
      <div className="usage-popover-rule" />
      <button
        className="usage-popover-open"
        onClick={() => {
          close();
          openInMain("agentman://usage");
        }}
      >
        <span>Open Usage</span>
        <Icon icon={ArrowUpRight} sf="arrow.up.right" size={9} weight="semibold" />
      </button>
    </div>
  );
}
