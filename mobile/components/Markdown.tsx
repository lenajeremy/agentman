import Feather from "@expo/vector-icons/Feather";
import * as WebBrowser from "expo-web-browser";
import { ReactNode, useMemo, useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";

import { parseBlocks, type Block } from "../lib/markdown-blocks";
import { tokenizeInline } from "../lib/markdown-inline";
import { cellWidth, type Table } from "../lib/markdown-table";
import { useStyles, useTheme } from "../lib/appearance";
import { CodeViewer } from "./CodeViewer";
import { SyntaxCode } from "./SyntaxCode";
import { font, Palette, radius, size, space } from "../lib/theme";

type Styles = ReturnType<typeof makeStyles>;

const MAX_MARKDOWN_CHARS = 200_000;

/**
 * Longest span rendered as a rounded chip. An inline View is a single
 * unbreakable box, so beyond roughly this it would push past the column
 * instead of wrapping; those fall back to a flat highlight that still wraps.
 */
const MAX_CODE_CHIP = 40;

/**
 * Lines of a code block shown before it asks to be opened.
 *
 * A reply that explains a change often carries the whole file after it, and a
 * block that runs for four screens costs more to scroll past than it gave.
 * Twelve matches what a tool's output is clamped to, so the two kinds of
 * block in a feed behave the same way.
 */
const CODE_LINES = 12;

/**
 * Renders the small markdown vocabulary agents use in ordinary replies.
 *
 * This is intentionally local and linear rather than a general HTML/markdown
 * engine. Agent output is untrusted: the former parser dependency had known
 * quadratic-complexity advisories, could fetch remote images, and opened custom
 * URL schemes. This renderer fetches nothing, bounds the amount of one message
 * it will parse, and opens only http(s) links — in an in-app browser, so a
 * tapped link can never reach the OS scheme handler. See lib/markdown-inline.
 */
export function Markdown({ children }: { children: string }) {
  const clipped = children.length > MAX_MARKDOWN_CHARS;
  const source = clipped ? children.slice(0, MAX_MARKDOWN_CHARS) : children;
  const blocks = parseBlocks(source);
  const styles = useStyles(makeStyles);

  return (
    <View style={styles.body}>
      {blocks.map((block, index) => (
        <BlockView key={`${index}:${block.kind}`} block={block} styles={styles} />
      ))}
      {clipped ? (
        <Text style={styles.truncated}>
          Message truncated on this device after {MAX_MARKDOWN_CHARS.toLocaleString()} characters.
        </Text>
      ) : null}
    </View>
  );
}

/**
 * Heading sizes.
 *
 * Only the top level is enlarged, which is what this renderer already did — the
 * change here is that levels four to six reach this function at all instead of
 * being left as literal hashes in a paragraph. Deeper levels stay at body size
 * and separate themselves by weight; a reply is not a document, and six
 * typographic ranks in a chat bubble would be noise.
 */
function headingSize(level: number, styles: Styles) {
  return level === 1 ? styles.heading1 : undefined;
}


/**
 * A fenced code block.
 *
 * Highlighted where the fence named a language, clamped to a dozen lines, and
 * openable — the same three moves a tool's output already makes, because a
 * reader meeting both in one feed should not have to learn two behaviours.
 * Unhighlighted is not a failure state: a fence with no language, or one
 * naming a grammar that is not bundled, renders as plain monospace and reads
 * exactly as it did before.
 */
function CodeBlock({
  source,
  language,
  styles,
}: {
  source: string;
  language: string;
  styles: Styles;
}) {
  const { color } = useTheme();
  const [full, setFull] = useState(false);
  const [viewer, setViewer] = useState(false);

  const lines = useMemo(() => source.split("\n"), [source]);
  const hidden = full ? 0 : Math.max(0, lines.length - CODE_LINES);

  return (
    <View>
      <View style={styles.codeFrame}>
        {language ? (
          <View style={styles.codeHeader}>
            <Text style={styles.codeLanguage}>{language}</Text>
          </View>
        ) : null}
        <SyntaxCode
          source={source}
          filename=""
          language={language}
          wrap
          lineNumbers={false}
          frameless
          maxLines={full ? undefined : CODE_LINES}
        />
      </View>
      {/* Under the block, not over it: a control floating in the corner
          covers the first line, which is the one most worth reading. */}
      <View style={styles.codeFooter}>
        {hidden > 0 ? (
          <Pressable
            onPress={() => setFull(true)}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel={`Show ${hidden} more lines inline`}
          >
            <Text style={styles.codeAction}>
              {hidden === 1
                ? "Show 1 more line"
                : `Show ${hidden.toLocaleString()} more lines`}
            </Text>
          </Pressable>
        ) : null}
        <Pressable
          onPress={() => setViewer(true)}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel="Open this code full screen"
        >
          <View style={styles.codeFullRow}>
            <Feather name="maximize-2" size={11} color={color.muted} />
            <Text style={styles.codeAction}>Full screen</Text>
          </View>
        </Pressable>
      </View>
      <CodeViewer
        visible={viewer}
        onClose={() => setViewer(false)}
        source={source}
        language={language}
      />
    </View>
  );
}

function BlockView({ block, styles }: { block: Block; styles: Styles }) {
  switch (block.kind) {
    case "heading":
      return (
        <Text selectable style={[styles.text, styles.heading, headingSize(block.level, styles)]}>
          {renderInline(block.text, styles)}
        </Text>
      );
    case "rule":
      return <View style={styles.rule} />;
    case "code":
      return <CodeBlock source={block.text} language={block.language ?? ""} styles={styles} />;
    case "quote":
      return (
        <View style={styles.quote}>
          <Text selectable style={[styles.text, styles.muted]}>
            {renderInline(block.text, styles)}
          </Text>
        </View>
      );
    case "table":
      return <TableView table={block.table} styles={styles} />;
    case "bullet":
    case "number":
      return (
        <View style={styles.listRow}>
          <Text style={styles.marker}>{block.marker}</Text>
          <Text selectable style={[styles.text, styles.listText]}>
            {renderInline(block.text, styles)}
          </Text>
        </View>
      );
    default:
      return (
        <Text selectable style={styles.text}>
          {renderInline(block.text, styles)}
        </Text>
      );
  }
}

function TableView({ table, styles }: { table: Table; styles: Styles }) {
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
              {renderInline(heading, styles)}
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
                {renderInline(cell, styles)}
              </Text>
            ))}
          </View>
        ))}
      </View>
    </ScrollView>
  );
}

