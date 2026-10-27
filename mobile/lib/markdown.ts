/**
 * Markdown, parsed by marked and reduced to the vocabulary this app draws.
 *
 * The app once used a renderer package that fetched remote images, opened
 * custom URL schemes and carried quadratic-complexity advisories. It was
 * replaced by a parser written here, which fixed those and then kept missing
 * markdown: deep headings, emphasis, links inside bold, nested lists. Parsing
 * is marked's job now. What stays here is the set of decisions that made
 * dropping the package worthwhile, each stated in one place:
 *
 * - Only http(s) links open (safeHref). The app registers `agentman://`, so a
 *   link like `agentman://pair?token=…` could otherwise try to re-pair the
 *   phone against someone else's relay.
 * - A reply fetches nothing. An image becomes a link to it.
 * - HTML is never interpreted. It is shown as the text the agent wrote.
 * - Agent output is untrusted, and marked is not linear on every input — a
 *   20 KB line of `**a *` takes three seconds, and so does a long word ending
 *   in `@`. Containers stop nesting at MAX_NESTING and inline markup is only
 *   parsed where its cost is bounded (inlineWork). Past either limit the text
 *   is still shown, just without its formatting.
 *
 * A Markdown file from the session's folder is read as a document instead
 * (parseDocument): HTML is laid out, its pictures are shown, from the folder
 * or the web, as anywhere else the reader opens it, and links to its files
 * open them. The other rules hold there too.
 *
 * Pure, so it is tested on Node like the other rules in this directory.
 */

import { Lexer, Marked, Tokenizer, type MarkedOptions, type Token, type Tokens } from "marked";

/** The most of one message parsed; the rest is cut and the reader told so. */
export const MAX_MARKDOWN_CHARS = 200_000;
/** A table wider than this is not readable on a phone at any zoom. */
export const MAX_COLUMNS = 12;
/** Beyond this the feed stops being scrollable in any useful way. */
export const MAX_ROWS = 200;
/**
 * Quotes and lists nested deeper than this are read as text. marked re-reads
 * a container's content once per level, and a list 800 deep took a second.
 */
export const MAX_NESTING = 10;
/** Emphasis nested deeper than this is shown with its markers. */
const MAX_INLINE_DEPTH = 8;
/**
 * inlineWork allowed for one run of inline text, and for a whole message.
 *
 * Across 18,677 real agent replies the costliest run scored 40,000 and the
 * costliest reply 430,000, so these leave six times that. Spent in full on
 * the worst input, the message budget is about 40 ms of parsing on a laptop.
 */
const MAX_UNIT_WORK = 250_000;
const MAX_MESSAGE_WORK = 2_500_000;
/** No address has a local part this long; a longer run before `@` is an attack. */
const MAX_EMAIL_RUN = 256;
/** A link target long enough for any real URL. */
const MAX_HREF_CHARS = 2048;

export type Alignment = "left" | "center" | "right";

export type Inline =
  | { kind: "text"; text: string }
  | { kind: "code" | "math"; text: string }
  | { kind: "bold" | "italic" | "strike"; children: Inline[] }
  | { kind: "link"; href: string; children: Inline[] }
  // A document's link to a file in its folder, by the file's path there.
  | { kind: "file"; path: string; children: Inline[] }
  | { kind: "break" };

/** A picture in a document, from its folder or from the web. */
export interface Picture {
  alt: string;
  /** Its path in the folder. */
  path?: string;
  /** Its address on the web. */
  url?: string;
  /** What a tap on it opens, from a link around it. */
  link?: { href: string } | { path: string };
  /** The width the document asks for, in points. */
  width?: number;
}

export interface ListItem {
  /** "•", "◦" or "3." — the renderer draws it as given. */
  marker: string;
  /** Present only on a task item: `- [ ]` or `- [x]`. */
  checked?: boolean;
  blocks: Block[];
}

export interface Table {
  header: Inline[][];
  align: Alignment[];
  rows: Inline[][][];
  /** Rows past MAX_ROWS, counted so the reader knows they exist. */
  hiddenRows: number;
}

