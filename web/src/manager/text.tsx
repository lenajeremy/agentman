// Text as SwiftUI draws it where CSS can't on its own: inline Markdown in a
// title (AttributedString's inlineOnlyPreservingWhitespace), and a line cut
// in the middle (truncationMode(.middle)).

import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { parseMarkdown, safeHref, type Inline } from "@shared/markdown";
import { openURL } from "../host";

function inline(nodes: Inline[], key = ""): ReactNode[] {
  return nodes.map((node, i) => {
    const k = `${key}${i}`;
    switch (node.kind) {
      case "text":
        return node.text;
      case "code":
      case "math":
        return <code key={k} className="am-inline-code">{node.text}</code>;
      case "bold":
        return <strong key={k}>{inline(node.children, k)}</strong>;
      case "italic":
        return <em key={k}>{inline(node.children, k)}</em>;
      case "strike":
        return <s key={k}>{inline(node.children, k)}</s>;
      case "link": {
        // In the link colour, opened by a click, as SwiftUI's Text has it;
        // the card under it isn't clicked as well.
        const href = safeHref(node.href);
        return href ? (
          <a key={k} className="inline-link" href={href} onClick={(e) => { e.preventDefault(); e.stopPropagation(); void openURL(href); }}>
            {inline(node.children, k)}
          </a>
        ) : <span key={k}>{inline(node.children, k)}</span>;
      }
      case "file":
        return <span key={k}>{inline(node.children, k)}</span>;
      case "break":
        return "\n";
    }
  });
}

/** SwiftUI's Text never leaves a word alone on a paragraph's last line (its
 *  standard line-break strategy brings the word before down with it), so
 *  the last two words of each paragraph are kept together. */
export function keepLastWords(text: string): string {
  return text
    .split("\n")
    .map((line) => line.replace(/ (?=\S+\s*$)/, " "))
    .join("\n");
}

/** Bold, italics and code in a line of text, without their marks. */
export function InlineMarkdown({ text }: { text: string }) {
  text = keepLastWords(text);
  const blocks = parseMarkdown(text);
  if (blocks.length === 1 && blocks[0].kind === "paragraph") return <>{inline(blocks[0].content)}</>;
  // Anything more than a paragraph reads as it was written.
  return <>{text}</>;
}

let canvas: HTMLCanvasElement | undefined;

/** One line, its middle cut out when it doesn't fit. */
export function MiddleTruncated({ text, className, title }: { text: string; className?: string; title?: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [shown, setShown] = useState(text);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const fit = () => {
      const width = el.clientWidth;
      if (!width) return;
      canvas ??= document.createElement("canvas");
      const ctx = canvas.getContext("2d")!;
      const style = getComputedStyle(el);
      ctx.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
      if (ctx.measureText(text).width <= width) return setShown(text);
      let lo = 0;
      let hi = text.length;
      while (lo < hi) {
        const mid = Math.ceil((lo + hi) / 2);
        const head = Math.floor(mid / 2);
        const candidate = text.slice(0, head) + "…" + text.slice(text.length - head);
        if (ctx.measureText(candidate).width <= width) lo = mid;
        else hi = mid - 1;
      }
      const head = Math.floor(lo / 2);
      setShown(text.slice(0, head) + "…" + text.slice(text.length - head));
    };
    fit();
    const observer = new ResizeObserver(fit);
    observer.observe(el);
    return () => observer.disconnect();
  }, [text]);
  return <span ref={ref} className={className} title={title}>{shown}</span>;
}