/**
 * Opens a link the agent wrote.
 *
 * Deliberately the in-app browser rather than Linking.openURL: the tokenizer
 * already refuses every scheme but http(s), and routing through WebBrowser
 * means even a mistake there cannot hand a custom scheme to the OS. A failure
 * to open is swallowed — a tap that does nothing beats a crash mid-transcript.
 */
function openLink(href: string) {
  void WebBrowser.openBrowserAsync(href).catch(() => {});
}

// Bold, code spans and links cover the vocabulary agents actually use.
// Tokenising is in lib/markdown-inline so it can be tested on Node.
function renderInline(text: string, styles: Styles): ReactNode[] {
  return tokenizeInline(text).map((token, key) => {
    switch (token.kind) {
      case "text":
        return token.text;
      case "bold":
        return (
          <Text key={key} style={styles.bold}>
            {token.text}
          </Text>
        );
      case "italic":
        return (
          <Text key={key} style={styles.italic}>
            {token.text}
          </Text>
        );
      case "math":
        // Not typeset — that would be a maths engine, and this renderer
        // deliberately has no dependencies. Set in mono because that is what
        // this system does with machine notation, which reads O(N^2)
        // correctly and drops the dollars nobody meant to see.
        return (
          <Text key={key} style={styles.math}>
            {token.text}
          </Text>
        );
      case "code":
        // A View, not a styled Text: a nested Text is an attributed-string
        // span, and both platforms draw its background as a plain rectangle —
        // the radius and padding set on it are silently dropped. A View
        // nested in Text is laid out as an inline block, which does honour
        // them. It cannot wrap internally though, so a long span stays flat
        // text rather than becoming a chip that overflows the column.
        return token.text.length <= MAX_CODE_CHIP ? (
          <View key={key} style={styles.inlineCodeChip}>
            <Text style={styles.inlineCode}>{token.text}</Text>
          </View>
        ) : (
          <Text key={key} style={[styles.inlineCode, styles.inlineCodeFlat]}>
            {token.text}
          </Text>
        );
      case "link":
        return (
          <Text
            key={key}
            style={styles.link}
            onPress={() => openLink(token.href)}
            accessibilityRole="link"
            accessibilityHint={`Opens ${token.href}`}
          >
            {token.text}
          </Text>
        );
    }
  });
}

