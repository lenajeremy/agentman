import { useMemo } from "react";
import { StyleSheet } from "react-native";
import MarkdownDisplay from "react-native-markdown-display";

import { color, font, radius, size, space } from "../lib/theme";

/**
 * Renders an agent's reply as markdown.
 *
 * Agents write markdown constantly — backticked paths, fenced diffs, bullet
 * lists of what they changed — and showing the raw source means reading
 * `**done**` and counting backticks on a phone. The rules here follow the same
 * split as the rest of the app: prose in the sans face, anything the machine
 * produced (code, paths, commands) in mono.
 *
 * Deliberately restrained: no syntax highlighting, no tables with borders, no
 * images. A transcript viewer needs to be legible at a glance, not to be a
 * document renderer.
 */
export function Markdown({ children }: { children: string }) {
  // The stylesheet is static but built once per mount to keep the theme in
  // one place rather than duplicating literals across the tree.
  const styles = useMemo(() => markdownStyles, []);
  return (
    <MarkdownDisplay style={styles} mergeStyle={false}>
      {children}
    </MarkdownDisplay>
  );
}

const markdownStyles = StyleSheet.create({
  body: {
    fontFamily: font.sans,
    fontSize: size.body,
    color: color.text,
    lineHeight: 22,
  },
  paragraph: { marginTop: 0, marginBottom: space.sm },

  // Headings stay close to body size: an agent's "## Summary" is a signpost in
  // a chat message, not a page title, and blowing it up wrecks the rhythm.
  heading1: { fontFamily: font.sansBold, fontSize: size.title, color: color.text, marginBottom: space.xs },
  heading2: { fontFamily: font.sansBold, fontSize: size.body, color: color.text, marginBottom: space.xs },
  heading3: { fontFamily: font.sansMedium, fontSize: size.body, color: color.text, marginBottom: space.xs },
  heading4: { fontFamily: font.sansMedium, fontSize: size.body, color: color.muted },
  heading5: { fontFamily: font.sansMedium, fontSize: size.caption, color: color.muted },
  heading6: { fontFamily: font.sansMedium, fontSize: size.caption, color: color.muted },

  strong: { fontFamily: font.sansBold, color: color.text },
  em: { fontStyle: "italic" },
  s: { textDecorationLine: "line-through", color: color.muted },

  link: { color: color.working, textDecorationLine: "underline" },

  // Inline code is usually a path or a symbol — machine text, so mono, and
  // tinted to separate it from prose without shouting.
  code_inline: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.working,
    backgroundColor: color.sunken,
    borderRadius: radius.sm,
    paddingHorizontal: 4,
    paddingVertical: 1,
  },
  code_block: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.text,
    backgroundColor: color.sunken,
    borderRadius: radius.sm,
    borderWidth: 0,
    padding: space.sm,
    marginBottom: space.sm,
  },
  fence: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.text,
    backgroundColor: color.sunken,
    borderRadius: radius.sm,
    borderWidth: 0,
    padding: space.sm,
    marginBottom: space.sm,
  },

  bullet_list: { marginBottom: space.sm },
  ordered_list: { marginBottom: space.sm },
  list_item: { flexDirection: "row", marginBottom: space.xs },
  bullet_list_icon: { color: color.faint, marginRight: space.sm, marginLeft: 0 },
  ordered_list_icon: { color: color.faint, marginRight: space.sm, marginLeft: 0 },

  blockquote: {
    backgroundColor: color.sunken,
    borderLeftWidth: 2,
    borderLeftColor: color.line,
    paddingHorizontal: space.sm,
    paddingVertical: space.xs,
    marginBottom: space.sm,
  },

  hr: { backgroundColor: color.line, height: StyleSheet.hairlineWidth, marginVertical: space.sm },

  table: { borderWidth: 0, marginBottom: space.sm },
  thead: { borderBottomWidth: StyleSheet.hairlineWidth, borderColor: color.line },
  th: { fontFamily: font.sansMedium, color: color.muted, padding: space.xs },
  td: { padding: space.xs, borderWidth: 0 },
  tr: { borderBottomWidth: StyleSheet.hairlineWidth, borderColor: color.line },
});
