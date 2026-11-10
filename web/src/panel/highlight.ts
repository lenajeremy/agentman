// Light syntax colouring for code (Design/CodeHighlighter.swift): comments,
// strings, numbers, keywords and type names, by the file's extension. One
// pass of one expression, so a string inside a comment stays a comment.

export type Token = "comment" | "string" | "number" | "keyword" | "type";

interface Language {
  keywords: string[];
  lineComment: string[];
  blockComment: boolean;
  hashComment: boolean;
  backtick: boolean;
}

function lang(keywords: string, opts: Partial<Omit<Language, "keywords">> = {}): Language {
  return {
    keywords: keywords.split(/[ \n]+/).filter(Boolean),
    lineComment: ["//"],
    blockComment: true,
    hashComment: false,
    backtick: false,
    ...opts,
  };
}

const C_LIKE_TS = `const let var function return if else for while do switch case default break continue new class extends super this
import export from as async await try catch finally throw typeof instanceof in of null undefined true false void
interface type enum implements public private protected readonly static yield delete`;

export function languageFor(path: string): Language | null {
  const name = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
  const dot = name.lastIndexOf(".");
  const ext = dot > 0 ? name.slice(dot + 1) : "";
  switch (ext) {
    case "swift": return lang(`let var func struct class enum protocol extension import return if else guard switch case default for in while repeat
break continue throw throws rethrows try catch do async await actor init deinit self Self super nil true false some any
static private fileprivate public internal open final override mutating nonisolated where as is inout defer`);
    case "go": return lang(`package import func return if else for range switch case default break continue go defer select chan map struct
interface type var const nil true false fallthrough goto`, { backtick: true });
    case "ts": case "tsx": case "js": case "jsx": case "mjs": case "cjs": case "mts": case "cts":
      return lang(C_LIKE_TS, { backtick: true });
    case "py": return lang(`def class return if elif else for while in not and or is import from as with try except finally raise pass break
continue lambda yield global nonlocal None True False async await assert del`, { lineComment: [], blockComment: false, hashComment: true });
    case "rs": return lang(`fn let mut const static struct enum impl trait pub use mod crate self Self super return if else match for in while loop
break continue move ref as where async await dyn unsafe true false type`);
    case "java": case "kt": case "kts": case "scala": return lang(`class interface object fun val var public private protected internal static final abstract override return if else when
for while do switch case default break continue new this super import package try catch finally throw null true false
void extends implements`);
    case "c": case "h": case "cpp": case "cc": case "hpp": case "m": case "mm": return lang(`int char float double void long short unsigned signed const static struct union enum typedef return if else for while do
switch case default break continue sizeof include define ifdef ifndef endif class public private protected namespace
template typename new delete nullptr true false auto`);
    case "rb": return lang(`def class module end return if elsif else unless while until for in do begin rescue ensure yield self nil true false and
or not require`, { lineComment: [], blockComment: false, hashComment: true });
    case "sh": case "bash": case "zsh": return lang(`if then else elif fi for in do done while until case esac function return export local set echo exit`,
      { lineComment: [], blockComment: false, hashComment: true });
    case "yaml": case "yml": case "toml": return lang("true false null yes no", { lineComment: [], blockComment: false, hashComment: true });
    case "json": return lang("true false null", { lineComment: [], blockComment: false });
    case "css": case "scss": return lang("important media import", { lineComment: [], blockComment: true });
    case "sql": return lang(`select from where insert into values update set delete create table index drop alter join left right inner outer on and
or not null as order by group having limit primary key references`, { lineComment: ["--"], blockComment: true });
    default:
      if (name === "dockerfile" || name === "makefile") {
        return lang("FROM RUN COPY ADD CMD ENTRYPOINT ENV WORKDIR EXPOSE ARG", { lineComment: [], blockComment: false, hashComment: true });
      }
      return null;
  }
}

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\/-]/g, "\\$&");
const cache = new Map<Language, RegExp>();

function expression(language: Language): RegExp {
  const cached = cache.get(language);
  if (cached) return cached;
  const comment = language.lineComment.map((c) => escape(c) + "[^\\n]*");
  if (language.hashComment) comment.push("#[^\\n]*");
  if (language.blockComment) comment.push("/\\*[\\s\\S]*?\\*/");
  const strings = [`"(?:\\\\.|[^"\\\\\\n])*"`, `'(?:\\\\.|[^'\\\\\\n])*'`];
  if (language.backtick) strings.push("`[^`]*`");
  const keywords = [...new Set(language.keywords)].sort((a, b) => b.length - a.length).map(escape).join("|");
  let pattern = "";
  if (comment.length) pattern += `(?<comment>${comment.join("|")})|`;
  pattern += `(?<string>${strings.join("|")})`;
  pattern += "|(?<number>\\b\\d[\\d_]*(?:\\.\\d+)?\\b)";
  if (keywords) pattern += `|(?<keyword>\\b(?:${keywords})\\b)`;
  pattern += "|(?<type>\\b[A-Z][A-Za-z0-9]+\\b)";
  const re = new RegExp(pattern, "g");
  cache.set(language, re);
  return re;
}

export interface Span {
  start: number;
  end: number;
  token: Token;
}

/** Where each kind of token is in text, in UTF-16 offsets. */
export function tokens(text: string, language: Language): Span[] {
  const re = expression(language);
  re.lastIndex = 0;
  const found: Span[] = [];
  for (const match of text.matchAll(re)) {
    const groups = match.groups ?? {};
    const token = (["comment", "string", "number", "keyword", "type"] as Token[]).find((t) => groups[t] !== undefined);
    if (token && match[0].length) found.push({ start: match.index!, end: match.index! + match[0].length, token });
  }
  return found;
}

/** The text cut into runs, each with its token or none. */
export function runs(text: string, language: Language | null): { text: string; token?: Token }[] {
  if (!language) return [{ text }];
  const out: { text: string; token?: Token }[] = [];
  let at = 0;
  for (const span of tokens(text, language)) {
    if (span.start > at) out.push({ text: text.slice(at, span.start) });
    out.push({ text: text.slice(span.start, span.end), token: span.token });
    at = span.end;
  }
  if (at < text.length) out.push({ text: text.slice(at) });
  return out;
}