function makeStyles(c: Palette) {
  return StyleSheet.create({
    body: { gap: space.sm },
    text: {
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.text,
      lineHeight: 23,
    },
    heading: { fontFamily: font.sansBold, marginTop: space.xs, letterSpacing: -0.2 },
    heading1: { fontSize: size.title },
    // Thematic breaks separate sections, so they need the air a paragraph gap
    // does not give on its own.
    rule: {
      height: StyleSheet.hairlineWidth,
      backgroundColor: c.line,
      marginVertical: space.sm,
    },
    bold: { fontFamily: font.sansBold },
    link: { color: c.workingText, textDecorationLine: "underline" },
    // A chip rather than a raw highlight: without the inset the background sits
    // flush against the glyphs and reads as a sharp block mid-sentence.
    //
    // Only horizontal inset and a radius. Vertical padding on a nested Text is
    // measured into the line box, so it pushes wrapped lines apart unevenly
    // rather than growing the chip. Android ignores a radius on an inline span
    // altogether — there it degrades to the flat highlight this replaces.
    // The chip carries the ground and the shape; the text inside carries none,
    // or the background would be drawn twice with only the outer one rounded.
    inlineCodeChip: {
      backgroundColor: c.fill,
      borderRadius: 6,
      paddingHorizontal: 6,
      paddingVertical: 1,
      // Nudges the box down so its text sits on the surrounding baseline
      // rather than riding above it.
      transform: [{ translateY: 3 }],
    },
    inlineCode: {
      fontFamily: font.mono,
      fontSize: size.caption,
      color: c.textSecondary,
      lineHeight: 18,
    },
    // The fallback keeps the old flat highlight, which wraps.
    inlineCodeFlat: { backgroundColor: c.fill },
    italic: { fontStyle: "italic" },
    math: { fontFamily: font.mono, fontSize: size.caption },
    codeFrame: {
      backgroundColor: c.fill,
      borderRadius: radius.md,
      paddingVertical: space.sm,
      paddingHorizontal: space.md,
      overflow: "hidden",
    },
    codeHeader: {
      flexDirection: "row",
      alignItems: "center",
      paddingBottom: space.xs,
    },
    // The language is the one thing a reader wants confirmed at a glance, and
    // it is machine text, so it is set as such rather than as a label.
    codeLanguage: {
      fontFamily: font.mono,
      fontSize: size.label,
      color: c.faint,
    },
    codeFooter: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      paddingTop: space.xs,
      paddingHorizontal: space.xs,
    },
    codeAction: {
      fontFamily: font.sansMedium,
      fontSize: size.label,
      color: c.muted,
    },
    codeFullRow: { flexDirection: "row", alignItems: "center", gap: space.xs },
    quote: {
      borderLeftWidth: 3,
      borderLeftColor: c.fillStrong,
      paddingLeft: space.md,
    },
    muted: { color: c.muted },
    listRow: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
    marker: {
      minWidth: 16,
      fontFamily: font.mono,
      fontSize: size.caption,
      color: c.faint,
      lineHeight: 23,
    },
    listText: { flex: 1 },
    // A table is the one block with a natural width the screen cannot always
    // give it, so it scrolls sideways inside its own container rather than
    // forcing the whole feed to.
    tableScroll: { marginVertical: space.xs },
    tableScrollContent: { paddingRight: space.lg },
    table: {
      borderWidth: 1,
      borderColor: c.line,
      borderRadius: radius.md,
      overflow: "hidden",
      backgroundColor: c.surface,
    },
    tableRow: { flexDirection: "row" },
    // The header sits on a filled strip, which is what separates it from the
    // body without needing a heavier rule.
    tableHeadRow: { backgroundColor: c.fill },
    tableRowRuled: { borderTopWidth: 1, borderTopColor: c.line },
    tableCell: {
      paddingHorizontal: space.md,
      paddingVertical: space.sm,
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 19,
      color: c.text,
    },
    tableHeadCell: { fontFamily: font.sansMedium, color: c.muted },

    truncated: {
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.faint,
      fontStyle: "italic",
    },
  });
}
