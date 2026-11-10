// Draws markdown parsed by the phone's parser (mobile/lib/markdown.ts), so
// both apps read an agent's words the same way, laid out as the Mac app's
// MarkdownView.swift lays it out: blocks 14 apart, its quotes curled.

import { memo, useMemo, type ReactNode } from "react";
import { parseMarkdown, safeHref, type Block, type Inline } from "@shared/markdown";
import { openURL } from "./host";
import { smartBlocks } from "./thread/smart";

function inline(nodes: Inline[], key = ""): ReactNode[] {
  return nodes.map((node, i) => {
    const k = `${key}${i}`;
    switch (node.kind) {
      case "text":
        return node.text;
      case "code":
        return <code key={k}>{node.text}</code>;
      case "math":
        return <code key={k} className="math">{node.text}</code>;
      case "bold":
        return <strong key={k}>{inline(node.children, k)}</strong>;
      case "italic":
        return <em key={k}>{inline(node.children, k)}</em>;
      case "strike":
        return <s key={k}>{inline(node.children, k)}</s>;
      case "link": {
        const href = safeHref(node.href);
        return href ? (
          <a
            key={k}
            href={href}
            onClick={(e) => {
              e.preventDefault();
              void openURL(href);
            }}
          >
            {inline(node.children, k)}
          </a>
        ) : (
          <span key={k}>{inline(node.children, k)}</span>
        );
      }
      case "file":
        return <span key={k} className="file-link">{inline(node.children, k)}</span>;
      case "break":
        return <br key={k} />;
    }
  });
}

/** A task item's box, as the Mac app puts it ahead of the item's words. */
function withBox(blocks: Block[], checked: boolean | undefined): Block[] {
  if (checked === undefined) return blocks;
  const mark: Inline = { kind: "text", text: checked ? "☑︎ " : "☐ " };
  const [first, ...rest] = blocks;
  if (first?.kind === "paragraph") return [{ ...first, content: [mark, ...first.content] }, ...rest];
  return [{ kind: "paragraph", content: [mark] }, ...blocks];
}

/** As wide as its words, up to the width a table of its columns allows. */
function cellWidth(columns: number): number {
  return Math.min(360, Math.max(140, 700 / Math.max(columns, 1)));
}

function blocks(list: Block[], key = ""): ReactNode[] {
  return list.map((block, i) => {
    const k = `${key}${i}`;
    const centered = block.centered ? " centered" : "";
    switch (block.kind) {
      case "paragraph":
        return <p key={k} className={`md-p${centered}`}>{inline(block.content, k)}</p>;
      case "heading": {
        const level = Math.min(Math.max(block.level, 1), 6);
        const Tag = `h${level}` as "h1";
        return <Tag key={k} className={`md-h md-h${Math.min(level, 3)}${centered}`}>{inline(block.content, k)}</Tag>;
      }
      case "code":
        return (
          <div key={k} className="md-code">
            <pre data-language={block.language}><code>{block.text}</code></pre>
          </div>
        );
      case "quote":
        return (
          <div key={k} className="md-quote">
            <span className="md-quote-bar" />
            <div className="md-quote-body">{blocks(block.blocks, k)}</div>
          </div>
        );
      case "list": {
        const ordered = /^\d+\.$/.test(block.items[0]?.marker ?? "");
        return (
          <div key={k} className="md-list">
            {block.items.map((item, j) => (
              <div key={j} className="md-item">
                <span className={`md-marker ${ordered ? "ordered" : ""}`}>{ordered ? item.marker : "•"}</span>
                <div className="md-item-body">{blocks(withBox(item.blocks, item.checked), `${k}.${j}.`)}</div>
              </div>
            ))}
          </div>
        );
      }
      case "rule":
        return <div key={k} className="md-rule" />;
      case "table": {
        const width = cellWidth(block.table.header.length);
        return (
          <div key={k} className="md-table-scroll">
            <table className="md-table">
              <thead>
                <tr>{block.table.header.map((cell, j) => <th key={j}><div style={{ maxWidth: width }}>{inline(cell, `${k}h${j}`)}</div></th>)}</tr>
              </thead>
              <tbody>
                {block.table.rows.map((row, r) => (
                  <tr key={r}>{row.map((cell, j) => <td key={j}><div style={{ maxWidth: width }}>{inline(cell, `${k}r${r}c${j}`)}</div></td>)}</tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      }
      case "pictures":
        return null;
    }
  });
}

export const Markdown = memo(function Markdown({ text }: { text: string }) {
  const parsed = useMemo(() => smartBlocks(parseMarkdown(text)), [text]);
  return <div className="md">{blocks(parsed)}</div>;
});
