import { ReactNode } from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";

import { cellWidth, parseTable, type Table } from "../lib/markdown-table";
import { color, font, radius, size, space } from "../lib/theme";

const MAX_MARKDOWN_CHARS = 200_000;

type Block =
  | { kind: "paragraph" | "quote" | "code"; text: string }
  | { kind: "heading"; level: number; text: string }
  | { kind: "bullet" | "number"; marker: string; text: string }
  | { kind: "table"; table: Table };

/**
 * Renders the small markdown vocabulary agents use in ordinary replies.
 *
 * This is intentionally local and linear rather than a general HTML/markdown
 * engine. Agent output is untrusted: the former parser dependency had known
 * quadratic-complexity advisories, could fetch remote images, and opened custom
 * URL schemes. This renderer never performs network or OS actions and bounds
 * the amount of one message it will parse.
 */
export function Markdown({ children }: { children: string }) {
  const clipped = children.length > MAX_MARKDOWN_CHARS;
  const source = clipped ? children.slice(0, MAX_MARKDOWN_CHARS) : children;
  const blocks = parseBlocks(source);

  return (
    <View style={styles.body}>
      {blocks.map((block, index) => (
        <BlockView key={`${index}:${block.kind}`} block={block} />
      ))}
      {clipped ? (
        <Text style={styles.truncated}>
          Message truncated on this device after {MAX_MARKDOWN_CHARS.toLocaleString()} characters.
        </Text>
      ) : null}
    </View>
  );
}

function BlockView({ block }: { block: Block }) {
  switch (block.kind) {
    case "heading":
      return (
        <Text selectable style={[styles.text, styles.heading, block.level === 1 && styles.heading1]}>
          {renderInline(block.text)}
        </Text>
      );
    case "code":
      return (
        <Text selectable style={styles.codeBlock}>
          {block.text}
        </Text>
      );
    case "quote":
      return (
        <View style={styles.quote}>
          <Text selectable style={[styles.text, styles.muted]}>
            {renderInline(block.text)}
          </Text>
        </View>
      );
    case "table":
      return <TableView table={block.table} />;
    case "bullet":
    case "number":
      return (
        <View style={styles.listRow}>
          <Text style={styles.marker}>{block.marker}</Text>
          <Text selectable style={[styles.text, styles.listText]}>
            {renderInline(block.text)}
          </Text>
        </View>
      );
    default:
      return (
        <Text selectable style={styles.text}>
          {renderInline(block.text)}
        </Text>
      );
  }
}

function TableView({ table }: { table: Table }) {
  // React Native has no table layout, so columns are measured here and given
  // fixed widths. Without that, each row sizes independently and the columns
  // do not line up with one another.
  const widths = table.header.map((heading, index) => {
    const longest = table.rows.reduce(
      (worst, row) => Math.max(worst, cellWidth(row[index] ?? "")),
      cellWidth(heading),
    );
    return Math.min(240, Math.max(76, longest * 7.6 + 24));
  });

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      style={styles.tableScroll}
      contentContainerStyle={styles.tableScrollContent}
    >
      <View style={styles.table}>
        <View style={[styles.tableRow, styles.tableHeadRow]}>
          {table.header.map((heading, index) => (
            <Text
              key={index}
              selectable
              style={[
                styles.tableCell,
                styles.tableHeadCell,
                { width: widths[index], textAlign: table.align[index] },
              ]}
            >
              {renderInline(heading)}
            </Text>
          ))}
        </View>
        {table.rows.map((row, rowIndex) => (
          <View
            key={rowIndex}
            style={[styles.tableRow, rowIndex > 0 && styles.tableRowRuled]}
          >
            {row.map((cell, index) => (
              <Text
                key={index}
                selectable
                style={[
                  styles.tableCell,
                  { width: widths[index], textAlign: table.align[index] },
                ]}
              >
                {renderInline(cell)}
              </Text>
            ))}
          </View>
        ))}
      </View>
    </ScrollView>
  );
}

