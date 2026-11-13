// The words of a message box (Design/MessageField.swift, FileChip.swift):
// Return sends, Shift- or Option-Return starts a new line, ⌘Return sends;
// it grows to ten lines before it scrolls. A file named with @ shows as a
// chip, its icon and name, one character to the caret and to Backspace, and
// copies as @path. Images pasted on it go to the box's attachments.
//
// An editable element rather than a textarea, which can't hold a chip. Its
// text is kept as the Mac's NSTextView keeps it: the chips as one character
// each ("display" text, for the caret), and as @path in what is sent.

import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useRef, type ClipboardEvent, type KeyboardEvent } from "react";
import { fileTypeIcon } from "./fileTypeIcon";

const CHIP = "￼";

export interface FieldApi {
  /** Focus, with the caret at the end. */
  focus(): void;
  /** Puts text in place of [start, end) of the display text, as typing would. */
  replace(start: number, end: number, text: string): void;
  /** Puts text in place of everything, as typing would. */
  replaceAll(text: string): void;
  /** Whether the caret, with nothing selected, is on the field's first or
   *  last line as drawn: a long line wraps onto several (MessageField.swift's
   *  caretLine). */
  caretOnLine(line: "first" | "last"): boolean;
  /** Puts a prompt from the history, or the draft back, in the field, as one
   *  change undo takes back: going back, the caret at its start, so the next
   *  ↑ goes on back; coming forward, at its end. */
  show(text: string, caretAtStart: boolean): void;
}

interface Props {
  value: string;
  placeholder: string;
  enabled: boolean;
  onChange: (plain: string) => void;
  onSubmit: () => void;
  /** Keys for the lists over the box; true when taken. */
  onKey?: (e: KeyboardEvent<HTMLDivElement>) => boolean;
  /** The display text and the caret, as either changes (for @). */
  onCaret?: (display: string, caret: number | null) => void;
  onFocusChange?: (focused: boolean) => void;
  /** Images pasted; true when they were taken. */
  onPasteImages?: (files: File[]) => boolean;
  /** Whether @path names a file in the folder, to draw as a chip. */
  isFile: (path: string) => boolean;
}

interface Segment {
  node: Node;
  kind: "text" | "chip" | "break";
  start: number;
  length: number;
  path?: string;
}

/** The field's pieces in order, with where each starts in the display text. */
function segments(root: HTMLElement): Segment[] {
  const out: Segment[] = [];
  let at = 0;
  const walk = (parent: Node) => {
    const children = Array.from(parent.childNodes);
    children.forEach((node, i) => {
      if (node.nodeType === Node.TEXT_NODE) {
        const length = (node as Text).data.length;
        out.push({ node, kind: "text", start: at, length });
        at += length;
      } else if (node instanceof HTMLElement && node.dataset.path !== undefined) {
        out.push({ node, kind: "chip", start: at, length: 1, path: node.dataset.path });
        at += 1;
      } else if (node.nodeName === "BR") {
        // The break WebKit keeps in an empty field, or at its end, isn't a line.
        if (parent === root && i === children.length - 1) return;
        out.push({ node, kind: "break", start: at, length: 1 });
        at += 1;
      } else if (node instanceof HTMLElement) {
        // A block an edit made: a line of its own.
        if (out.length && /^(DIV|P)$/.test(node.nodeName)) {
          out.push({ node, kind: "break", start: at, length: 1 });
          at += 1;
        }
        walk(node);
      }
    });
  };
  walk(root);
  return out;
}

function display(segs: Segment[]): string {
  return segs.map((s) => (s.kind === "text" ? (s.node as Text).data : s.kind === "chip" ? CHIP : "\n")).join("");
}

function plain(segs: Segment[]): string {
  return segs.map((s) => (s.kind === "text" ? (s.node as Text).data : s.kind === "chip" ? `@${s.path}` : "\n")).join("");
}

