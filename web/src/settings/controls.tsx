// The macOS form controls the Settings window uses, drawn as AppKit draws
// them in SwiftUI (macOS 26): switches, push buttons, link buttons,
// segmented controls, pop-up buttons, text fields, text editors, checkboxes
// and time fields. Sizes and colours are measured from the Mac app's own
// Settings window (styles/settings.css); pop-up menus are the system's own
// (thread/menu.ts). Each follows whether its window is active, as AppKit's
// do: an inactive window's controls lose their accent colour.

import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { menuUnder, type MenuEntry } from "../thread/menu";
import { Icon } from "../ui";
import type { LucideIcon } from "lucide-react";

/** A switch (Toggle with .switch style), 54×24. */
export function Toggle({ on, onChange, disabled, label }: { on: boolean; onChange: (on: boolean) => void; disabled?: boolean; label?: string }) {
  return (
    <button
      className={`mac-switch ${on ? "on" : ""}`}
      role="switch"
      aria-checked={on}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!on)}
    >
      <span className="knob" />
    </button>
  );
}

/** A push button: bordered, or prominent (the accent colour), or a
 *  destructive role (red text, as SwiftUI's role: .destructive). */
export function PushButton({
  children,
  onClick,
  disabled,
  prominent,
  destructive,
  icon,
  sf,
  title,
  className,
}: {
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  prominent?: boolean;
  destructive?: boolean;
  icon?: LucideIcon;
  sf?: string;
  title?: string;
  className?: string;
}) {
  return (
    <button
      className={`mac-button ${prominent ? "prominent" : ""} ${destructive ? "destructive" : ""} ${className ?? ""}`}
      disabled={disabled}
      title={title}
      onClick={onClick}
    >
      {(icon || sf) && <Icon icon={icon} sf={sf} size={13} />}
      <span>{children}</span>
    </button>
  );
}

/** A link-style button (.buttonStyle(.link)): accent-coloured text. */
export function LinkButton({ children, onClick, disabled, className, style }: { children: ReactNode; onClick?: () => void; disabled?: boolean; className?: string; style?: CSSProperties }) {
  return (
    <button className={`mac-link ${className ?? ""}`} disabled={disabled} onClick={onClick} style={style}>
      {children}
    </button>
  );
}

/** A segmented control (.pickerStyle(.segmented)); segments as wide as
 *  the widest, as .fixedSize() lays them out. */
/** site: one of the pickers New draws as the site's edged segments (Look
 *  and Theme); the rest stay the Mac's own control in both themes. */
