// Draws markdown parsed by the phone's parser (mobile/lib/markdown.ts), so
// both apps read an agent's words the same way.

import { memo, type ReactNode } from "react";
import { parseMarkdown, safeHref, type Block, type Inline } from "@shared/markdown";

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
          <a key={k} href={href} target="_blank" rel="noreferrer">{inline(node.children, k)}</a>
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

function blocks(list: Block[], key = ""): ReactNode[] {
  return list.map((block, i) => {
    const k = `${key}${i}`;
    const centered = block.centered ? " centered" : "";
    switch (block.kind) {
      case "paragraph":
        return <p key={k} className={centered}>{inline(block.content, k)}</p>;
      case "heading": {
        const Tag = `h${Math.min(Math.max(block.level, 1), 6)}` as "h1";
        return <Tag key={k} className={centered}>{inline(block.content, k)}</Tag>;
      }
      case "code":
        return (
          <pre key={k} className="code" data-language={block.language}>
            <code>{block.text}</code>
          </pre>
        );
      case "quote":
        return <blockquote key={k}><div className="quote-body">{blocks(block.blocks, k)}</div></blockquote>;
      case "list":
        return (
          <ul key={k} className="md-list">
            {block.items.map((item, j) => (
              <li key={j}>
                <span className="marker">{item.checked === undefined ? item.marker : item.checked ? "☑" : "☐"}</span>
                <div className="item">{blocks(item.blocks, `${k}.${j}.`)}</div>
              </li>
            ))}
          </ul>
        );
      case "rule":
        return <hr key={k} />;
      case "table":
        return (
          <div key={k} className="table-wrap">
            <table>
              <thead>
                <tr>{block.table.header.map((cell, j) => <th key={j} style={{ textAlign: block.table.align[j] }}>{inline(cell, `${k}h${j}`)}</th>)}</tr>
              </thead>
              <tbody>
                {block.table.rows.map((row, r) => (
                  <tr key={r}>{row.map((cell, j) => <td key={j} style={{ textAlign: block.table.align[j] }}>{inline(cell, `${k}r${r}c${j}`)}</td>)}</tr>
                ))}
              </tbody>
            </table>
            {block.table.hiddenRows > 0 && <div className="faint">{block.table.hiddenRows} more rows</div>}
          </div>
        );
      case "pictures":
        return null;
    }
  });
}

export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return <div className="md">{blocks(parseMarkdown(text))}</div>;
});