/** A display offset for a DOM position in the field. */
function offsetOf(root: HTMLElement, segs: Segment[], node: Node, offset: number): number {
  if (node.nodeType === Node.TEXT_NODE) {
    const seg = segs.find((s) => s.node === node);
    return seg ? seg.start + Math.min(offset, seg.length) : 0;
  }
  // A position between children: everything before the offset-th child.
  const children = Array.from(node.childNodes);
  const before = children[offset];
  if (!before) {
    const last = [...segs].reverse().find((s) => node === root || node.contains(s.node));
    return last ? last.start + last.length : node === root ? 0 : offsetOf(root, segs, node.parentNode!, Array.from(node.parentNode!.childNodes).indexOf(node as ChildNode) + 1);
  }
  const first = segs.find((s) => s.node === before || before.contains(s.node));
  if (first) return first.start;
  const total = segs.length ? segs[segs.length - 1].start + segs[segs.length - 1].length : 0;
  return total;
}

/** The DOM position of a display offset. */
function positionOf(root: HTMLElement, segs: Segment[], at: number): { node: Node; offset: number } {
  for (const s of segs) {
    if (at > s.start + s.length) continue;
    if (s.kind === "text") return { node: s.node, offset: at - s.start };
    const parent = s.node.parentNode!;
    const index = Array.from(parent.childNodes).indexOf(s.node as ChildNode);
    return { node: parent, offset: at <= s.start ? index : index + 1 };
  }
  return { node: root, offset: root.childNodes.length };
}

/** Where a collapsed range is drawn; null where the browser can't say (a
 *  position between elements). */
function caretRect(range: Range): DOMRect | null {
  const rects = range.getClientRects();
  const rect = rects.length ? rects[0] : range.getBoundingClientRect();
  return rect.height === 0 && rect.top === 0 ? null : rect;
}

/** Two rects on one line as drawn: their middles within half a line. */
function sameLine(a: DOMRect, b: DOMRect): boolean {
  return Math.abs(a.top + a.height / 2 - (b.top + b.height / 2)) < Math.max(4, Math.min(a.height, b.height) / 2);
}

function escape(text: string): string {
  return text.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
}

/** A chip's markup: the file's icon and its name, uneditable, one unit. */
function chipHTML(path: string): string {
  const name = path.split("/").pop() || path;
  return `<span class="chip" contenteditable="false" data-path="${escape(path)}"><img class="chip-icon" src="/assets/files/${fileTypeIcon(name)}.svg" alt="">${escape(name)}</span>`;
}

const WORD = /(?<![^\s])@([^\s@￼]+)/g;