export type Block = (
  | { kind: "paragraph"; content: Inline[] }
  | { kind: "heading"; level: number; content: Inline[] }
  // The fence's language, lowercased, when it names one: ```java. It is what
  // lets a block be highlighted.
  | { kind: "code"; text: string; language?: string }
  | { kind: "quote"; blocks: Block[] }
  | { kind: "list"; items: ListItem[] }
  | { kind: "rule" }
  | { kind: "table"; table: Table }
  // A document's pictures on a line of their own: a banner, a row of badges.
  | { kind: "pictures"; pictures: Picture[] }
) & {
  // Inside a document's <div align="center">, as READMEs open.
  centered?: boolean;
};

/**
 * Returns the href to open, or null when it must not be opened.
 *
 * Deliberately an allowlist: anything not recognised as http(s) is refused
 * rather than guessed at. Scheme-relative (`//host`) and relative URLs are
 * refused too — there is no page context to resolve them against.
 */
export function safeHref(raw: string): string | null {
  const href = raw.trim();
  if (href.length === 0 || href.length > MAX_HREF_CHARS) return null;
  // A control character or whitespace inside the target is either a broken
  // parse or an attempt to smuggle one URL past a check and open another.
  if (/[\u0000- \u007f-\u009f]/.test(href)) return null;
  return /^https?:\/\/[^/]/i.test(href) ? href : null;
}

/**
 * The language named by a fence's info string, or "" when it names none.
 *
 * Only the first word: agents write ```ts twoslash and ```python
 * {highlight=3}, and the rest is not a language.
 */
