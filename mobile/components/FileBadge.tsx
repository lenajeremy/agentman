import Feather from "@expo/vector-icons/Feather";
import { StyleSheet, Text, View } from "react-native";

import { useTheme } from "../lib/appearance";
import { font } from "../lib/theme";

/**
 * A short, coloured mark for a file's type.
 *
 * A single grey document icon on every row gives the eye nothing to scan by,
 * which is the main reason a long directory reads as a wall. A two-or-three
 * letter tag in the language's own colour lets you find the Go files without
 * reading a single filename.
 *
 * Colours are fixed rather than themed: they carry the same meaning in light
 * and dark, and they are the tints people already associate with each
 * language. Each is paired with a wash tinted from the same hue so the tag
 * keeps its contrast on both grounds.
 */
const types: Record<string, { label: string; tint: string; wash: string }> = {
  go: { label: "GO", tint: "#00A6D6", wash: "#DFF4FB" },
  ts: { label: "TS", tint: "#2F74C0", wash: "#E3EDF9" },
  tsx: { label: "TSX", tint: "#2F74C0", wash: "#E3EDF9" },
  mts: { label: "TS", tint: "#2F74C0", wash: "#E3EDF9" },
  js: { label: "JS", tint: "#B0A012", wash: "#F8F4DA" },
  jsx: { label: "JSX", tint: "#B0A012", wash: "#F8F4DA" },
  mjs: { label: "JS", tint: "#B0A012", wash: "#F8F4DA" },
  cjs: { label: "JS", tint: "#B0A012", wash: "#F8F4DA" },
  json: { label: "{ }", tint: "#8A7A1F", wash: "#F6F2DC" },
  md: { label: "MD", tint: "#3B8A4E", wash: "#E4F3E7" },
  mdx: { label: "MD", tint: "#3B8A4E", wash: "#E4F3E7" },
  py: { label: "PY", tint: "#3672A4", wash: "#E4EDF6" },
  rs: { label: "RS", tint: "#B7563B", wash: "#F8E8E3" },
  rb: { label: "RB", tint: "#B03A3A", wash: "#F9E5E5" },
  swift: { label: "SW", tint: "#E5643E", wash: "#FCEAE3" },
  java: { label: "JV", tint: "#B07219", wash: "#F7EEDC" },
  kt: { label: "KT", tint: "#7F52FF", wash: "#EDE6FF" },
  c: { label: "C", tint: "#555555", wash: "#ECECEC" },
  h: { label: "H", tint: "#555555", wash: "#ECECEC" },
  cpp: { label: "C++", tint: "#00599C", wash: "#E0EBF4" },
  css: { label: "CSS", tint: "#B4478A", wash: "#F8E7F1" },
  scss: { label: "CSS", tint: "#B4478A", wash: "#F8E7F1" },
  html: { label: "<>", tint: "#C4551E", wash: "#FAEAE1" },
  xml: { label: "<>", tint: "#C4551E", wash: "#FAEAE1" },
  svg: { label: "SVG", tint: "#C4551E", wash: "#FAEAE1" },
  yaml: { label: "YML", tint: "#6C7A89", wash: "#EDF0F2" },
  yml: { label: "YML", tint: "#6C7A89", wash: "#EDF0F2" },
  sh: { label: "SH", tint: "#3F8A3F", wash: "#E5F2E5" },
  bash: { label: "SH", tint: "#3F8A3F", wash: "#E5F2E5" },
  zsh: { label: "SH", tint: "#3F8A3F", wash: "#E5F2E5" },
  sql: { label: "SQL", tint: "#2E7D8A", wash: "#E1EFF1" },
  toml: { label: "TML", tint: "#8A6D3B", wash: "#F4EDE1" },
  lock: { label: "LCK", tint: "#8A8A8A", wash: "#EFEFEF" },
  mod: { label: "GO", tint: "#00A6D6", wash: "#DFF4FB" },
  sum: { label: "GO", tint: "#00A6D6", wash: "#DFF4FB" },
};

const imageTypes = new Set(["png", "jpg", "jpeg", "gif", "webp", "avif", "ico", "bmp"]);

export function FileBadge({ name, directory }: { name: string; directory: boolean }) {
  const { color } = useTheme();

  if (directory) {
    return (
      <View style={[styles.badge, { backgroundColor: color.workingWash }]}>
        <Feather name="folder" size={13} color={color.working} />
      </View>
    );
  }

  const ext = name.split(".").pop()?.toLowerCase() ?? "";
  if (imageTypes.has(ext)) {
    return (
      <View style={[styles.badge, { backgroundColor: color.needsYouWash }]}>
        <Feather name="image" size={13} color={color.needsYouText} />
      </View>
    );
  }

  const type = types[ext];
  if (!type) {
    return (
      <View style={[styles.badge, { backgroundColor: color.fill }]}>
        <Feather name="file" size={13} color={color.faint} />
      </View>
    );
  }
  return (
    <View style={[styles.badge, { backgroundColor: type.wash }]}>
      <Text style={[styles.label, { color: type.tint }]} numberOfLines={1}>
        {type.label}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  badge: {
    width: 26,
    height: 26,
    borderRadius: 7,
    alignItems: "center",
    justifyContent: "center",
    // flexShrink, not `flex: 0`: the shorthand also sets a 0% basis, which
    // collapses the square in a row that is tight for space.
    flexShrink: 0,
  },
  // Tight tracking keeps a three-character tag inside the square.
  label: { fontFamily: font.monoMedium, fontSize: 9.5, letterSpacing: -0.3 },
});