export function Segmented<T extends string>({ value, options, onChange, equal = true, site = false }: { value: T; options: { value: T; label: string }[]; onChange: (value: T) => void; equal?: boolean; site?: boolean }) {
  const index = options.findIndex((o) => o.value === value);
  return (
    <div className={`mac-segmented ${equal ? "equal" : ""} ${site ? "site-segments" : ""}`} role="radiogroup" style={{ ["--segments" as string]: options.length }}>
      {options.map((o, i) => (
        <button
          key={o.value}
          role="radio"
          aria-checked={i === index}
          className={`segment ${i === index ? "selected" : ""} ${i === index - 1 || i === index ? "no-sep" : ""}`}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export interface PopUpOption<T> {
  value: T;
  label: string;
  /** A symbol before the label, as a Label in a Picker shows. */
  sf?: string;
  icon?: LucideIcon;
}

/** A pop-up button (Picker's default style): the chosen item, and the
 *  system's own menu of the others, with section headings. */
export function PopUp<T extends string | number>({
  value,
  options,
  sections,
  onChange,
  disabled,
  className,
}: {
  value: T;
  options?: PopUpOption<T>[];
  sections?: { title: string; options: PopUpOption<T>[] }[];
  onChange: (value: T) => void;
  disabled?: boolean;
  className?: string;
}) {
  const all = sections ? sections.flatMap((s) => s.options) : (options ?? []);
  const chosen = all.find((o) => o.value === value);
  const open = (button: HTMLElement) => {
    const entries: MenuEntry[] = [];
    const add = (o: PopUpOption<T>) => entries.push({ label: o.label, checked: o.value === value, action: () => onChange(o.value) });
    if (sections) {
      sections.forEach((s, i) => {
        if (i > 0) entries.push({ separator: true });
        entries.push({ label: s.title, header: true });
        s.options.forEach(add);
      });
    } else {
      all.forEach(add);
    }
    menuUnder(button, entries);
  };
  return (
    <button className={`mac-popup ${className ?? ""}`} disabled={disabled} onClick={(e) => open(e.currentTarget)}>
      {/* As wide as its widest item, as NSPopUpButton sizes itself. */}
      <span className="label stack">
        {all.map((o) => (
          <span key={String(o.value)} className={o.value === chosen?.value ? "" : "ghost"} aria-hidden={o.value !== chosen?.value}>
            {(o.sf || o.icon) && <Icon icon={o.icon} sf={o.sf} size={13} />}
            {o.label}
          </span>
        ))}
      </span>
      <Chevrons />
    </button>
  );
}

/** A menu button (Menu with .menuStyle(.button)): a title, and a menu. */
export function MenuButton({ children, entries, disabled, className }: { children: ReactNode; entries: () => MenuEntry[]; disabled?: boolean; className?: string }) {
  return (
    <button className={`mac-popup menu ${className ?? ""}`} disabled={disabled} onClick={(e) => menuUnder(e.currentTarget, entries())}>
      <span className="label">{children}</span>
      <Chevron />
    </button>
  );
}

/** The up-and-down chevrons at the end of a pop-up button. */
function Chevrons() {
  return (
    <svg className="chevrons" width="7.5" height="10" viewBox="0 0 7.5 10" aria-hidden>
      <path d="M1 3.6 3.75 1 6.5 3.6M1 6.4l2.75 2.6 2.75-2.6" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** The down chevron at the end of a menu button. */
function Chevron() {
  return (
    <svg className="chevrons" width="8" height="5" viewBox="0 0 8 5" aria-hidden>
      <path d="M1 1l3 3 3-3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** A text field (.textFieldStyle(.roundedBorder)), or a secure one. */
export function TextField({
  value,
  onChange,
  placeholder,
  secure,
  mono,
  width,
  onSubmit,
  disabled,
  className,
  autoFocus,
  align,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  secure?: boolean;
  mono?: boolean;
  width?: number;
  onSubmit?: () => void;
  disabled?: boolean;
  className?: string;
  autoFocus?: boolean;
  align?: "left" | "right";
}) {
  return (
    <input
      className={`mac-field ${mono ? "mono" : ""} ${className ?? ""}`}
      type={secure ? "password" : "text"}
      value={value}
      placeholder={placeholder}
      disabled={disabled}
      autoFocus={autoFocus}
      spellCheck={false}
      autoComplete="off"
      autoCorrect="off"
      autoCapitalize="off"
      style={{ width, textAlign: align }}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e: KeyboardEvent<HTMLInputElement>) => {
        if (e.key === "Enter" && onSubmit) {
          e.preventDefault();
          onSubmit();
        }
      }}
    />
  );
}

/** A multi-line text editor (TextEditor), clear on its card. */
export function TextArea({ value, onChange, placeholder, className, style }: { value: string; onChange: (value: string) => void; placeholder?: string; className?: string; style?: CSSProperties }) {
  return (
    <textarea
      className={`mac-editor ${className ?? ""}`}
      value={value}
      placeholder={placeholder}
      spellCheck={false}
      style={style}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

/** A checkbox with its label (.toggleStyle(.checkbox)). */
export function Checkbox({ on, onChange, children, title }: { on: boolean; onChange: (on: boolean) => void; children: ReactNode; title?: string }) {
  return (
    <button className={`mac-checkbox ${on ? "on" : ""}`} role="checkbox" aria-checked={on} title={title} onClick={() => onChange(!on)}>
      <span className="box">
        {on && (
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden>
            <path d="M2 5.3 4.1 7.4 8 2.6" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        )}
      </span>
      <span className="label">{children}</span>
    </button>
  );
}

/** A time of day (DatePicker, .field style, hour and minute): minutes after
 *  midnight. Its parts take typed digits and the arrow keys; the stepper
 *  beside it steps the part last chosen. */
export function TimeField({ minutes, onChange, disabled }: { minutes: number; onChange: (minutes: number) => void; disabled?: boolean }) {
  const [part, setPart] = useState<"h" | "m" | null>(null);
  const typed = useRef("");
  const hour24 = Math.floor(minutes / 60) % 24;
  const minute = minutes % 60;
  const twelve = uses12Hour();
  const hourShown = twelve ? ((hour24 + 11) % 12) + 1 : hour24;
  const set = (h: number, m: number) => onChange((((h % 24) + 24) % 24) * 60 + (((m % 60) + 60) % 60));
  const step = (by: number, which = part ?? "h") => {
    if (which === "h") set(hour24 + by, minute);
    else set(hour24, minute + by);
  };
  useEffect(() => {
    typed.current = "";
  }, [part]);
  const onKey = (e: KeyboardEvent) => {
    if (disabled || !part) return;
    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      e.preventDefault();
      step(e.key === "ArrowUp" ? 1 : -1);
    } else if (e.key === "ArrowRight" || e.key === "Tab") {
      if (part === "h") {
        e.preventDefault();
        setPart("m");
      }
    } else if (e.key === "ArrowLeft") {
      setPart("h");
    } else if (/^\d$/.test(e.key)) {
      e.preventDefault();
      typed.current = (typed.current + e.key).slice(-2);
      const n = Number(typed.current);
      if (part === "h") set(twelve ? (n % 12) + (hour24 >= 12 ? 12 : 0) : n, minute);
      else set(hour24, n);
      if (typed.current.length === 2 && part === "h") setPart("m");
    } else if (twelve && (e.key === "a" || e.key === "p")) {
      set(e.key === "a" ? hour24 % 12 : (hour24 % 12) + 12, minute);
    }
  };
  return (
    <div className="mac-time" aria-disabled={disabled}>
      <div className="field" tabIndex={disabled ? -1 : 0} onKeyDown={onKey} onBlur={() => setPart(null)}>
        <span className={`part ${part === "h" ? "chosen" : ""}`} onMouseDown={() => !disabled && setPart("h")}>
          {twelve ? hourShown : String(hourShown).padStart(2, "0")}
        </span>
        <span>:</span>
        <span className={`part ${part === "m" ? "chosen" : ""}`} onMouseDown={() => !disabled && setPart("m")}>
          {String(minute).padStart(2, "0")}
        </span>
        {twelve && <span className="ampm">{hour24 < 12 ? "AM" : "PM"}</span>}
      </div>
      <div className="stepper">
        <button disabled={disabled} tabIndex={-1} aria-label="Later" onClick={() => step(1)}>
          <svg width="7" height="4" viewBox="0 0 7 4" aria-hidden>
            <path d="M.8 3.4 3.5.8l2.7 2.6" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
        <button disabled={disabled} tabIndex={-1} aria-label="Earlier" onClick={() => step(-1)}>
          <svg width="7" height="4" viewBox="0 0 7 4" aria-hidden>
            <path d="M.8.6 3.5 3.2 6.2.6" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
      </div>
    </div>
  );
}

/** Whether the computer shows times on a 12-hour clock. */
function uses12Hour(): boolean {
  try {
    return new Intl.DateTimeFormat(undefined, { hour: "numeric" }).resolvedOptions().hour12 === true;
  } catch {
    return false;
  }
}

/** A small spinner (ProgressView, .small). */
export function Spinner() {
  return <span className="mac-spinner" aria-label="Loading" />;
}

/** ~/… for a path in the home folder, as abbreviatingWithTildeInPath. */
export function tilde(path: string, home: string): string {
  if (home && (path === home || path.startsWith(home + "/"))) return "~" + path.slice(home.length);
  return path;
}

/** The last part of a path, as lastPathComponent. */
export function lastPart(path: string): string {
  const parts = path.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || path;
}
