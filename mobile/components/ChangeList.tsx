import Feather from "@expo/vector-icons/Feather";
import { StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { WorkspaceChange } from "../lib/protocol";
import { font, Palette, radius, space, scaled } from "../lib/theme";
import { FileBadge } from "./FileBadge";
import { MotionPressable } from "./MotionPressable";

/**
 * Changed files, one row each: what the file is, where it lives, and how much
 * of it changed. The working tree's changes and a commit's files are the same
 * list, so they are drawn by the same code.
 */
export function ChangeList({
  changes,
  onOpen,
}: {
  changes: WorkspaceChange[];
  onOpen: (change: WorkspaceChange) => void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  // One bordered surface with hairline dividers, rather than a card per row:
  // per-row chrome was costing about half the rows that fit on screen.
  return (
    <View style={styles.list}>
      {changes.map((change, index) => (
        <MotionPressable
          key={change.path}
          onPress={() => onOpen(change)}
          style={[styles.row, index > 0 && styles.rowDivided]}
          pressedScale={0.995}
          accessibilityRole="button"
          accessibilityLabel={`${change.path}, ${changeLabel(change.status)}`}
        >
          <FileBadge name={change.path} directory={false} />
          <View style={styles.identity}>
            <Text style={styles.name} numberOfLines={1}>
              {change.path.split("/").at(-1)}
            </Text>
            {change.path.includes("/") ? (
              <Text style={styles.dir} numberOfLines={1}>
                {change.path.split("/").slice(0, -1).join("/")}
              </Text>
            ) : null}
          </View>
          <ChangeCount change={change} styles={styles} />
          <Feather name="chevron-right" size={14} color={color.faint} />
        </MotionPressable>
      ))}
    </View>
  );
}

function changeLabel(status: string): string {
  if (status === "??") return "New";
  if (status.includes("U")) return "Conflict";
  if (status.includes("D")) return "Deleted";
  if (status.includes("A")) return "Added";
  if (status.includes("R")) return "Renamed";
  return "Modified";
}

/**
 * How much a file changed, rather than that it changed.
 *
 * "Modified" is the same word for a typo and a rewrite. Counts answer the
 * question the word raises, and they are colour-coded the way a diff already
 * is: green where a file only grew, amber where it was edited in place, red
 * where it went away. Binary files report no counts, so they keep a word.
 */
function ChangeCount({
  change,
  styles,
}: {
  change: WorkspaceChange;
  styles: ReturnType<typeof makeStyles>;
}) {
  const added = change.added ?? 0;
  const removed = change.removed ?? 0;
  if (change.status.includes("D"))
    return <Text style={[styles.count, styles.countRemoved]}>deleted</Text>;
  if (change.status.includes("U"))
    return <Text style={[styles.count, styles.countConflict]}>conflict</Text>;
  if (added === 0 && removed === 0) {
    return (
      <Text style={[styles.count, styles.countNeutral]}>
        {change.status === "??" || change.status.includes("A") ? "new" : "changed"}
      </Text>
    );
  }
  return (
    <View style={styles.counts}>
      {added > 0 ? (
        <Text
          style={[
            styles.count,
            removed > 0 ? styles.countEdited : styles.countAdded,
          ]}
        >
          +{added}
        </Text>
      ) : null}
      {removed > 0 ? (
        <Text style={[styles.count, styles.countRemoved]}>−{removed}</Text>
      ) : null}
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    list: {
      borderWidth: 1,
      borderColor: c.line,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      overflow: "hidden",
    },
    // Two lines need room to breathe: at a plain row's 44pt the name and its
    // directory were pressed against the dividers above and below.
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      minHeight: 56,
      paddingHorizontal: space.md,
      paddingVertical: space.sm,
    },
    rowDivided: {
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    // justifyContent keeps a root-level file — one with no directory beneath it
    // — on the row's centre line instead of sitting where the first of two
    // lines would. The gap is small because the path is a continuation of the
    // name, not a separate field.
    identity: { flex: 1, minWidth: 0, gap: 1, justifyContent: "center" },
    name: { color: c.text, ...font.mono, fontSize: scaled(13) },
    dir: { color: c.faint, ...font.mono, fontSize: scaled(10.5) },
    counts: { flexDirection: "row", alignItems: "center", gap: 6 },
    count: { ...font.monoMedium, fontSize: scaled(11.5) },
    countAdded: { color: c.ok },
    // Amber, not blue: an edited file sits between wholly new and deleted, and
    // the palette's "working" blue already means an agent is running.
    countEdited: { color: "#B07A16" },
    countRemoved: { color: c.errorText },
    countConflict: { color: c.needsYouText },
    countNeutral: { color: c.faint },
  });
