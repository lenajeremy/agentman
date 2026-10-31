// The terminal panel (Terminal/TerminalPanel.swift), in the manner of VS
// Code's: a row of shells, each a tab, with new, split, clear, stop, move and
// hide at its end, over the shell in front. Every shell of the folder keeps
// its view here, shown or not, so none is attached again when tabs change;
// a split tab shows two side by side.

import { useEffect, useState } from "react";
import { ChevronDown, Columns2, Eraser, PanelBottom, PanelRight, Plus, SquareTerminal, Trash2, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";
import { clearShell, focusedShell, ShellView } from "./ShellView";
import { isSplitHalf, shellCommand, splitPartner, type FolderShells, type Shell } from "./shells";
import { dockTerminal } from "../panel/state";
import { Sym } from "../panel/Sym";
import "../styles/terminal.css";

/** Where a terminal in the main window can go instead. */
export type TerminalMove = "toBottom" | "toSide";

const MOVES: Record<TerminalMove, { sf: string; icon: LucideIcon; help: string; action: () => void }> = {
  toBottom: { sf: "rectangle.bottomhalf.inset.filled", icon: PanelBottom, help: "Move the terminal under the conversation", action: () => dockTerminal("bottom") },
  toSide: { sf: "rectangle.righthalf.inset.filled", icon: PanelRight, help: "Move the terminal into the side panel", action: () => dockTerminal("side") },
};

function BarButton({ sf, icon, help, disabled, onClick }: { sf: string; icon: LucideIcon; help: string; disabled?: boolean; onClick: () => void }) {
  return (
    <button className="term-button" title={help} aria-label={help} disabled={disabled} onClick={onClick}>
      <Icon icon={icon} sf={sf} size={11} weight="medium" />
    </button>
  );
}

function Tab({ folder, shell, active, split, partner }: { folder: string; shell: Shell; active: boolean; split: boolean; partner?: Shell }) {
  return (
    <div className={`term-tab ${active ? "active" : ""}`} onClick={() => shellCommand(folder, { type: "activate", id: shell.id })}>
      <span className={`term-tab-icon ${shell.exited ? "exited" : ""}`}>
        <Sym icon={split ? Columns2 : SquareTerminal} sf={split ? "square.split.2x1" : "terminal"} size={10} />
      </span>
      <span className="term-tab-title">{shell.title}</span>
      {shell.exited && <span className="term-tab-ended">ended</span>}
      <button
        className="term-tab-close"
        title="Stop this shell and close it"
        onClick={(e) => {
          e.stopPropagation();
          // A split tab closes whole, both its shells.
          if (partner) shellCommand(folder, { type: "close", id: partner.id });
          shellCommand(folder, { type: "close", id: shell.id });
        }}
      >
        <Icon icon={X} sf="xmark" size={8} weight="bold" />
      </button>
    </div>
  );
}

export function TerminalPanel({ folder, state, hide, move }: { folder: string; state: FolderShells; hide?: () => void; move?: TerminalMove }) {
  const active = state.shells.find((s) => s.id === state.active);
  const right = splitPartner(state, state.active);
  const showing = [active, right].filter((s): s is Shell => !!s);
  // The shell with the keyboard, of the one or two showing; else the one in front.
  const [focusTick, setFocusTick] = useState(0);
  const focused = showing.find((s) => s.id === focusedShell(showing.map((x) => x.id))) ?? active;
  useEffect(() => {
    const onFocus = () => setFocusTick((n) => n + 1);
    document.addEventListener("focusin", onFocus);
    return () => document.removeEventListener("focusin", onFocus);
  }, []);
  void focusTick;
  const tabs = state.shells.filter((s) => !isSplitHalf(state, s.id));
  const m = move ? MOVES[move] : undefined;

  return (
    <div className="term-panel">
      <div className="term-bar">
        <span className="term-label">TERMINAL</span>
        <div className="term-tabs">
          {tabs.map((shell) => (
            <Tab key={shell.id} folder={folder} shell={shell} active={shell.id === state.active}
              split={!!state.splits[shell.id]} partner={splitPartner(state, shell.id)} />
          ))}
        </div>
        <span className="term-spacer" />
        {focused?.exited && (
          <button className="push-button small" onClick={() => shellCommand(folder, { type: "restart", id: focused.id })}>Restart</button>
        )}
        <BarButton sf="plus" icon={Plus} help="New terminal (⌃⇧`)" onClick={() => shellCommand(folder, { type: "new" })} />
        {right ? (
          <BarButton sf="square.split.2x1.fill" icon={Columns2} help="Close the shell on the right" onClick={() => shellCommand(folder, { type: "unsplit" })} />
        ) : (
          <BarButton sf="square.split.2x1" icon={Columns2} help="Split the terminal: a new shell beside this one" disabled={!active}
            onClick={() => shellCommand(folder, { type: "split" })} />
        )}
        <BarButton sf="eraser" icon={Eraser} help="Clear (⌘K in the terminal)" disabled={!active} onClick={() => focused && clearShell(focused.id)} />
        <BarButton sf="trash" icon={Trash2} help="Stop this shell and close it" disabled={!active}
          onClick={() => focused && shellCommand(folder, { type: "close", id: focused.id })} />
        {m && <BarButton sf={m.sf} icon={m.icon} help={m.help} onClick={m.action} />}
        {hide && <BarButton sf="chevron.down" icon={ChevronDown} help="Hide the terminal (⌃`)" onClick={hide} />}
      </div>
      <div className={`term-body ${right ? "split" : ""}`}>
        {state.shells.map((shell) => {
          const isRight = shell.id === right?.id;
          const visible = shell.id === state.active || isRight;
          return (
            <div key={shell.id} className={`term-slot ${isRight ? "right" : "left"}`} style={{ zIndex: visible ? 1 : 0 }}>
              <ShellView id={shell.id} visible={visible} />
            </div>
          );
        })}
        {right && <div className="term-split-line" />}
      </div>
    </div>
  );
}
