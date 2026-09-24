import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import css from "highlight.js/lib/languages/css";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import kotlin from "highlight.js/lib/languages/kotlin";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import ruby from "highlight.js/lib/languages/ruby";
import rust from "highlight.js/lib/languages/rust";
import sql from "highlight.js/lib/languages/sql";
import swift from "highlight.js/lib/languages/swift";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";
import { useMemo } from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";

import { useTheme } from "../lib/appearance";
import { font, Palette, space } from "../lib/theme";

for (const [name, grammar] of Object.entries({ bash, c, cpp, css, dockerfile, go, java, javascript, json, kotlin, markdown, python, ruby, rust, sql, swift, typescript, xml, yaml })) {
  hljs.registerLanguage(name, grammar);
}

const languages: Record<string, string> = {
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  ts: "typescript", tsx: "typescript", mts: "typescript", go: "go",
  py: "python", rs: "rust", sh: "bash", zsh: "bash", bash: "bash",
  css: "css", scss: "css", html: "xml", xml: "xml", svg: "xml",
  json: "json", jsonc: "json", md: "markdown", yaml: "yaml", yml: "yaml",
  c: "c", h: "c", cc: "cpp", cpp: "cpp", cxx: "cpp", hpp: "cpp",
  java: "java", kt: "kotlin", kts: "kotlin", rb: "ruby", sql: "sql", swift: "swift",
};

/**
 * A file is rendered as one row per line so each can carry a line number.
 * Every row is a View, so a long file is a lot of them: the daemon already
 * caps a preview at 256 KiB, which is still thousands of lines, and mounting
 * those at once drops frames on a phone. The visible window is capped and the
 * remainder is reported rather than silently dropped.
 */
const MAX_RENDERED_LINES = 1200;

type Token = { text: string; scope: string };

function decode(value: string): string {
  return value.replace(/&(?:amp|lt|gt|quot|#x27|#39);/g, (entity) => ({
    "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": '"', "&#x27;": "'", "&#39;": "'",
  }[entity] ?? entity));
}

// highlight.js supplies the grammar. Convert its small span-only output into
// native Text runs; rendering HTML in a WebView would expose file content to a
// second browser context and lose native text selection.
function tokens(source: string, filename: string): Token[] {
  const ext = filename.split(".").pop()?.toLowerCase() ?? "";
  const language = filename.toLowerCase() === "dockerfile" ? "dockerfile" : languages[ext];
  if (!language) return [{ text: source, scope: "" }];
  let html: string;
  try { html = hljs.highlight(source, { language, ignoreIllegals: true }).value; }
  catch { return [{ text: source, scope: "" }]; }
  const stack: string[] = [];
  const out: Token[] = [];
  for (const part of html.match(/<span class="[^"]+">|<\/span>|[^<]+/g) ?? []) {
    if (part.startsWith("<span")) {
      stack.push(part.match(/hljs-([\w-]+)/)?.[1] ?? "");
    } else if (part === "</span>") {
      stack.pop();
    } else {
      out.push({ text: decode(part), scope: stack.at(-1) ?? "" });
    }
  }
  return out;
}

/**
 * Splits highlighted runs into one array per source line.
 *
 * A token can straddle a newline — a block comment, a template literal — so
 * the split happens on the runs rather than on the text, which is what keeps
 * highlighting intact once each line is rendered separately.
 */
function toLines(runs: Token[]): Token[][] {
  const lines: Token[][] = [[]];
  for (const run of runs) {
    const parts = run.text.split("\n");
    parts.forEach((part, index) => {
      if (index > 0) lines.push([]);
      if (part.length > 0) lines[lines.length - 1].push({ text: part, scope: run.scope });
    });
  }
  return lines;
}

export function SyntaxCode({
  source,
  filename,
  wrap,
}: {
  source: string;
  filename: string;
  /** Wrapped is the phone default: panning a long line loses your place. */
  wrap: boolean;
}) {
  const { scheme, color } = useTheme();
  const styles = makeStyles(color);
  const lines = useMemo(() => toLines(tokens(source, filename)), [source, filename]);
  const dark = scheme === "dark";
  const tint: Record<string, string> = {
    keyword: dark ? "#C792EA" : "#7B3BB2",
    string: dark ? "#C3E88D" : "#527A25",
    number: dark ? "#F78C6C" : "#B04A2D",
    literal: dark ? "#F78C6C" : "#B04A2D",
    comment: dark ? "#7D8798" : "#77818E",
    title: dark ? "#82AAFF" : "#2459A6",
    built_in: dark ? "#82AAFF" : "#2459A6",
    type: dark ? "#FFCB6B" : "#906000",
    attr: dark ? "#FFCB6B" : "#906000",
    meta: dark ? "#89DDFF" : "#147286",
    variable: dark ? "#EEFFFF" : "#344054",
    property: dark ? "#89DDFF" : "#147286",
  };

  const shown = lines.slice(0, MAX_RENDERED_LINES);
  const hidden = lines.length - shown.length;
  // The gutter has to fit the widest number it will show, or the code column
  // shifts left as you scroll past line 99.
  const gutterWidth = 12 + String(lines.length).length * 8;

  const body = (
    <View style={wrap ? styles.bodyWrap : undefined}>
      {shown.map((line, index) => (
        <View key={index} style={styles.row}>
          <Text style={[styles.gutter, { width: gutterWidth }]} selectable={false}>
            {index + 1}
          </Text>
          <Text selectable style={[styles.code, wrap ? styles.codeWrap : null]}>
            {line.length === 0
              ? " "
              : line.map((run, runIndex) => (
                  <Text key={runIndex} style={{ color: tint[run.scope] ?? color.text }}>
                    {run.text}
                  </Text>
                ))}
          </Text>
        </View>
      ))}
      {hidden > 0 ? (
        <Text style={styles.more}>
          {hidden.toLocaleString()} more {hidden === 1 ? "line" : "lines"} not shown on this device.
        </Text>
      ) : null}
    </View>
  );

  // Wrapped content must not sit in a horizontal scroller: the row would size
  // to its content instead of the screen and never wrap.
  return wrap ? (
    <View style={styles.frame}>{body}</View>
  ) : (
    <ScrollView horizontal style={styles.frame} contentContainerStyle={styles.scroll}>
      {body}
    </ScrollView>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    frame: { backgroundColor: c.surface, borderTopWidth: 1, borderTopColor: c.line },
    scroll: { minWidth: "100%" },
    bodyWrap: { width: "100%" },
    // Top-aligned so a wrapped line keeps its number on the first visual row
    // and leaves the gutter blank beneath, rather than repeating it and
    // reading as several new lines.
    row: { flexDirection: "row", alignItems: "flex-start", paddingRight: space.md },
    gutter: {
      fontFamily: font.mono,
      fontSize: 11,
      lineHeight: 20,
      color: c.faint,
      textAlign: "right",
      paddingRight: space.sm,
    },
    code: { fontFamily: font.mono, fontSize: 12.5, lineHeight: 20, color: c.text },
    codeWrap: { flex: 1 },
    more: {
      fontFamily: font.sans,
      fontSize: 12,
      color: c.muted,
      padding: space.md,
      borderTopWidth: 1,
      borderTopColor: c.line,
    },
  });
