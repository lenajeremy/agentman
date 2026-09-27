import * as WebBrowser from "expo-web-browser";
import { ReactNode } from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";

import { parseBlocks, type Block } from "../lib/markdown-blocks";
import { tokenizeInline } from "../lib/markdown-inline";
import { cellWidth, type Table } from "../lib/markdown-table";
import { useStyles } from "../lib/appearance";
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
      return (
        <Text selectable style={styles.codeBlock}>
          {block.text}
        </Text>
      );
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
    codeBlock: {
      fontFamily: font.mono,
      fontSize: 12.5,
      lineHeight: 19,
      color: c.text,
      backgroundColor: c.fill,
      borderRadius: radius.md,
      padding: space.md,
      overflow: "hidden",
    },
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
