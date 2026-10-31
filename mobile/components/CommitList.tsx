import Feather from "@expo/vector-icons/Feather";
import { useMemo } from "react";
import { StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { clockTime, groupByDay, shortHash } from "../lib/commits";
import { WorkspaceCommit } from "../lib/protocol";
import { font, Palette, radius, size, space, scaled } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

/**
 * Commits under a heading for each day, newest first.
 *
 * The day is said once, in a heading like the board's own groups, so each row
 * only needs a time. A row is two columns: what the commit says and who made
 * it on the left, and the two things that identify and size it — its hash and
 * its lines added and removed — stacked on the right, where the changed-file
 * list keeps its counts.
 */
export function CommitList({
  commits,
  onOpen,
}: {
  commits: WorkspaceCommit[];
  onOpen: (commit: WorkspaceCommit) => void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const days = useMemo(() => groupByDay(commits), [commits]);
  return (
    <View>
      {days.map((day, dayIndex) => (
        <View key={day.key}>
          <View style={[styles.dayHeading, dayIndex === 0 && styles.firstDay]}>
            <Text style={styles.dayLabel}>{day.label}</Text>
            <Text style={styles.dayCount}>{day.commits.length}</Text>
          </View>
          <View style={styles.list}>
            {day.commits.map((commit, index) => (
              <MotionPressable
                key={commit.hash}
                onPress={() => onOpen(commit)}
                style={[styles.row, index > 0 && styles.rowDivided]}
                pressedScale={0.995}
                accessibilityRole="button"
                accessibilityLabel={`${commit.subject}. ${commit.merge ? "Merge " : ""}commit ${shortHash(commit.hash)} by ${commit.author}, ${day.label} at ${clockTime(commit.authoredAt)}`}
              >
                <View style={styles.main}>
                  <Text style={styles.subject} numberOfLines={2}>
                    {commit.subject || "(no message)"}
                  </Text>
                  <View style={styles.meta}>
                    {commit.merge ? (
                      <Feather name="git-merge" size={11} color={color.faint} />
                    ) : null}
                    <Text style={styles.byline} numberOfLines={1}>
                      {commit.author}
                    </Text>
                    <Text style={styles.dot}>·</Text>
                    <Text style={styles.time}>{clockTime(commit.authoredAt)}</Text>
                  </View>
                </View>
                <View style={styles.side}>
                  <View style={styles.hashTag}>
                    <Text style={styles.hash}>{shortHash(commit.hash)}</Text>
                  </View>
                  <CommitSize commit={commit} />
                </View>
              </MotionPressable>
            ))}
          </View>
        </View>
      ))}
    </View>
  );
}

/** Lines added and removed, in a diff's own colours. A commit that changed
 *  nothing countable shows nothing rather than "+0". */
export function CommitSize({
  commit,
  large = false,
}: {
  commit: Pick<WorkspaceCommit, "added" | "removed">;
  large?: boolean;
}) {
  const styles = useStyles(makeStyles);
  const added = commit.added ?? 0;
  const removed = commit.removed ?? 0;
  if (added === 0 && removed === 0) return null;
  const count = [styles.count, large && styles.countLarge];
  return (
    <View style={styles.counts}>
      {added > 0 ? (
        <Text style={[count, styles.countAdded]}>+{compact(added)}</Text>
      ) : null}
      {removed > 0 ? (
        <Text style={[count, styles.countRemoved]}>−{compact(removed)}</Text>
      ) : null}
    </View>
  );
}

/** A lockfile commit's "+48213" would push the subject off the row. */
function compact(lines: number): string {
  if (lines < 10_000) return String(lines);
  if (lines < 1_000_000) return `${Math.round(lines / 1000)}k`;
  return `${(lines / 1_000_000).toFixed(1)}m`;
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    // The board's group headings, so a day reads like "Working" or "Idle".
    dayHeading: {
      flexDirection: "row",
      alignItems: "baseline",
      justifyContent: "space-between",
      paddingHorizontal: space.xxs,
      marginTop: space.xl,
      marginBottom: space.sm,
    },
    firstDay: { marginTop: space.xs },
    dayLabel: { color: c.muted, ...font.sansBold, fontSize: size.caption },
    dayCount: { color: c.faint, ...font.mono, fontSize: size.label },

    list: {
      borderWidth: 1,
      borderColor: c.line,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      overflow: "hidden",
    },
    row: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.md,
      paddingHorizontal: space.lg,
      paddingVertical: space.md,
    },
    rowDivided: {
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    main: { flex: 1, minWidth: 0, gap: 5 },
    subject: {
      color: c.text,
      ...font.sansMedium,
      fontSize: scaled(14.5),
      lineHeight: scaled(20),
      letterSpacing: -0.1,
    },
    meta: { flexDirection: "row", alignItems: "center", gap: 6, minWidth: 0 },
    byline: {
      flexShrink: 1,
      color: c.muted,
      ...font.sans,
      fontSize: scaled(12),
    },
    dot: { color: c.faint, ...font.sans, fontSize: scaled(12) },
    time: { color: c.faint, ...font.mono, fontSize: scaled(11) },

    // Stacked and right-aligned, so hashes and counts form two tidy columns
    // down the list however long each subject runs.
    side: { alignItems: "flex-end", gap: 7, paddingTop: 1 },
    hashTag: {
      paddingHorizontal: 6,
      paddingVertical: 2,
      borderRadius: radius.sm - 2,
      backgroundColor: c.fill,
    },
    hash: { color: c.muted, ...font.mono, fontSize: scaled(11) },
    counts: { flexDirection: "row", alignItems: "center", gap: 6 },
    count: { ...font.monoMedium, fontSize: scaled(11.5) },
    countLarge: { fontSize: size.caption },
    countAdded: { color: c.ok },
    countRemoved: { color: c.errorText },
  });
