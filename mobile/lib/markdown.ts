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
 * - Nothing is fetched. An image becomes a link to it.
 * - HTML is never interpreted. It is shown as the text the agent wrote.
 * - Agent output is untrusted, and marked is not linear on every input — a
 *   20 KB line of `**a *` takes three seconds, and so does a long word ending
 *   in `@`. Containers stop nesting at MAX_NESTING and inline markup is only
 *   parsed where its cost is bounded (inlineWork). Past either limit the text
 *   is still shown, just without its formatting.
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
  | { kind: "break" };

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

export type Block =
  | { kind: "paragraph"; content: Inline[] }
  | { kind: "heading"; level: number; content: Inline[] }
  // The fence's language, lowercased, when it names one: ```java. It is what
  // lets a block be highlighted.
  | { kind: "code"; text: string; language?: string }
  | { kind: "quote"; blocks: Block[] }
  | { kind: "list"; items: ListItem[] }
  | { kind: "rule" }
  | { kind: "table"; table: Table };

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
    return toBlocks(lex(source), 0);
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
function lex(source: string): Token[] {
  const lexer = new Lexer({ ...OPTIONS, tokenizer: new BoundedTokenizer() });
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

function toBlocks(tokens: Token[], listDepth: number): Block[] {
  const blocks: Block[] = [];
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
      case "text":
        blocks.push({ kind: "paragraph", content: toInlines(token.tokens ?? [], 0) });
        break;
      case "heading": {
        // A bare `####` is an empty heading, which draws as nothing at all.
        const content = toInlines(token.tokens ?? [], 0);
        if (content.length > 0) blocks.push({ kind: "heading", level: token.depth, content });
        break;
      }
      case "code": {
        const language = fenceLanguage(token.lang ?? "");
        blocks.push(
          language ? { kind: "code", text: token.text, language } : { kind: "code", text: token.text },
        );
        break;
      }
      case "blockquote":
        blocks.push({ kind: "quote", blocks: toBlocks(token.tokens ?? [], listDepth) });
        break;
      case "list":
        blocks.push({ kind: "list", items: toListItems(token as Tokens.List, listDepth) });
        break;
      case "hr":
        blocks.push({ kind: "rule" });
        break;
      case "table":
        blocks.push(toTable(token as Tokens.Table));
        break;
      default:
        // HTML, and anything a later marked adds: shown as written.
        if (token.raw.trim() !== "") blocks.push(textBlock(token.raw.trim()));
    }
  }
  return blocks;
}

function toListItems(list: Tokens.List, depth: number): ListItem[] {
  const start = typeof list.start === "number" ? list.start : 1;
  return list.items.map((item, index) => ({
    marker: list.ordered ? `${start + index}.` : depth === 0 ? "•" : "◦",
    ...(item.task ? { checked: item.checked === true } : {}),
    blocks: toBlocks(item.tokens, depth + 1),
  }));
}

function toTable(table: Tokens.Table): Block {
  // Measured in columns because the renderer gives each a fixed width; past
  // a phone's worth the text reads better than the grid.
  if (table.header.length > MAX_COLUMNS) return textBlock(table.raw.trim());
  const cell = (entry: Tokens.TableCell) => toInlines(entry.tokens, 0);
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

function toInlines(tokens: Token[], depth: number): Inline[] {
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
        push({ kind: "text", text: token.text });
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
            children: toInlines(token.tokens ?? [], depth + 1),
          });
        break;
      case "link": {
        const label = toInlines(token.tokens ?? [], depth + 1);
        const href = safeHref(token.href);
        // A refused target still shows its label, formatting and all: hiding
        // that the agent wrote a link would be worse than an inert one.
        if (!href) label.forEach(push);
        else push({ kind: "link", href, children: label.length > 0 ? label : [{ kind: "text", text: href }] });
        break;
      }
      case "image": {
        // Never fetched. Shown as a link the reader can choose to open.
        const href = safeHref(token.href);
        const alt = token.text.trim();
        if (href) push({ kind: "link", href, children: [{ kind: "text", text: alt || href }] });
        else if (alt) push({ kind: "text", text: alt });
        break;
      }
      case "html":
        push(/^<br\s*\/?>$/i.test(token.text.trim()) ? { kind: "break" } : { kind: "text", text: token.text });
        break;
      // Drawn by the list item, which knows whether it is ticked.
      case "checkbox":
        break;
      default:
        push({ kind: "text", text: token.raw });
    }
  }
  return content;
}
