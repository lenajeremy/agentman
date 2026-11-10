// Swiping a card away with two fingers on the trackpad, the way a Mac
// notification goes (Design/SwipeToDismiss.swift). The card follows the
// fingers; let go past a third of its width and it leaves, short of that
// it springs back. Up-and-down swipes still scroll the conversation.
//
// A page sees a trackpad swipe as wheel events without its phases, so the
// swipe ends when the events stop for a moment.

import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";

const END_GAP = 140;

export function useSwipeToDismiss(onDismiss: () => Promise<boolean>): { ref: (el: HTMLElement | null) => void; style: CSSProperties } {
  const [offset, setOffset] = useState(0);
  const [animate, setAnimate] = useState<string | null>(null);
  const element = useRef<HTMLElement | null>(null);
  const live = useRef({ offset: 0, travelX: 0, travelY: 0, sideways: null as boolean | null, leaving: false, timer: 0 });
  const dismiss = useRef(onDismiss);
  dismiss.current = onDismiss;

  const move = (to: number, transition: string | null) => {
    live.current.offset = to;
    setAnimate(transition);
    setOffset(to);
  };

  const settle = useCallback(() => {
    const s = live.current;
    const width = element.current?.offsetWidth ?? 400;
    if (Math.abs(s.offset) <= width * 0.33) {
      move(0, "transform 0.3s cubic-bezier(0.34, 1.3, 0.64, 1), opacity 0.3s");
      return;
    }
    s.leaving = true;
    const direction = s.offset > 0 ? 1 : -1;
    move(direction * width * 1.1, "transform 0.18s ease-out, opacity 0.18s ease-out");
    void dismiss.current().then((gone) => {
      if (!gone) move(0, "transform 0.35s cubic-bezier(0.34, 1.25, 0.64, 1), opacity 0.35s");
      s.leaving = false;
    });
  }, []);

  const onWheel = useCallback((e: WheelEvent) => {
    const s = live.current;
    if (s.leaving || e.deltaMode !== 0) return;
    window.clearTimeout(s.timer);
    s.travelX += e.deltaX;
    s.travelY += e.deltaY;
    if (s.sideways === null && Math.abs(s.travelX) + Math.abs(s.travelY) > 6) {
      s.sideways = Math.abs(s.travelX) > Math.abs(s.travelY) * 1.5;
    }
    const sideways = s.sideways;
    s.timer = window.setTimeout(() => {
      const was = s.sideways === true;
      s.sideways = null;
      s.travelX = 0;
      s.travelY = 0;
      if (was) settle();
    }, END_GAP);
    if (sideways !== true) return;
    e.preventDefault();
    move(s.offset - e.deltaX, null);
  }, [settle]);

  const ref = useCallback((el: HTMLElement | null) => {
    element.current?.removeEventListener("wheel", onWheel);
    element.current = el;
    el?.addEventListener("wheel", onWheel, { passive: false });
  }, [onWheel]);

  useEffect(() => () => window.clearTimeout(live.current.timer), []);

  const width = element.current?.offsetWidth ?? 400;
  return {
    ref,
    style: offset === 0 && !animate ? {} : {
      transform: `translateX(${offset}px)`,
      opacity: 1 - Math.min(0.7, Math.abs(offset) / Math.max(width, 1)),
      transition: animate ?? "none",
    },
  };
}