export function fenceLanguage(info: string): string {
  const first = info.replace(/^`+/, "").trim().split(/[\s,{]/)[0] ?? "";
  return /^[A-Za-z0-9_+#-]+$/.test(first) ? first.toLowerCase() : "";
}

/** The text of a run of inline content, without its markup. */
export function plainText(content: Inline[]): string {
  return content
    .map((node) =>
      node.kind === "break" ? " " : "children" in node ? plainText(node.children) : node.text,
    )
    .join("");
}

/** Splits a message into the blocks the renderer draws. */
export function parseMarkdown(source: string): Block[] {
  try {
    return toBlocks(lex(source, OPTIONS), 0);
  } catch {
    // marked recursing past the stack on some shape nobody anticipated. The
    // message is still worth reading as text.
    return source
      .split(/\n{2,}/)
      .filter((part) => part.trim() !== "")
      .map(textBlock);
  }
}

/**
 * Splits a Markdown file from the folder into blocks, read as GitHub shows
 * it: lines join into paragraphs, a README's HTML is laid out (its centred
 * banner, pictures and line breaks; other tags give their text), pictures
 * in the folder are shown, and links to its files open them. `path` is the
 * file's own, which its links are relative to.
 */
export function parseDocument(source: string, path: string): Block[] {
  const doc: Doc = { dir: path.split("/").slice(0, -1).join("/"), centered: [] };
  try {
    return toBlocks(lex(source, DOCUMENT), 0, doc);
  } catch {
    return parseMarkdown(source);
  }
}

/** Where a document is, and the <div>s it has opened so far. */
interface Doc {
  dir: string;
  /** Each <div> or <p> open, and whether it centres. */
  centered: boolean[];
}

/**
 * The path in the folder a document's link points to, or null for one that
 * leaves it: an address, an anchor alone, or a path above the folder.
 * `/docs/a.md` is from the folder's top, as GitHub reads it.
 */
export function documentPath(href: string, dir: string): string | null {
  let target = href.trim();
  if (target === "" || target.length > MAX_HREF_CHARS || /[\u0000-\u001f]/.test(target)) return null;
  if (/^[a-z][a-z0-9+.-]*:/i.test(target) || target.startsWith("//")) return null;
  target = target.split("#")[0].split("?")[0];
  try {
    target = decodeURIComponent(target);
  } catch {
    // Kept as written.
  }
  if (target === "") return null;
  const parts = target.startsWith("/") ? [] : dir.split("/").filter(Boolean);
  for (const part of target.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") {
      if (parts.length === 0) return null;
      parts.pop();
    } else {
      parts.push(part);
    }
  }
  return parts.length > 0 ? parts.join("/") : null;
}

/**
 * An estimate of what marked's inline pass costs on `src`.
 *
 * Emphasis is the expensive construct: every opening marker scans forward for
 * its closer, so the cost is roughly the markers times the length. Infinite
 * for the other known blow-up, a long word ending in `@`, which GFM's email
 * autolink rescans from every position.
 */
export function inlineWork(src: string): number {
  let markers = 0;
  let run = 0;
  for (let index = 0; index < src.length; index += 1) {
    const character = src[index];
    if (character === "@" && run > MAX_EMAIL_RUN) return Infinity;
    run = EMAIL_CHARACTER.test(character) ? run + 1 : 0;
    if (MARKUP_CHARACTER.test(character)) markers += 1;
  }
  return src.length * (markers + 1);
}

/** Characters that can open, close or escape an inline construct. */
const MARKUP_CHARACTER = /[*_~`[\]()<>!\\$@]/;
/** What GFM's email autolink accepts before the `@`. */
const EMAIL_CHARACTER = /[A-Za-z0-9.!#$%&'*+/=?_`{|}~-]/;

/**
 * Inline TeX: $O(N^2)$. Nothing here typesets it, but it is machine notation,
 * so it is set as such and loses the dollars nobody meant to be read.
 *
 * The content has to hug its dollars and the closing one cannot run into a
 * digit, which is what keeps "costs $5 and $10" as money.
 */
const MATH = /^\$([^\s$](?:[^$\n]*[^\s$])?)\$(?!\d)/;

const OPTIONS: MarkedOptions = new Marked({
  gfm: true,
  // A newline inside a paragraph is a line break, as agents mean it.
  breaks: true,
  extensions: [
    {
      name: "math",
      level: "inline",
      start: (src: string) => src.indexOf("$"),
      tokenizer(src: string) {
        const match = MATH.exec(src);
        return match ? { type: "math", raw: match[0], text: match[1] } : undefined;
      },
    },
  ],
}).defaults;

/**
 * A document's: its lines join into paragraphs, as GitHub shows a file. A
 * reply's newline is a line break, as agents mean it; a file's is wrapping.
 */
const DOCUMENT: MarkedOptions = { ...OPTIONS, breaks: false };

/** marked's tokenizer, with quotes and lists that stop at MAX_NESTING. */
class BoundedTokenizer extends Tokenizer {
  depth = 0;

  blockquote(src: string) {
    return this.nested(() => super.blockquote(src));
  }

  list(src: string) {
    return this.nested(() => super.list(src));
  }

  // Declining at the limit hands the line to marked's paragraph and text
  // rules, which take it as it stands.
  private nested<T>(read: () => T): T | undefined {
    if (this.depth >= MAX_NESTING) return undefined;
    this.depth += 1;
    try {
      return read();
    } finally {
      this.depth -= 1;
    }
  }
}

/**
 * marked's own lex(), with the inline pass metered: each queued run of inline
 * text is parsed only while it and the message are within budget.
 */
function lex(source: string, options: MarkedOptions): Token[] {
  const lexer = new Lexer({ ...options, tokenizer: new BoundedTokenizer() });
  lexer.blockTokens(source.replace(/\r\n?/g, "\n"), lexer.tokens);
  let budget = MAX_MESSAGE_WORK;
  for (const { src, tokens } of lexer.inlineQueue) {
    const work = inlineWork(src);
    if (work <= MAX_UNIT_WORK && work <= budget) {
      budget -= work;
      lexer.inlineTokens(src, tokens);
    } else {
      tokens.push({ type: "text", raw: src, text: src });
    }
  }
  lexer.inlineQueue = [];
  return lexer.tokens;
}

function toBlocks(tokens: Token[], listDepth: number, doc?: Doc): Block[] {
  const blocks: Block[] = [];
  const push = (block: Block) =>
    blocks.push(doc?.centered.includes(true) ? { ...block, centered: true } : block);
  for (const token of tokens) {
    switch (token.type) {
      case "space":
      // A link reference definition; marked has already resolved the links
      // that use it.
      case "def":
      // A tight task item's box, which the list item draws.
      case "checkbox":
        break;
      case "paragraph":
      case "text": {
        const pictures = doc ? picturesOf(token.tokens ?? [], doc) : null;
        if (pictures) push({ kind: "pictures", pictures });
        else push({ kind: "paragraph", content: toInlines(token.tokens ?? [], 0, doc) });
        break;
      }
      case "heading": {
        // A bare `####` is an empty heading, which draws as nothing at all.
        const content = toInlines(token.tokens ?? [], 0, doc);
        if (content.length > 0) push({ kind: "heading", level: token.depth, content });
        break;
      }
      case "code": {
        const language = fenceLanguage(token.lang ?? "");
        push(language ? { kind: "code", text: token.text, language } : { kind: "code", text: token.text });
        break;
      }
      case "blockquote":
        push({ kind: "quote", blocks: toBlocks(token.tokens ?? [], listDepth, doc) });
        break;
      case "list":
        push({ kind: "list", items: toListItems(token as Tokens.List, listDepth, doc) });
        break;
      case "hr":
        push({ kind: "rule" });
        break;
      case "table":
        push(toTable(token as Tokens.Table, doc));
        break;
      case "html":
        // A document's HTML is laid out; a reply's is shown as written.
        if (doc) htmlBlocks(token.text, doc).forEach((block) => blocks.push(block));
        else if (token.raw.trim() !== "") blocks.push(textBlock(token.raw.trim()));
        break;
      default:
        // Anything a later marked adds: shown as written.
        if (token.raw.trim() !== "") push(textBlock(token.raw.trim()));
    }
  }
  return blocks;
}

function toListItems(list: Tokens.List, depth: number, doc?: Doc): ListItem[] {
  const start = typeof list.start === "number" ? list.start : 1;
  return list.items.map((item, index) => ({
    marker: list.ordered ? `${start + index}.` : depth === 0 ? "•" : "◦",
    ...(item.task ? { checked: item.checked === true } : {}),
    blocks: toBlocks(item.tokens, depth + 1, doc),
  }));
}

function toTable(table: Tokens.Table, doc?: Doc): Block {
  // Measured in columns because the renderer gives each a fixed width; past
  // a phone's worth the text reads better than the grid.
  if (table.header.length > MAX_COLUMNS) return textBlock(table.raw.trim());
  const cell = (entry: Tokens.TableCell) => toInlines(entry.tokens, 0, doc);
  return {
    kind: "table",
    table: {
      header: table.header.map(cell),
      align: table.align.map((align) => align ?? "left"),
      rows: table.rows.slice(0, MAX_ROWS).map((row) => row.map(cell)),
      hiddenRows: Math.max(0, table.rows.length - MAX_ROWS),
    },
  };
}

function textBlock(text: string): Block {
  return { kind: "paragraph", content: [{ kind: "text", text }] };
}

function toInlines(tokens: Token[], depth: number, doc?: Doc): Inline[] {
  const content: Inline[] = [];
  const push = (node: Inline) => {
    const last = content[content.length - 1];
    // Escapes and decoded entities arrive as separate pieces of text.
    if (node.kind === "text" && last?.kind === "text") last.text += node.text;
    else content.push(node);
  };

  for (const token of tokens) {
    switch (token.type) {
      case "text":
      case "escape":
        // A document's lines join, as its paragraph's wrapping.
        push({ kind: "text", text: doc ? token.text.replace(/\n/g, " ") : token.text });
        break;
      case "codespan":
        push({ kind: "code", text: token.text });
        break;
      case "math":
        push({ kind: "math", text: token.text });
        break;
      case "br":
        push({ kind: "break" });
        break;
      case "strong":
      case "em":
      case "del":
        if (depth >= MAX_INLINE_DEPTH) push({ kind: "text", text: token.raw });
        else
          push({
            kind: token.type === "strong" ? "bold" : token.type === "em" ? "italic" : "strike",
            children: toInlines(token.tokens ?? [], depth + 1, doc),
          });
        break;
      case "link": {
        const label = toInlines(token.tokens ?? [], depth + 1, doc);
        const href = safeHref(token.href);
        const path = !href && doc ? documentPath(token.href, doc.dir) : null;
        // A refused target still shows its label, formatting and all: hiding
        // that the agent wrote a link would be worse than an inert one.
        if (path) push({ kind: "file", path, children: label.length > 0 ? label : [{ kind: "text", text: path }] });
        else if (!href) label.forEach(push);
        else push({ kind: "link", href, children: label.length > 0 ? label : [{ kind: "text", text: href }] });
        break;
      }
      case "image": {
        // Among words, a link the reader can choose to open; in a document,
        // one in the folder opens in the app. (A document's pictures on a
        // line of their own are shown: see picturesOf.)
        const href = safeHref(token.href);
        const alt = token.text.trim();
        const path = !href && doc ? documentPath(token.href, doc.dir) : null;
        if (href) push({ kind: "link", href, children: [{ kind: "text", text: alt || href }] });
        else if (path) push({ kind: "file", path, children: [{ kind: "text", text: alt || path }] });
        else if (alt) push({ kind: "text", text: alt });
        break;
      }
      case "html": {
        const tag = token.text.trim();
        if (/^<br\s*\/?>$/i.test(tag)) push({ kind: "break" });
        // A document's tags give their text, which comes as tokens of its own.
        else if (!doc) push({ kind: "text", text: token.text });
        break;
      }
      // Drawn by the list item, which knows whether it is ticked.
      case "checkbox":
        break;
      default:
        push({ kind: "text", text: token.raw });
    }
  }
  return content;
}

// MARK: - Documents

/**
 * A paragraph that is pictures alone, perhaps each in a link (a README's
 * banner, its row of badges), as pictures; null for one with words in it.
 */
function picturesOf(tokens: Token[], doc: Doc): Picture[] | null {
  const pictures: Picture[] = [];
  let link: Picture["link"];
  for (const token of tokens) {
    if (token.type === "image") {
      pictures.push(picture(token.href, token.text, undefined, link, doc));
    } else if (token.type === "link" && token.tokens?.some((inner) => inner.type === "image")) {
      const around = linkTarget(token.href, doc);
      for (const inner of token.tokens) {
        if (inner.type === "image") pictures.push(picture(inner.href, inner.text, undefined, around, doc));
        else if (!blank(inner)) return null;
      }
    } else if (token.type === "html" && /^<img\b/i.test(token.text.trim())) {
      pictures.push(imgTag(token.text, link, doc));
    } else if (token.type === "html" && /^<a\b/i.test(token.text.trim())) {
      link = linkTarget(attribute(token.text, "href") ?? "", doc);
    } else if (token.type === "html" && /^<\/a>/i.test(token.text.trim())) {
      link = undefined;
    } else if (!blank(token)) {
      return null;
    }
  }
  return pictures.length > 0 ? pictures : null;
}

/** Space between pictures, a line break, a closing tag: nothing to read. */
function blank(token: Token): boolean {
  if (token.type === "br") return true;
  if (token.type === "html") return /^<(br|\/?(p|div|span|picture|source)\b)/i.test(token.text.trim());
  return (token.type === "text" || token.type === "escape") && token.text.trim() === "";
}

function picture(src: string, alt: string, width: number | undefined, link: Picture["link"], doc: Doc): Picture {
  const url = safeHref(src);
  const path = url ? null : documentPath(src, doc.dir);
  return {
    alt: decodeEntities(alt.trim()),
    ...(url ? { url } : {}),
    ...(path ? { path } : {}),
    ...(link ? { link } : {}),
    ...(width ? { width } : {}),
  };
}

function imgTag(tag: string, link: Picture["link"], doc: Doc): Picture {
  const width = Number.parseInt(attribute(tag, "width") ?? "", 10);
  return picture(attribute(tag, "src") ?? "", attribute(tag, "alt") ?? "", width > 0 ? width : undefined, link, doc);
}

function linkTarget(href: string, doc: Doc): Picture["link"] {
  const url = safeHref(href);
  if (url) return { href: url };
  const path = documentPath(href, doc.dir);
  return path ? { path } : undefined;
}

/** An attribute's value in a tag, entities decoded. */
function attribute(tag: string, name: string): string | null {
  const match = new RegExp(`\\b${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|([^\\s>]+))`, "i").exec(tag);
  if (!match) return null;
  return decodeEntities(match[1] ?? match[2] ?? match[3] ?? "");
}

const ENTITIES: Record<string, string> = {
  amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: "\u00a0", mdash: "—", ndash: "–",
  hellip: "…", copy: "©", reg: "®", trade: "™", middot: "·", bull: "•", rarr: "→", larr: "←",
};

function decodeEntities(text: string): string {
  return text.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (whole, name: string) => {
    if (name[0] === "#") {
      const code = name[1] === "x" || name[1] === "X" ? Number.parseInt(name.slice(2), 16) : Number.parseInt(name.slice(1), 10);
      return Number.isFinite(code) && code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : whole;
    }
    return ENTITIES[name.toLowerCase()] ?? whole;
  });
}

const HTML_TOKEN = /<!--[\s\S]*?-->|<(\/?)([a-zA-Z][a-zA-Z0-9]*)\b([^>]*)>|[^<]+|</g;

/**
 * A document's block of HTML, laid out: a centred <div> or <p> centres what
 * follows until it closes, even past the HTML (a README's banner opens with
 * one, then writes Markdown inside it); pictures, links around them and
 * <br> are kept; headings stay headings; any other tag gives its text.
 */
function htmlBlocks(html: string, doc: Doc): Block[] {
  const blocks: Block[] = [];
  let content: Inline[] = [];
  let pictures: Picture[] = [];
  let heading = 0;
  let link: Picture["link"];
  const centred = () => doc.centered.includes(true);
  const flushText = () => {
    trimEnds(content);
    if (content.length > 0) {
      const block: Block = heading ? { kind: "heading", level: heading, content } : { kind: "paragraph", content };
      blocks.push(centred() ? { ...block, centered: true } : block);
    }
    content = [];
  };
  const flushPictures = () => {
    if (pictures.length > 0) blocks.push(centred() ? { kind: "pictures", pictures, centered: true } : { kind: "pictures", pictures });
    pictures = [];
  };
  const flush = () => {
    flushPictures();
    flushText();
  };
  for (const match of html.matchAll(HTML_TOKEN)) {
    const [whole, closing, rawName, attributes] = match;
    if (whole.startsWith("<!--")) continue;
    if (!rawName) {
      const text = decodeEntities(whole).replace(/\s+/g, " ");
      if (text.trim() === "" && content.length === 0) continue;
      flushPictures();
      const node: Inline = { kind: "text", text };
      if (link && "href" in link) content.push({ kind: "link", href: link.href, children: [node] });
      else if (link) content.push({ kind: "file", path: link.path, children: [node] });
      else content.push(node);
      continue;
    }
    const name = rawName.toLowerCase();
    if (name === "img") {
      flushText();
      pictures.push(imgTag(whole, link, doc));
    } else if (name === "br") {
      if (content.length > 0) content.push({ kind: "break" });
    } else if (name === "a") {
      link = closing ? undefined : linkTarget(attribute(whole, "href") ?? "", doc);
    } else if (/^h[1-6]$/.test(name)) {
      flush();
      heading = closing ? 0 : Number(name[1]);
    } else if (name === "div" || name === "p" || name === "center") {
      flush();
      if (closing) doc.centered.pop();
      else doc.centered.push(name === "center" || /\balign\s*=\s*["']?center/i.test(attributes));
    }
  }
  flush();
  return blocks;
}

/** A paragraph's text without the space and breaks its tags left round it. */
function trimEnds(content: Inline[]) {
  while (content.length > 0 && isSpace(content[0])) content.shift();
  while (content.length > 0 && isSpace(content[content.length - 1])) content.pop();
  const first = content[0];
  if (first?.kind === "text") first.text = first.text.trimStart();
  const last = content[content.length - 1];
  if (last?.kind === "text") last.text = last.text.trimEnd();
}

function isSpace(node: Inline): boolean {
  return node.kind === "break" || (node.kind === "text" && node.text.trim() === "");
}