export const MessageField = forwardRef<FieldApi, Props>(function MessageField(props, ref) {
  const { value, placeholder, enabled, onChange, onSubmit, onKey, onCaret, onFocusChange, onPasteImages, isFile } = props;
  const el = useRef<HTMLDivElement>(null);
  const last = useRef<string | null>(null);
  const chipping = useRef(false);

  // Off while a message sends, the box can't hold the keyboard, and a
  // browser drops it; the Mac's keeps it. Had it when it went off, it takes
  // it back when it's on again, unless something else has it by then. Read
  // as it renders, before the box goes off and loses it.
  const wasOn = useRef(enabled);
  const hadKeyboard = useRef(false);
  if (wasOn.current && !enabled && el.current && document.activeElement === el.current) hadKeyboard.current = true;
  wasOn.current = enabled;
  useLayoutEffect(() => {
    if (!enabled || !hadKeyboard.current) return;
    hadKeyboard.current = false;
    const now = document.activeElement;
    if (now && now !== document.body) return;
    el.current?.focus();
  }, [enabled]);

  /** As tall as the Mac measures its text: n lines of 17
   *  with 3 between them, one line at least, ten at most; the lines
   *  themselves are 20 apart, as its text view sets them. */
  const fit = useCallback(() => {
    const root = el.current;
    if (!root) return;
    root.style.height = "auto";
    const lines = Math.max(1, Math.round(root.scrollHeight / 20));
    root.style.height = `${Math.min(200, Math.max(20, lines * 17 + (lines - 1) * 3))}px`;
  }, []);

  useEffect(() => {
    const root = el.current;
    if (!root) return;
    let width = root.clientWidth;
    const observer = new ResizeObserver(() => {
      if (root.clientWidth !== width) {
        width = root.clientWidth;
        fit();
      }
    });
    observer.observe(root);
    return () => observer.disconnect();
  }, [fit]);

  const report = useCallback(() => {
    const root = el.current;
    if (!root) return;
    const segs = segments(root);
    const text = plain(segs);
    root.dataset.empty = String(segs.length === 0 || display(segs) === "");
    fit();
    if (text !== last.current) {
      last.current = text;
      onChange(text);
    }
    const selection = window.getSelection();
    let caret: number | null = null;
    if (selection && selection.rangeCount && selection.isCollapsed && root.contains(selection.anchorNode) && document.activeElement === root) {
      caret = offsetOf(root, segs, selection.anchorNode!, selection.anchorOffset);
    }
    onCaret?.(display(segs), caret);
  }, [onChange, onCaret]);

  const select = (start: number, end: number) => {
    const root = el.current!;
    const segs = segments(root);
    const a = positionOf(root, segs, start);
    const b = positionOf(root, segs, end);
    const range = document.createRange();
    range.setStart(a.node, a.offset);
    range.setEnd(b.node, b.offset);
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
  };

  /** Draws each @path naming a file as a chip. Not the word the caret ends,
   *  still being typed, unless all: text set or pasted in. */
  const makeChips = useCallback((all: boolean, undoable: boolean) => {
    const root = el.current;
    if (!root || chipping.current) return;
    const segs = segments(root);
    const text = display(segs);
    if (!text.includes("@")) return;
    const selection = window.getSelection();
    const caret = selection && selection.rangeCount && root.contains(selection.anchorNode) ? offsetOf(root, segs, selection.anchorNode!, selection.anchorOffset) : null;
    const found: { start: number; end: number; path: string }[] = [];
    for (const m of text.matchAll(WORD)) {
      let path = m[1];
      let end = m.index! + m[0].length;
      while (path && !isFile(path) && ".,;:!?)]}'\"".includes(path.at(-1)!)) {
        path = path.slice(0, -1);
        end -= 1;
      }
      if (!path || !isFile(path)) continue;
      if (!all && caret !== null && caret >= m.index! && caret <= end) continue;
      found.push({ start: m.index!, end, path });
    }
    if (!found.length) return;
    chipping.current = true;
    const before = caret === null ? null : found.filter((f) => f.end <= caret).reduce((n, f) => n + (f.end - f.start - 1), 0);
    for (const chip of [...found].reverse()) {
      if (undoable) {
        select(chip.start, chip.end);
        document.execCommand("insertHTML", false, chipHTML(chip.path));
      } else {
        const now = segments(root);
        const a = positionOf(root, now, chip.start);
        const b = positionOf(root, now, chip.end);
        const range = document.createRange();
        range.setStart(a.node, a.offset);
        range.setEnd(b.node, b.offset);
        range.deleteContents();
        const holder = document.createElement("span");
        holder.innerHTML = chipHTML(chip.path);
        range.insertNode(holder.firstChild!);
      }
    }
    // The caret keeps its place among the letters around the chips.
    if (caret !== null && before !== null && document.activeElement === root) select(caret - before, caret - before);
    chipping.current = false;
    report();
  }, [isFile, report]);

  // The text set from outside (a draft, words spoken, a send): the field
  // takes it, with its chips.
  useLayoutEffect(() => {
    const root = el.current;
    if (!root || value === last.current) return;
    root.textContent = value;
    last.current = value;
    makeChips(true, false);
    root.dataset.empty = String(value === "");
    fit();
  }, [value, makeChips, fit]);

  // Chips for paths found after they were typed.
  useEffect(() => {
    if (document.activeElement !== el.current) makeChips(true, false);
  }, [isFile, makeChips]);

  useImperativeHandle(ref, () => ({
    focus() {
      const root = el.current;
      if (!root) return;
      root.focus();
      const segs = segments(root);
      const end = segs.length ? segs[segs.length - 1].start + segs[segs.length - 1].length : 0;
      select(end, end);
      report();
    },
    replace(start, end, text) {
      const root = el.current;
      if (!root) return;
      root.focus();
      select(start, end);
      document.execCommand("insertText", false, text);
      makeChips(false, true);
      report();
    },
    replaceAll(text) {
      const root = el.current;
      if (!root) return;
      root.focus();
      const segs = segments(root);
      select(0, segs.length ? segs[segs.length - 1].start + segs[segs.length - 1].length : 0);
      document.execCommand("insertText", false, text);
      report();
    },
    caretOnLine(line) {
      const root = el.current;
      const selection = window.getSelection();
      if (!root || !selection || !selection.rangeCount || !selection.isCollapsed || !root.contains(selection.anchorNode)) return false;
      const segs = segments(root);
      const text = display(segs);
      if (!text) return true;
      const caret = offsetOf(root, segs, selection.anchorNode!, selection.anchorOffset);
      // The lines as written first, then as drawn.
      if (line === "first" ? text.slice(0, caret).includes("\n") : text.slice(caret).includes("\n")) return false;
      const edgeAt = positionOf(root, segs, line === "first" ? 0 : text.length);
      const edge = document.createRange();
      edge.setStart(edgeAt.node, edgeAt.offset);
      edge.collapse(true);
      const here = caretRect(selection.getRangeAt(0));
      const there = caretRect(edge);
      return !here || !there || sameLine(here, there);
    },
    show(text, caretAtStart) {
      const root = el.current;
      if (!root) return;
      root.focus();
      const segs = segments(root);
      const total = segs.length ? segs[segs.length - 1].start + segs[segs.length - 1].length : 0;
      select(0, total);
      if (text) document.execCommand("insertText", false, text);
      else if (total) document.execCommand("delete");
      makeChips(true, true);
      const now = segments(root);
      const end = caretAtStart ? 0 : now.length ? now[now.length - 1].start + now[now.length - 1].length : 0;
      select(end, end);
      root.scrollTop = caretAtStart ? 0 : root.scrollHeight;
      report();
    },
  }), [makeChips, report]);

  useEffect(() => {
    const onSelection = () => {
      if (document.activeElement === el.current) report();
    };
    document.addEventListener("selectionchange", onSelection);
    return () => document.removeEventListener("selectionchange", onSelection);
  }, [report]);

  const keyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (onKey?.(e)) {
      e.preventDefault();
      return;
    }
    if (e.key !== "Enter") return;
    e.preventDefault();
    if (e.shiftKey || e.altKey) {
      document.execCommand("insertLineBreak");
    } else if (!e.ctrlKey) {
      // Return, or ⌘Return as the send button's shortcut.
      onSubmit();
    }
  };

  const paste = (e: ClipboardEvent<HTMLDivElement>) => {
    const files = Array.from(e.clipboardData.files).filter((f) => f.type.startsWith("image/"));
    if (files.length && onPasteImages?.(files)) {
      e.preventDefault();
      return;
    }
    e.preventDefault();
    const text = e.clipboardData.getData("text/plain");
    if (text) {
      document.execCommand("insertText", false, text);
      makeChips(true, true);
    }
  };

  const copy = (e: ClipboardEvent<HTMLDivElement>, cut: boolean) => {
    const root = el.current;
    const selection = window.getSelection();
    if (!root || !selection || selection.isCollapsed) return;
    const segs = segments(root);
    const range = selection.getRangeAt(0);
    const start = offsetOf(root, segs, range.startContainer, range.startOffset);
    const end = offsetOf(root, segs, range.endContainer, range.endOffset);
    let text = "";
    for (const s of segs) {
      const from = Math.max(start, s.start);
      const to = Math.min(end, s.start + s.length);
      if (to <= from) continue;
      text += s.kind === "text" ? (s.node as Text).data.slice(from - s.start, to - s.start) : s.kind === "chip" ? `@${s.path}` : "\n";
    }
    e.preventDefault();
    e.clipboardData.setData("text/plain", text);
    if (cut) document.execCommand("delete");
  };

  return (
    <div
      ref={el}
      className="message-field"
      contentEditable={enabled}
      suppressContentEditableWarning
      role="textbox"
      aria-multiline
      aria-placeholder={placeholder}
      data-placeholder={placeholder}
      data-empty="true"
      spellCheck
      onInput={() => {
        report();
        // Once WebKit is done with the change.
        setTimeout(() => makeChips(false, true), 0);
      }}
      onKeyDown={keyDown}
      onPaste={paste}
      onCopy={(e) => copy(e, false)}
      onCut={(e) => copy(e, true)}
      onFocus={() => {
        onFocusChange?.(true);
        report();
      }}
      onBlur={() => {
        onFocusChange?.(false);
        onCaret?.(display(segments(el.current!)), null);
      }}
      onDrop={() => setTimeout(() => makeChips(true, true), 0)}
    />
  );
});
