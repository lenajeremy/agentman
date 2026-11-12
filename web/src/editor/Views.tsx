// What the editor shows besides the text (EditorWindow.swift's EditorEmpty,
// NonTextView and MarkdownPreview, with Design/MarkdownView.swift's look).

import { Fragment, useMemo, type ReactNode } from "react";
import { CodeXml, FileQuestion, FolderSearch } from "lucide-react";
import { parseDocument, type Block, type Inline, type Picture } from "@shared/markdown";
import { Icon } from "../ui";
import { openPath, openURL } from "../host";
import type { EditorDocument } from "./document";
import { fileURL, paths } from "./fs";
import { fenceFile, highlight } from "./highlight";
import { MARK_LETTER, MARK_TINT, type EditorStore } from "./store";
import { ZoomableImage } from "./ZoomableImage";

/** No file open: how to find one, and what agents changed lately. */
export function EditorEmpty({ store }: { store: EditorStore }) {
  const changed = Object.entries(store.marks)
    .filter(([, mark]) => mark !== "deleted")
    .map(([path]) => path)
    .sort()
    .slice(0, 8);
  const shortcut = (title: string, keys: string, action?: () => void) => (
    <div className="empty-shortcut">
      {action ? (
        <button className="mac-link" onClick={action}>{title}</button>
      ) : (
        <span className="empty-shortcut-title">{title}</span>
      )}
      <span style={{ flex: 1 }} />
      <span className="empty-shortcut-keys">{keys}</span>
    </div>
  );
  return (
    <div className="editor-empty">
      <div className="empty-head">
        <span className="empty-icon">
          <Icon icon={CodeXml} sf="chevron.left.forwardslash.chevron.right" size={26} weight="regular" />
        </span>
        <span className="empty-name">{store.name}</span>
        <span className="empty-path">{paths.tilde(store.root, store.home)}</span>
      </div>
      <div className="empty-shortcuts">
        {shortcut("Go to File", "⌘P", () => {
          store.quickOpen = true;
          store.changed();
        })}
        {shortcut("Save", "⌘S")}
        {shortcut("Close Tab", "⌘W")}
        {shortcut("Comment Lines", "⌘/")}
      </div>
      {changed.length > 0 && (
        <div className="empty-changed">
          <span className="empty-changed-title">Changed</span>
          {changed.map((path) => {
            const mark = store.marks[path];
            return (
              <button key={path} className="empty-changed-row" onClick={() => store.open(path)}>
                <span className="empty-changed-letter" style={{ color: MARK_TINT[mark] }}>{MARK_LETTER[mark]}</span>
                <span className="empty-changed-path">{path}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** A file the editor shows but doesn't edit: an image, or one it can't. */
export function NonTextView({ document }: { document: EditorDocument }) {
  if (document.kind === "image") {
    return (
      <div className="non-text">
        <ZoomableImage src={`${fileURL(document.url)}&v=${document.modified}`} alt={document.name} />
      </div>
    );
  }
  const message =
    document.kind === "binary" ? "This file isn't text." : document.kind === "tooLarge" ? "This file is too large to edit here." : "This file isn't there any more.";
  return (
    <div className="non-text">
      <div className="non-text-body">
        <span className="non-text-icon">
          <Icon icon={document.kind === "missing" ? FolderSearch : FileQuestion} sf={document.kind === "missing" ? "questionmark.folder" : "doc.badge.ellipsis"} size={28} />
        </span>
        <span className="non-text-message">{message}</span>
        {document.kind !== "missing" && (
          <button className="mac-button" onClick={() => void openPath(document.url)}>Open with Its App</button>
        )}
      </div>
    </div>
  );
}

/** A Markdown file as it reads, kept up with the text as it is edited. */
export function MarkdownPreview({ store, document }: { store: EditorStore; document: EditorDocument }) {
  const text = document.text;
  const path = document.readOnly ? document.url.slice(1) : document.path;
  const blocks = useMemo(() => parseDocument(text, path), [text, path]);
  const base = document.readOnly ? "/" : store.root;
  const ctx: Ctx = {
    image: (picture) => (picture.url ? picture.url : picture.path ? fileURL(paths.join(base, picture.path)) : ""),
    open: (target) => {
      if ("href" in target) void openURL(target.href);
      else void store.followLink("/" + target.path, document);
    },
  };
  return (
    <div className="md-preview" onMouseDown={() => (window.document.activeElement as HTMLElement | null)?.blur?.()}>
      <div className="md-doc">{renderBlocks(blocks, ctx, "")}</div>
    </div>
  );
}

interface Ctx {
  image: (picture: Picture) => string;
  open: (target: { href: string } | { path: string }) => void;
}

function renderInline(nodes: Inline[], ctx: Ctx, key: string): ReactNode[] {
  return nodes.map((node, i) => {
    const k = `${key}${i}`;
    switch (node.kind) {
      case "text":
        return <Fragment key={k}>{node.text}</Fragment>;
      case "code":
      case "math":
        return <code key={k}>{node.text}</code>;
      case "bold":
        return <strong key={k}>{renderInline(node.children, ctx, k)}</strong>;
      case "italic":
        return <em key={k}>{renderInline(node.children, ctx, k)}</em>;
      case "strike":
        return <s key={k}>{renderInline(node.children, ctx, k)}</s>;
      case "link":
        return (
          <a key={k} href={node.href} onClick={(e) => { e.preventDefault(); ctx.open({ href: node.href }); }}>
            {renderInline(node.children, ctx, k)}
          </a>
        );
      case "file":
        return (
          <a key={k} href={node.path} onClick={(e) => { e.preventDefault(); ctx.open({ path: node.path }); }}>
            {renderInline(node.children, ctx, k)}
          </a>
        );
      case "break":
        return <br key={k} />;
    }
  });
}

function renderBlocks(list: Block[], ctx: Ctx, key: string): ReactNode[] {
  return list.map((block, i) => {
    const k = `${key}${i}`;
    const center = block.centered ? "centered" : "";
    switch (block.kind) {
      case "paragraph":
        return <p key={k} className={center}>{renderInline(block.content, ctx, k)}</p>;
      case "heading":
        return (
          <div key={k} className={`md-h md-h${Math.min(block.level, 3)} ${center}`}>
            {renderInline(block.content, ctx, k)}
          </div>
        );
      case "code": {
        const file = fenceFile(block.language);
        return (
          <pre key={k} className="md-code">
            <code>
              {file
                ? highlight(block.text, file).map((run, j) => (run.token ? <span key={j} className={`tok-${run.token}`}>{run.text}</span> : run.text))
                : block.text}
            </code>
          </pre>
        );
      }
      case "quote":
        return (
          <div key={k} className="md-quote">
            <span className="md-quote-bar" />
            <div className="md-quote-body">{renderBlocks(block.blocks, ctx, k)}</div>
          </div>
        );
      case "list": {
        const ordered = /\d/.test(block.items[0]?.marker ?? "");
        return (
          <div key={k} className="md-list">
            {block.items.map((item, j) => (
              <div key={j} className="md-item">
                <span className="md-marker" style={{ minWidth: ordered ? 18 : 10 }}>
                  {item.checked === undefined ? (ordered ? item.marker : "•") : item.checked ? "☑" : "☐"}
                </span>
                <div className="md-item-body">{renderBlocks(item.blocks, ctx, `${k}.${j}.`)}</div>
              </div>
            ))}
          </div>
        );
      }
      case "rule":
        return <div key={k} className="md-rule" />;
      case "table": {
        const width = Math.min(360, Math.max(140, 700 / Math.max(block.table.header.length, 1)));
        return (
          <div key={k} className="md-table-wrap">
            <table className="md-table">
              <thead>
                <tr>
                  {block.table.header.map((cell, j) => (
                    <th key={j} style={{ textAlign: block.table.align[j], maxWidth: width }}>{renderInline(cell, ctx, `${k}h${j}`)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {block.table.rows.map((row, r) => (
                  <tr key={r}>
                    {row.map((cell, j) => (
                      <td key={j} style={{ textAlign: block.table.align[j], maxWidth: width }}>{renderInline(cell, ctx, `${k}r${r}c${j}`)}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      }
      case "pictures":
        return (
          <div key={k} className={`md-pictures ${center}`}>
            {block.pictures.map((picture, j) => {
              const src = ctx.image(picture);
              const img = src ? (
                <img src={src} alt={picture.alt} style={picture.width ? { maxWidth: picture.width } : undefined} />
              ) : (
                <span className="md-picture-words">{picture.alt || picture.path || picture.url}</span>
              );
              const link = picture.link;
              return link ? (
                <a key={j} href="#" onClick={(e) => { e.preventDefault(); ctx.open(link); }}>{img}</a>
              ) : (
                <Fragment key={j}>{img}</Fragment>
              );
            })}
          </div>
        );
    }
  });
}
