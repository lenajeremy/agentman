// What the editor shows besides the text (EditorWindow.swift's EditorEmpty,
// NonTextView and MarkdownPreview, with Design/MarkdownView.swift's look).

import { Fragment, useMemo, type ReactNode } from "react";
import { CodeXml, Ellipsis, FileQuestion, FolderSearch, PanelRight, Search, Split, SquareTerminal, TextSearch, type LucideIcon } from "lucide-react";
import { parseDocument, type Block, type Inline, type Picture } from "@shared/markdown";
import { Icon } from "../ui";
import { openPath, openURL } from "../host";
import type { EditorDocument } from "./document";
import { fileURL, paths } from "./fs";
import { fenceFile, highlight } from "./highlight";
import { MARK_LETTER, MARK_TINT, type EditorStore } from "./store";
import { ZoomableImage } from "./ZoomableImage";
import { FileTypeIcon } from "./fileIcons";

/** No file open (EditorEmpty): where you are, the folder by name and where
 *  it is, under the empty page's mark; the commands to start with, in one
 *  card; and the files changed and not yet committed, in another. Centred
 *  while it fits; scrolls from the top once it doesn't. */
export function EditorEmpty({ store }: { store: EditorStore }) {
  const changed = Object.entries(store.marks)
    .filter(([, mark]) => mark !== "deleted")
    .map(([path]) => path)
    .sort();
  const act = (work: () => void) => () => {
    work();
    store.changed();
  };
  return (
    <div className="editor-empty">
      <div className="empty-column">
        <div className="empty-head">
          <EmptyMark />
          <span className="empty-name">{store.name}</span>
          <span className="empty-path">{paths.tilde(store.root, store.home)}</span>
        </div>
        <div className="empty-list">
          <EmptyAction icon={Search} sf="magnifyingglass" title="Go to File…" keys={["⌘", "P"]} onClick={act(() => { store.searchOpen = false; store.quickOpen = true; })} />
          <EmptyAction icon={TextSearch} sf="text.magnifyingglass" title="Search in Folder…" keys={["⇧", "⌘", "F"]} onClick={act(() => store.showSearch())} />
          <EmptyAction icon={SquareTerminal} sf="terminal" title={store.showTerminal ? "Hide Terminal" : "Open Terminal"} keys={["⌃", "`"]} onClick={act(() => store.toggleTerminal())} />
          <EmptyAction icon={PanelRight} sf="sidebar.right" title={store.showAgentPanel ? "Hide Agent Panel" : "Show Agent Panel"} keys={["⌘", "L"]} onClick={act(() => store.setShowAgentPanel(!store.showAgentPanel))} />
        </div>
        {changed.length > 0 && (
          <div className="empty-changes">
            <div className="empty-changes-head">
              <Icon icon={Split} sf="arrow.triangle.branch" size={11} />
              <span className="empty-changes-title">Changed</span>
              <span style={{ flex: 1, minWidth: 4 }} />
              <span className="empty-changes-count">{changed.length}</span>
            </div>
            <div className="empty-list">
              {changed.slice(0, 8).map((path) => {
                const mark = store.marks[path];
                const slash = path.lastIndexOf("/");
                return (
                  <button key={path} className="empty-row empty-file" onClick={() => store.open(path)} aria-label={`${path}, ${mark}`}>
                    <span className="empty-row-icon"><FileTypeIcon name={path.slice(slash + 1)} size={15} /></span>
                    <span className="empty-file-words">
                      <span className="empty-file-name">{path.slice(slash + 1)}</span>
                      <span className="empty-file-folder">{slash < 0 ? "Top of the folder" : path.slice(0, slash)}</span>
                    </span>
                    <span style={{ flex: 1, minWidth: 8 }} />
                    <span className="empty-file-mark" style={{ color: MARK_TINT[mark] }} title={mark}>{MARK_LETTER[mark]}</span>
                  </button>
                );
              })}
              {changed.length > 8 && (
                <EmptyAction icon={Ellipsis} sf="ellipsis" title={`${changed.length - 8} more in Go to File`} keys={[]} onClick={act(() => { store.searchOpen = false; store.quickOpen = true; })} />
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/** The empty page's mark: an icon on a small raised tile, with two ghost
 *  tiles fanned out behind it. */
function EmptyMark() {
  return (
    <span className="empty-mark" aria-hidden>
      <span className="empty-tile ghost left" />
      <span className="empty-tile ghost right" />
      <span className="empty-tile"><Icon icon={CodeXml} sf="chevron.left.forwardslash.chevron.right" size={15} weight="medium" /></span>
    </span>
  );
}

/** A command in the list: what it does, and its keys. */
function EmptyAction({ icon, sf, title, keys, onClick }: { icon: LucideIcon; sf: string; title: string; keys: string[]; onClick: () => void }) {
  return (
    <button className="empty-row empty-action" onClick={onClick}>
      <span className="empty-row-icon"><Icon icon={icon} sf={sf} size={12.5} weight="medium" /></span>
      <span className="empty-action-title">{title}</span>
      <span style={{ flex: 1, minWidth: 8 }} />
      {keys.length > 0 && <KeyCaps keys={keys} />}
    </button>
  );
}

/** Keys as small caps, one each (KeyCaps). */
export function KeyCaps({ keys }: { keys: string[] }) {
  return (
    <span className="key-caps" aria-label={keys.join(" ")}>
      {keys.map((key, i) => <span key={i} className={`key-cap${key.length > 1 ? " wide" : ""}`}>{key}</span>)}
    </span>
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
