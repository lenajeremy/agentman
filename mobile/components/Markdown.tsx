import Feather from "@expo/vector-icons/Feather";
import * as WebBrowser from "expo-web-browser";
import { ReactNode, useMemo, useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";

import {
  MAX_MARKDOWN_CHARS,
  parseMarkdown,
  plainText,
  type Block,
  type Inline,
  type ListItem,
  type Table,
} from "../lib/markdown";
import { useStyles, useTheme } from "../lib/appearance";
import { CodeViewer } from "./CodeViewer";
import { SyntaxCode } from "./SyntaxCode";
import { font, Palette, radius, size, space } from "../lib/theme";

type Styles = ReturnType<typeof makeStyles>;

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
 * Renders an agent's reply as markdown.
 *
 * Parsing is marked's, through lib/markdown, which is also where the safety
 * rules live: agent output is untrusted, so nothing is fetched, HTML is shown
 * rather than interpreted, parsing is bounded, and only http(s) links open —
 * in an in-app browser, so a tapped link can never reach the OS scheme
 * handler. This file only draws the result.
 */
export function Markdown({ children }: { children: string }) {
  const clipped = children.length > MAX_MARKDOWN_CHARS;
  // A transcript re-renders far more often than a message changes.
  const blocks = useMemo(
    () => parseMarkdown(clipped ? children.slice(0, MAX_MARKDOWN_CHARS) : children),
    [children, clipped],
  );
  const styles = useStyles(makeStyles);

  return (
    <View style={styles.body}>
      <Blocks blocks={blocks} styles={styles} muted={false} />
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

/** `muted` carries a quote's quieter tone into everything inside it. */
function Blocks({ blocks, styles, muted }: { blocks: Block[]; styles: Styles; muted: boolean }) {
  return blocks.map((block, index) => (
    <BlockView key={`${index}:${block.kind}`} block={block} styles={styles} muted={muted} />
  ));
}

function BlockView({ block, styles, muted }: { block: Block; styles: Styles; muted: boolean }) {
  switch (block.kind) {
    case "heading":
      return (
        <Text
          selectable
          style={[
            styles.text,
            styles.heading,
            headingSize(block.level, styles),
            muted && styles.muted,
          ]}
        >
          {renderInline(block.content, styles, { ...PLAIN, bold: true })}
        </Text>
      );
    case "rule":
      return <View style={styles.rule} />;
    case "code":
      return <CodeBlock source={block.text} language={block.language ?? ""} styles={styles} />;
    case "quote":
      return (
        <View style={styles.quote}>
          <Blocks blocks={block.blocks} styles={styles} muted />
        </View>
      );
    case "table":
      return <TableView table={block.table} styles={styles} />;
    case "list":
      return <ListView items={block.items} styles={styles} muted={muted} />;
    case "paragraph":
      return (
        <Text selectable style={[styles.text, muted && styles.muted]}>
          {renderInline(block.content, styles)}
        </Text>
      );
  }
}

function ListView({
  items,
  styles,
  muted,
}: {
  items: ListItem[];
  styles: Styles;
  muted: boolean;
}) {
  const { color } = useTheme();
  return (
    <View style={styles.list}>
      {items.map((item, index) => (
        <View key={index} style={styles.listRow}>
          {item.checked === undefined ? (
            <Text style={styles.marker}>{item.marker}</Text>
          ) : (
            // A task's box stands where its bullet would: whether it is done
            // is the point of the line.
            <View
              style={styles.taskBox}
              accessible
              accessibilityLabel={item.checked ? "Done" : "Not done"}
            >
              <Feather
                name={item.checked ? "check-square" : "square"}
                size={13}
                color={color.faint}
              />
            </View>
          )}
          <View style={styles.listBody}>
            <Blocks blocks={item.blocks} styles={styles} muted={muted} />
          </View>
        </View>
      ))}
    </View>
  );
}

function TableView({ table, styles }: { table: Table; styles: Styles }) {
  // React Native has no table layout, so columns are measured here and given
  // fixed widths. Without that, each row sizes independently and the columns
  // do not line up with one another.
  const widths = table.header.map((heading, index) => {
    const longest = table.rows.reduce(
      (worst, row) => Math.max(worst, plainText(row[index] ?? []).length),
      plainText(heading).length,
    );
    return Math.min(240, Math.max(76, longest * 7.6 + 24));
  });

  return (
    <View>
      <TableGrid table={table} widths={widths} styles={styles} />
      {table.hiddenRows > 0 ? (
        <Text style={styles.truncated}>
          {table.hiddenRows === 1
            ? "1 more row not shown."
            : `${table.hiddenRows.toLocaleString()} more rows not shown.`}
        </Text>
      ) : null}
    </View>
  );
}

function TableGrid({
  table,
  widths,
  styles,
}: {
  table: Table;
  widths: number[];
  styles: Styles;
}) {
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
 * Deliberately the in-app browser rather than Linking.openURL: lib/markdown
 * already refuses every scheme but http(s), and routing through WebBrowser
 * means even a mistake there cannot hand a custom scheme to the OS. A failure
 * to open is swallowed — a tap that does nothing beats a crash mid-transcript.
 */
function openLink(href: string) {
  void WebBrowser.openBrowserAsync(href).catch(() => {});
}

/** What the text around an inline span is already set in. */
interface Setting {
  bold: boolean;
  italic: boolean;
  inLink: boolean;
}

const PLAIN: Setting = { bold: false, italic: false, inLink: false };

function renderInline(content: Inline[], styles: Styles, setting = PLAIN): ReactNode[] {
  return content.map((node, key) => {
    switch (node.kind) {
      case "text":
        return node.text;
      case "break":
        return "\n";
      case "bold":
      case "italic": {
        // Each weight and slant is a face of its own, and a nested span
        // replaces the face around it rather than adding to it — so italic
        // inside bold has to ask for the bold italic by name.
        const inner = {
          ...setting,
          bold: setting.bold || node.kind === "bold",
          italic: setting.italic || node.kind === "italic",
        };
        return (
          <Text
            key={key}
            style={inner.bold ? (inner.italic ? styles.boldItalic : styles.bold) : styles.italic}
          >
            {renderInline(node.children, styles, inner)}
          </Text>
        );
      }
      case "strike":
        return (
          <Text key={key} style={styles.strike}>
            {renderInline(node.children, styles, setting)}
          </Text>
        );
      case "math":
        // Not typeset — that would be a maths engine. Set in mono because
        // that is what this system does with machine notation, which reads
        // O(N^2) correctly and drops the dollars nobody meant to see.
        return (
          <Text key={key} style={styles.math}>
            {node.text}
          </Text>
        );
      case "code":
        // A View, not a styled Text: a nested Text is an attributed-string
        // span, and both platforms draw its background as a plain rectangle —
        // the radius and padding set on it are silently dropped. A View
        // nested in Text is laid out as an inline block, which does honour
        // them. It cannot wrap internally though, so a long span stays flat
        // text rather than becoming a chip that overflows the column. Inside
        // a link it stays text too, so a tap on it reaches the link.
        return !setting.inLink && node.text.length <= MAX_CODE_CHIP ? (
          <View key={key} style={styles.inlineCodeChip}>
            <Text style={styles.inlineCode}>{node.text}</Text>
          </View>
        ) : (
          <Text
            key={key}
            // Its own colour would otherwise hide that it is part of a link.
            style={[styles.inlineCode, styles.inlineCodeFlat, setting.inLink && styles.link]}
          >
            {node.text}
          </Text>
        );
      case "link":
        return (
          <Text
            key={key}
            style={styles.link}
            onPress={() => openLink(node.href)}
            accessibilityRole="link"
            accessibilityHint={`Opens ${node.href}`}
          >
            {renderInline(node.children, styles, { ...setting, inLink: true })}
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
    italic: { fontFamily: font.sansItalic },
    boldItalic: { fontFamily: font.sansBoldItalic },
    strike: { textDecorationLine: "line-through" },
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
      gap: space.sm,
      borderLeftWidth: 3,
      borderLeftColor: c.fillStrong,
      paddingLeft: space.md,
    },
    muted: { color: c.muted },
    list: { gap: space.sm },
    listRow: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
    marker: {
      minWidth: 16,
      fontFamily: font.mono,
      fontSize: size.caption,
      color: c.faint,
      lineHeight: 23,
    },
    taskBox: { minWidth: 16, height: 23, justifyContent: "center" },
    listBody: { flex: 1, gap: space.sm },
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
      fontFamily: font.sansItalic,
      fontSize: size.caption,
      color: c.faint,
    },
  });
}