function parseBlocks(source: string): Block[] {
  const blocks: Block[] = [];
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  let paragraph: string[] = [];
  let code: string[] | null = null;

  const flushParagraph = () => {
    if (paragraph.length > 0) {
      blocks.push({ kind: "paragraph", text: paragraph.join("\n") });
      paragraph = [];
    }
  };
  const flushCode = () => {
    if (code !== null) {
      blocks.push({ kind: "code", text: code.join("\n") });
      code = null;
    }
  };

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trimStart();
    if (trimmed.startsWith("```")) {
      if (code === null) {
        flushParagraph();
        code = [];
      } else {
        flushCode();
      }
      continue;
    }
    if (code !== null) {
      code.push(line);
      continue;
    }
    if (trimmed === "") {
      flushParagraph();
      continue;
    }

    const heading = headingLine(trimmed);
    if (heading) {
      flushParagraph();
      blocks.push(heading);
      continue;
    }
    if (trimmed.startsWith("> ")) {
      flushParagraph();
      blocks.push({ kind: "quote", text: trimmed.slice(2) });
      continue;
    }
    if (/^[-*+]\s/.test(trimmed)) {
      flushParagraph();
      blocks.push({ kind: "bullet", marker: "•", text: trimmed.slice(2) });
      continue;
    }
    const numbered = numberedLine(trimmed);
    if (numbered) {
      flushParagraph();
      blocks.push(numbered);
      continue;
    }
    // Checked late: a table only exists if the next line is a rule, so every
    // cheaper block shape gets to claim the line first.
    const table = parseTable(lines, index);
    if (table) {
      flushParagraph();
      blocks.push({ kind: "table", table: table.table });
      index = table.next - 1;
      continue;
    }
    paragraph.push(line);
  }
  flushParagraph();
  flushCode();
  return blocks;
}

function headingLine(line: string): Block | null {
  let level = 0;
  while (level < 3 && line[level] === "#") level += 1;
  if (level === 0 || line[level] !== " ") return null;
  return { kind: "heading", level, text: line.slice(level + 1) };
}

function numberedLine(line: string): Block | null {
  let end = 0;
  while (end < line.length && end < 6 && line.charCodeAt(end) >= 48 && line.charCodeAt(end) <= 57) {
    end += 1;
  }
  if (end === 0 || line[end] !== "." || line[end + 1] !== " ") return null;
  return { kind: "number", marker: line.slice(0, end + 1), text: line.slice(end + 2) };
}

// Inline code and bold cover the high-value cases (paths, commands, result
// labels) without auto-linking or interpreting arbitrary HTML.
function renderInline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let cursor = 0;
  let plainStart = 0;
  let key = 0;
  while (cursor < text.length) {
    const marker = text.startsWith("**", cursor) ? "**" : text[cursor] === "`" ? "`" : "";
    if (!marker) {
      cursor += 1;
      continue;
    }
    const contentStart = cursor + marker.length;
    const close = text.indexOf(marker, contentStart);
    if (close < 0) {
      // There cannot be another occurrence of this marker, so continuing the
      // single pass cannot repeatedly rescan the same suffix.
      cursor += marker.length;
      continue;
    }
    if (cursor > plainStart) out.push(text.slice(plainStart, cursor));
    out.push(
      <Text key={key++} style={marker === "`" ? styles.inlineCode : styles.bold}>
        {text.slice(contentStart, close)}
      </Text>,
    );
    cursor = close + marker.length;
    plainStart = cursor;
  }
  if (plainStart < text.length) out.push(text.slice(plainStart));
  return out;
}

const styles = StyleSheet.create({
  body: { gap: space.sm },
  text: {
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.text,
    lineHeight: 22,
  },
  heading: { fontFamily: font.sansBold, marginTop: space.xs },
  heading1: { fontSize: size.title },
  bold: { fontFamily: font.sansBold },
  inlineCode: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.working,
    backgroundColor: color.sunken,
  },
  codeBlock: {
    fontFamily: font.mono,
    fontSize: size.caption,
    lineHeight: 18,
    color: color.text,
    backgroundColor: color.sunken,
    borderRadius: radius.sm,
    padding: space.sm,
  },
  quote: {
    borderLeftWidth: 2,
    borderLeftColor: color.line,
    paddingLeft: space.sm,
  },
  muted: { color: color.muted },
  listRow: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
  marker: {
    minWidth: 16,
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.faint,
    lineHeight: 22,
  },
  listText: { flex: 1 },
  // A table is the one block with a natural width the screen cannot always
  // give it, so it scrolls sideways inside its own container rather than
  // forcing the whole feed to.
  tableScroll: { marginVertical: space.xs },
  tableScrollContent: { paddingRight: space.lg },
  table: {
    borderWidth: 1,
    borderColor: color.line,
    borderRadius: radius.md,
    overflow: "hidden",
  },
  tableRow: { flexDirection: "row" },
  // The header sits on a lifted strip, which is what separates it from the
  // body without needing a heavier rule.
  tableHeadRow: { backgroundColor: color.surfaceRaised },
  tableRowRuled: { borderTopWidth: 1, borderTopColor: color.line },
  tableCell: {
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    fontFamily: font.sans,
    fontSize: size.caption,
    lineHeight: 19,
    color: color.text,
  },
  tableHeadCell: { fontFamily: font.sansMedium, color: color.muted },

  truncated: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.faint,
    fontStyle: "italic",
  },
});
