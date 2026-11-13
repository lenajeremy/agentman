// Wrapped text as wide as its widest line, as SwiftUI sizes a Text: CSS
// makes a wrapped block as wide as the room it had, so a bubble whose words
// wrap stood wider than the Mac's.

import { useLayoutEffect, type RefObject } from "react";

/** Narrows el to its widest line whenever its words or its room change. */
export function useTightWidth(el: RefObject<HTMLElement | null>, deps: unknown[]) {
  useLayoutEffect(() => {
    const node = el.current;
    if (!node) return;
    const fit = () => {
      node.style.width = "";
      const range = document.createRange();
      range.selectNodeContents(node);
      const rects = Array.from(range.getClientRects());
      if (rects.length < 2) return;
      const left = Math.min(...rects.map((r) => r.left));
      const right = Math.max(...rects.map((r) => r.right));
      const widest = right - left;
      if (widest > 0 && widest < node.getBoundingClientRect().width - 0.5) node.style.width = `${Math.ceil(widest)}px`;
    };
    fit();
    // The room changes with the window and the panels beside the thread.
    const parent = node.parentElement?.closest(".thread-column") ?? node.parentElement;
    let width = parent?.getBoundingClientRect().width ?? 0;
    const observer = new ResizeObserver(() => {
      const now = parent?.getBoundingClientRect().width ?? 0;
      if (now !== width) {
        width = now;
        fit();
      }
    });
    if (parent) observer.observe(parent);
    return () => observer.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}
